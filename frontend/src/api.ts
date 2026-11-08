import type { Dashboard, NotificationInput, ReadingsPage, ReadingsQuery, Series, SeriesMetric, SeriesRange, Settings } from './types/api'

/** 接口错误只携带面向页面的安全信息；status 为 0 表示结果未确认，可能源于网络、取消或响应解析失败。 */
export class ApiError extends Error {
  /** 保留状态码及两种限流秒数表示，供普通倒计时和超长等待分别使用。 */
  constructor(message: string, readonly status = 0, readonly retryAfterSeconds = 0, readonly retryAfter: string | null = null) {
    super(message)
  }
}

/** 执行带超时和取消的同源请求；empty 允许成功的 204，写操作失败不会自动重试。 */
async function requestJSON<T>(path: string, signal: AbortSignal | undefined, message: string, init: RequestInit = {}, empty = false): Promise<T> {
  // 将调用方取消和本地超时汇入同一信号，并在结束时移除监听。
  const controller = new AbortController()
  /** 将调用方的取消转发给当前请求。 */
  const abort = () => controller.abort()
  signal?.addEventListener('abort', abort, { once: true })
  if (signal?.aborted) abort()
  const timeout = window.setTimeout(abort, 15000)
  try {
    const response = await fetch(path, { ...init, signal: controller.signal, headers: { Accept: 'application/json', ...init.headers } })
    if (empty && response.status === 204) return undefined as T
    if (!response.ok || response.status === 204) {
      const retry = response.headers.get('Retry-After') ?? ''
      const rawRetry = /^\d+$/.test(retry) ? retry : null
      // 原始整数文本供 BigInt 使用，普通秒数则限制在安全整数范围内。
      const seconds = rawRetry ? Number(BigInt(rawRetry) > BigInt(Number.MAX_SAFE_INTEGER) ? BigInt(Number.MAX_SAFE_INTEGER) : BigInt(rawRetry)) : 0
      const body = await response.json().catch(() => null) as { error?: { code?: unknown } } | null
      // 仅映射允许的错误码，避免直接显示服务端或代理返回的任意错误文本。
      const messages: Record<string, string> = {
        notification_not_configured: '请先配置服务端 Telegram 并保存 Chat ID',
        notification_rejected: 'Telegram 拒绝发送，请检查已保存的通知设置',
        notification_uncertain: '发送结果未确认，请先检查 Telegram，避免重复发送',
        notification_rate_limited: 'Telegram 请求过于频繁，请稍后再试',
        invalid_settings: '通知设置无效，请检查输入',
      }
      const rawCode = body?.error?.code
      const code = typeof rawCode === 'string' && Object.hasOwn(messages, rawCode) ? rawCode : ''
      throw new ApiError(messages[code] ?? (response.status === 429 ? '刷新过于频繁，请稍后再试' : message), response.status, seconds, rawRetry)
    }
    return await response.json() as T
  } catch (cause) {
    if (cause instanceof ApiError) throw cause
    // 网络失败、取消及无效响应都无法确认写操作结果，统一交给调用方处理。
    throw new ApiError(message)
  } finally {
    window.clearTimeout(timeout)
    signal?.removeEventListener('abort', abort)
  }
}

/** 读取概览快照，包含余额、统计摘要和数据过期时间。 */
export function getDashboard(signal?: AbortSignal): Promise<Dashboard> {
  return requestJSON('/api/dashboard', signal, '读取用电数据失败，请稍后重试')
}

/** 按范围和指标读取趋势，由后端统一计算用量及数据质量。 */
export function getSeries(range: SeriesRange, metric: SeriesMetric, signal?: AbortSignal): Promise<Series> {
  const query = new URLSearchParams({ range, metric })
  return requestJSON(`/api/series?${query}`, signal, '读取用电趋势失败，请稍后重试')
}

/** 读取历史记录；before_id 用于继续向较旧记录分页。 */
export function getReadings(query: ReadingsQuery = {}, signal?: AbortSignal): Promise<ReadingsPage> {
  const params = new URLSearchParams()
  if (query.range) params.set('range', query.range)
  if (query.limit !== undefined) params.set('limit', String(query.limit))
  if (query.before_id !== undefined) params.set('before_id', String(query.before_id))
  return requestJSON(`/api/readings${params.size ? `?${params}` : ''}`, signal, '读取采集记录失败，请稍后重试')
}

/** 触发一次手动采集；成功无响应体，调用方随后重新读取页面数据。 */
export function collect(signal?: AbortSignal): Promise<void> {
  return requestJSON('/api/collect', signal, '采集请求失败，请稍后读取最新数据', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}',
  }, true)
}

/** 读取已保存设置及采集信息，服务端密钥不会返回前端。 */
export function getSettings(signal?: AbortSignal): Promise<Settings> {
  return requestJSON('/api/settings', signal, '读取设置失败，请稍后重试')
}

/** 保存完整通知配置；未确认的网络结果需先重新读取，避免盲目重复提交。 */
export function saveSettings(input: NotificationInput, signal?: AbortSignal): Promise<void> {
  return requestJSON('/api/settings', signal, '保存结果未确认，请重新读取设置后再试', {
    method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input),
  }, true)
}

/** 按已保存的配置发送测试通知，结果未确认时由用户检查实际送达情况。 */
export function testNotification(signal?: AbortSignal): Promise<void> {
  return requestJSON('/api/notifications/test', signal, '发送结果未确认，请先检查 Telegram，避免重复发送', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}',
  }, true)
}
