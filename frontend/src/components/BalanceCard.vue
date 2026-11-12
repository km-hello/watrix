<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { NSkeleton, NPopover } from 'naive-ui'
import { Info } from '@lucide/vue'
import { formatNumber, formatTimestamp } from '../format'
import type { Dashboard } from '../types/api'

const props = defineProps<{ dashboard: Dashboard | null; loading: boolean; error: string | null }>()
const timestampOpen = ref(false)
const timestampTrigger = ref<HTMLButtonElement | null>(null)
let timestampPinned = false

function closeTimestamp() {
  timestampOpen.value = false
  timestampPinned = false
}

function toggleTimestamp() {
  timestampPinned = !timestampPinned
  timestampOpen.value = timestampPinned
}

function handleClickOutside(event: MouseEvent) {
  if (event.target instanceof Node && timestampTrigger.value?.contains(event.target)) return
  closeTimestamp()
}

function handleKeydown(event: KeyboardEvent) {
  if (event.key !== 'Escape' || !timestampOpen.value) return
  closeTimestamp()
  event.preventDefault()
  event.stopPropagation()
}

onMounted(() => window.addEventListener('keydown', handleKeydown, true))
onUnmounted(() => window.removeEventListener('keydown', handleKeydown, true))
const current = computed(() => props.dashboard?.current)
// 先处理过期和空数据，再展示低量提醒，读取错误在独立提示中呈现。
const status = computed(() => {
  if (props.loading && !current.value) return { text: '正在加载', tone: 'neutral' }
  if (props.dashboard?.collection.state === 'stale') return { text: '数据较旧', tone: 'warning' }
  if (!current.value) return { text: '等待首次采集', tone: 'neutral' }
  if (props.dashboard?.low.is_low) return { text: '电量不足', tone: 'warning' }
  return { text: '已更新', tone: 'success' }
})
</script>

<template>
  <section class="dashboard-card balance-card" aria-labelledby="balance-title" :aria-busy="loading">
    <div class="balance-heading">
      <h2 id="balance-title">当前剩余</h2>
      <NPopover v-if="current && dashboard" :show="timestampOpen" trigger="manual" @clickoutside="handleClickOutside" placement="top-start" style="max-width: calc(100vw - 32px)">
        <template #trigger>
          <button ref="timestampTrigger" type="button" class="balance-time-trigger" :aria-expanded="timestampOpen" aria-label="查看数据更新时间" :aria-describedby="timestampOpen ? 'balance-timestamp' : undefined" @pointerenter="($event.pointerType === 'mouse') && (timestampOpen = true)" @click="toggleTimestamp" @blur="closeTimestamp">
            <Info :size="15" aria-hidden="true" />
          </button>
        </template>
        <span id="balance-timestamp">数据截至 {{ formatTimestamp(current.collected_at, dashboard.timezone) }}</span>
      </NPopover>
    </div>
    <div v-if="loading && !current" class="balance-skeleton" aria-hidden="true">
      <NSkeleton height="65px" width="65%" :sharp="false" />
      <NSkeleton height="24px" width="100%" :sharp="false" />
      <NSkeleton height="24px" width="100%" :sharp="false" />
    </div>
    <template v-else>
      <p class="balance"><span class="balance-value">{{ current ? formatNumber(current.remaining_kwh) : '—' }}</span><span class="balance-unit">kWh</span></p>
      <dl class="balance-details">
        <div><dt>余额</dt><dd>¥ {{ current ? formatNumber(current.remaining_yuan) : '—' }}</dd></div>
        <div><dt>电价</dt><dd>¥ {{ current ? formatNumber(current.unit_price_yuan_per_kwh, 4) : '—' }}<span class="detail-unit">/kWh</span></dd></div>
      </dl>
    </template>
    <p v-if="error" class="balance-request-error" role="alert">{{ error }}</p>
    <p class="balance-status" :class="`status-${status.tone}`" role="status">{{ status.text }}</p>
  </section>
</template>
