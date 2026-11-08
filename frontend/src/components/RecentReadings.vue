<script setup lang="ts">
import { NSkeleton } from 'naive-ui'
import { ChevronRight } from '@lucide/vue'
import RecordsTable from './RecordsTable.vue'
import type { ReadingsPage } from '../types/api'

// 首页仅展示传入的最近记录，历史分页由抽屉管理，共用同一记录表格。
defineProps<{ page: ReadingsPage | null; loading: boolean; error: string | null }>()
defineEmits<{ openHistory: [] }>()
</script>

<template>
  <section class="dashboard-card records-card" aria-labelledby="records-title" :aria-busy="loading">
    <div class="records-heading"><h2 id="records-title">最近记录</h2><button type="button" class="all-records-button" @click="$emit('openHistory')">全部记录<ChevronRight :size="16" aria-hidden="true" /></button></div>
    <p v-if="error" class="records-error" role="alert">{{ error }}</p>
    <RecordsTable v-if="page?.items.length" :items="page.items" :timezone="page.timezone" />
    <div v-else-if="loading" class="records-skeleton">
      <NSkeleton v-for="row in 4" :key="row" height="32px" width="100%" :sharp="false" aria-hidden="true" />
      <span class="sr-only" role="status">正在加载采集记录</span>
    </div>
    <p v-else-if="!error" class="records-empty empty-state">暂无采集记录</p>
  </section>
</template>
