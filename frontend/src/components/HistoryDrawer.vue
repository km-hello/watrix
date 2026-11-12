<script setup lang="ts">
import { onUnmounted, ref, watch } from 'vue'
import { NDrawer, NSkeleton } from 'naive-ui'
import { X } from '@lucide/vue'
import { getReadings } from '../api'
import type { ReadingsPage, ReadingsRange } from '../types/api'
import RecordsTable from './RecordsTable.vue'

const show = defineModel<boolean>('show', { required: true })
const range = ref<ReadingsRange>('7d')
const page = ref<ReadingsPage | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)
let request: AbortController | null = null
let sequence = 0

/** 读取或追加历史分页；更换范围会替换数据，追加失败则保留已加载记录供重试。 */
async function loadPage(append = false) {
  if (!show.value || append && (loading.value || !page.value?.next_before_id)) return
  const cursor = append ? page.value!.next_before_id : null
  request?.abort()
  const controller = new AbortController()
  request = controller
  // 请求取消后仍可能返回，用序号隔离旧范围或已关闭抽屉的响应。
  const current = ++sequence
  if (!append) page.value = null
  loading.value = true
  error.value = null
  try {
    const result = await getReadings({ range: range.value, limit: 50, ...(cursor !== null ? { before_id: cursor } : {}) }, controller.signal)
    if (current === sequence) page.value = append && page.value ? { ...result, items: [...page.value.items, ...result.items] } : result
  } catch (cause) {
    if (current === sequence && !controller.signal.aborted) error.value = (cause as Error).message
  } finally {
    if (current === sequence) loading.value = false
  }
}

// 每次打开重新读取；关闭时使请求失效并恢复默认范围。
watch(show, open => {
  if (open) void loadPage()
  else {
    sequence++
    request?.abort()
    loading.value = false
    range.value = '7d'
  }
})
watch(range, () => { if (show.value) void loadPage() })
onUnmounted(() => { sequence++; request?.abort() })
</script>

<template>
  <NDrawer v-model:show="show" width="min(640px, 100vw)" placement="right" class="history-drawer" aria-labelledby="history-title" :trap-focus="true" :auto-focus="true" :close-on-esc="true" :mask-closable="true">
    <div class="history-panel">
      <header class="history-header"><h2 id="history-title">采集记录</h2><button type="button" tabindex="0" class="dialog-close" aria-label="关闭采集记录" @click="show = false"><X :size="20" aria-hidden="true" /></button></header>
      <fieldset class="history-ranges">
        <legend class="sr-only">记录范围</legend>
        <label><input v-model="range" class="sr-only" type="radio" name="history-range" value="today"><span>今天</span></label>
        <label><input v-model="range" class="sr-only" type="radio" name="history-range" value="7d"><span>7天</span></label>
        <label><input v-model="range" class="sr-only" type="radio" name="history-range" value="30d"><span>30天</span></label>
      </fieldset>
      <div class="history-body" :aria-busy="loading">
        <RecordsTable v-if="page?.items.length" :items="page.items" :timezone="page.timezone" />
        <div v-else-if="loading" class="records-skeleton"><NSkeleton v-for="row in 4" :key="row" height="32px" width="100%" :sharp="false" aria-hidden="true" /><span class="sr-only" role="status">正在加载采集记录</span></div>
        <p v-else-if="!error" class="records-empty empty-state">所选范围暂无采集记录</p>
        <p v-if="error" class="records-error" role="alert">{{ error }}<span v-if="page?.items.length">，已保留已加载记录</span></p>
        <div v-if="error || page?.next_before_id" class="history-pagination">
          <button type="button" tabindex="0" class="read-retry-button" :aria-label="error ? page ? '重试加载更多' : '重试读取记录' : '加载更多'" :disabled="loading" :aria-busy="loading" @click="loadPage(page !== null)">{{ loading ? '正在加载更多' : error ? page ? '重试加载更多' : '重试读取记录' : '加载更多' }}</button>
        </div>
      </div>
    </div>
  </NDrawer>
</template>
