package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrNotConfigured = errors.New("Telegram notification is not configured")
	ErrRejected      = errors.New("Telegram rejected the notification")
	ErrUncertain     = errors.New("Telegram delivery could not be confirmed")
)

// RateLimitError 保存 Telegram 要求的重试等待秒数，属于明确拒绝。
type RateLimitError struct{ RetryAfterSeconds int64 }

// Error 返回固定的限流描述，不携带请求内容或凭据。
func (e *RateLimitError) Error() string { return "Telegram rate limit exceeded" }

// Unwrap 使限流错误同时匹配 ErrRejected，进入明确失败的退避流程。
func (e *RateLimitError) Unwrap() error { return ErrRejected }

// Telegram 封装 Telegram 发送请求，并对外屏蔽包含 token 的底层错误。
type Telegram struct {
	token  string
	client *http.Client
}

// NewTelegram 创建带超时且不跟随重定向的客户端，避免凭据被转发到其他地址。
func NewTelegram(token string, transport http.RoundTripper) *Telegram {
	return &Telegram{token: strings.TrimSpace(token), client: &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// String 返回脱敏描述，防止格式化日志意外输出 token。
func (t *Telegram) String() string { return "Watrix Telegram client (token redacted)" }

// GoString 为 Go 语法格式化同样提供脱敏描述。
func (t *Telegram) GoString() string { return t.String() }

// TokenConfigured 报告是否提供 token，不执行网络验证。
func (t *Telegram) TokenConfigured() bool { return t.token != "" }

// ValidateRecipient 校验数字 Chat ID、公开用户名及可选 Topic ID 的输入格式。
func ValidateRecipient(chatID string, topicID *int64) error {
	if topicID != nil && (*topicID < 1 || *topicID > math.MaxInt32) {
		return ErrRejected
	}
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return ErrNotConfigured
	}
	// 这是应用的输入长度上限，不代表 Telegram 用户名规则。
	if len(chatID) > 64 {
		return ErrRejected
	}
	if chatID[0] == '@' {
		if len(chatID) == 1 {
			return ErrRejected
		}
		for _, r := range chatID[1:] {
			if !asciiIdentifier(r) {
				return ErrRejected
			}
		}
	} else {
		value, err := strconv.ParseInt(chatID, 10, 64)
		if err != nil || value == 0 || chatID[0] == '+' {
			return ErrRejected
		}
	}
	return nil
}

// asciiIdentifier 判断字符是否属于受支持的 ASCII 字母、数字或下划线。
func asciiIdentifier(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_'
}

// Send 发送纯文本消息；只有可确认的拒绝才允许上层按失败重试，其他不确定结果返回 ErrUncertain。
func (t *Telegram) Send(ctx context.Context, chatID string, topicID *int64, text string) error {
	if !t.TokenConfigured() {
		return ErrNotConfigured
	}
	chatID = strings.TrimSpace(chatID)
	if err := ValidateRecipient(chatID, topicID); err != nil {
		return err
	}
	if !utf8.ValidString(text) || strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > 4096 {
		return ErrRejected
	}
	// token 将进入 URL 路径，限制可用字符，且绝不向上层传播包含它的网络错误。
	for _, r := range t.token {
		if !asciiIdentifier(r) && r != '-' && r != ':' {
			return ErrRejected
		}
	}
	payload := struct {
		ChatID  string `json:"chat_id"`
		TopicID *int64 `json:"message_thread_id,omitempty"`
		Text    string `json:"text"`
	}{chatID, topicID, text}
	body, err := json.Marshal(payload)
	if err != nil {
		return ErrRejected
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.telegram.org/bot"+t.token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return ErrRejected
	}
	request.Header.Set("Content-Type", "application/json")
	// 请求发出后的网络故障无法判断 Telegram 是否已接收，按结果未知处理。
	response, err := t.client.Do(request)
	if err != nil {
		return ErrUncertain
	}
	defer response.Body.Close()
	// 重定向、服务端故障等响应不足以确认发送失败，不进入自动失败重试。
	if response.StatusCode < 200 || response.StatusCode >= 300 && response.StatusCode < 400 || response.StatusCode >= 500 {
		return ErrUncertain
	}
	// 仅解析有界且完整的响应；读取失败或结构损坏同样无法证明消息未发送。
	const limit = 64 << 10
	if response.ContentLength > limit {
		return ErrUncertain
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || len(data) > limit {
		return ErrUncertain
	}
	var reply struct {
		OK         *bool           `json:"ok"`
		ErrorCode  json.RawMessage `json:"error_code"`
		Parameters json.RawMessage `json:"parameters"`
	}
	if json.Unmarshal(data, &reply) != nil || reply.OK == nil {
		return ErrUncertain
	}
	if *reply.OK {
		if response.StatusCode >= 300 {
			return ErrUncertain
		}
		return nil
	}
	var code int64
	_ = json.Unmarshal(reply.ErrorCode, &code)
	// HTTP 状态或 Telegram 错误码都可能携带限流，保留有效的等待秒数。
	if response.StatusCode == 429 || code == 429 {
		var parameters struct {
			RetryAfter json.RawMessage `json:"retry_after"`
		}
		var seconds int64
		if json.Unmarshal(reply.Parameters, &parameters) == nil && len(parameters.RetryAfter) > 0 {
			if json.Unmarshal(parameters.RetryAfter, &seconds) != nil {
				return ErrUncertain
			}
			if seconds > 0 {
				return &RateLimitError{RetryAfterSeconds: seconds}
			}
		}
	}
	return ErrRejected
}
