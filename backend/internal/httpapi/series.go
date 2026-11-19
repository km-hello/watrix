package httpapi

import (
	"database/sql"
	"net/http"
	"net/url"
	"slices"
	"time"

	"watrik/internal/database"
	"watrik/internal/stats"
)

type seriesPoint struct {
	Timestamp         time.Time `json:"timestamp"`
	Value             *float64  `json:"value"` // 千瓦时；null 表示缺失或无法可靠估算，不能当作零。
	Quality           string    `json:"quality"`
	Reason            string    `json:"reason"`
	Kind              string    `json:"kind,omitempty"`
	Recharge          bool      `json:"recharge,omitempty"` // 观测到电量上升，仅表示疑似充值或调整。
	Coverage          *float64  `json:"coverage,omitempty"`
	ValidIntervals    *int      `json:"valid_intervals,omitempty"`
	ExpectedIntervals *int      `json:"expected_intervals,omitempty"`
}

type seriesResponse struct {
	Timezone string        `json:"timezone"`
	Points   []seriesPoint `json:"points"`
}

// seriesParameters 校验趋势范围与指标，缺省使用最近 24 小时的剩余电量。
func seriesParameters(raw string) (string, string, error) {
	query, err := url.ParseQuery(raw)
	if err != nil {
		return "", "", err
	}
	for key, values := range query {
		if (key != "range" && key != "metric") || len(values) != 1 || values[0] == "" {
			return "", "", stats.ErrInvalidPeriod
		}
	}
	rangeName, metric := "24h", "remaining"
	if value, exists := query["range"]; exists {
		rangeName = value[0]
	}
	if value, exists := query["metric"]; exists {
		metric = value[0]
	}
	if (rangeName != "24h" && rangeName != "7d" && rangeName != "30d") || (metric != "remaining" && metric != "consumption") {
		return "", "", stats.ErrInvalidPeriod
	}
	return rangeName, metric, nil
}

// seriesHandler 按滚动小时或本地自然日组织趋势数据和质量标记。
func seriesHandler(db *sql.DB, location *time.Location, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, "GET")
			return
		}
		rangeName, metric, err := seriesParameters(r.URL.RawQuery)
		if err != nil {
			Error(w, 400, "invalid_request", "趋势查询参数无效")
			return
		}
		if db == nil || location == nil {
			Error(w, 503, "series_unavailable", "用电趋势暂不可用")
			return
		}
		at := now().Truncate(time.Millisecond)
		start := at.Add(-24 * time.Hour)
		days := 0
		var civilStart time.Time
		if rangeName != "24h" {
			days = 7
			if rangeName == "30d" {
				days = 30
			}
			local := at.In(location)
			// 7 天和 30 天包含今天，按自然日而非固定的 24 小时倍数回溯。
			civilStart = time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1-days)
			start, _ = stats.CalendarBounds(civilStart.Year(), civilStart.Month(), civilStart.Day(), location)
		}
		readings, err := database.ReadingsForStats(r.Context(), db, start.UnixMilli(), at.UnixMilli())
		if err != nil {
			Error(w, 500, "database_error", "读取用电数据失败")
			return
		}
		previous, err := database.ReadingBefore(r.Context(), db, start.UnixMilli())
		if err != nil {
			Error(w, 500, "database_error", "读取用电数据失败")
			return
		}
		rises, dailyRises := stats.ObservedRises(readings, previous, start, at, location)
		var points []seriesPoint
		if days == 0 {
			points, err = rollingPoints(readings, stats.Hourly(readings), rises, start, at, location, metric)
		} else {
			points, err = dailyPoints(readings, stats.Hourly(readings), dailyRises, civilStart, days, at, location, metric)
		}
		if err != nil {
			Error(w, 500, "statistics_error", "用电趋势暂不可用")
			return
		}
		JSON(w, 200, seriesResponse{Timezone: location.String(), Points: points})
	}
}

// rollingPoints 生成最近 24 小时的观测点或用量区间，并补齐应采未采的空点。
func rollingPoints(readings []database.Reading, intervals []stats.Interval, rises map[int64]bool, start, now time.Time, location *time.Location, metric string) ([]seriesPoint, error) {
	points := make([]seriesPoint, 0)
	scheduled := make(map[int64]bool)
	for _, reading := range readings {
		at := time.UnixMilli(reading.CollectedAtMS).UTC()
		if at.Before(start) || at.After(now) {
			continue
		}
		if reading.Kind == "scheduled" && reading.HourBucketMS != nil {
			scheduled[*reading.HourBucketMS] = true
		}
		if metric == "remaining" {
			point := seriesPoint{Timestamp: at, Value: kwhValue(reading.RemainingKWhX10000), Quality: "complete", Kind: reading.Kind, Recharge: rises[reading.ID]}
			if point.Recharge {
				point.Reason = "recharge_or_adjustment"
			}
			points = append(points, point)
		}
	}
	if metric == "consumption" {
		for _, interval := range intervals {
			at := time.UnixMilli(interval.EndedAtMS).UTC()
			if !at.After(start) || at.After(now) {
				continue
			}
			point := seriesPoint{Timestamp: at, Quality: interval.Quality, Reason: interval.Reason, Kind: "scheduled", Recharge: rises[interval.EndReadingID] || interval.Reason == "recharge_or_adjustment"}
			if interval.EstimatedKWhX10000 != nil {
				point.Value = kwhValue(*interval.EstimatedKWhX10000)
			}
			points = append(points, point)
		}
	}
	// 用量区间以结束时刻归属窗口，起点本身不计入；余量观测则包含起点。
	slotStart := start
	if metric == "consumption" {
		slotStart = slotStart.Add(time.Nanosecond)
	}
	slots, err := stats.DueSlots(slotStart, now.Add(time.Nanosecond), now, location)
	if err != nil {
		return nil, err
	}
	// 缺失点显式返回 null，图表不会跨越缺口画成连续有效数据。
	for _, due := range slots {
		if !scheduled[due.UTC().Truncate(time.Hour).UnixMilli()] {
			points = append(points, seriesPoint{Timestamp: due.UTC(), Quality: "missing", Reason: "gap", Kind: "scheduled"})
		}
	}
	slices.SortStableFunc(points, func(a, b seriesPoint) int { return a.Timestamp.Compare(b.Timestamp) })
	return points, nil
}

// dailyPoints 按本地自然日生成每日最后定时余量或估算用量汇总。
func dailyPoints(readings []database.Reading, intervals []stats.Interval, rises map[string]bool, civilStart time.Time, days int, now time.Time, location *time.Location, metric string) ([]seriesPoint, error) {
	// 每日余量取截至当日 24:00 的最后定时读数，按计划边界归属以免午夜采集延迟跨日。
	lastScheduled := make(map[string]database.Reading)
	for _, reading := range readings {
		at := time.UnixMilli(reading.CollectedAtMS)
		if reading.Kind == "scheduled" && reading.HourBucketMS != nil && !at.After(now) {
			day := time.UnixMilli(*reading.HourBucketMS).Add(-time.Nanosecond)
			lastScheduled[day.In(location).Format("2006-01-02")] = reading
		}
	}
	points := make([]seriesPoint, 0, days)
	for day := 0; day < days; day++ {
		civil := civilStart.AddDate(0, 0, day)
		start, end := stats.CalendarBounds(civil.Year(), civil.Month(), civil.Day(), location)
		// 某些时区可能跳过整个自然日，该日期没有真实时间范围。
		if start.Equal(end) {
			continue
		}
		key := civil.Format("2006-01-02")
		point := seriesPoint{Timestamp: start.UTC(), Quality: "missing", Reason: "gap", Recharge: rises[key]}
		if metric == "remaining" {
			if reading, exists := lastScheduled[key]; exists {
				point.Value, point.Quality, point.Reason, point.Kind = kwhValue(reading.RemainingKWhX10000), "complete", "", "scheduled"
				if point.Recharge {
					point.Reason = "recharge_or_adjustment"
				}
			}
		} else {
			summary, err := stats.Aggregate(intervals, start, end, now, location)
			if err != nil {
				return nil, err
			}
			converted := publicSummary(summary)
			point.Value, point.Quality = converted.EstimatedKWh, converted.Quality
			point.Coverage, point.ValidIntervals, point.ExpectedIntervals = &converted.Coverage, &converted.ValidIntervals, &converted.ExpectedIntervals
			if summary.Quality == "complete" {
				point.Reason = ""
			} else {
				// 用量不完整时保留充值原因；覆盖率仍描述该日整体的数据完整度。
				for _, interval := range intervals {
					at := time.UnixMilli(interval.EndedAtMS)
					if interval.EndBucketMS == nil {
						continue
					}
					boundary := time.UnixMilli(*interval.EndBucketMS)
					if boundary.After(start) && !boundary.After(end) && !at.After(now) && interval.Reason == "recharge_or_adjustment" {
						point.Reason, point.Recharge = "recharge_or_adjustment", true
						break
					}
				}
			}
		}
		points = append(points, point)
	}
	return points, nil
}
