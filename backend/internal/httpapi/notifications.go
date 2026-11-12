package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"watrik/internal/database"
	"watrik/internal/notification"
)

// testNotificationHandler 使用已保存的收件人配置发送测试消息，并区分限流和结果未确认。
func testNotificationHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		if r.URL.RawQuery != "" {
			Error(w, 400, "invalid_request", "测试接口不接受查询参数")
			return
		}
		var input struct{}
		if !DecodeWriteJSON(w, r, &input) {
			return
		}
		if deps.Database == nil {
			Error(w, 503, "notifications_unavailable", "通知设置暂不可用")
			return
		}
		settings, err := database.GetSettings(r.Context(), deps.Database)
		if err != nil {
			Error(w, 500, "database_error", "读取通知设置失败")
			return
		}
		if deps.Telegram == nil || !deps.Telegram.TokenConfigured() || settings.TelegramChatID == "" {
			Error(w, 409, "notification_not_configured", "请先配置 Telegram 并保存 Chat ID")
			return
		}
		// 网络结果不确定时不能宣称失败后可安全重发，否则可能重复通知。
		err = deps.Telegram.Send(r.Context(), settings.TelegramChatID, settings.TelegramTopicID, "Watrix · Telegram 测试成功")
		if err == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var limited *notification.RateLimitError
		if errors.As(err, &limited) {
			w.Header().Set("Retry-After", strconv.FormatInt(limited.RetryAfterSeconds, 10))
			Error(w, 429, "notification_rate_limited", "Telegram 请求过于频繁，请稍后再试")
		} else if errors.Is(err, notification.ErrUncertain) {
			Error(w, 502, "notification_uncertain", "发送结果未确认，请先检查 Telegram，避免重复发送")
		} else {
			Error(w, 502, "notification_rejected", "Telegram 拒绝发送，请检查已保存的通知设置")
		}
	}
}
