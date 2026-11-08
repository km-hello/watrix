package scheduler

import (
	"context"
	"errors"
	"time"

	"watrik/internal/stats"
)

// Scheduler 按 UTC 整点触发小时采集，并在每轮检查通知任务。
type Scheduler struct {
	collect func(context.Context) error
	notify  func(context.Context) error
	now     func() time.Time
	wait    func(context.Context, time.Duration) bool
}

// New 创建共享采样时刻规则的调度器，任务的错误记录由传入回调负责。
func New(collect, notify func(context.Context) error) *Scheduler {
	return &Scheduler{collect: collect, notify: notify, now: time.Now, wait: wait}
}

// Tick 在采集分钟先执行采集，再检查通知；采集失败不会跳过通知检查。
func (s *Scheduler) Tick(ctx context.Context, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var collectionErr error
	if now.UTC().Minute() == stats.ScheduledMinute {
		collectionErr = s.collect(ctx)
	}
	return errors.Join(collectionErr, s.notify(ctx))
}

// Run 立即检查一次任务，此后对齐实际时钟的下一分钟，直到上下文取消。
func (s *Scheduler) Run(ctx context.Context) {
	for ctx.Err() == nil {
		_ = s.Tick(ctx, s.now())
		// 任务可能耗时，因此每轮结束后重新计算分钟边界，避免累计固定间隔漂移。
		now := s.now()
		if !s.wait(ctx, now.Truncate(time.Minute).Add(time.Minute).Sub(now)) {
			return
		}
	}
}

// wait 等待下一轮或上下文取消，返回是否应继续调度。
func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
