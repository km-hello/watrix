/** 按中文界面格式显示数值，至少保留两位小数，电价可指定更高精度。 */
export function formatNumber(value: number, maximumFractionDigits = 2): string {
  return new Intl.NumberFormat('zh-CN', {
    useGrouping: false,
    minimumFractionDigits: 2,
    maximumFractionDigits,
  }).format(value)
}

/** 按服务端业务时区显示日期时间；日统计可省略时分，无效输入返回占位符。 */
export function formatTimestamp(value: string, timezone: string, showTime = true): string {
  try {
    return new Intl.DateTimeFormat('zh-CN', {
      timeZone: timezone,
      year: 'numeric', month: '2-digit', day: '2-digit',
      ...(showTime ? { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' } as const : {}),
    }).format(new Date(value))
  } catch {
    return '—'
  }
}
