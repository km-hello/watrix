package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

// DecodeWriteJSON 校验写请求来源、大小和 JSON 对象格式；失败时直接写入错误响应。
// 不带来源头的命令行请求沿用 Go 的跨源策略，浏览器请求仍须通过同源检查。
func DecodeWriteJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	// 拒绝重复来源头，避免代理和应用对同一请求的来源产生不同解释。
	var protection http.CrossOriginProtection
	if len(r.Header.Values("Origin")) > 1 || len(r.Header.Values("Sec-Fetch-Site")) > 1 || protection.Check(r) != nil {
		Error(w, http.StatusForbidden, "origin_rejected", "仅接受同源请求")
		return false
	}
	// 显式 Origin 必须是规范的 HTTP(S) 来源，且主机与当前 Host 完全一致。
	if origins := r.Header.Values("Origin"); len(origins) > 0 {
		u, err := url.Parse(origins[0])
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Host != r.Host || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(origins[0], "#") || u.Opaque != "" {
			Error(w, http.StatusForbidden, "origin_rejected", "仅接受同源请求")
			return false
		}
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		Error(w, http.StatusUnsupportedMediaType, "json_required", "请使用 JSON 请求")
		return false
	}
	// 设置体很小，先限制到 4 KiB，再读取和解码，防止无界分配。
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if err != nil {
		var limitErr *http.MaxBytesError
		if errors.As(err, &limitErr) {
			Error(w, http.StatusRequestEntityTooLarge, "body_too_large", "请求内容过大")
		} else {
			Error(w, http.StatusBadRequest, "invalid_json", "JSON 请求格式无效")
		}
		return false
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 || body[0] != '{' {
		Error(w, http.StatusBadRequest, "invalid_json", "JSON 请求格式无效")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	// 未知字段和尾随的第二个 JSON 值均拒绝，避免拼写错误被静默忽略。
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		Error(w, http.StatusBadRequest, "invalid_json", "JSON 请求格式无效")
		return false
	}
	if decoder.Decode(new(any)) != io.EOF {
		Error(w, http.StatusBadRequest, "invalid_json", "JSON 请求格式无效")
		return false
	}
	return true
}
