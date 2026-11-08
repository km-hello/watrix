-- +goose Up
-- 成功快照保留定点数值：电量和电价乘 10000，余额以分存储；时间均为 Unix 毫秒。
CREATE TABLE readings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    collected_at_ms INTEGER NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('scheduled', 'startup', 'manual')),
    -- 定时采集以 UTC 整点毫秒去重；启动和手动采集用 NULL，不占用小时桶。
    hour_bucket_ms INTEGER UNIQUE,
    meter_id TEXT NOT NULL CHECK (length(meter_id) > 0),
    meter_name TEXT NOT NULL CHECK (length(meter_name) > 0),
    remaining_kwh_x10000 INTEGER NOT NULL CHECK (remaining_kwh_x10000 >= 0),
    remaining_cents INTEGER NOT NULL CHECK (remaining_cents >= 0),
    unit_price_x10000 INTEGER NOT NULL CHECK (unit_price_x10000 >= 0),
    CHECK ((kind = 'scheduled' AND hour_bucket_ms IS NOT NULL AND hour_bucket_ms % 3600000 = 0)
        OR (kind IN ('startup', 'manual') AND hour_bucket_ms IS NULL))
) STRICT;
CREATE INDEX readings_collected_at ON readings(collected_at_ms DESC, id DESC);

-- 采集尝试独立于成功快照，失败也需要持久化以支持冷却、定时去重和排障。
CREATE TABLE collection_attempts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at_ms INTEGER NOT NULL,
    completed_at_ms INTEGER NOT NULL CHECK (completed_at_ms >= started_at_ms),
    kind TEXT NOT NULL CHECK (kind IN ('scheduled', 'startup', 'manual')),
    success INTEGER NOT NULL CHECK (success IN (0, 1)),
    error_code TEXT NOT NULL DEFAULT '',
    hour_bucket_ms INTEGER,
    CHECK ((success = 1 AND error_code = '') OR (success = 0 AND length(error_code) > 0)),
    CHECK ((kind = 'scheduled' AND hour_bucket_ms IS NOT NULL AND hour_bucket_ms >= 0 AND hour_bucket_ms % 3600000 = 0)
        OR (kind IN ('startup', 'manual') AND hour_bucket_ms IS NULL))
) STRICT;
CREATE INDEX collection_attempts_completed_at ON collection_attempts(completed_at_ms DESC, id DESC);
CREATE UNIQUE INDEX collection_attempts_hour_bucket ON collection_attempts(hour_bucket_ms);

-- 全局仅一行设置；每日时间按应用配置的时区解释，Telegram token 不写入数据库。
CREATE TABLE app_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    telegram_chat_id TEXT NOT NULL DEFAULT '',
    telegram_topic_id INTEGER CHECK (telegram_topic_id > 0),
    daily_enabled INTEGER NOT NULL DEFAULT 1 CHECK (daily_enabled IN (0, 1)),
    daily_time TEXT NOT NULL DEFAULT '09:00' CHECK (
        daily_time GLOB '[0-2][0-9]:[0-5][0-9]' AND substr(daily_time, 1, 2) <= '23'),
    low_enabled INTEGER NOT NULL DEFAULT 1 CHECK (low_enabled IN (0, 1)),
    low_threshold_x10000 INTEGER NOT NULL DEFAULT 100000 CHECK (low_threshold_x10000 >= 0)
) STRICT;
INSERT INTO app_settings(id) VALUES (1);

-- 去重键标识一条逻辑通知；unknown 表示发送结果未确认，不能自动再次发送。
CREATE TABLE notification_runs (
    dedupe_key TEXT PRIMARY KEY NOT NULL,
    kind TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'sent', 'failed', 'unknown')),
    attempted_at_ms INTEGER NOT NULL,
    sent_at_ms INTEGER,
    -- 首次发送也计为一次，明确失败后最多累计三次尝试。
    attempt_count INTEGER NOT NULL DEFAULT 1 CHECK (attempt_count BETWEEN 1 AND 3),
    next_attempt_ms INTEGER CHECK (next_attempt_ms >= 0)
) STRICT;

CREATE INDEX notification_runs_retry ON notification_runs(status, next_attempt_ms);

-- 低量周期独立于设置；周期键和截止时间用于跨重启去重以及恢复后的重新触发。
CREATE TABLE alert_state (
    kind TEXT PRIMARY KEY NOT NULL CHECK (kind = 'low'),
    last_attempt_ms INTEGER CHECK (last_attempt_ms IS NULL OR last_attempt_ms >= 0),
    episode_key TEXT CHECK (episode_key IS NULL OR length(episode_key) > 0)
) STRICT;
INSERT INTO alert_state(kind) VALUES ('low');

-- +goose Down
-- 回滚按初始化顺序逆序移除全部业务表。
DROP TABLE alert_state;
DROP TABLE notification_runs;
DROP TABLE app_settings;
DROP TABLE collection_attempts;
DROP TABLE readings;
