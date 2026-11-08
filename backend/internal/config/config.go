package config

import (
	"errors"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"time"
	_ "time/tzdata"

	"github.com/joho/godotenv"
)

// Config 保存服务启动配置；电表地址与通知令牌不得直接输出到日志。
type Config struct {
	Addr          string
	Timezone      *time.Location
	DatabasePath  string
	StaticDir     string
	MeterURL      *url.URL
	TelegramToken string
}

// String 返回脱敏说明，防止常规格式化日志输出配置中的凭据。
func (c Config) String() string { return "Watrik configuration (secrets redacted)" }

// GoString 为 Go 语法格式化输出提供同样的脱敏保护。
func (c Config) GoString() string { return c.String() }

// Load 从工作目录读取可选的 .env，保留已有环境变量的优先级后校验配置。
func Load() (Config, error) {
	if err := godotenv.Load(".env"); err != nil && !os.IsNotExist(err) {
		// 解析错误可能包含文件原文，因此只返回固定说明。
		return Config{}, errors.New("load .env: unable to read or parse file")
	}
	return Parse(os.Getenv)
}

// Parse 通过给定的环境变量读取函数解析配置，并验证监听地址、时区和采集源。
func Parse(getenv func(string) string) (Config, error) {
	value := func(key, fallback string) string {
		if v := getenv("WATRIK_" + key); v != "" {
			return v
		}
		return fallback
	}
	c := Config{Addr: value("ADDR", ":8080"), DatabasePath: value("DATABASE_PATH", "data/watrik.db"), StaticDir: value("STATIC_DIR", "../frontend/dist"), TelegramToken: getenv("WATRIK_TELEGRAM_BOT_TOKEN")}
	_, port, err := net.SplitHostPort(c.Addr)
	if err != nil {
		return Config{}, errors.New("invalid WATRIK_ADDR")
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return Config{}, errors.New("invalid WATRIK_ADDR port")
	}
	c.Timezone, err = time.LoadLocation(value("TIMEZONE", "Asia/Shanghai"))
	if err != nil {
		return Config{}, errors.New("invalid WATRIK_TIMEZONE")
	}
	c.MeterURL, err = ValidateMeterURL(getenv("WATRIK_METER_URL"))
	if err != nil {
		return Config{}, err
	}
	return c, nil
}

var digits = regexp.MustCompile(`^[0-9]+$`)

// ValidateMeterURL 只允许固定 HTTPS 电表端点及一个纯数字 mid，限制采集请求的目标范围。
func ValidateMeterURL(raw string) (*url.URL, error) {
	invalid := errors.New("WATRIK_METER_URL must use the approved HTTPS meter endpoint and one numeric mid")
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "www.wap.cnyiot.com" || u.User != nil || u.Path != "/nat/pay.aspx" || u.RawPath != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, invalid
	}
	// 同时拒绝额外参数与重复 mid，避免配置校验和实际请求对地址产生不同解释。
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q) != 1 || len(q["mid"]) != 1 || !digits.MatchString(q.Get("mid")) {
		return nil, invalid
	}
	return u, nil
}
