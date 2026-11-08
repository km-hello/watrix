package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"watrik/internal/database"
	"watrik/internal/meter"
	"watrik/internal/notification"
)

type settingsCollectionResponse struct {
	LastSuccessAt *time.Time `json:"last_success_at"`
	MeterName     string     `json:"meter_name"`
	SourceHost    string     `json:"source_host"`
}

type settingsResponse struct {
	TelegramTokenConfigured bool                       `json:"telegram_token_configured"`
	TelegramChatID          string                     `json:"telegram_chat_id"`
	TelegramTopicID         *int64                     `json:"telegram_topic_id"`
	DailyEnabled            bool                       `json:"daily_enabled"`
	DailyTime               string                     `json:"daily_time"`
	LowEnabled              bool                       `json:"low_enabled"`
	LowThresholdKWh         string                     `json:"low_threshold_kwh"`
	Timezone                string                     `json:"timezone"`
	Collection              settingsCollectionResponse `json:"collection"`
}

// settingsInput 使用指针区分字段缺失与 false、空字符串等合法值。
type settingsInput struct {
	TelegramChatID  *string         `json:"telegram_chat_id"`
	TelegramTopicID json.RawMessage `json:"telegram_topic_id"`
	DailyEnabled    *bool           `json:"daily_enabled"`
	DailyTime       *string         `json:"daily_time"`
	LowEnabled      *bool           `json:"low_enabled"`
	LowThresholdKWh *string         `json:"low_threshold_kwh"`
}

// thresholdDecimal 将万分之一千瓦时转换为四位小数字符串，避免阈值精度损失。
func thresholdDecimal(value int64) string {
	return fmt.Sprintf("%d.%04d", value/10000, value%10000)
}

// settings 校验完整设置输入，并转换为数据库使用的定点阈值和收件人格式。
func (input settingsInput) settings() (database.Settings, error) {
	if input.TelegramChatID == nil || len(input.TelegramTopicID) == 0 || input.DailyEnabled == nil || input.DailyTime == nil || input.LowEnabled == nil || input.LowThresholdKWh == nil {
		return database.Settings{}, database.ErrInvalidSettings
	}
	// RawMessage 另外区分 Topic ID 缺失和显式 null，后者用于清除主题设置。
	var topic *int64
	if json.Unmarshal(input.TelegramTopicID, &topic) != nil {
		return database.Settings{}, database.ErrInvalidSettings
	}
	chat := strings.TrimSpace(*input.TelegramChatID)
	// 允许暂不配置收件人；已填写的收件人和主题必须符合发送端约束。
	if err := notification.ValidateRecipient(chat, topic); err != nil && !errors.Is(err, notification.ErrNotConfigured) {
		return database.Settings{}, database.ErrInvalidSettings
	}
	threshold, err := meter.ParseFixedDecimal(*input.LowThresholdKWh, 4)
	if err != nil {
		return database.Settings{}, database.ErrInvalidSettings
	}
	parsed, err := time.Parse("15:04", *input.DailyTime)
	if err != nil || parsed.Format("15:04") != *input.DailyTime {
		return database.Settings{}, database.ErrInvalidSettings
	}
	return database.Settings{TelegramChatID: chat, TelegramTopicID: topic, DailyEnabled: *input.DailyEnabled, DailyTime: *input.DailyTime, LowEnabled: *input.LowEnabled, LowThresholdX10000: threshold}, nil
}

// settingsHandler 读取通知与采集信息，或完整替换已验证的通知设置。
func settingsHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodPut {
			methodNotAllowed(w, "GET, PUT")
			return
		}
		if r.URL.RawQuery != "" {
			Error(w, 400, "invalid_request", "设置接口不接受查询参数")
			return
		}
		var updated database.Settings
		if r.Method == http.MethodPut {
			var input settingsInput
			if !DecodeWriteJSON(w, r, &input) {
				return
			}
			var err error
			updated, err = input.settings()
			if err != nil {
				Error(w, 400, "invalid_settings", "通知设置无效")
				return
			}
		}
		if deps.Database == nil || deps.Timezone == nil {
			Error(w, 503, "settings_unavailable", "通知设置暂不可用")
			return
		}
		if r.Method == http.MethodPut {
			if err := database.UpdateSettings(r.Context(), deps.Database, updated); err != nil {
				Error(w, 500, "database_error", "保存通知设置失败")
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		settings, err := database.GetSettings(r.Context(), deps.Database)
		if err != nil {
			Error(w, 500, "database_error", "读取通知设置失败")
			return
		}
		current, err := database.LatestReading(r.Context(), deps.Database)
		if err != nil {
			Error(w, 500, "database_error", "读取采集状态失败")
			return
		}
		// 只返回 Token 是否已配置，实际凭据始终留在服务端。
		result := settingsResponse{TelegramTokenConfigured: deps.Telegram != nil && deps.Telegram.TokenConfigured(), TelegramChatID: settings.TelegramChatID, TelegramTopicID: settings.TelegramTopicID, DailyEnabled: settings.DailyEnabled, DailyTime: settings.DailyTime, LowEnabled: settings.LowEnabled, LowThresholdKWh: thresholdDecimal(settings.LowThresholdX10000), Timezone: deps.Timezone.String(), Collection: settingsCollectionResponse{SourceHost: deps.SourceHost}}
		if current != nil {
			result.Collection.MeterName = current.MeterName
			collected := time.UnixMilli(current.CollectedAtMS).UTC()
			result.Collection.LastSuccessAt = &collected
		}
		JSON(w, 200, result)
	}
}
