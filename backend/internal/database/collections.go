package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"watrik/internal/meter"
)

// ErrInvalidCollection 表示采集结果的类型、时间或快照内容不满足持久化约束。
var ErrInvalidCollection = errors.New("invalid collection result")

// Attempt 保存一次采集尝试；时间均为 Unix 毫秒，HourBucketMS 仅用于定时采集去重。
type Attempt struct {
	ID            int64
	StartedAtMS   int64
	CompletedAtMS int64
	Kind          string
	HourBucketMS  *int64
	Success       bool
	ErrorCode     string
}

// SaveCollection 在同一事务中记录采集尝试和成功快照；失败尝试仅保存错误分类。
func SaveCollection(ctx context.Context, db *sql.DB, attempt Attempt, snapshot *meter.Snapshot) (*Reading, error) {
	if !validAttempt(attempt) || attempt.Success != (snapshot != nil) {
		return nil, ErrInvalidCollection
	}
	if snapshot != nil && (strings.TrimSpace(snapshot.MeterID) == "" || strings.TrimSpace(snapshot.MeterName) == "" || snapshot.RemainingKWhX10000 < 0 || snapshot.RemainingCents < 0 || snapshot.UnitPriceX10000 < 0) {
		return nil, ErrInvalidCollection
	}
	// 尝试记录和成功快照必须同时落库，避免成功标记与读数缺失。
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO collection_attempts
		(started_at_ms, completed_at_ms, kind, hour_bucket_ms, success, error_code) VALUES (?, ?, ?, ?, ?, ?)`,
		attempt.StartedAtMS, attempt.CompletedAtMS, attempt.Kind, attempt.HourBucketMS, attempt.Success, attempt.ErrorCode)
	if err != nil {
		return nil, err
	}
	var reading *Reading
	if snapshot != nil {
		result, err := tx.ExecContext(ctx, `INSERT INTO readings
			(collected_at_ms,kind,hour_bucket_ms,meter_id,meter_name,remaining_kwh_x10000,remaining_cents,unit_price_x10000)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, attempt.CompletedAtMS, attempt.Kind, attempt.HourBucketMS,
			snapshot.MeterID, snapshot.MeterName, snapshot.RemainingKWhX10000, snapshot.RemainingCents, snapshot.UnitPriceX10000)
		if err != nil {
			return nil, err
		}
		id, err := result.LastInsertId()
		if err != nil {
			return nil, err
		}
		reading = &Reading{Snapshot: *snapshot, ID: id, CollectedAtMS: attempt.CompletedAtMS, Kind: attempt.Kind, HourBucketMS: attempt.HourBucketMS}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return reading, nil
}

// validAttempt 校验采集时间、小时桶及成功结果与错误分类是否一致。
func validAttempt(a Attempt) bool {
	if a.StartedAtMS < 0 || a.CompletedAtMS < a.StartedAtMS {
		return false
	}
	switch a.Kind {
	case "scheduled":
		// 小时桶取采集开始时间，耗时跨小时也归入原来的调度周期。
		if a.HourBucketMS == nil || *a.HourBucketMS != (a.StartedAtMS/3600000)*3600000 {
			return false
		}
	case "manual", "startup":
		if a.HourBucketMS != nil {
			return false
		}
	default:
		return false
	}
	if a.Success {
		return a.ErrorCode == ""
	}
	switch a.ErrorCode {
	case "fetch_failed", "http_error", "too_large", "redirect_rejected", "parse_error":
		return true
	}
	return false
}

const attemptColumns = "id,started_at_ms,completed_at_ms,kind,hour_bucket_ms,success,error_code"

// LatestAttempt 返回最近完成的采集尝试；无记录时返回 nil。
func LatestAttempt(ctx context.Context, db *sql.DB) (*Attempt, error) {
	return scanAttempt(db.QueryRowContext(ctx, "SELECT "+attemptColumns+" FROM collection_attempts ORDER BY completed_at_ms DESC,id DESC LIMIT 1"))
}

// AttemptForBucket 查询指定 UTC 小时桶的定时尝试；无记录时返回 nil。
func AttemptForBucket(ctx context.Context, db *sql.DB, bucket int64) (*Attempt, error) {
	return scanAttempt(db.QueryRowContext(ctx, "SELECT "+attemptColumns+" FROM collection_attempts WHERE hour_bucket_ms=?", bucket))
}

// scanAttempt 将查询结果转为采集尝试，并将无记录统一表示为 nil。
func scanAttempt(row *sql.Row) (*Attempt, error) {
	a := &Attempt{}
	err := row.Scan(&a.ID, &a.StartedAtMS, &a.CompletedAtMS, &a.Kind, &a.HourBucketMS, &a.Success, &a.ErrorCode)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return a, nil
}
