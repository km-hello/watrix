package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Open 创建并打开 SQLite 数据库，应用内嵌迁移后返回可用连接。
func Open(ctx context.Context, path string) (*sql.DB, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("create database file: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	// WAL 允许读写协作；等待锁并提前获取写锁，避免事务中途升级锁失败。
	u := url.URL{Scheme: "file", Path: path, RawQuery: "_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on&_txlock=immediate"}
	db, err := sql.Open("sqlite3", u.String())
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// 单连接串行执行 SQLite 写入，并保持连接级配置一致。
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect database: %w", err)
	}
	migrationFS, err := fs.Sub(migrations, "migrations")
	if err == nil {
		var provider *goose.Provider
		provider, err = goose.NewProvider(goose.DialectSQLite3, db, migrationFS, goose.WithDisableGlobalRegistry(true))
		if err == nil {
			_, err = provider.Up(ctx)
		}
	}
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	return db, nil
}
