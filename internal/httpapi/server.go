// Package httpapi 提供 WebUI 与 Headless 两套 HTTP 通道。
//
// 处理顺序、错误体与状态码严格对齐 v2.0.3 的 api/api.php 与 headless/headless.php。
package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"

	"github.com/UeCook/Short_NURL/internal/apikey"
	"github.com/UeCook/Short_NURL/internal/auth"
	"github.com/UeCook/Short_NURL/internal/config"
	"github.com/UeCook/Short_NURL/internal/link"
	"github.com/UeCook/Short_NURL/internal/store"
)

// channel 错误体风格。
type channel int

const (
	channelWebUI channel = iota
	channelHeadless
)

// Server HTTP 服务。
type Server struct {
	Cfg   *config.Config
	Keys  *apikey.KeyStore
	Auth  *auth.Auth
	Links *link.Services
	Log   *slog.Logger
}

// NewServer 构造 HTTP 服务。
func NewServer(cfg *config.Config, keys *apikey.KeyStore, authSvc *auth.Auth, links *link.Services, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{Cfg: cfg, Keys: keys, Auth: authSvc, Links: links, Log: logger}
}

// ServeHTTP 路由到双通道。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w.Header())

	rawPath := r.URL.EscapedPath()
	path := strings.TrimRight(rawPath, "/")
	if path == "" {
		path = "/"
	}

	if strings.HasPrefix(path, "/headless") {
		st := &requestState{server: s, channel: channelHeadless, path: path, r: r}
		defer st.recoverAs(w)
		st.serveHeadless(w)
		return
	}
	st := &requestState{server: s, channel: channelWebUI, path: path, r: r}
	defer st.recoverAs(w)
	st.serveWebUI(w)
}

// setSecurityHeaders 为所有 API 响应设置基线安全头。
// 即使客户端绕过反向代理直连 Go，也不会缺少这些头；浏览器侧策略仍由边缘
// Nginx 的 CSP/HSTS 提供（HSTS 只在 HTTPS 响应上有意义）。
func setSecurityHeaders(h http.Header) {
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	h.Set("Permissions-Policy", "geolocation=(), camera=(), microphone=()")
	h.Set("Cache-Control", "no-store")
}

// requestState 单次请求的可变状态（认证上下文、原始 body）。
type requestState struct {
	server  *Server
	channel channel
	path    string
	r       *http.Request
	rawBody []byte
	bodyOK  bool
	authCtx *auth.Context
}

func (st *requestState) recoverAs(w http.ResponseWriter) {
	rec := recover()
	if rec == nil {
		return
	}
	st.server.Log.Error("[http] 未处理异常", "panic", rec, "stack", string(debug.Stack()))
	if st.authCtx != nil {
		_ = st.server.Auth.Complete(st.authCtx, false)
	}
	writeError(w, st.channel, http.StatusInternalServerError, "internal_error", "服务器内部错误")
}

// ── 响应工具 ────────────────────────────────────────────────────

const contentTypeJSON = "application/json; charset=utf-8"

func encode(v any) []byte {
	b, err := store.EncodeCompact(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}

// writeJSON 输出 JSON 响应。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	_, _ = w.Write(encode(v))
}

// writeRaw 输出预编码响应体。
func writeRaw(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// writeError 按通道风格输出错误体。
func writeError(w http.ResponseWriter, ch channel, status int, code, message string) {
	if ch == channelHeadless {
		writeJSON(w, status, headlessErr{Error: code, Message: message})
		return
	}
	writeJSON(w, status, webuiErr{Error: message, Code: code})
}

type webuiErr struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

type headlessErr struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

type pingOK struct {
	OK bool `json:"ok"`
}

// writeEmpty 输出空体响应（保留 Content-Type，对齐 PHP header() 行为）。
func writeEmpty(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
}

// ── 请求体 ──────────────────────────────────────────────────────

func (st *requestState) readBody() ([]byte, bool) {
	if st.bodyOK {
		return st.rawBody, false
	}
	st.bodyOK = true
	limit := st.server.Cfg.Server.MaxBodyBytes
	if limit <= 0 {
		limit = 1048576
	}
	if st.r.ContentLength > limit {
		st.rawBody = nil
		return nil, true
	}
	data, err := io.ReadAll(io.LimitReader(st.r.Body, limit+1))
	if err != nil {
		st.rawBody = []byte{}
		return st.rawBody, false
	}
	if int64(len(data)) > limit {
		st.rawBody = nil
		return nil, true
	}
	st.rawBody = data
	return st.rawBody, false
}

// decodeParams 解析请求体为参数表（对齐 PHP json_decode($raw, true) 的弱类型语义）。
func decodeParams(raw []byte) (map[string]any, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	// 尾随垃圾按非法 JSON 处理（PHP json_decode 会整体失败）
	if _, err := dec.Token(); err != io.EOF {
		return nil, false
	}
	switch t := v.(type) {
	case map[string]any:
		return t, true
	case []any:
		// PHP 的空数组/列表不会命中任何字符串键。
		return map[string]any{}, true
	default:
		return nil, false
	}
}
