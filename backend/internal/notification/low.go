package notification

import (
	"context"
	"fmt"
	"time"

	"watrik/internal/database"
	"watrik/internal/stats"
)

// low 更新低电量告警周期，并在数据新鲜、允许发送且满足阈值时尝试提醒。
func (s *Service) low(ctx context.Context, now time.Time, settings database.Settings, allowSend bool) error {
	current, err := database.LatestReading(ctx, s.db)
	if err != nil {
		return ErrStorage
	}
	if current == nil || now.Sub(time.UnixMilli(current.CollectedAtMS)) > stats.FreshnessWindow {
		return nil
	}
	remaining, threshold := current.RemainingKWhX10000, settings.LowThresholdX10000
	// 高出阈值超过 0.5 度才视为恢复，避免阈值附近波动反复开启新周期。
	if remaining > threshold && remaining-threshold > 5000 {
		if err := database.RearmLowAlert(ctx, s.db); err != nil {
			return ErrStorage
		}
		return nil
	}
	// 两次检查之间可能先恢复再下降，历史观测也必须参与周期重置。
	if err := database.RearmObservedLowRecovery(ctx, s.db, threshold, *current); err != nil {
		return ErrStorage
	}
	if remaining > threshold || !settings.LowEnabled || !allowSend {
		return nil
	}
	attempt, err := database.LatestAttempt(ctx, s.db)
	if err != nil {
		return ErrStorage
	}
	// 最近一次采集失败时不依据旧余额催促充值，即使旧读数尚未过期。
	if attempt != nil && !attempt.Success {
		return nil
	}
	ready, err := s.recipientReady(settings)
	if err != nil || !ready {
		return err
	}
	blocked, err := s.backoffActive(ctx, now)
	if err != nil || blocked {
		return err
	}
	run, err := database.ClaimLowAlert(ctx, s.db, *current, now.UnixMilli())
	if err != nil {
		return ErrStorage
	}
	if run == nil {
		return nil
	}
	text := fmt.Sprintf("Watrix · 电量不足\n剩余：%s kWh（提醒阈值：%s kWh）\n请及时充值。\n数据截至：%s", kwhText(remaining), kwhText(threshold), time.UnixMilli(current.CollectedAtMS).In(s.location).Format("2006-01-02 15:04"))
	return s.sendClaimed(ctx, *run, settings, text)
}
