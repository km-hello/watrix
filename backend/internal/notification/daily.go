package notification

import (
	"context"
	"fmt"
	"strings"
	"time"

	"watrik/internal/database"
	"watrik/internal/stats"
)

// daily 在本地发送时间到达后尝试领取当日日报，返回是否已经尝试发送。
func (s *Service) daily(ctx context.Context, now time.Time, settings database.Settings) (bool, error) {
	if !settings.DailyEnabled || now.In(s.location).Format("15:04") < settings.DailyTime {
		return false, nil
	}
	ready, err := s.recipientReady(settings)
	if err != nil || !ready {
		return false, err
	}
	blocked, err := s.backoffActive(ctx, now)
	if err != nil || blocked {
		return false, err
	}
	// 先构建内容再领取，避免统计失败后留下已领取但未发送的记录。
	text, err := s.dailyMessage(ctx, now)
	if err != nil {
		return false, err
	}
	// 使用业务时区的日期去重；最终是否可发送仍由数据库原子领取决定。
	key := "daily:" + now.In(s.location).Format("2006-01-02")
	run, err := database.ClaimNotification(ctx, s.db, key, "daily", now.UnixMilli())
	if err != nil {
		return false, ErrStorage
	}
	if run == nil {
		return false, nil
	}
	return true, s.sendClaimed(ctx, *run, settings, text)
}

// kwhText 将万分之一度的定点整数格式化为四位小数，避免浮点舍入。
func kwhText(value int64) string { return fmt.Sprintf("%d.%04d", value/10000, value%10000) }

// dailyMessage 组合当前余额、今日估算用量及数据质量提示。
func (s *Service) dailyMessage(ctx context.Context, now time.Time) (string, error) {
	current, err := database.LatestReading(ctx, s.db)
	if err != nil {
		return "", ErrStorage
	}
	attempt, err := database.LatestAttempt(ctx, s.db)
	if err != nil {
		return "", ErrStorage
	}
	start, end := stats.DayBounds(now, s.location)
	readings, err := database.ReadingsForStats(ctx, s.db, start.UnixMilli(), now.UnixMilli())
	if err != nil {
		return "", ErrStorage
	}
	summary, err := stats.Aggregate(stats.Hourly(readings), start, end, now, s.location)
	if err != nil {
		return "", ErrStatistics
	}
	remaining, asOf := "— kWh · ¥—", "—"
	var warnings []string
	if current != nil {
		remaining = fmt.Sprintf("%s kWh · ¥%d.%02d", kwhText(current.RemainingKWhX10000), current.RemainingCents/100, current.RemainingCents%100)
		collected := time.UnixMilli(current.CollectedAtMS)
		asOf = collected.In(s.location).Format("2006-01-02 15:04")
		if now.Sub(collected) > stats.FreshnessWindow {
			warnings = append(warnings, "数据较旧")
		}
	} else {
		warnings = append(warnings, "尚无采集数据")
	}
	if attempt != nil && !attempt.Success {
		warnings = append(warnings, "最近采集失败")
	}
	usage := "—（缺失数据）"
	if summary.EstimatedKWhX10000 != nil {
		usage = kwhText(*summary.EstimatedKWhX10000) + " kWh"
		if summary.Quality == "partial" {
			usage += "（部分数据）"
		}
	}
	text := fmt.Sprintf("Watrik · 用电日报 · %s\n剩余：%s\n今日估算：%s\n数据截至：%s", now.In(s.location).Format("2006-01-02"), remaining, usage, asOf)
	if len(warnings) > 0 {
		text += "\n" + strings.Join(warnings, " · ")
	}
	return text, nil
}
