/** 成功采集快照；电量单位 kWh，金额单位元，时间为服务端返回的 ISO 时间。 */
export interface Reading {
  id: number
  meter_name: string
  remaining_kwh: number
  remaining_yuan: number
  unit_price_yuan_per_kwh: number
  collected_at: string
  kind: 'startup' | 'manual' | 'scheduled'
}

/** 仅汇总有效区间；估算值为 null 表示没有可用估算，coverage 为 0 到 1 的覆盖率。 */
export interface UsageSummary {
  estimated_kwh: number | null
  quality: 'complete' | 'partial' | 'missing'
  valid_intervals: number
  expected_intervals: number
  coverage: number
}

/** 首页快照；current 为 null 表示尚无成功采集数据，统计质量由后端决定。 */
export interface Dashboard {
  meter_name: string
  timezone: string
  current: Reading | null
  today: UsageSummary
  month: UsageSummary
  collection: {
    state: 'empty' | 'fresh' | 'stale'
    /** 严格超过该时间才过期；无成功读数时为 null，供长开页面自动更新状态。 */
    stale_at: string | null
  }
  low: { is_low: boolean }
}

export type SeriesRange = '24h' | '7d' | '30d'
export type SeriesMetric = 'remaining' | 'consumption'

/** 趋势数据点；value 为 null 表示缺失或不可估算，不能作为零用电。 */
export interface SeriesPoint {
  timestamp: string
  value: number | null
  quality: 'complete' | 'partial' | 'missing'
  reason: '' | 'gap' | 'recharge_or_adjustment'
  kind?: Reading['kind']
  /** 观察到电量上升，只能表示疑似充值或调整，不代表真实充值订单。 */
  recharge?: boolean
  coverage?: number
  valid_intervals?: number
  expected_intervals?: number
}

/** 后端已完成聚合的趋势序列，前端只负责显示。 */
export interface Series {
  timezone: string
  points: SeriesPoint[]
}

/** 历史快照附带其对应定时区间的估算结果；非定时记录不产生区间估算。 */
export interface HistoryReading extends Reading {
  estimated_kwh: number | null
  quality: UsageSummary['quality']
  reason: '' | 'gap' | 'recharge_or_adjustment' | 'not_scheduled'
  recharge?: boolean
}

/** 向较旧记录翻页的结果；next_before_id 为 null 表示没有下一页。 */
export interface ReadingsPage {
  timezone: string
  items: HistoryReading[]
  next_before_id: number | null
}

export type ReadingsRange = 'today' | '7d' | '30d'
/** 历史范围按业务时区解释，before_id 是前一页给出的游标。 */
export interface ReadingsQuery {
  range?: ReadingsRange
  limit?: number
  before_id?: number
}

/** 可编辑的通知配置及只读采集信息；仅返回 Token 是否配置，不返回密钥。 */
export interface Settings {
  telegram_token_configured: boolean
  telegram_chat_id: string
  telegram_topic_id: number | null
  daily_enabled: boolean
  daily_time: string
  low_enabled: boolean
  /** 使用十进制字符串传输阈值，以保留服务端四位小数的定点精度。 */
  low_threshold_kwh: string
  timezone: string
  collection: {
    last_success_at: string | null
    meter_name: string
    source_host: string
  }
}

/** 保存时只提交可编辑字段，避免把只读采集信息混入写请求。 */
export type NotificationInput = Pick<Settings, 'telegram_chat_id' | 'telegram_topic_id' | 'daily_enabled' | 'daily_time' | 'low_enabled' | 'low_threshold_kwh'>
