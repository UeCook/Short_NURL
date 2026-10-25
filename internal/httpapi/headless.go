package httpapi

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/UeCook/Short_NURL/internal/auth"
)

var headlessGetRe = regexp.MustCompile(`^/headless/api/get/[0-9a-zA-Z-]{1,5}$`)

var headlessRoutes = map[string]string{
	"/headless/api/create": http.MethodPost,
	"/headless/api/delete": http.MethodPost,
	"/headless/api/list":   http.MethodGet,
	"/headless/api/stat":   http.MethodGet,
}

var headlessActions = map[string]string{
	"/headless/api/create": "create",
	"/headless/api/delete": "delete",
	"/headless/api/list":   "list",
	"/headless/api/stat":   "stat",
}

// serveHeadless 处理 /headless/api/*（对齐 headless/headless.php + bootstrap/headless.php）。
func (st *requestState) serveHeadless(w http.ResponseWriter) {
	r := st.r
	cfg := st.server.Cfg

	// 服务间调用，无需 CORS：显式拒绝浏览器预检
	if r.Method == http.MethodOptions {
		w.Header().Set("Allow", "GET, POST")
		writeEmpty(w, http.StatusMethodNotAllowed)
		return
	}

	if failed := failedFiles(cfg); len(failed) > 0 {
		st.server.Log.Warn("[headless_bootstrap] 数据文件不可访问", "files", strings.Join(failed, ", "))
		writeError(w, channelHeadless, 430, "data_inaccessible", "数据文件不可访问，请检查文件权限")
		return
	}
	if strings.Contains(cfg.Site.Domain, "{") {
		writeError(w, channelHeadless, http.StatusInternalServerError, "config_error", "配置的 domain 尚未配置")
		return
	}

	body, tooLarge := st.readBody()
	if tooLarge {
		writeError(w, channelHeadless, http.StatusRequestEntityTooLarge, "payload_too_large", "请求体过大")
		return
	}

	// 路由 + 方法
	expected, routed := headlessRoutes[st.path]
	action := ""
	if routed {
		action = headlessActions[st.path]
	} else if headlessGetRe.MatchString(st.path) {
		expected = http.MethodGet
		action = "get"
		routed = true
	}
	if !routed {
		writeError(w, channelHeadless, http.StatusNotFound, "not_found", "未知的端点")
		return
	}
	if r.Method != expected {
		w.Header().Set("Allow", expected)
		writeError(w, channelHeadless, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不允许")
		return
	}

	// 认证（仅 X-Headless-Token）
	credential := st.r.Header.Get("X-Headless-Token")
	allowed, _ := auth.AllowedKeyTypes("headless", action)
	result := st.server.Auth.Authenticate(credential, allowed)
	if !result.Success {
		st.writeHeadlessAuthError(w, result.Reason, body)
		return
	}
	st.authCtx = result.Ctx

	switch action {
	case "create":
		st.handleHeadlessCreate(w, body)
	case "delete":
		st.handleHeadlessDelete(w, body)
	case "list":
		st.respondBiz(w, channelHeadless, st.server.Links.ListGrouped(st.authCtx))
	case "stat":
		st.respondBiz(w, channelHeadless, st.server.Links.Stat(st.authCtx))
	case "get":
		code := st.path[len("/headless/api/get/"):]
		st.respondBiz(w, channelHeadless, st.server.Links.Get(st.authCtx, code))
	}
}

func (st *requestState) writeHeadlessAuthError(w http.ResponseWriter, reason string, body []byte) {
	switch reason {
	case "backend_error":
		writeError(w, channelHeadless, http.StatusServiceUnavailable, "auth_unavailable", "认证服务暂不可用")
	case "missing":
		hint := ""
		if st.r.Header.Get("X-Token") != "" {
			hint = "（检测到 X-Token 头，请改用 X-Headless-Token）"
		} else if len(body) > 0 && strings.Contains(string(body), `"key"`) {
			hint = "（检测到 body 中的 key 字段，请改用 X-Headless-Token 头）"
		}
		writeError(w, channelHeadless, http.StatusUnauthorized, "missing_key", "缺少密钥"+hint)
	case "wrong_channel":
		writeError(w, channelHeadless, http.StatusForbidden, "forbidden_key_type", "该密钥类型不可用于此端点")
	default:
		writeError(w, channelHeadless, http.StatusUnauthorized, "invalid_key", "密钥失效或不正确")
	}
}

func (st *requestState) handleHeadlessCreate(w http.ResponseWriter, body []byte) {
	params, ok := decodeParams(body)
	if !ok {
		st.completeSilently(false)
		writeError(w, channelHeadless, http.StatusBadRequest, "bad_request", "无效的请求体")
		return
	}
	st.respondBiz(w, channelHeadless, st.server.Links.Create(st.authCtx, params))
}

func (st *requestState) handleHeadlessDelete(w http.ResponseWriter, body []byte) {
	params, ok := decodeParams(body)
	if !ok {
		st.completeSilently(false)
		writeError(w, channelHeadless, http.StatusBadRequest, "bad_request", "无效的请求体")
		return
	}
	code, isStr := params["code"].(string)
	if !isStr {
		st.completeSilently(false)
		writeError(w, channelHeadless, http.StatusBadRequest, "invalid_code", "短链后缀必须为字符串")
		return
	}
	code = strings.TrimSpace(code)
	if code == "" {
		st.completeSilently(false)
		writeError(w, channelHeadless, http.StatusBadRequest, "missing_code", "缺少短链后缀")
		return
	}
	st.respondBiz(w, channelHeadless, st.server.Links.Delete(st.authCtx, code))
}
