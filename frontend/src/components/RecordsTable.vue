<script setup lang="ts">
import { ChevronDown } from '@lucide/vue'
import { formatNumber, formatTimestamp } from '../format'
import type { HistoryReading } from '../types/api'

defineProps<{ items: HistoryReading[]; timezone: string }>()

/** 将后端质量标记转换为展示标签，不重新推算用量或充值金额。 */
function tags(item: HistoryReading): string[] {
  const result: string[] = []
  if (item.recharge || item.reason === 'recharge_or_adjustment') result.push('充值')
  if (item.reason === 'gap') result.push('缺失')
  if (item.quality === 'partial') result.push('部分数据')
  return result
}

/** 显示区间估算电量；null 表示无法估算，不按零用电展示。 */
function interval(item: HistoryReading): string {
  return item.estimated_kwh === null ? '—' : `${formatNumber(item.estimated_kwh)} kWh`
}

/** 用 Escape 收起当前移动端记录并恢复焦点，阻止事件继续关闭外层抽屉。 */
function closeDetails(event: KeyboardEvent) {
  const details = event.currentTarget as HTMLDetailsElement
  if (event.key === 'Escape' && details.open) {
    details.open = false
    details.querySelector('summary')?.focus()
    event.stopPropagation()
    event.preventDefault()
  }
}
</script>

<template>
  <table class="records-table records-desktop" aria-label="采集记录">
    <thead>
      <tr><th scope="col">采集时间</th><th scope="col">剩余电量</th><th scope="col">余额</th><th scope="col">区间估算用电</th></tr>
    </thead>
    <tbody>
      <tr v-for="item in items" :key="item.id">
        <td><time :datetime="item.collected_at">{{ formatTimestamp(item.collected_at, timezone) }}</time></td>
        <td>{{ formatNumber(item.remaining_kwh) }} kWh</td>
        <td>¥ {{ formatNumber(item.remaining_yuan) }}</td>
        <td>
          <span class="record-usage">
            <span>{{ interval(item) }}</span>
            <span v-if="item.kind !== 'scheduled' || tags(item).length" class="record-tags">
              <span v-if="item.kind === 'manual'" class="record-tag">手动</span>
              <span v-else-if="item.kind === 'startup'" class="record-tag">启动</span>
              <span v-for="tag in tags(item)" :key="tag" class="record-tag">{{ tag }}</span>
            </span>
          </span>
        </td>
      </tr>
    </tbody>
  </table>

  <div class="records-mobile">
    <div class="records-mobile-heading" aria-hidden="true"><span>采集时间</span><span>剩余电量</span></div>
    <ul class="records-list" aria-label="采集记录">
      <li v-for="item in items" :key="item.id">
        <details class="record-details" @keydown="closeDetails">
          <summary>
            <time :datetime="item.collected_at">{{ formatTimestamp(item.collected_at, timezone) }}</time>
            <span class="record-remaining">{{ formatNumber(item.remaining_kwh) }} kWh<ChevronDown :size="16" class="record-chevron" aria-hidden="true" /></span>
            <span class="sr-only">，查看记录详情</span>
          </summary>
          <div class="record-detail-body">
            <dl>
              <div><dt>余额</dt><dd>¥ {{ formatNumber(item.remaining_yuan) }}</dd></div>
              <div>
                <dt>区间估算用电</dt>
                <dd>
                  <span class="record-usage">
                    <span>{{ interval(item) }}</span>
                    <span v-if="tags(item).length" class="record-tags"><span v-for="tag in tags(item)" :key="tag" class="record-tag">{{ tag }}</span></span>
                  </span>
                </dd>
              </div>
              <div><dt>采集类型</dt><dd>{{ item.kind === 'manual' ? '手动' : item.kind === 'startup' ? '启动' : '定时' }}</dd></div>
            </dl>
            <p v-if="item.reason === 'not_scheduled'" class="record-note">非定时采集不计入区间估算。</p>
            <p v-else-if="item.reason === 'gap'" class="record-note">采集区间缺失，无法估算用电。</p>
            <p v-else-if="item.reason === 'recharge_or_adjustment'" class="record-note">充值变化或余额调整，无法估算该区间用电。</p>
          </div>
        </details>
      </li>
    </ul>
  </div>
</template>
