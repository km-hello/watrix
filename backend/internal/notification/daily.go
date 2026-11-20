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

// dailyMessage 组合当前余额、昨日及今日估算用量及数据质量提示。
func (s *Service) dailyMessage(ctx context.Context, now time.Time) (string, error) {
	current, err := database.LatestReading(ctx, s.db)
	if err != nil {
		return "", ErrStorage
	}
	attempt, err := database.LatestAttempt(ctx, s.db)
	if err != nil {
		return "", ErrStorage
	}
	start, _ := stats.DayBounds(now, s.location)
	yesterday, _ := stats.DayBounds(now.In(s.location).AddDate(0, 0, -1), s.location)
	readings, err := database.ReadingsForStats(ctx, s.db, yesterday.UnixMilli(), now.UnixMilli())
	if err != nil {
		return "", ErrStorage
	}
	// 两个时段的费用均按最新采集电价估算，不代表历史实际扣费。
	var unitPrice *int64
	if current != nil {
		unitPrice = &current.UnitPriceX10000
	}
	intervals := stats.Hourly(readings)
	yesterdayUsage, err := s.periodUsage(intervals, yesterday, start, now, unitPrice)
	if err != nil {
		return "", err
	}
	todayUsage, err := s.periodUsage(intervals, start, now, now, unitPrice)
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
	text := fmt.Sprintf("Watrix · 用电日报\n%s\n\n昨日估算：%s\n%s 00:00–24:00\n\n今日估算：%s\n%s 00:00–%s\n\n剩余：%s\n数据截至：%s",
		now.In(s.location).Format("2006-01-02"),
		yesterdayUsage, yesterday.Format("2006-01-02"),
		todayUsage, start.Format("2006-01-02"), now.In(s.location).Format("15:04"),
		remaining, asOf)
	if len(warnings) > 0 {
		text += "\n\n提示：" + strings.Join(warnings, " · ")
	}
	return text, nil
}

// periodUsage 以 (start, end] 的小时结束边界统计，午夜结束的区间属于前一日。
func (s *Service) periodUsage(intervals []stats.Interval, start, end, now time.Time, unitPrice *int64) (string, error) {
	summary, err := stats.Aggregate(intervals, start, end, now, s.location)
	if err != nil {
		return "", ErrStatistics
	}
	if summary.EstimatedKWhX10000 == nil {
		return "— kWh · ¥—（缺失数据）", nil
	}
	cost := "—"
	if unitPrice != nil {
		cost = stats.UsageCostText(*summary.EstimatedKWhX10000, *unitPrice)
	}
	usage := kwhText(*summary.EstimatedKWhX10000) + " kWh · ¥" + cost
	if summary.Quality == "partial" {
		usage += "（部分数据）"
	}
	return usage, nil
}
