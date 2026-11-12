<script setup lang="ts">
import { NTooltip, useMessage, type MessageReactive, type MessageType } from 'naive-ui'
import { RefreshCw, Settings as SettingsIcon, Zap } from '@lucide/vue'
import { computed, h, onMounted, onUnmounted, ref, watch } from 'vue'
import BalanceCard from './components/BalanceCard.vue'
import UsageSummaryCard from './components/UsageSummaryCard.vue'
import TrendChart from './components/TrendChart.vue'
import RecentReadings from './components/RecentReadings.vue'
import SettingsModal from './components/SettingsModal.vue'
import HistoryDrawer from './components/HistoryDrawer.vue'
import { ApiError, collect, getDashboard, getReadings, getSeries } from './api'
import type { SeriesMetric, SeriesRange } from './types/api'
import { useReadRequest } from './useReadRequest'

const settingsOpen = ref(false)
const historyOpen = ref(false)
const range = ref<SeriesRange>('24h')
const metric = ref<SeriesMetric>('remaining')
const { data: dashboard, loading, error, load: loadDashboard } = useReadRequest(getDashboard)
const { data: readings, loading: readingsLoading, error: readingsError, load: loadReadings } = useReadRequest(signal => getReadings({ limit: 6 }, signal))
const { data: series, loading: seriesLoading, error: seriesError, load: loadSeries } = useReadRequest(signal => getSeries(range.value, metric.value, signal))
const collecting = ref(false)
const collectUncertain = ref(false)
const message = useMessage()
let feedback: MessageReactive | undefined
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
  return results.every(Boolean) && !reading.value && !error.value && !seriesError.value && !readingsError.value
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
  if (busy.value) return
  if (needsRead.value) return retryRead()
  if (cooldownSeconds.value) return
  collecting.value = true
  startCooldown(30)
  const controller = new AbortController()
  collectRequest = controller
  try {
    await collect(controller.signal)
    if (disposed) return
    const complete = await reloadAll()
    if (disposed) return
    notify(complete ? 'success' : 'warning', complete ? '采集成功' : reading.value ? '采集成功，部分数据仍在读取' : '部分数据读取失败，点击刷新按钮重试读取')
  } catch (cause) {
    if (disposed || controller.signal.aborted) return
    if (cause instanceof ApiError && cause.status === 429) startCooldown(Math.max(30, cause.retryAfterSeconds))
    // 结果未确认不等于采集未执行，提示先读取结果而不是重复写入。
    const uncertain = cause instanceof ApiError && cause.status === 0
    collectUncertain.value = uncertain
    notify(uncertain || cause instanceof ApiError && cause.status === 429 ? 'warning' : 'error', uncertain ? '请求结果未确认，点击刷新按钮重新读取' : cause instanceof ApiError && cause.status === 429 ? cause.message : '采集失败，已保留上次数据')
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
    if (complete) collectUncertain.value = false
    notify(complete ? 'success' : 'warning', complete ? '已重新读取数据' : reading.value ? '部分数据仍在读取，请稍后重试' : '部分数据读取失败，点击刷新按钮重试读取')
    retrying.value = false
  }
}

const reading = computed(() => loading.value || seriesLoading.value || readingsLoading.value)
const busy = computed(() => collecting.value || retrying.value || reading.value)
const needsRead = computed(() => !!(error.value || readingsError.value || seriesError.value || collectUncertain.value))
const refreshDisabled = computed(() => busy.value || !needsRead.value && cooldownSeconds.value > 0)
const refreshLabel = computed(() => retrying.value ? '正在读取…' : collecting.value ? '正在刷新…' : reading.value ? '正在读取…' : needsRead.value ? '重试读取' : '刷新')
const refreshTooltip = computed(() => !busy.value && !needsRead.value && cooldownSeconds.value ? `${cooldownSeconds.value} 秒后可刷新` : refreshLabel.value)

function notify(type: MessageType, text: string) {
  feedback?.destroy()
  feedback = message.create(() => h('span', { role: type === 'success' ? 'status' : 'alert' }, text), {
    type, duration: type === 'success' ? 3000 : 6000, closable: true, keepAliveOnHover: true,
  })
}

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
  feedback?.destroy()
  window.clearInterval(cooldownTimer)
  window.clearTimeout(staleTimer)
})
</script>

<template>
  <div class="page-shell">
    <header class="brand-header">
      <div class="brand-identity">
        <div class="brand-wordmark">
          <Zap :size="28" aria-hidden="true" class="brand-icon" />
          <span class="brand-name">Watrix</span>
        </div>
        <span v-if="dashboard?.meter_name" class="meter-name">{{ dashboard.meter_name }}</span>
      </div>
      <div class="header-actions">
        <NTooltip>
          <template #trigger>
            <span class="refresh-trigger">
              <button class="header-icon-button" type="button" :aria-label="refreshLabel" :aria-busy="busy" :disabled="refreshDisabled" @click="refresh">
                <RefreshCw :size="20" aria-hidden="true" :class="{ 'refresh-spinning': collecting || retrying }" />
              </button>
            </span>
          </template>
          {{ refreshTooltip }}
        </NTooltip>
        <button class="header-icon-button" type="button" aria-label="设置" @click="settingsOpen = true"><SettingsIcon :size="20" aria-hidden="true" /></button>
      </div>
    </header>
    <main>
      <div class="dashboard-grid">
        <BalanceCard :dashboard="dashboard" :loading="loading" :error="error" />

        <TrendChart v-model:range="range" v-model:metric="metric" :series="series" :loading="seriesLoading" :error="seriesError" />

        <UsageSummaryCard :today="dashboard?.today ?? null" :month="dashboard?.month ?? null" :loading="loading" :error="error" />

        <RecentReadings :page="readings" :loading="readingsLoading" :error="readingsError" @open-history="historyOpen = true" />
      </div>
    </main>
  </div>
  <SettingsModal v-model:show="settingsOpen" @saved="loadDashboard" />
  <HistoryDrawer v-model:show="historyOpen" />
</template>
