package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"watrik/internal/collection"
	"watrik/internal/config"
	"watrik/internal/database"
	"watrik/internal/httpapi"
	"watrik/internal/meter"
	"watrik/internal/notification"
	"watrik/internal/scheduler"
)

// main 将启动或运行期的致命错误交给进程退出处理。
func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run 组装服务依赖，启动 HTTP 与后台任务，并在退出时按依赖顺序释放资源。
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// 先完成数据库迁移和静态目录检查，再开始监听，避免暴露尚未初始化的服务。
	db, err := database.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer db.Close()
	static, err := os.OpenRoot(cfg.StaticDir)
	if err != nil {
		return errors.New("cannot open WATRIK_STATIC_DIR; build the frontend first")
	}
	defer static.Close()
	fetcher, err := meter.NewFetcher(cfg.MeterURL.String(), nil)
	if err != nil {
		return err
	}
	collector := collection.New(db, fetcher.Fetch)
	deps := httpapi.Dependencies{Database: db, Collector: collector, Timezone: cfg.Timezone, Telegram: notification.NewTelegram(cfg.TelegramToken, nil), SourceHost: cfg.MeterURL.Hostname()}
	notifier := notification.NewService(db, deps.Telegram, cfg.Timezone)
	server := &http.Server{Addr: cfg.Addr, Handler: httpapi.New(static.FS(), deps), BaseContext: func(net.Listener) context.Context { return ctx }, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	listener, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	// 使用缓冲通道，确保主协程因退出信号离开后 Serve 仍能报告结束。
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	// 后台任务独立取消；其退出必须早于数据库关闭，避免停止过程中继续访问已关闭的连接。
	collectionCtx, cancelCollection := context.WithCancel(ctx)
	collectionDone := make(chan struct{})
	go func() {
		defer close(collectionDone)
		if _, err := collector.Collect(collectionCtx, "startup"); err != nil && collectionCtx.Err() == nil {
			logTaskError("startup_collection", err)
		}
		scheduler.New(func(ctx context.Context) error {
			_, err := collector.Collect(ctx, "scheduled")
			if ctx.Err() == nil {
				logTaskError("scheduled_collection", err)
			}
			return err
		}, func(ctx context.Context) error {
			err := notifier.Tick(ctx, time.Now())
			if ctx.Err() == nil {
				logTaskError("notification", err)
			}
			return err
		}).Run(collectionCtx)
	}()
	defer func() {
		cancelCollection()
		<-collectionDone
	}()
	log.Print("Watrix HTTP service starting")
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		// 根上下文已取消，使用独立的超时上下文给 HTTP 连接留出清理时间。
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return err
		}
		return nil
	}
}

// logTaskError 仅记录固定的任务错误分类，避免原始错误泄露电表 URL 或通知凭据。
func logTaskError(task string, err error) {
	if err == nil {
		return
	}
	codes := []struct {
		target error
		code   string
	}{
		{meter.ErrFetch, "fetch_failed"}, {meter.ErrHTTP, "http_error"},
		{meter.ErrTooLarge, "too_large"}, {meter.ErrRedirect, "redirect_rejected"},
		{meter.ErrParse, "parse_error"}, {collection.ErrStorage, "collection_storage_error"},
		{collection.ErrInvalidKind, "invalid_collection_kind"},
		{notification.ErrStorage, "notification_storage_error"}, {notification.ErrStatistics, "statistics_error"},
		{notification.ErrNotConfigured, "notification_not_configured"},
		{notification.ErrRejected, "notification_rejected"}, {notification.ErrUncertain, "notification_uncertain"},
		{context.Canceled, "cancelled"}, {context.DeadlineExceeded, "timeout"},
	}
	// 合并错误可能包含多个原因，逐项匹配以保留各阶段的安全分类。
	matched := false
	for _, entry := range codes {
		if errors.Is(err, entry.target) {
			log.Printf("Watrix task=%s error=%s", task, entry.code)
			matched = true
		}
	}
	if !matched {
		log.Printf("Watrix task=%s error=unclassified", task)
	}
}
