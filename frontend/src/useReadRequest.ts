import { onScopeDispose, ref, shallowRef } from 'vue'

/** 管理可替换的只读请求：保留旧数据、取消前次请求，并随组件作用域释放资源。 */
export function useReadRequest<T>(request: (signal: AbortSignal) => Promise<T>) {
  const data = shallowRef<T | null>(null)
  const loading = ref(true)
  const error = ref<string | null>(null)
  let controller: AbortController | null = null
  let sequence = 0
  let disposed = false

  /** 加载最新结果；clear 清空旧值，仅当前请求的实际失败返回 false，被替代或取消的请求不报错。 */
  async function load(clear = false): Promise<boolean> {
    if (disposed) return true
    controller?.abort()
    const current = new AbortController()
    controller = current
    // 取消不保证请求立即停止，序号仍需阻止晚到的结果覆盖新数据。
    const id = ++sequence
    if (clear) data.value = null
    loading.value = true
    error.value = null
    try {
      const result = await request(current.signal)
      if (id === sequence) data.value = result
      return true
    } catch (cause) {
      if (id === sequence && !current.signal.aborted) {
        error.value = (cause as Error).message
        return false
      }
      return true
    } finally {
      if (id === sequence) loading.value = false
    }
  }

  // 先使序号失效，再取消网络请求，避免卸载后的响应继续更新状态。
  onScopeDispose(() => {
    disposed = true
    sequence++
    controller?.abort()
  })

  return { data, loading, error, load }
}
