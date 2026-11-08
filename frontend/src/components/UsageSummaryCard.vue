<script setup lang="ts">
import { computed, ref } from 'vue'
import { NPopover, NSkeleton } from 'naive-ui'
import { formatNumber } from '../format'
import type { UsageSummary } from '../types/api'

const props = defineProps<{
  today: UsageSummary | null
  month: UsageSummary | null
  loading: boolean
  error: string | null
}>()
const periods = computed(() => [
  { label: '今日估算用电', summary: props.today },
  { label: '本月估算', summary: props.month },
])
const openPeriod = ref<string | null>(null)

/** 同一时刻只展开一个统计说明，关闭旧弹层时不影响后来打开的弹层。 */
function updatePopover(label: string, show: boolean) {
  if (show) openPeriod.value = label
  else if (openPeriod.value === label) openPeriod.value = null
}

/** 优先用 Escape 关闭统计说明，并阻止事件继续作用于外层界面。 */
function handleKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && openPeriod.value !== null) {
    openPeriod.value = null
    event.stopPropagation()
    event.preventDefault()
  }
}

/** 解释后端返回的有效区间和覆盖率，明确缺失及充值变化不参与估算。 */
function coverageDetail(summary: UsageSummary | null): string {
  if (!summary) return props.error ? '统计数据暂不可用，请稍后重试。' : '尚无可用的估算数据。'
  const coverage = new Intl.NumberFormat('zh-CN', { style: 'percent', maximumFractionDigits: 1 }).format(summary.coverage)
  return `有效区间 ${summary.valid_intervals} / ${summary.expected_intervals}，覆盖率 ${coverage}。缺失或充值变化区间不计入估算。`
}
</script>

<template>
  <section class="dashboard-card summary-card" aria-label="估算用电摘要" :aria-busy="loading" @keydown="handleKeydown">
    <dl class="summary-values">
      <div v-for="period in periods" :key="period.label">
        <dt>{{ period.label }}</dt>
        <dd>
          <NSkeleton v-if="loading && !period.summary" width="100px" height="28px" :sharp="false" aria-hidden="true" />
          <template v-else>
            <strong class="summary-number">{{ period.summary?.estimated_kwh != null ? formatNumber(period.summary.estimated_kwh) : '—' }}</strong>
            <span>kWh</span>
            <NPopover v-if="period.summary?.estimated_kwh == null || period.summary.quality !== 'complete'" trigger="click" placement="bottom" :width="240" :show="openPeriod === period.label" @update:show="updatePopover(period.label, $event)">
              <template #trigger>
                <button type="button" class="quality-badge" :aria-expanded="openPeriod === period.label" :aria-label="`${period.label}：${period.summary?.estimated_kwh != null ? '部分数据' : '缺失数据'}，查看说明`">
                  {{ period.summary?.estimated_kwh != null ? '部分数据' : '缺失数据' }}
                </button>
              </template>
              <p class="summary-detail">{{ coverageDetail(period.summary) }}</p>
            </NPopover>
          </template>
        </dd>
      </div>
    </dl>
    <p v-if="error && (today || month)" class="summary-request-error" role="alert">{{ error }}，已保留上次估算</p>
    <span v-if="loading && !today && !month" class="sr-only" role="status">正在加载估算用电</span>
  </section>
</template>
