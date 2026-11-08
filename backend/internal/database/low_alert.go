package database

import (
	"context"
	"database/sql"
	"strconv"
)

// RearmLowAlert 在电量恢复后清除当前低量周期，使后续低量能够重新提醒。
func RearmLowAlert(ctx context.Context, db *sql.DB) error {
	result, err := db.ExecContext(ctx, "UPDATE alert_state SET episode_key=NULL,last_attempt_ms=NULL WHERE kind='low'")
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

// RearmObservedLowRecovery 查找上次提醒后、截至当前读数已观测到的恢复，避免漏掉恢复后再次下降的新周期。
func RearmObservedLowRecovery(ctx context.Context, db *sql.DB, threshold int64, through Reading) error {
	if threshold < 0 || through.ID < 1 || through.CollectedAtMS < 0 {
		return ErrNotificationState
	}
	// 恢复要求高于阈值 0.5 kWh，形成滞回区间，防止阈值附近波动反复开启周期。
	_, err := db.ExecContext(ctx, `UPDATE alert_state SET episode_key=NULL,last_attempt_ms=NULL
		WHERE kind='low' AND episode_key IS NOT NULL AND EXISTS (
			SELECT 1 FROM readings WHERE collected_at_ms>alert_state.last_attempt_ms
			AND (collected_at_ms,id)<=(?,?)
			AND remaining_kwh_x10000>? AND remaining_kwh_x10000-?>5000
		)`, through.CollectedAtMS, through.ID, threshold, threshold)
	return err
}

// ClaimLowAlert 原子领取低量通知并推进周期边界；同一旧读数不会重复触发重试。
func ClaimLowAlert(ctx context.Context, db *sql.DB, reading Reading, nowMS int64) (*NotificationRun, error) {
	if reading.ID < 1 || reading.CollectedAtMS < 0 || nowMS < 0 {
		return nil, ErrNotificationState
	}
	// 通知领取与周期边界必须共同提交，重启后才能延续同一去重周期。
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var episodeKey *string
	var lastAttemptMS *int64
	if err := tx.QueryRowContext(ctx, "SELECT episode_key,last_attempt_ms FROM alert_state WHERE kind='low'").Scan(&episodeKey, &lastAttemptMS); err != nil {
		return nil, err
	}
	key := "low:" + strconv.FormatInt(reading.ID, 10) + ":" + strconv.FormatInt(nowMS, 10)
	if episodeKey != nil {
		key = *episodeKey
		if lastAttemptMS != nil && reading.CollectedAtMS <= *lastAttemptMS {
			return nil, nil
		}
	}
	run, err := claimNotification(ctx, tx, key, "low", nowMS)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, nil
	}
	// 截止点不早于当前读数，也不早于本次尝试，只有后续新读数才能触发重试。
	cutoff := nowMS
	if reading.CollectedAtMS > cutoff {
		cutoff = reading.CollectedAtMS
	}
	if _, err := tx.ExecContext(ctx, "UPDATE alert_state SET episode_key=?,last_attempt_ms=? WHERE kind='low'", key, cutoff); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return run, nil
}
