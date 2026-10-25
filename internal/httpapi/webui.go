package httpapi

import (
	"net/http"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/UeCook/Short_NURL/internal/auth"
	"github.com/UeCook/Short_NURL/internal/config"
	"github.com/UeCook/Short_NURL/internal/link"
)

var webuiRoutes = map[string]string{
	"/api/create": http.MethodPost,
	"/api/delete": http.MethodPost,
	"/api/list":   http.MethodGet,
	"/api/stat":   http.MethodGet,
}

var webuiActions = map[string]string{
	"/api/create": "create",
	"/api/delete": "delete",
	"/api/list":   "list",
	"/api/stat":   "stat",
}

func (st *requestState) writeCORSHeaders(w http.ResponseWriter) {
	cfg := st.server.Cfg
	origin := cfg.Site.PanelOrigin
	if origin == "" {
		origin = cfg.Site.Domain
	}
	if origin != "" && origin != "*" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
	}
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Token")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
}

// serveWebUI 处理 /api/*（对齐 api/api.php + bootstrap/webui.php）。
func (st *requestState) serveWebUI(w http.ResponseWriter) {
	r := st.r
	cfg := st.server.Cfg

	// OPTIONS 预检：必须在方法校验之前处理，否则 OPTIONS 永远 405
	if r.Method == http.MethodOptions {
		st.writeCORSHeaders(w)
		writeEmpty(w, http.StatusNoContent)
		return
	}

	// 存活探测：无需引导、无需认证
	if st.path == "/api/ping" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Content-Type", contentTypeJSON)
			w.Header().Set("Allow", "GET, HEAD")
			w.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = w.Write(encode(webuiErr{Error: "请求方法不允许", Code: "method_not_allowed"}))
			return
		}
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Type", contentTypeJSON)
			w.Header().Set("Content-Length", "11")
			w.WriteHeader(http.StatusOK)
			return
		}
		writeJSON(w, http.StatusOK, pingOK{OK: true})
		return
	}

	expected, routed := webuiRoutes[st.path]
	if !routed {
		writeError(w, channelWebUI, http.StatusNotFound, "not_found", "未知的端点")
		return
	}
	if r.Method != expected {
		w.Header().Set("Allow", expected)
		writeError(w, channelWebUI, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不允许")
		return
	}

	// 引导：面板开关 → CORS → 文件检查 → domain → body → 认证
	if !cfg.Site.PanelEnabled {
		writeError(w, channelWebUI, http.StatusForbidden, "panel_disabled", "前端面板未启用")
		return
	}
	st.writeCORSHeaders(w)

	if failed := failedFiles(cfg); len(failed) > 0 {
		st.server.Log.Warn("[webui_bootstrap] 数据文件不可访问", "files", strings.Join(failed, ", "))
		writeError(w, channelWebUI, 430, "data_inaccessible", "数据文件不可访问，请检查文件权限")
		return
	}
	if strings.Contains(cfg.Site.Domain, "{") {
		writeError(w, channelWebUI, http.StatusInternalServerError, "config_error", "配置的 domain 尚未配置")
		return
	}

	body, tooLarge := st.readBody()
	if tooLarge {
		writeError(w, channelWebUI, http.StatusRequestEntityTooLarge, "payload_too_large", "请求体过大")
		return
	}

	credential := st.extractWebUICredential(body)
	allowed, _ := auth.AllowedKeyTypes("webui", webuiActions[st.path])
	result := st.server.Auth.Authenticate(credential, allowed)
	if !result.Success {
		st.writeAuthError(w, result.Reason)
		return
	}
	st.authCtx = result.Ctx

	switch webuiActions[st.path] {
	case "create":
		st.handleWebUICreate(w, body)
	case "delete":
		st.handleWebUIDelete(w, body)
	case "list":
		st.handleWebUIList(w)
	case "stat":
		st.handleWebUIStat(w)
	}
}

func (st *requestState) writeAuthError(w http.ResponseWriter, reason string) {
	switch reason {
	case "backend_error":
		writeError(w, channelWebUI, http.StatusServiceUnavailable, "auth_unavailable", "认证服务暂不可用")
	case "missing":
		writeError(w, channelWebUI, http.StatusUnauthorized, "missing_key", "缺少密钥")
	case "wrong_channel":
		writeError(w, channelWebUI, http.StatusForbidden, "wrong_channel", "该密钥类型不可用于此端点")
	default:
		writeError(w, channelWebUI, http.StatusUnauthorized, "invalid_key", "密钥失效或不正确")
	}
}

func (st *requestState) handleWebUICreate(w http.ResponseWriter, body []byte) {
	params, ok := decodeParams(body)
	if !ok {
		st.completeSilently(false)
		writeError(w, channelWebUI, http.StatusBadRequest, "bad_request", "无效的请求体")
		return
	}
	result := st.server.Links.Create(st.authCtx, params)
	st.respondBiz(w, channelWebUI, result)
}

func (st *requestState) handleWebUIDelete(w http.ResponseWriter, body []byte) {
	params, ok := decodeParams(body)
	if !ok {
		st.completeSilently(false)
		writeError(w, channelWebUI, http.StatusBadRequest, "bad_request", "无效的请求体")
		return
	}
	code, isStr := params["code"].(string)
	if !isStr {
		st.completeSilently(false)
		writeError(w, channelWebUI, http.StatusBadRequest, "invalid_code", "短链后缀必须为字符串")
		return
	}
	code = strings.TrimSpace(code)
	if code == "" {
		st.completeSilently(false)
		writeError(w, channelWebUI, http.StatusBadRequest, "missing_code", "缺少短链后缀")
		return
	}
	if !regexpWebUICode.MatchString(code) {
		st.completeSilently(false)
		writeError(w, channelWebUI, http.StatusBadRequest, "invalid_code", "短链后缀格式无效")
		return
	}
	result := st.server.Links.Delete(st.authCtx, code)
	st.respondBiz(w, channelWebUI, result)
}

func (st *requestState) handleWebUIList(w http.ResponseWriter) {
	result := st.server.Links.ListGrouped(st.authCtx)
	st.respondBiz(w, channelWebUI, result)
}

func (st *requestState) handleWebUIStat(w http.ResponseWriter) {
	result := st.server.Links.Stat(st.authCtx)
	st.respondBiz(w, channelWebUI, result)
}

// respondBiz 统一业务响应：失败释放预约，成功消费预约。
func (st *requestState) respondBiz(w http.ResponseWriter, ch channel, result link.BizResponse) {
	if !result.OK {
		st.completeSilently(false)
		status := result.HTTPStatus
		if status == 0 {
			status = http.StatusBadRequest
		}
		writeError(w, ch, status, result.Code, result.Message)
		return
	}
	if !st.server.Auth.Complete(st.authCtx, true) {
		writeError(w, ch, http.StatusInternalServerError, "internal_error", "服务器内部错误")
		return
	}
	writeJSON(w, http.StatusOK, result.Data)
}

// completeSilently 提交/释放一次性预约；失败仅记录（对齐 PHP 的 try/catch）。
func (st *requestState) completeSilently(success bool) {
	if st.authCtx == nil {
		return
	}
	if !st.server.Auth.Complete(st.authCtx, success) {
		st.server.Log.Warn("[http] 无法完成一次性密钥状态更新")
	}
}

// extractWebUICredential 提取凭证：X-Token → body.key。
func (st *requestState) extractWebUICredential(body []byte) string {
	if xToken := st.r.Header.Get("X-Token"); xToken != "" {
		return xToken
	}
	if len(body) == 0 {
		return ""
	}
	params, ok := decodeParams(body)
	if !ok {
		return ""
	}
	if v, ok := params["key"].(string); ok && v != "" {
		return v
	}
	return ""
}

var regexpWebUICode = regexp.MustCompile(`^[0-9a-zA-Z-]{1,5}$`)

func failedFiles(cfg *config.Config) []string {
	var out []string
	for _, p := range []string{cfg.PermPath, cfg.TempPath, cfg.KeysPath} {
		if !config.CheckFileAccess(p) {
			out = append(out, filepath.Base(p))
		}
	}
	return out
}
