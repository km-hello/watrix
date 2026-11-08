package httpapi

import (
	"database/sql"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"watrik/internal/collection"
	"watrik/internal/notification"
)

// JSON 写入统一的 JSON 响应头、状态码和响应内容。
func JSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// Error 写入前端可识别的固定错误码和安全提示，避免暴露底层错误。
func Error(w http.ResponseWriter, status int, code, message string) {
	JSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

// Dependencies 集中传入接口依赖；SourceHost 只含采集域名，不暴露完整电表地址。
type Dependencies struct {
	Database   *sql.DB
	Collector  *collection.Service
	Timezone   *time.Location
	Telegram   *notification.Telegram
	SourceHost string
}

// New 组装 API、存活检查与前端静态文件路由，并添加响应安全头。
func New(static fs.FS, deps Dependencies) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/collect", collectHandler(deps.Collector))
	mux.HandleFunc("/api/dashboard", dashboardHandler(deps.Database, deps.Timezone, time.Now))
	mux.HandleFunc("/api/series", seriesHandler(deps.Database, deps.Timezone, time.Now))
	mux.HandleFunc("/api/readings", readingsHandler(deps.Database, deps.Timezone, time.Now))
	mux.HandleFunc("/api/settings", settingsHandler(deps))
	mux.HandleFunc("/api/notifications/test", testNotificationHandler(deps))
	// 存活检查只确认 HTTP 可响应，不查询数据库或检测采集、通知是否成功。
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			methodNotAllowed(w, "GET, HEAD")
			return
		}
		JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// 未注册的 API 不能落入静态文件路由，以保持一致的 JSON 错误响应。
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			Error(w, 404, "not_found", "接口不存在")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			methodNotAllowed(w, "GET, HEAD")
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		// 仅服务静态根目录内的普通文件，拒绝无效路径和目录访问。
		if !fs.ValidPath(name) {
			Error(w, 404, "not_found", "页面不存在")
			return
		}
		info, err := fs.Stat(static, name)
		if err != nil || !info.Mode().IsRegular() {
			Error(w, 404, "not_found", "页面不存在")
			return
		}
		http.ServeFileFS(w, r, static, name)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}

// methodNotAllowed 返回 405，并通过 Allow 告知支持的请求方法。
func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不支持")
}
