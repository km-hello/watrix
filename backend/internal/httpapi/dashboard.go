package httpapi

import (
	"database/sql"
	"net/http"
	"time"

	"watrik/internal/database"
	"watrik/internal/stats"
)

// collectionResponse 描述数据新鲜度；刷新失败由本次操作反馈独立表达。
type collectionResponse struct {
	State   string     `json:"state"`
	StaleAt *time.Time `json:"stale_at"` // UTC 到期时刻；无读数时为 null，严格超过后才过期。
}

type dashboardResponse struct {
	MeterName  string               `json:"meter_name"`
	Timezone   string               `json:"timezone"`
	Current    *readingResponse     `json:"current"`
	Today      usageSummaryResponse `json:"today"`
	Month      usageSummaryResponse `json:"month"`
	Collection collectionResponse   `json:"collection"`
	Low        lowSettingsResponse  `json:"low"`
}

type lowSettingsResponse struct {
	IsLow bool `json:"is_low"`
}

// dashboardHandler 组合最新读数、今日和本月用量，以及数据新鲜度和低电量标记。
func dashboardHandler(db *sql.DB, location *time.Location, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, "GET")
			return
		}
		if r.URL.RawQuery != "" {
			Error(w, http.StatusBadRequest, "invalid_request", "概览接口不接受查询参数")
			return
		}
		if db == nil || location == nil {
			Error(w, http.StatusServiceUnavailable, "dashboard_unavailable", "用电概览暂不可用")
			return
		}
		current, err := database.LatestReading(r.Context(), db)
		if err != nil {
			Error(w, 500, "database_error", "读取用电数据失败")
			return
		}
		settings, err := database.GetSettings(r.Context(), db)
		if err != nil {
			Error(w, 500, "database_error", "读取通知设置失败")
			return
		}
		at := now()
		dayStart, dayEnd := stats.DayBounds(at, location)
		monthStart, monthEnd := stats.MonthBounds(at, location)
		readings, err := database.ReadingsForStats(r.Context(), db, monthStart.UnixMilli(), at.UnixMilli())
		if err != nil {
			Error(w, 500, "database_error", "读取用电数据失败")
			return
		}
		// 同一组月内区间同时服务今日和本月统计，边界由配置时区决定。
		intervals := stats.Hourly(readings)
		today, err := stats.Aggregate(intervals, dayStart, dayEnd, at, location)
		if err != nil {
			Error(w, 500, "statistics_error", "用电统计暂不可用")
			return
		}
		month, err := stats.Aggregate(intervals, monthStart, monthEnd, at, location)
		if err != nil {
			Error(w, 500, "statistics_error", "用电统计暂不可用")
			return
		}
		result := dashboardResponse{Timezone: location.String(), Today: publicSummary(today), Month: publicSummary(month), Collection: publicCollectionStatus(current, at)}
		// 电量提示只使用新鲜读数；是否发送 Telegram 由通知设置另行控制。
		result.Low.IsLow = current != nil && result.Collection.State == "fresh" && current.RemainingKWhX10000 <= settings.LowThresholdX10000
		if current != nil {
			public := publicReading(current)
			result.Current, result.MeterName = &public, current.MeterName
		}
		JSON(w, http.StatusOK, result)
	}
}

// publicCollectionStatus 根据最近成功读数判断新鲜度，并提供前端自动过期的时刻。
func publicCollectionStatus(current *database.Reading, at time.Time) collectionResponse {
	result := collectionResponse{State: "empty"}
	if current != nil {
		collected := time.UnixMilli(current.CollectedAtMS).UTC()
		staleAt := collected.Add(stats.FreshnessWindow)
		result.StaleAt = &staleAt
		result.State = "fresh"
		if at.Sub(collected) > stats.FreshnessWindow {
			result.State = "stale"
		}
	}
	return result
}
