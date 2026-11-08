package database

import (
	"context"
	"database/sql"
	"errors"

	"watrik/internal/meter"
)

// Reading 是成功采集的电表快照，保留定点数值及采集时间、类型和小时桶。
type Reading struct {
	meter.Snapshot
	ID            int64
	CollectedAtMS int64
	Kind          string
	HourBucketMS  *int64
}

const readingColumns = "id,collected_at_ms,kind,hour_bucket_ms,meter_id,meter_name,remaining_kwh_x10000,remaining_cents,unit_price_x10000"

// LatestReading 返回最近成功读数；相同采集时间按 ID 决定先后，无记录时返回 nil。
func LatestReading(ctx context.Context, db *sql.DB) (*Reading, error) {
	return scanReading(db.QueryRowContext(ctx, "SELECT "+readingColumns+" FROM readings ORDER BY collected_at_ms DESC,id DESC LIMIT 1"))
}

// ReadingForBucket 返回指定 UTC 小时桶内成功的定时读数。
func ReadingForBucket(ctx context.Context, db *sql.DB, bucket int64) (*Reading, error) {
	return scanReading(db.QueryRowContext(ctx, "SELECT "+readingColumns+" FROM readings WHERE hour_bucket_ms=?", bucket))
}

// scanReading 共用单条查询与结果集的字段映射，无记录时返回 nil。
func scanReading(row interface{ Scan(...any) error }) (*Reading, error) {
	r := &Reading{}
	err := row.Scan(&r.ID, &r.CollectedAtMS, &r.Kind, &r.HourBucketMS, &r.MeterID, &r.MeterName, &r.RemainingKWhX10000, &r.RemainingCents, &r.UnitPriceX10000)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

// ReadingBefore 返回指定 Unix 毫秒时间之前的最近读数，用于识别窗口起点的电量上升。
func ReadingBefore(ctx context.Context, db *sql.DB, beforeMS int64) (*Reading, error) {
	return scanReading(db.QueryRowContext(ctx, "SELECT "+readingColumns+" FROM readings WHERE collected_at_ms<? ORDER BY collected_at_ms DESC,id DESC LIMIT 1", beforeMS))
}

// ReadingsForStats 查询闭区间内的读数，并补齐计算首个小时用量所需的定时基线。
func ReadingsForStats(ctx context.Context, db *sql.DB, sinceMS, untilMS int64) ([]Reading, error) {
	baseline, err := scanReading(db.QueryRowContext(ctx, "SELECT "+readingColumns+" FROM readings WHERE kind='scheduled' AND collected_at_ms<? ORDER BY collected_at_ms DESC,id DESC LIMIT 1", sinceMS))
	if err != nil {
		return nil, err
	}
	readings := make([]Reading, 0)
	lower := sinceMS
	if baseline != nil {
		first, err := scanReading(db.QueryRowContext(ctx, "SELECT "+readingColumns+" FROM readings WHERE kind='scheduled' AND collected_at_ms>=? AND collected_at_ms<=? ORDER BY collected_at_ms,id LIMIT 1", sinceMS, untilMS))
		if err != nil {
			return nil, err
		}
		// 相邻定时桶之间的手动读数可能揭示充值，因此基线与首条定时读数连续时一并保留。
		if first != nil && baseline.HourBucketMS != nil && first.HourBucketMS != nil && *baseline.HourBucketMS >= 0 && *first.HourBucketMS > *baseline.HourBucketMS && *first.HourBucketMS-*baseline.HourBucketMS == 3600000 {
			lower = baseline.CollectedAtMS
		} else {
			readings = append(readings, *baseline)
		}
	}
	rows, err := db.QueryContext(ctx, "SELECT "+readingColumns+" FROM readings WHERE collected_at_ms>=? AND collected_at_ms<=? ORDER BY collected_at_ms,id", lower, untilMS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		reading, err := scanReading(rows)
		if err != nil {
			return nil, err
		}
		readings = append(readings, *reading)
	}
	return readings, rows.Err()
}

var (
	ErrUnknownCursor      = errors.New("reading cursor does not exist")
	ErrInvalidHistoryPage = errors.New("invalid readings page")
)

// ReadingsPage 按采集时间和 ID 倒序分页，多取一条记录供调用方判断是否还有下一页。
func ReadingsPage(ctx context.Context, db *sql.DB, limit int, beforeID int64, sinceMS *int64, untilMS int64) ([]Reading, error) {
	if limit < 1 || limit > 50 || beforeID < 0 {
		return nil, ErrInvalidHistoryPage
	}
	query := "SELECT " + readingColumns + " FROM readings WHERE collected_at_ms<=?"
	arguments := []any{untilMS}
	if sinceMS != nil {
		query += " AND collected_at_ms>=?"
		arguments = append(arguments, *sinceMS)
	}
	if beforeID > 0 {
		cursor, err := scanReading(db.QueryRowContext(ctx, "SELECT "+readingColumns+" FROM readings WHERE id=?", beforeID))
		if err != nil {
			return nil, err
		}
		if cursor == nil {
			return nil, ErrUnknownCursor
		}
		// 复合游标按时间和 ID 排序，避免同一毫秒内的多条读数跨页遗漏。
		query += " AND (collected_at_ms,id)<(?,?)"
		arguments = append(arguments, cursor.CollectedAtMS, cursor.ID)
	}
	query += " ORDER BY collected_at_ms DESC,id DESC LIMIT ?"
	arguments = append(arguments, limit+1)
	rows, err := db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	readings := make([]Reading, 0, limit+1)
	for rows.Next() {
		reading, err := scanReading(rows)
		if err != nil {
			return nil, err
		}
		readings = append(readings, *reading)
	}
	return readings, rows.Err()
}
