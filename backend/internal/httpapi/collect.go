package httpapi

import (
	"context"
	"errors"
	"net/http"

	"watrik/internal/collection"
)

// collectHandler 处理手动采集，并将冷却、存储和采集源错误映射为 HTTP 响应。
func collectHandler(collector *collection.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		var body struct{}
		if !DecodeWriteJSON(w, r, &body) {
			return
		}
		if r.URL.RawQuery != "" {
			Error(w, http.StatusBadRequest, "invalid_request", "采集接口不接受查询参数")
			return
		}
		if collector == nil {
			Error(w, http.StatusServiceUnavailable, "collection_unavailable", "采集服务暂不可用")
			return
		}
		_, err := collector.Collect(r.Context(), "manual")
		switch {
		case err == nil:
			// 页面会重新读取概览、趋势和记录，此处仅确认采集完成。
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, collection.ErrCooldown):
			w.Header().Set("Retry-After", "30")
			Error(w, http.StatusTooManyRequests, "collection_cooldown", "请稍后再刷新")
		case errors.Is(err, collection.ErrStorage), errors.Is(err, collection.ErrInvalidKind):
			Error(w, http.StatusInternalServerError, "storage_error", "保存采集结果失败")
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			Error(w, http.StatusRequestTimeout, "request_cancelled", "请求已取消")
		default:
			Error(w, http.StatusBadGateway, "collection_failed", "采集失败，请稍后重试")
		}
	}
}
