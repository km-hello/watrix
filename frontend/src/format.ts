/** 按中文界面格式显示数值，至少保留两位小数，电价可指定更高精度。 */
export function formatNumber(value: number, maximumFractionDigits = 2): string {
  return new Intl.NumberFormat('zh-CN', {
    useGrouping: false,
    minimumFractionDigits: 2,
    maximumFractionDigits,
  }).format(value)
}

/** 按服务端业务时区显示时间；无效时间或时区返回占位符，date 控制是否显示日期。 */
export function formatTimestamp(value: string, timezone: string, date = true): string {
  try {
    return new Intl.DateTimeFormat('zh-CN', {
      timeZone: timezone,
      ...(date ? { year: 'numeric', month: '2-digit', day: '2-digit' } as const : {}),
      hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
    }).format(new Date(value))
  } catch {
    return '—'
  }
}
