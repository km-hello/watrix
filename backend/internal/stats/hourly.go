package stats

import (
	"cmp"
	"slices"
	"time"

	"watrik/internal/database"
)

const hourMS int64 = 3600000

// FreshnessWindow 是数据仍可用于当前状态和低电量提醒的最长时长。
const FreshnessWindow = 90 * time.Minute

// Interval 表示以一条定时读数结束的小时估算；缺失或疑似充值时不提供用量。
type Interval struct {
	EndReadingID       int64
	EndedAtMS          int64
	EndBucketMS        *int64
	EstimatedKWhX10000 *int64 // 万分之一度；nil 表示该区间无法可靠估算。
	Quality            string
	Reason             string
}

// Hourly 用相邻定时读数估算小时用量，并检查期间全部观测是否出现上升或无效值。
func Hourly(readings []database.Reading) []Interval {
	// 在副本上按时间和 ID 稳定排序，不改变调用方持有的读数顺序。
	ordered := slices.Clone(readings)
	slices.SortStableFunc(ordered, func(a, b database.Reading) int {
		if result := cmp.Compare(a.CollectedAtMS, b.CollectedAtMS); result != 0 {
			return result
		}
		return cmp.Compare(a.ID, b.ID)
	})
	intervals := make([]Interval, 0)
	var start, previous *database.Reading
	var rose, invalid bool
	for i := range ordered {
		current := &ordered[i]
		if start != nil {
			invalid = invalid || !validObservation(current)
			rose = rose || (previous.RemainingKWhX10000 >= 0 && current.RemainingKWhX10000 > previous.RemainingKWhX10000)
		}
		// 手动与启动采集不形成小时边界，但其电量上升会使所在区间无法估算。
		if current.Kind != "scheduled" {
			if start != nil {
				previous = current
			}
			continue
		}
		interval := Interval{EndReadingID: current.ID, EndedAtMS: current.CollectedAtMS, Quality: "missing", Reason: "gap"}
		if validBucket(current.HourBucketMS) {
			bucket := *current.HourBucketMS
			interval.EndBucketMS = &bucket
		}
		if start != nil {
			// 边界无效或小时不连续优先标为缺失，不能用充值标记掩盖采样缺口。
			if !invalid && current.CollectedAtMS > start.CollectedAtMS && *current.HourBucketMS > *start.HourBucketMS && *current.HourBucketMS-*start.HourBucketMS == hourMS {
				if rose {
					interval.Reason = "recharge_or_adjustment"
				} else {
					usage := start.RemainingKWhX10000 - current.RemainingKWhX10000
					interval.EstimatedKWhX10000, interval.Quality, interval.Reason = &usage, "complete", ""
				}
			}
		}
		intervals = append(intervals, interval)
		// 每条定时读数结束当前区间，并成为下一个区间的起点。
		start, previous = current, current
		rose, invalid = false, !validObservation(current)
	}
	return intervals
}

// validBucket 校验 UTC 小时桶是否非负且对齐整小时，单位为 Unix 毫秒。
func validBucket(bucket *int64) bool {
	return bucket != nil && *bucket >= 0 && *bucket%hourMS == 0
}

// validObservation 校验读数基础值及采集类型与小时桶的对应关系。
func validObservation(reading *database.Reading) bool {
	if reading.CollectedAtMS < 0 || reading.RemainingKWhX10000 < 0 {
		return false
	}
	switch reading.Kind {
	case "scheduled":
		return validBucket(reading.HourBucketMS)
	case "manual", "startup":
		return reading.HourBucketMS == nil
	}
	return false
}

// ObservedRises 按输入时间顺序识别窗口内的电量上升，返回读数标记和本地日期标记。
func ObservedRises(readings []database.Reading, previous *database.Reading, start, now time.Time, location *time.Location) (map[int64]bool, map[string]bool) {
	// previous 可来自窗口前一条读数，使窗口内首次观测也能识别上升。
	rises, daily := make(map[int64]bool), make(map[string]bool)
	for i := range readings {
		reading := &readings[i]
		at := time.UnixMilli(reading.CollectedAtMS)
		if at.Before(start) || at.After(now) {
			continue
		}
		if previous != nil && reading.RemainingKWhX10000 > previous.RemainingKWhX10000 {
			day := at
			// 定时读数的日标记与用量、日末余量一致，午夜边界归前一日。
			if reading.Kind == "scheduled" && reading.HourBucketMS != nil {
				day = time.UnixMilli(*reading.HourBucketMS).Add(-time.Nanosecond)
			}
			rises[reading.ID], daily[day.In(location).Format("2006-01-02")] = true, true
		}
		previous = reading
	}
	return rises, daily
}
