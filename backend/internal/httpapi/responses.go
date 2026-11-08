package httpapi

import (
	"time"

	"watrik/internal/database"
	"watrik/internal/stats"
)

// readingResponse 使用千瓦时、元和元/千瓦时作为对外展示单位。
type readingResponse struct {
	ID                  int64     `json:"id"`
	MeterName           string    `json:"meter_name"`
	RemainingKWh        float64   `json:"remaining_kwh"`
	RemainingYuan       float64   `json:"remaining_yuan"`
	UnitPriceYuanPerKWh float64   `json:"unit_price_yuan_per_kwh"`
	CollectedAt         time.Time `json:"collected_at"`
	Kind                string    `json:"kind"`
}

// publicReading 将数据库定点整数转换为展示单位，并将采集时间统一为 UTC。
func publicReading(reading *database.Reading) readingResponse {
	return readingResponse{
		ID: reading.ID, MeterName: reading.MeterName,
		RemainingKWh:        float64(reading.RemainingKWhX10000) / 10000,
		RemainingYuan:       float64(reading.RemainingCents) / 100,
		UnitPriceYuanPerKWh: float64(reading.UnitPriceX10000) / 10000,
		CollectedAt:         time.UnixMilli(reading.CollectedAtMS).UTC(), Kind: reading.Kind,
	}
}

// usageSummaryResponse 同时返回估算值和覆盖情况，避免将不完整统计表现为完整用量。
type usageSummaryResponse struct {
	EstimatedKWh      *float64 `json:"estimated_kwh"`
	Quality           string   `json:"quality"`
	ValidIntervals    int      `json:"valid_intervals"`
	ExpectedIntervals int      `json:"expected_intervals"`
	Coverage          float64  `json:"coverage"` // 有效区间占应有区间的比例，范围为 0 到 1。
}

// publicSummary 转换用量统计，保留 nil 以区分缺失数据与实际零用量。
func publicSummary(summary stats.Summary) usageSummaryResponse {
	result := usageSummaryResponse{Quality: summary.Quality, ValidIntervals: summary.ValidIntervals, ExpectedIntervals: summary.ExpectedIntervals, Coverage: summary.Coverage}
	if summary.EstimatedKWhX10000 != nil {
		value := float64(*summary.EstimatedKWhX10000) / 10000
		result.EstimatedKWh = &value
	}
	return result
}

// kwhValue 将万分之一千瓦时转换为可空响应中的千瓦时数值。
func kwhValue(value int64) *float64 { result := float64(value) / 10000; return &result }
