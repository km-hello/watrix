package collection

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"watrik/internal/database"
	"watrik/internal/meter"
)

var (
	ErrCooldown    = errors.New("manual collection is cooling down")
	ErrStorage     = errors.New("collection storage failed")
	ErrInvalidKind = errors.New("invalid collection kind")
)

// Service 串行执行采集，并协调手动冷却、定时去重与结果持久化。
type Service struct {
	db          *sql.DB
	fetch       func(context.Context) (meter.Snapshot, error)
	gate        chan struct{}
	now         func() time.Time
	lastStarted time.Time
}

// New 使用同一抓取函数创建供启动、手动和定时任务共用的采集服务。
func New(db *sql.DB, fetch func(context.Context) (meter.Snapshot, error)) *Service {
	return &Service{db: db, fetch: fetch, gate: make(chan struct{}, 1), now: time.Now}
}

// Collect 执行指定类型的采集，将尝试和成功读数一并保存，并响应等待与抓取阶段的上下文取消。
func (s *Service) Collect(ctx context.Context, kind string) (*database.Reading, error) {
	if kind != "startup" && kind != "manual" && kind != "scheduled" {
		return nil, ErrInvalidKind
	}
	// 等待采集锁时也响应取消，避免已经离开的请求继续排队抓取。
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	started := s.now()
	var bucket *int64
	// 小时桶以本次开始时间确定；同一桶已有成功或失败尝试时都不重复抓取。
	if kind == "scheduled" {
		value := started.UTC().Truncate(time.Hour).UnixMilli()
		bucket = &value
		if reading, recorded, err := s.recordedBucket(ctx, value); recorded || err != nil {
			return reading, err
		}
	}
	if kind == "manual" {
		latest, err := database.LatestAttempt(ctx, s.db)
		if err != nil {
			return nil, ErrStorage
		}
		// 内存时间覆盖保存失败或取消的尝试，数据库时间让冷却在重启后仍有效。
		last := s.lastStarted
		if latest != nil && time.UnixMilli(latest.StartedAtMS).After(last) {
			last = time.UnixMilli(latest.StartedAtMS)
		}
		if !last.IsZero() && started.Sub(last) < 30*time.Second {
			return nil, ErrCooldown
		}
	}
	s.lastStarted = started
	snapshot, fetchErr := s.fetch(ctx)
	// 已取消的请求不再把抓取结果记作完成的采集尝试。
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	attempt := database.Attempt{StartedAtMS: started.UnixMilli(), CompletedAtMS: s.now().UnixMilli(), Kind: kind, HourBucketMS: bucket, Success: fetchErr == nil}
	var savedSnapshot *meter.Snapshot
	if fetchErr == nil {
		savedSnapshot = &snapshot
	} else {
		attempt.ErrorCode, fetchErr = classifyError(fetchErr)
	}
	// 普通抓取失败仍需保存尝试，成功时由存储层同时写入读数。
	reading, err := database.SaveCollection(ctx, s.db, attempt, savedSnapshot)
	if err != nil {
		return nil, ErrStorage
	}
	return reading, fetchErr
}

// recordedBucket 返回已记录小时的结果；布尔值表示该小时是否已经完成一次尝试。
func (s *Service) recordedBucket(ctx context.Context, bucket int64) (*database.Reading, bool, error) {
	attempt, err := database.AttemptForBucket(ctx, s.db, bucket)
	if err != nil {
		return nil, false, ErrStorage
	}
	reading, err := database.ReadingForBucket(ctx, s.db, bucket)
	if err != nil {
		return nil, false, ErrStorage
	}
	if reading != nil {
		return reading, true, nil
	}
	if attempt == nil {
		return nil, false, nil
	}
	// 成功尝试应与读数在同一事务落盘，缺少读数说明存储状态不一致。
	if attempt.Success {
		return nil, true, ErrStorage
	}
	return nil, true, errorForCode(attempt.ErrorCode)
}

// classifyError 将抓取错误收敛为可持久化的安全代码和不含来源地址的错误。
func classifyError(err error) (string, error) {
	switch {
	case errors.Is(err, meter.ErrHTTP):
		return "http_error", meter.ErrHTTP
	case errors.Is(err, meter.ErrTooLarge):
		return "too_large", meter.ErrTooLarge
	case errors.Is(err, meter.ErrRedirect):
		return "redirect_rejected", meter.ErrRedirect
	case errors.Is(err, meter.ErrParse):
		return "parse_error", meter.ErrParse
	default:
		return "fetch_failed", meter.ErrFetch
	}
}

// errorForCode 将历史失败代码还原为当前接口可识别的错误类型。
func errorForCode(code string) error {
	switch code {
	case "http_error":
		return meter.ErrHTTP
	case "too_large":
		return meter.ErrTooLarge
	case "redirect_rejected":
		return meter.ErrRedirect
	case "parse_error":
		return meter.ErrParse
	default:
		return meter.ErrFetch
	}
}
