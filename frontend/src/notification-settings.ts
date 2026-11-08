import type { NotificationInput, Settings } from './types/api'

/** 表单草稿以文本保存 Topic ID，允许空输入和编辑中的未完成值。 */
export interface NotificationDraft extends Omit<NotificationInput, 'telegram_topic_id'> {
  telegram_topic_id: string
}

/** 将已保存配置转换为可编辑草稿，空 Topic ID 对应空字符串。 */
export function draftFromSettings(settings: Settings): NotificationDraft {
  return {
    telegram_chat_id: settings.telegram_chat_id,
    telegram_topic_id: settings.telegram_topic_id === null ? '' : String(settings.telegram_topic_id),
    daily_enabled: settings.daily_enabled, daily_time: settings.daily_time,
    low_enabled: settings.low_enabled, low_threshold_kwh: settings.low_threshold_kwh,
  }
}

/** 把非负电量规范成四位小数字符串；超出精度或服务端 int64 范围时返回 null。 */
function normalizeThreshold(value: string): string | null {
  if (!/^\d+(?:\.\d+)?$/.test(value)) return null
  const [whole = '', rawFraction = ''] = value.split('.')
  const fraction = rawFraction.replace(/0+$/, '')
  if (fraction.length > 4) return null
  // 使用十进制文本和 BigInt 检查定点整数上限，避免浮点换算损失精度。
  const digits = (whole + fraction.padEnd(4, '0')).replace(/^0+/, '') || '0'
  if (digits.length > 19 || BigInt(digits) > 9223372036854775807n) return null
  const padded = digits.padStart(5, '0')
  return `${padded.slice(0, -4)}.${padded.slice(-4)}`
}

/** 校验草稿并生成规范化请求；存在字段错误时 input 为 null，供表单定位错误。 */
export function validateNotification(draft: NotificationDraft) {
  const errors: Partial<Record<keyof NotificationDraft, string>> = {}
  const chat = draft.telegram_chat_id.trim()
  if (chat && (chat.length > 64 || (chat.startsWith('@') ? !/^@[A-Za-z0-9_]+$/.test(chat)
    : !/^-?\d+$/.test(chat) || BigInt(chat) === 0n || BigInt(chat) < -9223372036854775808n || BigInt(chat) > 9223372036854775807n))) {
    errors.telegram_chat_id = '请输入非零整数或 @用户名，最多 64 个字符'
  }
  const topic = draft.telegram_topic_id.trim()
  if (topic && (!/^\d+$/.test(topic) || BigInt(topic) < 1n || BigInt(topic) > 2147483647n)) {
    errors.telegram_topic_id = '请输入 1–2147483647 的整数，或留空'
  }
  if (!/^(?:[01]\d|2[0-3]):[0-5]\d$/.test(draft.daily_time)) errors.daily_time = '请输入 HH:mm 格式的时间'
  const threshold = normalizeThreshold(draft.low_threshold_kwh)
  if (threshold === null) errors.low_threshold_kwh = '请输入非负电量，最多 4 位有效小数，上限 922337203685477.5807'
  // 只有全部字段有效才生成提交对象，Topic ID 的范围允许安全转成 Number。
  const input: NotificationInput | null = Object.keys(errors).length ? null : {
    telegram_chat_id: chat, telegram_topic_id: topic ? Number(topic) : null,
    daily_enabled: draft.daily_enabled, daily_time: draft.daily_time,
    low_enabled: draft.low_enabled, low_threshold_kwh: threshold!,
  }
  return { errors, input }
}
