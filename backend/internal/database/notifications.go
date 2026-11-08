package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// ErrNotificationState 表示通知状态、尝试标识或状态转换不合法。
var ErrNotificationState = errors.New("invalid notification state")

// NotificationRun 标识一次已领取的发送尝试，用于去重和防止旧请求覆盖新尝试。
type NotificationRun struct {
	DedupeKey     string
	Kind          string
	Status        string
	AttemptedAtMS int64
	AttemptCount  int
}

const notificationColumns = "dedupe_key,kind,status,attempted_at_ms,attempt_count"

// scanNotificationRun 读取通知领取结果，无记录时返回 nil。
func scanNotificationRun(row *sql.Row) (*NotificationRun, error) {
	var run NotificationRun
	err := row.Scan(&run.DedupeKey, &run.Kind, &run.Status, &run.AttemptedAtMS, &run.AttemptCount)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

// ClaimNotification 在事务中领取一次通知发送机会；无需发送时返回 nil。
func ClaimNotification(ctx context.Context, db *sql.DB, key, kind string, nowMS int64) (*NotificationRun, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	run, err := claimNotification(ctx, tx, key, kind, nowMS)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return run, nil
}

// claimNotification 在调用方事务中创建或重试通知，保证去重与告警状态更新可以原子提交。
func claimNotification(ctx context.Context, tx *sql.Tx, key, kind string, nowMS int64) (*NotificationRun, error) {
	if strings.TrimSpace(key) == "" || len(key) > 128 || strings.TrimSpace(kind) == "" || len(kind) > 32 || nowMS < 0 {
		return nil, ErrNotificationState
	}
	// 仅明确失败的通知允许重试；含首次发送共最多三次尝试，间隔至少一分钟，pending、sent、unknown 均不重复领取。
	result, err := tx.ExecContext(ctx, `INSERT INTO notification_runs(dedupe_key,kind,status,attempted_at_ms,sent_at_ms,attempt_count,next_attempt_ms)
		VALUES(?,?,'pending',?,NULL,1,NULL)
		ON CONFLICT(dedupe_key) DO UPDATE SET status='pending',attempted_at_ms=excluded.attempted_at_ms,attempt_count=notification_runs.attempt_count+1,next_attempt_ms=NULL
		WHERE notification_runs.kind=excluded.kind AND notification_runs.status='failed' AND notification_runs.attempt_count<3
		AND notification_runs.next_attempt_ms IS NOT NULL AND notification_runs.next_attempt_ms<=excluded.attempted_at_ms
		AND notification_runs.attempted_at_ms<=excluded.attempted_at_ms-60000`, key, kind, nowMS)
	if err != nil {
		return nil, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, nil
	}
	return scanNotificationRun(tx.QueryRowContext(ctx, "SELECT "+notificationColumns+" FROM notification_runs WHERE dedupe_key=?", key))
}

// FinishNotification 记录本次发送结果，完成时间与下次重试时间均为 Unix 毫秒。
func FinishNotification(ctx context.Context, db *sql.DB, run NotificationRun, status string, finishedMS int64, nextAttemptMS *int64) error {
	if run.Status != "pending" || run.AttemptCount < 1 || run.AttemptCount > 3 || finishedMS < run.AttemptedAtMS {
		return ErrNotificationState
	}
	// unknown 表示结果未确认，不能自动重试；failed 必须给出退避截止时间。
	var sentMS *int64
	switch status {
	case "sent":
		sentMS = &finishedMS
		if nextAttemptMS != nil {
			return ErrNotificationState
		}
	case "unknown":
		if nextAttemptMS != nil {
			return ErrNotificationState
		}
	case "failed":
		if nextAttemptMS == nil || *nextAttemptMS < finishedMS {
			return ErrNotificationState
		}
	default:
		return ErrNotificationState
	}
	// 匹配领取时的次数和时间，防止迟到的发送结果覆盖后续尝试。
	result, err := db.ExecContext(ctx, `UPDATE notification_runs SET status=?,sent_at_ms=?,next_attempt_ms=? WHERE dedupe_key=? AND kind=? AND status='pending' AND attempt_count=? AND attempted_at_ms=?`, status, sentMS, nextAttemptMS, run.DedupeKey, run.Kind, run.AttemptCount, run.AttemptedAtMS)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrNotificationState
	}
	return nil
}

// NotificationBlockedUntil 返回日报和低量通知共享的最晚退避截止时间，无退避记录时返回 nil。
func NotificationBlockedUntil(ctx context.Context, db *sql.DB) (*int64, error) {
	var until sql.NullInt64
	if err := db.QueryRowContext(ctx, "SELECT max(next_attempt_ms) FROM notification_runs WHERE status='failed' AND kind IN ('daily','low')").Scan(&until); err != nil {
		return nil, err
	}
	if !until.Valid {
		return nil, nil
	}
	return &until.Int64, nil
}
