package stats

import (
	"errors"
	"math"
	"time"
)

// ScheduledMinute 是 UTC 每小时的计划采集分钟，供调度与统计完整度计算共同使用。
const ScheduledMinute = 0

var (
	ErrInvalidPeriod     = errors.New("invalid usage period")
	ErrDuplicateInterval = errors.New("duplicate usage interval")
	ErrUsageOverflow     = errors.New("estimated usage exceeds integer range")
)

// Summary 描述估算用量及有效区间覆盖率；没有有效区间时用量为空。
type Summary struct {
	EstimatedKWhX10000 *int64 // 万分之一度；nil 表示无有效用量，而非零用电。
	Quality            string
	ValidIntervals     int
	ExpectedIntervals  int
	Coverage           float64
}

// DayBounds 返回指定时区中该日期的半开时间范围 [开始, 下一日开始)。
func DayBounds(date time.Time, location *time.Location) (time.Time, time.Time) {
	local := date.In(location)
	return CalendarBounds(local.Year(), local.Month(), local.Day(), location)
}

// CalendarBounds 按本地日历构造日期边界，兼容跨月和夏令时导致的非 24 小时日期。
func CalendarBounds(year int, month time.Month, day int, location *time.Location) (time.Time, time.Time) {
	return calendarBoundary(year, month, day, location), calendarBoundary(year, month, day+1, location)
}

// MonthBounds 返回业务时区中当月第一天至下月第一天的半开时间范围。
func MonthBounds(date time.Time, location *time.Location) (time.Time, time.Time) {
	local := date.In(location)
	return calendarBoundary(local.Year(), local.Month(), 1, location), calendarBoundary(local.Year(), local.Month()+1, 1, location)
}

// calendarBoundary 找到目标本地日期的首个真实时刻，处理午夜跳过、重复及整日跳过。
func calendarBoundary(year int, month time.Month, day int, location *time.Location) time.Time {
	target := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	year, month, day = target.Date()
	boundary := time.Date(year, month, day, 0, 0, 0, 0, location)
	calendarDate := func(instant time.Time) time.Time {
		local := instant.In(location)
		return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	}
	// 本地午夜可能不存在或出现两次，向前后搜索该日期最早实际存在的分钟。
	for calendarDate(boundary).Before(target) {
		boundary = boundary.Add(time.Minute)
	}
	for !calendarDate(boundary.Add(-time.Minute)).Before(target) {
		boundary = boundary.Add(-time.Minute)
	}
	return boundary
}

// DueSlots 列出 [start, end) 内截至 now 已到期的 UTC 整点采样时刻。
func DueSlots(start, end, now time.Time, location *time.Location) ([]time.Time, error) {
	if location == nil || end.Before(start) || end.Sub(start) > 32*24*time.Hour {
		return nil, ErrInvalidPeriod
	}
	slots := make([]time.Time, 0)
	firstMinute := start.Truncate(time.Minute)
	if firstMinute.Before(start) {
		firstMinute = firstMinute.Add(time.Minute)
	}
	// 按 UTC 匹配采样时刻；统计窗口仍由业务时区决定，不将半小时或四十五分钟偏移当作采样偏移。
	for due := firstMinute; due.Before(end) && !due.After(now); due = due.Add(time.Minute) {
		if due.UTC().Minute() == ScheduledMinute {
			slots = append(slots, due)
		}
	}
	return slots, nil
}

// Aggregate 按区间结束时刻汇总有效小时用量，并计算截至 now 的完整度。
func Aggregate(intervals []Interval, start, end, now time.Time, location *time.Location) (Summary, error) {
	slots, err := DueSlots(start, end, now, location)
	if err != nil {
		return Summary{}, err
	}
	summary := Summary{Quality: "missing"}
	expected := make(map[int64]time.Time)
	for _, due := range slots {
		// 采样时刻与采集记录均使用 UTC 小时桶，直接按整点标识匹配。
		bucket := due.UTC().Truncate(time.Hour).UnixMilli()
		if _, exists := expected[bucket]; exists {
			return Summary{}, ErrInvalidPeriod
		}
		expected[bucket] = due
	}
	summary.ExpectedIntervals = len(expected)
	seen := make(map[int64]bool)
	var total int64
	for _, interval := range intervals {
		ended := time.UnixMilli(interval.EndedAtMS)
		if ended.Before(start) || !ended.Before(end) || ended.After(now) || interval.EndBucketMS == nil {
			continue
		}
		bucket := *interval.EndBucketMS
		// 只有已到期且实际结束时间不早于计划时刻的区间，才可填充该采样槽。
		due, exists := expected[bucket]
		if !exists || ended.Before(due) {
			continue
		}
		if seen[bucket] {
			return Summary{}, ErrDuplicateInterval
		}
		seen[bucket] = true
		if interval.Quality != "complete" || interval.Reason != "" || interval.EstimatedKWhX10000 == nil || *interval.EstimatedKWhX10000 < 0 {
			continue
		}
		value := *interval.EstimatedKWhX10000
		if value > math.MaxInt64-total {
			return Summary{}, ErrUsageOverflow
		}
		total += value
		summary.ValidIntervals++
	}
	// 没有有效区间时保持 nil，不能把缺失数据误报为零用电。
	if summary.ValidIntervals == 0 {
		return summary, nil
	}
	summary.EstimatedKWhX10000 = &total
	summary.Quality = "partial"
	summary.Coverage = float64(summary.ValidIntervals) / float64(summary.ExpectedIntervals)
	if summary.ValidIntervals == summary.ExpectedIntervals {
		summary.Quality = "complete"
	}
	return summary, nil
}
