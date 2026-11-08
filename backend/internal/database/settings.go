package database

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"time"
)

// ErrInvalidSettings 表示通知设置不满足存储前的格式与数值约束。
var ErrInvalidSettings = errors.New("invalid notification settings")

// Settings 保存通知偏好和接收目标；低量阈值以万分之一 kWh 为单位。
type Settings struct {
	TelegramChatID     string
	TelegramTopicID    *int64
	DailyEnabled       bool
	DailyTime          string
	LowEnabled         bool
	LowThresholdX10000 int64
}

const settingsQuery = "SELECT telegram_chat_id,telegram_topic_id,daily_enabled,daily_time,low_enabled,low_threshold_x10000 FROM app_settings WHERE id=1"

// scanSettings 读取唯一设置行，保留缺失记录等数据库错误供调用方处理。
func scanSettings(row *sql.Row) (Settings, error) {
	var settings Settings
	err := row.Scan(&settings.TelegramChatID, &settings.TelegramTopicID, &settings.DailyEnabled, &settings.DailyTime, &settings.LowEnabled, &settings.LowThresholdX10000)
	return settings, err
}

// GetSettings 返回 ID 为 1 的全局通知设置。
func GetSettings(ctx context.Context, db *sql.DB) (Settings, error) {
	return scanSettings(db.QueryRowContext(ctx, settingsQuery))
}

// UpdateSettings 校验并原子更新全局设置；设置行缺失时返回 sql.ErrNoRows。
func UpdateSettings(ctx context.Context, db *sql.DB, settings Settings) error {
	parsed, err := time.Parse("15:04", settings.DailyTime)
	if err != nil || parsed.Format("15:04") != settings.DailyTime || len(settings.TelegramChatID) > 64 || strings.TrimSpace(settings.TelegramChatID) != settings.TelegramChatID || settings.LowThresholdX10000 < 0 || (settings.TelegramTopicID != nil && (*settings.TelegramTopicID < 1 || *settings.TelegramTopicID > math.MaxInt32)) {
		return ErrInvalidSettings
	}
	// 单条 UPDATE 已具备原子性，无需另开事务或回读设置。
	result, err := db.ExecContext(ctx, `UPDATE app_settings SET telegram_chat_id=?,telegram_topic_id=?,daily_enabled=?,daily_time=?,low_enabled=?,low_threshold_x10000=? WHERE id=1`, settings.TelegramChatID, settings.TelegramTopicID, settings.DailyEnabled, settings.DailyTime, settings.LowEnabled, settings.LowThresholdX10000)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return sql.ErrNoRows
	}
	return nil
}
