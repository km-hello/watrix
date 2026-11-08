package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"watrik/internal/database"
	"watrik/internal/stats"
)

type historyItem struct {
	readingResponse
	EstimatedKWh *float64 `json:"estimated_kwh"`
	Quality      string   `json:"quality"`
	Reason       string   `json:"reason"`
	Recharge     bool     `json:"recharge,omitempty"`
}

type readingsResponse struct {
	Timezone     string        `json:"timezone"`
	Items        []historyItem `json:"items"`
	NextBeforeID *int64        `json:"next_before_id"` // 下一页从本页最后一条之后继续；null 表示没有更多记录。
}

// positiveHistoryInteger 解析历史分页参数所需的正整数，拒绝符号和溢出。
func positiveHistoryInteger(raw string) (int64, error) {
	if raw == "" || raw[0] < '0' || raw[0] > '9' {
		return 0, database.ErrInvalidHistoryPage
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, database.ErrInvalidHistoryPage
	}
	return value, nil
}

// readingsParameters 校验分页大小、游标和日期范围，拒绝重复及未知参数。
func readingsParameters(raw string) (int, int64, string, error) {
	query, err := url.ParseQuery(raw)
	if err != nil {
		return 0, 0, "", err
	}
	limit, beforeID, rangeName := 4, int64(0), ""
	for key, values := range query {
		if len(values) != 1 || values[0] == "" {
			return 0, 0, "", database.ErrInvalidHistoryPage
		}
		switch key {
		case "limit":
			value, err := positiveHistoryInteger(values[0])
			if err != nil || value > 50 {
				return 0, 0, "", database.ErrInvalidHistoryPage
			}
			limit = int(value)
		case "before_id":
			beforeID, err = positiveHistoryInteger(values[0])
			if err != nil {
				return 0, 0, "", err
			}
		case "range":
			rangeName = values[0]
			if rangeName != "today" && rangeName != "7d" && rangeName != "30d" {
				return 0, 0, "", database.ErrInvalidHistoryPage
			}
		default:
			return 0, 0, "", database.ErrInvalidHistoryPage
		}
	}
	return limit, beforeID, rangeName, nil
}

// readingsHandler 返回一页历史读数，并补充区间估算用量和疑似充值标记。
func readingsHandler(db *sql.DB, location *time.Location, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, "GET")
			return
		}
		limit, beforeID, rangeName, err := readingsParameters(r.URL.RawQuery)
		if err != nil {
			Error(w, 400, "invalid_request", "记录查询参数无效")
			return
		}
		if db == nil || location == nil {
			Error(w, 503, "readings_unavailable", "采集记录暂不可用")
			return
		}
		at := now().Truncate(time.Millisecond)
		var sinceMS *int64
		if rangeName != "" {
			days := 1
			if rangeName == "7d" {
				days = 7
			} else if rangeName == "30d" {
				days = 30
			}
			local := at.In(location)
			// UTC 日期仅用于自然日加减，再由日历边界转换到真实本地时刻，避免夏令时偏移。
			civil := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1-days)
			start, _ := stats.CalendarBounds(civil.Year(), civil.Month(), civil.Day(), location)
			value := start.UnixMilli()
			sinceMS = &value
		}
		page, err := database.ReadingsPage(r.Context(), db, limit, beforeID, sinceMS, at.UnixMilli())
		if errors.Is(err, database.ErrUnknownCursor) {
			Error(w, 400, "invalid_cursor", "记录游标不存在")
			return
		}
		if err != nil {
			Error(w, 500, "database_error", "读取采集记录失败")
			return
		}
		result := readingsResponse{Timezone: location.String(), Items: make([]historyItem, 0, limit)}
		// 数据库多取一条以判断是否还有下一页，游标仍指向本页最后一条。
		if len(page) > limit {
			page = page[:limit]
			id := page[len(page)-1].ID
			result.NextBeforeID = &id
		}
		if len(page) == 0 {
			JSON(w, 200, result)
			return
		}
		start, end := time.UnixMilli(page[len(page)-1].CollectedAtMS), time.UnixMilli(page[0].CollectedAtMS)
		// 分页外的前置定时读数和中间观测仍参与首个区间及充值判断。
		contextRows, err := database.ReadingsForStats(r.Context(), db, start.UnixMilli(), end.UnixMilli())
		if err != nil {
			Error(w, 500, "database_error", "读取采集记录失败")
			return
		}
		previous, err := database.ReadingBefore(r.Context(), db, start.UnixMilli())
		if err != nil {
			Error(w, 500, "database_error", "读取采集记录失败")
			return
		}
		rises, _ := stats.ObservedRises(contextRows, previous, start, end, location)
		intervals := make(map[int64]stats.Interval)
		for _, interval := range stats.Hourly(contextRows) {
			intervals[interval.EndReadingID] = interval
		}
		// 手动和启动读数可显示及识别上升，但只有定时边界形成小时用量区间。
		for _, reading := range page {
			item := historyItem{readingResponse: publicReading(&reading), Quality: "missing", Reason: "not_scheduled", Recharge: rises[reading.ID]}
			if reading.Kind == "scheduled" {
				item.Reason = "gap"
				if interval, exists := intervals[reading.ID]; exists {
					item.Quality, item.Reason = interval.Quality, interval.Reason
					if interval.EstimatedKWhX10000 != nil {
						item.EstimatedKWh = kwhValue(*interval.EstimatedKWhX10000)
					}
					item.Recharge = item.Recharge || interval.Reason == "recharge_or_adjustment"
				}
			}
			result.Items = append(result.Items, item)
		}
		JSON(w, 200, result)
	}
}
