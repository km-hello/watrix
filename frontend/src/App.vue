<script setup lang="ts">
import { NConfigProvider, NGlobalStyle, type GlobalThemeOverrides } from 'naive-ui'
import { RefreshCw, Settings as SettingsIcon, Zap } from '@lucide/vue'
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import BalanceCard from './components/BalanceCard.vue'
import UsageSummaryCard from './components/UsageSummaryCard.vue'
import TrendChart from './components/TrendChart.vue'
import RecentReadings from './components/RecentReadings.vue'
import SettingsModal from './components/SettingsModal.vue'
import HistoryDrawer from './components/HistoryDrawer.vue'
import { ApiError, collect, getDashboard, getReadings, getSeries } from './api'
import { formatTimestamp } from './format'
import type { SeriesMetric, SeriesRange } from './types/api'
import { useReadRequest } from './useReadRequest'

const settingsOpen = ref(false)
const historyOpen = ref(false)
const range = ref<SeriesRange>('24h')
const metric = ref<SeriesMetric>('remaining')
const { data: dashboard, loading, error, load: loadDashboard } = useReadRequest(getDashboard)
const { data: readings, loading: readingsLoading, error: readingsError, load: loadReadings } = useReadRequest(signal => getReadings({}, signal))
const { data: series, loading: seriesLoading, error: seriesError, load: loadSeries } = useReadRequest(signal => getSeries(range.value, metric.value, signal))
const collecting = ref(false)
const refreshMessage = ref<string | null>(null)
const refreshFailed = ref(false)
const retrying = ref(false)
const cooldownSeconds = ref(0)
let cooldownUntil = 0
let cooldownTimer: number | undefined
let staleTimer: number | undefined
let collectRequest: AbortController | null = null
let disposed = false

/** 并行重新读取三个页面区域，任一当前区域失败时返回 false，供操作结果提示使用。 */
async function reloadAll(): Promise<boolean> {
  const results = await Promise.all([loadDashboard(), loadSeries(), loadReadings()])
  return results.every(Boolean) && !error.value && !seriesError.value && !readingsError.value
}

/** 按绝对截止时间更新采集倒计时；后续限流只能延长已有等待。 */
function startCooldown(seconds: number) {
  cooldownUntil = Math.max(cooldownUntil, Date.now() + seconds * 1000)
  // 用截止时间而非逐秒减一计算，后台标签页定时器被节流时仍能校正倒计时。
  /** 根据截止时间刷新剩余秒数，到期后释放间隔计时器。 */
  const update = () => {
    cooldownSeconds.value = Math.max(0, Math.ceil((cooldownUntil - Date.now()) / 1000))
    if (!cooldownSeconds.value) {
      window.clearInterval(cooldownTimer)
      cooldownTimer = undefined
    }
  }
  update()
  if (cooldownTimer === undefined && cooldownSeconds.value) cooldownTimer = window.setInterval(update, 1000)
}

/** 执行一次手动采集并重读页面；失败时保留已展示的数据，不自动再次采集。 */
async function refresh() {
  if (collecting.value || retrying.value || cooldownSeconds.value) return
  collecting.value = true
  refreshMessage.value = null
  refreshFailed.value = false
  startCooldown(30)
  const controller = new AbortController()
  collectRequest = controller
  try {
    await collect(controller.signal)
    if (disposed) return
    const complete = await reloadAll()
    if (disposed) return
    refreshFailed.value = !complete
    refreshMessage.value = complete ? '采集成功' : '采集成功，但部分数据读取失败'
  } catch (cause) {
    if (disposed || controller.signal.aborted) return
    if (cause instanceof ApiError && cause.status === 429) startCooldown(Math.max(30, cause.retryAfterSeconds))
    // 结果未确认不等于采集未执行，提示先读取结果而不是重复写入。
    const uncertain = cause instanceof ApiError && cause.status === 0
    refreshFailed.value = true
    refreshMessage.value = uncertain ? '请求结果未确认，已保留上次数据，请先重试读取' : cause instanceof ApiError && cause.status === 429 ? cause.message : '采集失败，已保留上次数据'
  } finally {
    if (!disposed) collecting.value = false
  }
}

/** 仅重读页面数据，用于恢复读取失败或核对结果未确认的采集。 */
async function retryRead() {
  if (retrying.value || collecting.value) return
  retrying.value = true
  const complete = await reloadAll()
  if (!disposed) {
    refreshFailed.value = !complete
    refreshMessage.value = complete
      ? seriesLoading.value ? '概览与记录已重新读取' : '已重新读取数据'
      : '部分数据读取失败，请稍后重试'
    retrying.value = false
  }
}

const hasReadError = computed(() => !!(error.value || readingsError.value || seriesError.value || refreshFailed.value))
// 数据新鲜度与本次请求是否成功分开表达，失败读取仍保留旧数据状态。
const statusTone = computed(() => {
  if (dashboard.value?.collection.state === 'stale') return 'warning'
  return dashboard.value?.current ? 'success' : 'neutral'
})
const updateLabel = computed(() => {
  const current = dashboard.value?.current
  if (!current || !dashboard.value) return loading.value ? '正在加载' : '尚无采集数据'
  if (dashboard.value.collection.state === 'stale') {
    return `数据较旧 · ${formatTimestamp(current.collected_at, dashboard.value.timezone)}`
  }
  return `${formatTimestamp(current.collected_at, dashboard.value.timezone, false)} 已更新`
})

// 到期时间由后端决定，页面长开也会过期；更换快照时撤销旧快照的计时器。
watch(dashboard, current => {
  window.clearTimeout(staleTimer)
  staleTimer = undefined
  if (current?.collection.state !== 'fresh' || !current.collection.stale_at) return
  const deadline = Date.parse(current.collection.stale_at)
  if (!Number.isFinite(deadline)) return
  // 回调再次核对快照身份和当前时间，避免旧回调或时钟变化覆盖新响应。
  /** 检查当前快照是否已到期，必要时替换状态并停止低量提示。 */
  const expire = () => {
    if (disposed || dashboard.value !== current) return
    const remaining = deadline - Date.now()
    if (remaining >= 0) {
      // 定时器有有符号 32 位延迟上限；额外 1ms 保持“严格超过”才过期的边界。
      staleTimer = window.setTimeout(expire, Math.min(remaining + 1, 2147483647))
      return
    }
    // 同时移除过期数据的低量提示，替换快照以触发 shallowRef 更新。
    dashboard.value = {
      ...current,
      collection: { ...current.collection, state: 'stale' },
      low: { ...current.low, is_low: false },
    }
  }
  expire()
})

// 切换查询时清空旧趋势，避免用新标签展示上一次范围的数据。
watch([range, metric], () => { void loadSeries(true) })
onMounted(() => { void reloadAll() })
// 读取请求由各自组合函数释放，这里清理页面持有的写请求和计时器。
onUnmounted(() => {
  disposed = true
  collectRequest?.abort()
  window.clearInterval(cooldownTimer)
  window.clearTimeout(staleTimer)
})

const theme: GlobalThemeOverrides = {
  common: {
    primaryColor: '#2F8C59',
    primaryColorHover: '#267447',
    primaryColorPressed: '#20623D',
    primaryColorSuppl: '#2F8C59',
    bodyColor: '#F7FAF7',
    cardColor: '#FFFFFF',
    textColorBase: '#1E3628',
    textColor1: '#1E3628',
    textColor2: '#1E3628',
    textColor3: '#718276',
    borderColor: '#E1EBE3',
    borderRadius: '10px',
    fontFamily: 'Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif',
  },
}
</script>

<template>
  <NConfigProvider :theme-overrides="theme">
    <NGlobalStyle />
    <div class="page-shell">
      <header class="brand-header">
        <Zap :size="28" aria-hidden="true" class="brand-icon" />
        <span class="brand-name">Watrik</span>
        <span v-if="dashboard?.meter_name" class="meter-name">{{ dashboard.meter_name }}</span>
        <div class="header-actions">
          <button class="header-icon-button" type="button" aria-label="刷新" :aria-busy="collecting" :disabled="collecting || retrying || cooldownSeconds > 0" :title="collecting ? '正在刷新' : cooldownSeconds ? `${cooldownSeconds} 秒后可再次刷新` : '刷新'" @click="refresh">
            <RefreshCw :size="20" aria-hidden="true" :class="{ 'refresh-spinning': collecting }" />
          </button>
          <button class="header-icon-button" type="button" aria-label="设置" @click="settingsOpen = true"><SettingsIcon :size="20" aria-hidden="true" /></button>
        </div>
      </header>
      <main>
        <div class="overview-heading">
          <h1>用电概览</h1>
          <span class="overview-update empty-state"><span class="status-dot" :class="`dot-${statusTone}`" aria-hidden="true" />{{ updateLabel }}</span>
        </div>
        <div v-if="refreshMessage || cooldownSeconds || hasReadError" class="refresh-feedback">
          <p v-if="refreshMessage" :class="{ 'refresh-error': refreshFailed }" :role="refreshFailed ? 'alert' : 'status'">{{ refreshMessage }}</p>
          <span v-if="cooldownSeconds" class="cooldown-note">{{ cooldownSeconds }} 秒后可再次刷新</span>
          <button v-if="hasReadError" type="button" class="read-retry-button" :disabled="retrying || collecting" @click="retryRead">{{ retrying ? '正在重试读取' : '重试读取' }}</button>
        </div>

        <div class="dashboard-grid">
          <BalanceCard :dashboard="dashboard" :loading="loading" :error="error" />

          <UsageSummaryCard :today="dashboard?.today ?? null" :month="dashboard?.month ?? null" :loading="loading" :error="error" />

          <TrendChart v-model:range="range" v-model:metric="metric" :series="series" :loading="seriesLoading" :error="seriesError" />

          <RecentReadings :page="readings" :loading="readingsLoading" :error="readingsError" @open-history="historyOpen = true" />
        </div>
      </main>
    </div>
    <SettingsModal v-model:show="settingsOpen" :refresh="refresh" :collecting="collecting" :retrying="retrying" :cooldown-seconds="cooldownSeconds" :refresh-message="refreshMessage" :refresh-failed="refreshFailed" @saved="loadDashboard" />
    <HistoryDrawer v-model:show="historyOpen" />
  </NConfigProvider>
</template>
