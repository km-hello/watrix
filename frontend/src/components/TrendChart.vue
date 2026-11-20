<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { NCheckbox, NPopover, NRadioButton, NRadioGroup } from 'naive-ui'
import { ChartColumn, ChartLine, ChevronDown, Info } from '@lucide/vue'
import { init, use, type EChartsType } from 'echarts/core'
import { BarChart, LineChart } from 'echarts/charts'
import { AriaComponent, GridComponent, LegendComponent, MarkPointComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import { formatNumber, formatTimestamp } from '../format'
import type { Series, SeriesMetric, SeriesPoint, SeriesRange } from '../types/api'

use([LineChart, BarChart, GridComponent, LegendComponent, TooltipComponent, MarkPointComponent, AriaComponent, CanvasRenderer])
const props = defineProps<{ series: Record<SeriesMetric, Series> | null; loading: boolean; error: string | null }>()
const range = defineModel<SeriesRange>('range', { required: true })
const selected = ref<Record<SeriesMetric, boolean>>({ remaining: true, consumption: true })
const metrics = [
  { key: 'remaining', label: '剩余电量', color: '#278263' },
  { key: 'consumption', label: '估算用电', color: '#AFD3BE' },
] as const
const shown = computed(() => metrics.filter(metric => selected.value[metric.key]).map(metric => {
  const points = props.series?.[metric.key].points ?? []
  const valid = points.filter(point => point.value !== null)
  // 手动采集不计入应采区间，避免刷新次数稀释缺失占比。
  const total = metric.key === 'remaining' && range.value === '24h' ? points.filter(point => point.kind === 'scheduled').length : points.length
  const missing = points.filter(point => point.value === null).length
  const partial = points.filter(point => point.quality === 'partial').length
  const recharge = points.filter(point => point.recharge).length
  const waiting = valid.length > 0 && (metric.key === 'remaining' ? valid.filter(point => point.kind === 'scheduled').length : valid.length) < 2
  return { ...metric, points, valid, total, missing, partial, recharge, waiting }
}))
const selectorLabel = computed(() => shown.value.length === 2 ? '全部指标' : shown.value[0]?.label ?? '选择指标')
const validPoints = computed(() => shown.value.flatMap(metric => metric.valid))
const points = computed(() => shown.value.flatMap(metric => metric.points))
const qualityUnit = computed(() => range.value === '24h' ? '区间' : '天数')
function qualityRatio(count: number, total: number): string {
  const percent = new Intl.NumberFormat('zh-CN', { style: 'percent', maximumFractionDigits: 1 }).format(total ? count / total : 0)
  return `${count} / ${total}（${percent}）`
}
const description = computed(() => shown.value.length ? shown.value.map(metric =>
  `${metric.label}趋势，${range.value === '24h' ? '有效区间' : '可用天数'}：${qualityRatio(metric.total - metric.missing, metric.total)}，缺失${qualityUnit.value}：${qualityRatio(metric.missing, metric.total)}，可用数据点：${metric.valid.length}，充值变化：${metric.recharge} ${range.value === '24h' ? '处' : '天'}`,
).join('；') : '选择指标查看趋势')
const emptyMessage = computed(() => !shown.value.length ? '选择指标查看趋势' : props.error || (props.loading ? '正在加载趋势' : '暂无可用趋势数据'))
const qualityOpen = ref(false)
const selectorOpen = ref(false)
const selectorButton = ref<HTMLButtonElement | null>(null)
const selectorOptions = ref<HTMLDivElement | null>(null)
watch(selectorOpen, async open => {
  if (open) {
    await nextTick()
    selectorOptions.value?.querySelector<HTMLElement>('[role="checkbox"]')?.focus()
  }
})
const qualityLabel = computed(() => {
  if (shown.value.some(metric => metric.missing || metric.partial)) return validPoints.value.length ? '部分数据' : '缺失数据'
  if (shown.value.some(metric => metric.waiting)) return '等待采集'
  return shown.value.some(metric => metric.recharge) ? '充值变化' : ''
})
const chartElement = ref<HTMLDivElement | null>(null)
let chart: EChartsType | undefined
let observer: ResizeObserver | undefined
let motion: MediaQueryList | undefined
watch(() => [range.value, selected.value.remaining, selected.value.consumption, props.series], () => { qualityOpen.value = false })

/** 各指标保留自己的采集时刻，避免将手动读数与相邻小时用量误标为同时发生。 */
function pointTooltip(params: unknown): string {
  type Item = { seriesId?: string; marker?: string; data?: { point?: SeriesPoint; metric?: SeriesMetric } }
  const items = (Array.isArray(params) ? params : [params]) as Item[]
  const entries = items.flatMap(item => {
    const metric = shown.value.find(metric => metric.key === (item.data?.metric ?? item.seriesId))
    return metric && item.data?.point ? [{ metric, point: item.data.point, marker: item.marker ?? '' }] : []
  })
  const timezone = props.series?.remaining.timezone
  if (!entries.length || !timezone) return ''
  const sameTime = entries.every(entry => entry.point.timestamp === entries[0]!.point.timestamp)
  const lines = sameTime ? [formatTimestamp(entries[0]!.point.timestamp, timezone, range.value === '24h')] : []
  for (const { metric, point, marker } of entries) {
    if (!sameTime) lines.push(formatTimestamp(point.timestamp, timezone, range.value === '24h'))
    lines.push(`${marker} ${metric.label}  ${point.value === null ? '—' : formatNumber(point.value)} kWh · ¥${point.amount_yuan ?? '—'}`)
    if (point.reason === 'recharge_or_adjustment' || point.recharge) lines.push('充值变化或余额调整')
    if (point.coverage !== undefined) lines.push(`有效区间：${point.valid_intervals ?? 0} / ${point.expected_intervals ?? 0}（${Math.round(point.coverage * 100)}%）`)
  }
  return lines.join('\n')
}

function renderChart() {
  if (!chart) return
  const visible = validPoints.value.length > 0
  const timezone = props.series?.remaining.timezone
  const times = points.value.map(point => new Date(point.timestamp).getTime())
  const firstTime = times.length ? Math.min(...times) : undefined
  const lastTime = times.length ? Math.max(...times) : undefined
  chart.setOption({
    animation: !motion?.matches,
    textStyle: { fontFamily: 'Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif' },
    aria: { enabled: true, label: { description: description.value } },
    grid: { left: 8, right: 8, top: 40, bottom: 26, containLabel: true },
    legend: { show: visible, bottom: 0, padding: 0, selectedMode: false, itemWidth: 16, itemHeight: 8, itemGap: 18, textStyle: { color: '#617167', fontSize: 12 }, data: shown.value.map(metric => ({ name: metric.label, ...(metric.key === 'consumption' ? { icon: 'roundRect' } : {}) })) },
    tooltip: {
      // Canvas 富文本不会把响应内容作为 HTML 插入页面。
      trigger: 'axis', triggerOn: 'mousemove|click', confine: true, renderMode: 'richText', formatter: pointTooltip,
      textStyle: { color: '#3E5147' },
    },
    xAxis: {
      type: 'time', show: visible, min: firstTime === lastTime && lastTime !== undefined ? lastTime - 3600000 : firstTime, max: lastTime,
      splitNumber: range.value === '24h' ? 4 : 5,
      axisLabel: { color: '#617167', hideOverlap: true, formatter: (value: number) => {
        if (!timezone) return '—'
        return new Intl.DateTimeFormat('zh-CN', { timeZone: timezone, ...(range.value === '24h' ? { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' } as const : { month: 'numeric', day: 'numeric' } as const) }).format(new Date(value))
      } },
      axisLine: { lineStyle: { color: '#E7EDE9' } }, axisTick: { show: false }, splitLine: { show: false },
    },
    yAxis: shown.value.map((metric, index) => ({
      type: 'value', show: visible, position: index === 0 ? 'left' : 'right', name: `${metric.label} (kWh)`, nameTextStyle: { color: '#617167', fontSize: 11, align: index === 0 ? 'left' : 'right' }, nameGap: 16,
      scale: metric.key === 'remaining', min: metric.key === 'remaining' ? undefined : 0,
      axisLabel: { color: '#617167' }, splitLine: { show: index === 0, lineStyle: { color: '#E7EDE9' } },
    })),
    series: shown.value.map((metric, index) => {
      const remaining = metric.key === 'remaining'
      const markers = remaining ? metric.points.filter(point => point.recharge && point.value !== null).map(point => ({
        coord: [new Date(point.timestamp).getTime(), point.value], point, metric: metric.key, value: '充值变化', symbol: 'circle', symbolSize: 8,
        label: { show: true, formatter: '充值变化', position: 'top', fontSize: 11, color: metric.color },
      })) : metric.points.filter(point => point.value === null).map(point => ({
        coord: [new Date(point.timestamp).getTime(), 0], point, metric: metric.key, value: '—', symbol: 'circle', symbolSize: 20,
        itemStyle: { color: 'transparent' }, label: { show: true, formatter: '—', position: 'top', color: '#617167' },
      }))
      return {
        id: metric.key, name: metric.label, type: remaining ? 'line' : 'bar', yAxisIndex: index,
        data: metric.points.map(point => ({ value: [new Date(point.timestamp).getTime(), point.value], point, metric: metric.key })),
        itemStyle: { color: metric.color, ...(remaining ? {} : { borderRadius: [2, 2, 0, 0] }) },
        connectNulls: false, smooth: false, showSymbol: true, symbol: 'emptyCircle', symbolSize: 6,
        lineStyle: { color: metric.color, width: 2, opacity: metric.waiting ? 0 : 1 }, z: remaining ? 3 : 2,
        barMaxWidth: 24, markPoint: { tooltip: { trigger: 'item' }, data: markers },
      }
    }),
  }, { notMerge: true })
}
watch(() => [props.series, props.loading, props.error, selected.value.remaining, selected.value.consumption, range.value], renderChart)
onMounted(() => {
  if (!chartElement.value) return
  motion = window.matchMedia('(prefers-reduced-motion: reduce)')
  motion.addEventListener('change', renderChart)
  chart = init(chartElement.value)
  observer = new ResizeObserver(() => chart?.resize())
  observer.observe(chartElement.value)
  renderChart()
})
onUnmounted(() => {
  observer?.disconnect()
  motion?.removeEventListener('change', renderChart)
  chart?.dispose()
})
</script>

<template>
  <section class="dashboard-card trend-card" aria-labelledby="trend-title" :aria-busy="loading" @keydown.esc.stop="qualityOpen = false; selectorOpen = false">
    <div class="trend-header">
      <div class="trend-heading">
        <h2 id="trend-title">用电趋势</h2>
        <NPopover v-if="qualityLabel" v-model:show="qualityOpen" trigger="click" placement="bottom-start" :width="280" style="max-width: calc(100vw - 32px)">
          <template #trigger>
            <button type="button" class="trend-quality" :aria-expanded="qualityOpen" :aria-label="`${qualityLabel}，查看详情`"><Info :size="15" aria-hidden="true" /><span class="trend-quality-label">{{ qualityLabel }}</span></button>
          </template>
          <div class="trend-quality-detail">
            <div v-for="metric in shown" :key="metric.key" class="trend-quality-metric">
              <strong>{{ metric.label }}</strong>
              <p>{{ range === '24h' ? '有效区间' : '可用天数' }}：{{ qualityRatio(metric.total - metric.missing, metric.total) }}</p>
              <p v-if="metric.missing">缺失{{ qualityUnit }}：{{ qualityRatio(metric.missing, metric.total) }}</p>
              <p v-if="metric.waiting && range === '24h'">可用数据点：{{ metric.valid.length }}</p>
              <p v-if="metric.recharge">充值变化：{{ metric.recharge }} {{ range === '24h' ? '处' : '天' }}</p>
            </div>
          </div>
        </NPopover>
      </div>
      <div class="trend-controls">
        <NPopover v-model:show="selectorOpen" trigger="click" placement="bottom-start">
          <template #trigger>
            <button ref="selectorButton" type="button" class="trend-metric-select" aria-label="选择趋势指标" :aria-expanded="selectorOpen"><span>{{ selectorLabel }}</span><ChevronDown :size="14" aria-hidden="true" /></button>
          </template>
          <div ref="selectorOptions" class="trend-metric-options" role="group" aria-label="显示指标" @keydown.esc.stop="selectorOpen = false; selectorButton?.focus()">
            <NCheckbox v-for="metric in metrics" :key="metric.key" v-model:checked="selected[metric.key]">
              <span class="trend-metric-option"><ChartLine v-if="metric.key === 'remaining'" :size="16" :color="metric.color" aria-hidden="true" /><ChartColumn v-else :size="16" :color="metric.color" aria-hidden="true" />{{ metric.label }}</span>
            </NCheckbox>
          </div>
        </NPopover>
        <NRadioGroup v-model:value="range" class="trend-range" name="trend-range" aria-label="趋势时间范围" :theme-overrides="{ buttonHeightMedium: '32px', fontSizeMedium: '13px', buttonColorActive: '#EAF4EE', buttonTextColorActive: '#226E54' }">
          <NRadioButton value="24h">24小时</NRadioButton><NRadioButton value="7d">7天</NRadioButton><NRadioButton value="30d">30天</NRadioButton>
        </NRadioGroup>
      </div>
    </div>
    <p v-if="error && validPoints.length" class="trend-request-error" role="alert">{{ error }}，已保留上次趋势</p>
    <p v-else-if="loading && validPoints.length" class="trend-note" role="status">正在读取最新趋势</p>
    <div class="trend-plot">
      <div ref="chartElement" class="trend-canvas" role="img" :aria-label="description" />
      <p v-if="!validPoints.length" class="trend-overlay empty-state" :role="error && shown.length ? 'alert' : 'status'">{{ emptyMessage }}</p>
    </div>
    <p class="sr-only">{{ description }}</p>
  </section>
</template>
