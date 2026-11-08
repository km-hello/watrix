<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { NRadioButton, NRadioGroup } from 'naive-ui'
import { init, use, type EChartsType } from 'echarts/core'
import { BarChart, LineChart } from 'echarts/charts'
import { AriaComponent, GridComponent, MarkPointComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import { formatNumber, formatTimestamp } from '../format'
import type { Series, SeriesMetric, SeriesPoint, SeriesRange } from '../types/api'

use([LineChart, BarChart, GridComponent, TooltipComponent, MarkPointComponent, AriaComponent, CanvasRenderer])

const props = defineProps<{ series: Series | null; loading: boolean; error: string | null }>()
const range = defineModel<SeriesRange>('range', { required: true })
const metric = defineModel<SeriesMetric>('metric', { required: true })
const chartElement = ref<HTMLDivElement | null>(null)
let chart: EChartsType | undefined
let observer: ResizeObserver | undefined
let motion: MediaQueryList | undefined
const points = computed(() => props.series?.points ?? [])
const validPoints = computed(() => points.value.filter(point => point.value !== null))
const missingCount = computed(() => points.value.filter(point => point.value === null).length)
const partialCount = computed(() => points.value.filter(point => point.quality === 'partial').length)
const rechargeCount = computed(() => points.value.filter(point => point.recharge).length)
// 单个点不足以表达趋势；剩余电量以定时样本数量判断，避免手动刷新制造连续趋势。
const waiting = computed(() => validPoints.value.length > 0 && (metric.value === 'remaining'
  ? validPoints.value.filter(point => point.kind === 'scheduled').length < 2
  : validPoints.value.length < 2))
const description = computed(() => {
  const label = metric.value === 'remaining' ? '剩余电量' : '估算用电'
  return `${label}趋势，${validPoints.value.length} 个可用数据点，${missingCount.value} 个缺失区间，${partialCount.value} 个部分数据区间，${rechargeCount.value} 处充值变化。缺失区间不按零计算。`
})
const emptyMessage = computed(() => props.error || (props.loading ? '正在加载趋势' : '暂无可用趋势数据'))

/** 按业务时区描述数据点及质量，null 保留为缺失，覆盖率沿用后端计算结果。 */
function pointTooltip(point: SeriesPoint, timezone: string): string {
  const lines = [formatTimestamp(point.timestamp, timezone), `${point.value === null ? '—' : formatNumber(point.value)} kWh`]
  if (point.quality === 'partial') lines.push('部分数据')
  if (point.reason === 'gap') lines.push('采集缺失，无法完整估算')
  if (point.reason === 'recharge_or_adjustment' || point.recharge) lines.push('充值变化或余额调整')
  if (point.coverage !== undefined) {
    lines.push(`有效区间 ${point.valid_intervals ?? 0} / ${point.expected_intervals ?? 0}，覆盖率 ${Math.round(point.coverage * 100)}%`)
  }
  return lines.join('\n')
}

/** 把当前响应映射为剩余电量折线或估算用电柱图，并完整替换图表配置。 */
function renderChart() {
  if (!chart) return
  const data = props.series
  const visible = validPoints.value.length > 0
  const timezone = data?.timezone
  const times = points.value.map(point => new Date(point.timestamp).getTime())
  const firstTime = times.length ? Math.min(...times) : undefined
  const lastTime = times.length ? Math.max(...times) : undefined
  const remaining = metric.value === 'remaining'
  // 剩余图标记电量上升；用量图仅用“—”提示缺失，不把缺失区间变成零用电。
  const markers = remaining
    ? points.value.filter(point => point.recharge && point.value !== null).map(point => ({
      coord: [new Date(point.timestamp).getTime(), point.value], point, value: '充值变化', symbol: 'circle', symbolSize: 8,
      label: { show: true, formatter: '充值变化', position: 'top' as const, fontSize: 11, color: '#2F8C59' },
    }))
    : points.value.filter(point => point.value === null).map(point => ({
      coord: [new Date(point.timestamp).getTime(), 0], point, value: '—', symbol: 'circle', symbolSize: 20,
      itemStyle: { color: 'transparent' },
      label: { show: true, formatter: '—', position: 'top' as const, color: '#718276' },
    }))
  chart.setOption({
    animation: !motion?.matches,
    color: ['#40A970'],
    textStyle: { fontFamily: 'Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif' },
    aria: { enabled: true, label: { description: description.value } },
    grid: { left: 12, right: 16, top: 26, bottom: 12, containLabel: true },
    tooltip: {
      // 使用 Canvas 富文本展示提示，响应内容不会作为 HTML 插入页面。
      trigger: 'axis', triggerOn: 'mousemove|click', confine: true, renderMode: 'richText',
      formatter: (params: unknown) => {
        const item = (Array.isArray(params) ? params[0] : params) as { data?: { point?: SeriesPoint } } | undefined
        return item?.data?.point && timezone ? pointTooltip(item.data.point, timezone) : ''
      },
    },
    xAxis: {
      type: 'time', show: visible, min: firstTime === lastTime && lastTime !== undefined ? lastTime - 3600000 : firstTime, max: lastTime,
      splitNumber: range.value === '24h' ? 4 : 5,
      axisLabel: { color: '#718276', hideOverlap: true, formatter: (value: number) => {
        if (!timezone) return '—'
        return new Intl.DateTimeFormat('zh-CN', {
          timeZone: timezone,
          ...(range.value === '24h' ? { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' } as const : { month: 'numeric', day: 'numeric' } as const),
        }).format(new Date(value))
      } },
      axisLine: { lineStyle: { color: '#E1EBE3' } }, axisTick: { show: false }, splitLine: { show: false },
    },
    yAxis: { type: 'value', show: visible, scale: remaining, min: remaining ? undefined : 0, axisLabel: { color: '#718276' }, splitLine: { lineStyle: { color: '#E1EBE3' } } },
    series: [{
      name: remaining ? '剩余电量' : '估算用电', type: remaining ? 'line' : 'bar',
      data: points.value.map(point => ({ value: [new Date(point.timestamp).getTime(), point.value], point })),
      // 缺失点断开连线；数据不足时仅保留点，避免暗示已知的连续变化。
      connectNulls: false, smooth: false, showSymbol: true, symbolSize: 6,
      lineStyle: { width: 2, opacity: waiting.value ? 0 : 1 },
      areaStyle: remaining ? { opacity: waiting.value ? 0 : 0.08 } : undefined,
      barMaxWidth: 24, markPoint: { tooltip: { trigger: 'item' }, data: markers },
    }],
  // 切换指标时同时替换旧的图形、标记和坐标配置，避免 ECharts 合并残留。
  }, { notMerge: true })
}

watch(() => [props.series, props.loading, props.error, metric.value, range.value], renderChart)
// 图表尺寸和减少动态效果偏好独立监听，组件释放时成对解除。
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
  <section class="dashboard-card trend-card" aria-labelledby="trend-title" :aria-busy="loading">
    <div class="trend-header">
      <h2 id="trend-title">用电趋势</h2>
      <NRadioGroup v-model:value="range" class="trend-range" name="trend-range" aria-label="趋势时间范围" size="large" :theme-overrides="{ buttonHeightLarge: '44px' }">
        <NRadioButton value="24h">24小时</NRadioButton><NRadioButton value="7d">7天</NRadioButton><NRadioButton value="30d">30天</NRadioButton>
      </NRadioGroup>
    </div>
    <div class="trend-metric-row">
      <select v-model="metric" class="trend-metric-select" aria-label="趋势指标">
        <option value="remaining">剩余电量</option>
        <option value="consumption">估算用电</option>
      </select>
      <span class="trend-unit">kWh</span>
    </div>
    <p v-if="error && validPoints.length" class="trend-request-error" role="alert">{{ error }}，已保留上次趋势</p>
    <p v-else-if="loading && validPoints.length" class="trend-note" role="status">正在读取最新趋势</p>
    <div class="trend-plot">
      <div ref="chartElement" class="trend-canvas" role="img" :aria-label="description" />
      <p v-if="!validPoints.length" class="trend-overlay empty-state" :role="error ? 'alert' : 'status'">{{ emptyMessage }}</p>
    </div>
    <p v-if="waiting || missingCount || partialCount || rechargeCount" class="trend-note">
      <span v-if="waiting">等待更多采集数据</span>
      <span v-if="missingCount">含缺失区间<span v-if="metric === 'consumption'">，— 表示无法估算</span></span>
      <span v-if="partialCount">含部分数据</span>
      <span v-if="rechargeCount">含充值变化</span>
    </p>
    <p class="sr-only">{{ description }}</p>
  </section>
</template>
