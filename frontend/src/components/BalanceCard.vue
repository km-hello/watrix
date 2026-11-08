<script setup lang="ts">
import { computed } from 'vue'
import { NSkeleton } from 'naive-ui'
import { formatNumber, formatTimestamp } from '../format'
import type { Dashboard } from '../types/api'

const props = defineProps<{ dashboard: Dashboard | null; loading: boolean; error: string | null }>()
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
    <h2 id="balance-title">当前剩余</h2>
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
    <p v-if="current && dashboard" class="balance-timestamp">数据截至 {{ formatTimestamp(current.collected_at, dashboard.timezone) }}</p>
    <p v-if="error" class="balance-request-error" role="alert">{{ error }}</p>
    <p class="balance-status" :class="`status-${status.tone}`" role="status">{{ status.text }}</p>
  </section>
</template>
