package notification

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"time"

	"watrik/internal/database"
)

var ErrStorage = errors.New("notification storage failed")
var ErrStatistics = errors.New("notification usage statistics unavailable")

// Service 协调日报和低电量提醒，并将发送结果写回通知记录。
type Service struct {
	db       *sql.DB
	telegram *Telegram
	location *time.Location
	now      func() time.Time
}

// NewService 使用同一数据库、Telegram 客户端和业务时区创建通知服务。
func NewService(db *sql.DB, telegram *Telegram, location *time.Location) *Service {
	return &Service{db: db, telegram: telegram, location: location, now: time.Now}
}

// Tick 按日报优先的顺序检查通知条件，每轮至多尝试发送一条消息。
func (s *Service) Tick(ctx context.Context, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.db == nil || s.location == nil {
		return ErrStorage
	}
	settings, err := database.GetSettings(ctx, s.db)
	if err != nil {
		return ErrStorage
	}
	attempted, dailyErr := s.daily(ctx, now, settings)
	if err := ctx.Err(); err != nil {
		if dailyErr != nil {
			return dailyErr
		}
		return err
	}
	// 日报已尝试发送时仍检查低量恢复，但本轮不再发送第二条消息。
	lowErr := s.low(ctx, now, settings, !attempted)
	if dailyErr == nil && lowErr == nil {
		return ctx.Err()
	}
	return errors.Join(dailyErr, lowErr)
}

// recipientReady 区分尚未配置收件人与已经配置但格式无效的情况。
func (s *Service) recipientReady(settings database.Settings) (bool, error) {
	if s.telegram == nil || !s.telegram.TokenConfigured() || strings.TrimSpace(settings.TelegramChatID) == "" {
		return false, nil
	}
	if err := ValidateRecipient(settings.TelegramChatID, settings.TelegramTopicID); err != nil {
		return false, err
	}
	return true, nil
}

// backoffActive 检查持久化退避期限，避免不同通知在限流期间继续发送。
func (s *Service) backoffActive(ctx context.Context, now time.Time) (bool, error) {
	blocked, err := database.NotificationBlockedUntil(ctx, s.db)
	if err != nil {
		return false, ErrStorage
	}
	return blocked != nil && *blocked > now.UnixMilli(), nil
}

// sendClaimed 发送已领取的通知，并按成功、明确拒绝或结果未知记录最终状态。
func (s *Service) sendClaimed(ctx context.Context, run database.NotificationRun, settings database.Settings, text string) error {
	sendErr := s.telegram.Send(ctx, settings.TelegramChatID, settings.TelegramTopicID, text)
	finished := s.now().UnixMilli()
	// 墙上时钟回拨时仍保持完成时间不早于本次领取时间。
	if finished < run.AttemptedAtMS {
		finished = run.AttemptedAtMS
	}
	status := "sent"
	var next *int64
	if sendErr != nil {
		// 超时或响应损坏无法证明未发送，保留未知状态以避免盲目重发。
		status = "unknown"
		if errors.Is(sendErr, ErrRejected) {
			status = "failed"
			seconds := int64(60)
			var limited *RateLimitError
			if errors.As(sendErr, &limited) && limited.RetryAfterSeconds > seconds {
				seconds = limited.RetryAfterSeconds
			}
			// 限流秒数来自远端，乘加前检查溢出，超出范围则保持最长退避。
			deadline := int64(math.MaxInt64)
			if seconds <= (math.MaxInt64-finished)/1000 {
				deadline = finished + seconds*1000
			}
			next = &deadline
		}
	}
	// 即使上游已取消，也给状态落盘独立的短时限，避免本次发送结果丢失。
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := database.FinishNotification(finishCtx, s.db, run, status, finished, next); err != nil {
		return ErrStorage
	}
	return sendErr
}
