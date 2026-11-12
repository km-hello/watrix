<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import { NModal } from 'naive-ui'
import { X } from '@lucide/vue'
import { ApiError, getSettings, saveSettings, testNotification } from '../api'
import { formatTimestamp } from '../format'
import type { Settings } from '../types/api'
import { draftFromSettings, validateNotification, type NotificationDraft } from '../notification-settings'

const show = defineModel<boolean>('show', { required: true })
const emit = defineEmits<{ saved: [] }>()
const settingsDialog = ref<HTMLDivElement | null>(null)
const continueButton = ref<HTMLButtonElement | null>(null)
const confirmOpen = ref(false)
let discardPending = false
const settings = ref<Settings | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)
const activeTab = ref('notification')
const collectionTab = ref<HTMLButtonElement | null>(null)
const notificationTab = ref<HTMLButtonElement | null>(null)
const draft = ref<NotificationDraft | null>(null)
const saving = ref(false)
const testing = ref(false)
const attempted = ref(false)
const saveUncertain = ref(false)
const saveMessage = ref<string | null>(null)
const saveFailed = ref(false)
const testMessage = ref<string | null>(null)
const testFailed = ref(false)
const testBlocked = ref(false)
let testDeadline = 0n
let testTimer: number | undefined
let request: AbortController | null = null
let writeRequest: AbortController | null = null
let sequence = 0
const validation = computed(() => draft.value ? validateNotification(draft.value) : null)
// 比较规范化后的请求而非原始输入，忽略等价的数字格式，非法输入仍视为未保存。
const dirty = computed(() => {
  if (!draft.value || !settings.value) return false
  const saved = validateNotification(draftFromSettings(settings.value)).input
  return !validation.value?.input || JSON.stringify(validation.value.input) !== JSON.stringify(saved)
})
const writeBusy = computed(() => saving.value || testing.value)
// 测试通知使用服务端已保存值，草稿修改或保存结果未确认时不能测试。
const canTest = computed(() => settings.value?.telegram_token_configured && settings.value.telegram_chat_id.trim()
  && !dirty.value && !loading.value && !writeBusy.value && !testBlocked.value && !saveUncertain.value)

/** 读取服务端设置；默认保留正在编辑的草稿，resetDraft 用于首次打开或确认保存后重置。 */
async function loadSettings(resetDraft = false) {
  request?.abort()
  const controller = new AbortController()
  request = controller
  const current = ++sequence
  loading.value = true
  error.value = null
  try {
    const result = await getSettings(controller.signal)
    if (current === sequence) {
      settings.value = result
      if (resetDraft || !draft.value) draft.value = draftFromSettings(result)
      saveUncertain.value = false
    }
  } catch (cause) {
    if (current === sequence && !controller.signal.aborted) error.value = (cause as Error).message
  } finally {
    if (current === sequence) loading.value = false
  }
}

/** 统一处理取消、遮罩和 Escape 关闭请求；写入中阻止关闭，脏草稿需先确认。 */
function requestClose() {
  if (writeBusy.value || discardPending || confirmOpen.value) return
  if (dirty.value) confirmOpen.value = true
  else show.value = false
}

/** 先关闭确认弹窗并标记放弃，等待退场完成后再关闭父弹窗。 */
function discardChanges() {
  discardPending = true
  confirmOpen.value = false
}

/** 确认弹窗退场完成后处理放弃操作，让两层焦点锁按顺序释放。 */
function finishConfirmation() {
  if (discardPending) {
    discardPending = false
    show.value = false
  }
}

/** 将组件库发出的隐藏请求接入同一草稿保护流程。 */
function updateShow(value: boolean) {
  if (!value) requestClose()
}

/** 按标签页键盘约定切换分类，并把焦点移到当前标签。 */
function switchTab(event: KeyboardEvent) {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
  event.preventDefault()
  activeTab.value = event.key === 'Home' ? 'collection' : event.key === 'End' ? 'notification'
    : activeTab.value === 'collection' ? 'notification' : 'collection'
  const tab = activeTab.value === 'collection' ? collectionTab.value : notificationTab.value
  tab?.focus()
}

/** 校验并保存草稿，成功后回读规范化配置；结果未确认时阻止重复保存。 */
async function save() {
  if (writeBusy.value || loading.value || !dirty.value || saveUncertain.value) return
  attempted.value = true
  if (!validation.value?.input || !settings.value) {
    // 等错误属性渲染后再定位首个无效输入，便于键盘和移动端用户修正。
    await nextTick()
    const invalid = settingsDialog.value?.querySelector<HTMLInputElement>('[aria-invalid="true"]')
    invalid?.focus()
    invalid?.scrollIntoView({ block: 'nearest' })
    return
  }
  const input = validation.value.input
  saving.value = true
  saveMessage.value = null
  const controller = new AbortController()
  writeRequest = controller
  try {
    await saveSettings(input, controller.signal)
    settings.value = { ...settings.value, ...input }
    saveFailed.value = false
    saveMessage.value = '设置已保存'
    emit('saved')
    await loadSettings(true)
  } catch (cause) {
    if (controller.signal.aborted) return
    saveFailed.value = true
    saveMessage.value = (cause as Error).message
    // 请求未确认可能已经保存，必须先回读配置才能再次提交或发送测试。
    saveUncertain.value = cause instanceof ApiError && cause.status === 0
  } finally {
    saving.value = false
  }
}

/** 按绝对截止时间维护测试通知限流，超长等待分段安排定时器。 */
function updateTestBlock() {
  window.clearTimeout(testTimer)
  const remaining = testDeadline - BigInt(Date.now())
  testBlocked.value = remaining > 0n
  // 保留 BigInt 截止时间，仅将单次定时器延迟转为可安全表示的 Number。
  if (remaining > 0n) testTimer = window.setTimeout(updateTestBlock, Number(remaining > 2147483647n ? 2147483647n : remaining))
}

/** 仅按已保存且未修改的设置发送测试通知，记录成功、未确认或限流结果。 */
async function sendTest() {
  if (!canTest.value) return
  testing.value = true
  testMessage.value = null
  const controller = new AbortController()
  writeRequest = controller
  try {
    await testNotification(controller.signal)
    testFailed.value = false
    testMessage.value = '测试消息已发送'
  } catch (cause) {
    if (controller.signal.aborted) return
    testFailed.value = true
    testMessage.value = (cause as Error).message
    if (cause instanceof ApiError && cause.status === 429) {
      const seconds = cause.retryAfter && BigInt(cause.retryAfter) > 0n ? cause.retryAfter : '60'
      testDeadline = BigInt(Date.now()) + BigInt(seconds) * 1000n
      updateTestBlock()
      testMessage.value = `Telegram 请求过于频繁，请等待 ${seconds} 秒后再试；关闭再打开不会清除限流。`
    }
  } finally {
    testing.value = false
  }
}

// 重新打开重建草稿，但不清除测试限流；关闭只取消读取，不把写入结果当作未发生。
watch(show, open => {
  if (open) {
    activeTab.value = 'notification'
    draft.value = null
    settings.value = null
    attempted.value = false
    saveMessage.value = null
    if (!testBlocked.value) testMessage.value = null
    void loadSettings(true)
  }
  else {
    confirmOpen.value = false
    discardPending = false
    sequence++
    request?.abort()
    loading.value = false
  }
})
onUnmounted(() => {
  sequence++
  request?.abort()
  writeRequest?.abort()
  window.clearTimeout(testTimer)
})
</script>

<template>
  <NModal :show="show" :auto-focus="true" :trap-focus="true" :mask-closable="!writeBusy && !confirmOpen" :close-on-esc="!writeBusy && !confirmOpen" @update:show="updateShow">
    <div ref="settingsDialog" class="settings-dialog" role="dialog" aria-modal="true" aria-labelledby="settings-title" tabindex="-1">
      <header class="settings-header">
        <h2 id="settings-title">设置</h2>
        <button type="button" class="dialog-close" aria-label="关闭设置" :disabled="writeBusy" @click="requestClose"><X :size="20" aria-hidden="true" /></button>
      </header>
      <div v-if="loading || error" class="settings-read-status" :aria-busy="loading">
        <p v-if="loading" class="settings-loading" role="status">正在读取设置</p>
        <div v-if="error" class="settings-load-error">
          <p role="alert">{{ error }}</p>
          <button type="button" class="read-retry-button" :disabled="loading || writeBusy" @click="loadSettings()">重试读取设置</button>
        </div>
      </div>
      <div class="settings-tabs">
        <div class="settings-tab-list" role="tablist" aria-label="设置分类" @keydown="switchTab">
          <button id="settings-tab-collection" ref="collectionTab" type="button" role="tab" class="settings-tab" aria-controls="settings-panel-collection" :aria-selected="activeTab === 'collection'" :tabindex="activeTab === 'collection' ? 0 : -1" @click="activeTab = 'collection'">采集</button>
          <button id="settings-tab-notification" ref="notificationTab" type="button" role="tab" class="settings-tab" aria-controls="settings-panel-notification" :aria-selected="activeTab === 'notification'" :tabindex="activeTab === 'notification' ? 0 : -1" @click="activeTab = 'notification'">通知</button>
        </div>
        <div v-show="activeTab === 'collection'" id="settings-panel-collection" class="settings-tab-panel" role="tabpanel" aria-labelledby="settings-tab-collection">
          <div class="settings-content" :aria-busy="loading">
            <template v-if="settings">
              <dl class="settings-collection">
                <div><dt>电表名称</dt><dd>{{ settings.collection.meter_name || '—' }}</dd></div>
                <div><dt>采集源域名</dt><dd>{{ settings.collection.source_host || '—' }}</dd></div>
                <div><dt>采集频率</dt><dd>每小时 UTC 整点</dd></div>
                <div><dt>时区</dt><dd>{{ settings.timezone }}</dd></div>
                <div><dt>最近成功时间</dt><dd>{{ settings.collection.last_success_at ? formatTimestamp(settings.collection.last_success_at, settings.timezone) : '—' }}</dd></div>
              </dl>
            </template>
          </div>
        </div>
        <div v-show="activeTab === 'notification'" id="settings-panel-notification" class="settings-tab-panel" role="tabpanel" aria-labelledby="settings-tab-notification">
          <form v-if="settings && draft" id="notification-form" class="notification-form" aria-label="通知设置" novalidate @submit.prevent="save">
            <div class="telegram-status"><strong>Telegram</strong><span>{{ settings.telegram_token_configured ? '服务端已配置' : '服务端未配置' }}</span></div>
            <div class="notification-field recipient-field">
              <label for="telegram-chat">Chat ID</label>
              <input id="telegram-chat" v-model="draft.telegram_chat_id" type="text" maxlength="64" autocomplete="off" :disabled="writeBusy || loading" :aria-invalid="attempted && !!validation?.errors.telegram_chat_id" :aria-describedby="attempted && validation?.errors.telegram_chat_id ? 'chat-error' : undefined">
              <p v-if="attempted && validation?.errors.telegram_chat_id" id="chat-error" class="field-error" role="alert">{{ validation.errors.telegram_chat_id }}</p>
            </div>
            <div class="notification-field recipient-field">
              <label for="telegram-topic">Topic ID<span class="optional-label">可选</span></label>
              <input id="telegram-topic" v-model="draft.telegram_topic_id" type="text" inputmode="numeric" autocomplete="off" :disabled="writeBusy || loading" :aria-invalid="attempted && !!validation?.errors.telegram_topic_id" :aria-describedby="attempted && validation?.errors.telegram_topic_id ? 'topic-error' : undefined">
              <p v-if="attempted && validation?.errors.telegram_topic_id" id="topic-error" class="field-error" role="alert">{{ validation.errors.telegram_topic_id }}</p>
            </div>
            <div class="notification-section">
              <label class="notification-label" for="daily-enabled">每日提醒</label>
              <div class="notification-field notification-control">
                <label for="daily-time" class="sr-only">提醒时间</label>
                <input id="daily-time" v-model="draft.daily_time" type="time" step="60" :disabled="writeBusy || loading" :aria-invalid="attempted && !!validation?.errors.daily_time" :aria-describedby="attempted && validation?.errors.daily_time ? 'time-error' : undefined">
                <p v-if="attempted && validation?.errors.daily_time" id="time-error" class="field-error" role="alert">{{ validation.errors.daily_time }}</p>
              </div>
              <input id="daily-enabled" v-model="draft.daily_enabled" class="settings-switch" type="checkbox" role="switch" :disabled="writeBusy || loading">
            </div>
            <div class="notification-section">
              <label class="notification-label" for="low-enabled">低电量提醒</label>
              <div class="notification-field notification-control">
                <label for="low-threshold" class="sr-only">低电量阈值</label>
                <div class="threshold-input"><input id="low-threshold" v-model="draft.low_threshold_kwh" type="text" inputmode="decimal" autocomplete="off" :disabled="writeBusy || loading" :aria-invalid="attempted && !!validation?.errors.low_threshold_kwh" :aria-describedby="attempted && validation?.errors.low_threshold_kwh ? 'threshold-error' : undefined"><span>kWh</span></div>
                <p v-if="attempted && validation?.errors.low_threshold_kwh" id="threshold-error" class="field-error" role="alert">{{ validation.errors.low_threshold_kwh }}</p>
              </div>
              <input id="low-enabled" v-model="draft.low_enabled" class="settings-switch" type="checkbox" role="switch" :disabled="writeBusy || loading">
            </div>
            <p v-if="testMessage" class="notification-feedback" :class="{ 'refresh-error': testFailed }" :role="testFailed ? 'alert' : 'status'">{{ testMessage }}</p>
          </form>
        </div>
      </div>
      <footer class="settings-footer">
        <p v-if="saveMessage && activeTab === 'notification'" class="notification-feedback" :class="{ 'refresh-error': saveFailed }" :role="saveFailed ? 'alert' : 'status'">{{ saveMessage }}</p>
        <button v-if="saveUncertain && activeTab === 'notification'" type="button" class="read-retry-button" :disabled="loading" @click="loadSettings()">重新读取设置</button>
        <div class="settings-footer-actions">
          <button v-if="activeTab === 'notification' && settings && draft" type="button" class="read-retry-button settings-test" aria-label="发送测试消息" :disabled="!canTest" :aria-busy="testing" @click="sendTest">{{ testing ? '正在发送' : '发送测试消息' }}</button>
          <button type="button" class="read-retry-button" :aria-label="activeTab === 'notification' ? '取消' : '关闭'" :disabled="writeBusy" @click="requestClose">{{ activeTab === 'notification' ? '取消' : '关闭' }}</button>
          <button v-if="activeTab === 'notification'" type="submit" form="notification-form" class="settings-save" aria-label="保存设置" :aria-busy="saving" :disabled="!dirty || writeBusy || loading || saveUncertain">{{ saving ? '正在保存' : '保存设置' }}</button>
        </div>
      </footer>
    </div>
  </NModal>
  <NModal v-model:show="confirmOpen" :auto-focus="true" :trap-focus="true" @after-enter="continueButton?.focus()" @after-leave="finishConfirmation">
    <div class="settings-dialog settings-confirm-dialog" role="alertdialog" aria-modal="true" aria-labelledby="discard-title" aria-describedby="discard-description" tabindex="-1">
      <header class="settings-header">
        <h2 id="discard-title">放弃未保存的修改？</h2>
        <button type="button" class="dialog-close" tabindex="0" aria-label="关闭确认" @click="confirmOpen = false"><X :size="20" aria-hidden="true" /></button>
      </header>
      <p id="discard-description" class="settings-confirm-description">关闭后，本次修改将不会保存。</p>
      <footer class="settings-footer">
        <div class="settings-footer-actions">
          <button ref="continueButton" type="button" tabindex="0" class="read-retry-button" @click="confirmOpen = false">继续编辑</button>
          <button type="button" class="settings-save settings-discard" tabindex="0" @click="discardChanges">放弃修改</button>
        </div>
      </footer>
    </div>
  </NModal>
</template>

<style scoped>
/* 设置、通知表单及确认弹窗的私有样式；共享按钮和基础变量由全局提供。 */
.settings-dialog { display: flex; flex-direction: column; width: 560px; max-width: calc(100vw - 32px); max-height: calc(100dvh - 48px); border: 1px solid var(--ui-border); border-radius: 18px; background: var(--ui-surface); color: var(--ui-text); }
.settings-header { display: flex; align-items: center; justify-content: space-between; flex-shrink: 0; gap: 16px; padding: 20px 24px 12px; }
.settings-header h2 { font-size: 24px; }
.settings-tabs { min-height: 0; overflow: auto; padding: 0 24px; }
.settings-tab-list { display: grid; grid-template-columns: 1fr 1fr; gap: 4px; padding: 3px; border: 1px solid var(--ui-border); border-radius: 12px; }
.settings-tab { min-height: 44px; padding: 8px 12px; border: 0; border-radius: 8px; background: var(--ui-surface); color: var(--ui-muted); cursor: pointer; }
.settings-tab[aria-selected="true"] { background: var(--ui-soft-green); color: var(--ui-text); }
.settings-tab-panel { padding-top: 20px; }
.settings-loading { margin: 0 0 16px; color: var(--ui-muted); }
.settings-load-error { margin-bottom: 16px; }
.settings-load-error p { margin: 0 0 12px; color: var(--ui-error); }
.settings-collection { margin: 0; }
.settings-collection > div { display: grid; grid-template-columns: 120px minmax(0, 1fr); gap: 16px; padding: 12px 0; border-bottom: 1px solid var(--ui-border); }
.settings-collection dd { min-width: 0; font-size: 14px; font-weight: 500; overflow-wrap: anywhere; }
.settings-footer { display: flex; flex-direction: column; align-items: stretch; gap: 12px; flex-shrink: 0; padding: 20px 24px; }
.settings-footer-actions { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 12px; }
.settings-test { margin-right: auto; }
.settings-read-status { padding: 0 24px; }
.notification-form { padding-bottom: 4px; }
.telegram-status { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 12px; margin-bottom: 16px; padding-bottom: 12px; border-bottom: 1px solid var(--ui-border); }
.telegram-status strong { font-size: 18px; }
.telegram-status span { color: var(--ui-muted); font-size: 13px; }
.notification-field { display: grid; gap: 8px; margin-bottom: 16px; }
.notification-field > label { color: var(--ui-muted); }
.recipient-field { grid-template-columns: 104px minmax(0, 1fr); gap: 8px 16px; }
.recipient-field > label { align-self: center; color: var(--ui-text); font-weight: 600; }
.optional-label { display: block; color: var(--ui-muted); font-size: 12px; font-weight: 400; }
.recipient-field .field-error { grid-column: 2; }
.notification-control { min-width: 0; margin-bottom: 0; }
.notification-field input { width: 100%; min-width: 0; min-height: 44px; padding: 10px 12px; border: 1px solid var(--ui-border); border-radius: 10px; background: var(--ui-surface); color: var(--ui-text); }
.notification-field input[aria-invalid="true"] { border-color: var(--ui-error); }
.notification-field input:disabled, .settings-switch:disabled { opacity: 0.6; }
.notification-section { display: grid; grid-template-columns: 104px minmax(0, 1fr) 44px; align-items: start; gap: 16px; padding: 16px 0; border-top: 1px solid var(--ui-border); }
.notification-label { display: flex; align-items: center; min-height: 44px; cursor: pointer; font-size: 16px; font-weight: 600; }
.notification-section:last-of-type { border-bottom: 1px solid var(--ui-border); }
.settings-switch { appearance: none; position: relative; flex-shrink: 0; width: 44px; height: 44px; margin: 0; border: 0; border-radius: 10px; background: transparent; cursor: pointer; }
.settings-switch::before { content: ''; position: absolute; width: 44px; height: 24px; top: 10px; left: 0; border: 1px solid var(--ui-border); border-radius: 20px; background: var(--ui-border); }
.settings-switch::after { content: ''; position: absolute; width: 18px; height: 18px; top: 13px; left: 3px; border-radius: 50%; background: var(--ui-surface); }
.settings-switch:checked::before { border-color: var(--ui-primary); background: var(--ui-primary); }
.settings-switch:checked::after { left: 23px; }
.threshold-input { display: flex; align-items: center; min-width: 0; border: 1px solid var(--ui-border); border-radius: 10px; }
.threshold-input input { border: 0; }
.threshold-input span { padding: 0 12px; color: var(--ui-muted); }
.field-error { margin: 0; color: var(--ui-error); font-size: 12px; }
.notification-feedback { margin: 12px 0 0; font-size: 13px; overflow-wrap: anywhere; }
.settings-footer .notification-feedback { margin: 0; }
.settings-save { min-height: 44px; padding: 8px 16px; border: 1px solid var(--ui-primary); border-radius: 10px; background: var(--ui-primary); color: white; cursor: pointer; }
.settings-save:disabled { opacity: 0.5; cursor: default; }
@media (max-width: 767px) {
  .settings-header { padding: 16px 18px 8px; }
  .settings-tabs { padding: 0 18px; }
  .settings-collection > div { grid-template-columns: 104px minmax(0, 1fr); gap: 12px; }
  .settings-footer { padding: 16px 18px; }
  .settings-read-status { padding: 0 18px; }
}
@media (max-width: 420px) {
  .recipient-field { grid-template-columns: 88px minmax(0, 1fr); gap: 8px 12px; }
  .notification-section { grid-template-columns: 88px minmax(0, 1fr) 44px; gap: 12px; }
  .notification-label { font-size: 14px; }
  .notification-field input { padding-right: 8px; padding-left: 8px; }
  .threshold-input span { padding: 0 8px; }
  .settings-footer-actions { gap: 8px; }
  .settings-footer-actions > button { padding-right: 10px; padding-left: 10px; }
}
@media (max-width: 359px) {
  .notification-section { grid-template-columns: minmax(0, 1fr) 44px; }
  .notification-control { grid-column: 1; grid-row: 2; }
  .notification-section > .settings-switch { grid-column: 2; grid-row: 1; }
  .settings-test { flex-basis: 100%; }
}
.settings-confirm-dialog { width: 400px; }
.settings-confirm-description { margin: 0; padding: 0 24px 12px; overflow: auto; }
.settings-discard { border-color: var(--ui-error); background: var(--ui-error); }
@media (max-width: 767px) {
  .settings-confirm-description { padding: 0 18px 12px; }
}
</style>
