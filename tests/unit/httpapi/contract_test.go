package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func decodeBody(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("invalid JSON body %q: %v", raw, err)
	}
	return m
}

func TestPing(t *testing.T) {
	h := newHarness(t)
	resp, raw := h.do(t, http.MethodGet, "/api/ping", "", "")
	if resp.StatusCode != 200 || raw != `{"ok":true}` {
		t.Fatalf("ping = %d %s", resp.StatusCode, raw)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("content-type = %q", ct)
	}

	resp, _ = h.do(t, http.MethodHead, "/api/ping", "", "")
	if resp.StatusCode != 200 {
		t.Fatalf("HEAD ping = %d", resp.StatusCode)
	}
	if resp.Header.Get("Content-Length") != "11" {
		t.Fatalf("HEAD ping content-length = %q", resp.Header.Get("Content-Length"))
	}

	resp, raw = h.do(t, http.MethodPost, "/api/ping", "", "")
	if resp.StatusCode != 405 {
		t.Fatalf("POST ping = %d", resp.StatusCode)
	}
	if resp.Header.Get("Allow") != "GET, HEAD" {
		t.Fatalf("allow = %q", resp.Header.Get("Allow"))
	}
	m := decodeBody(t, raw)
	if m["code"] != "method_not_allowed" || m["error"] != "请求方法不允许" {
		t.Fatalf("body = %s", raw)
	}
}

func TestWebUIRouting(t *testing.T) {
	h := newHarness(t)

	// 404 无需认证
	resp, raw := h.do(t, http.MethodGet, "/api/nope", "", "")
	if resp.StatusCode != 404 {
		t.Fatalf("404 = %d", resp.StatusCode)
	}
	if m := decodeBody(t, raw); m["code"] != "not_found" || m["error"] != "未知的端点" {
		t.Fatalf("body = %s", raw)
	}

	// 尾斜杠归一
	resp, _ = h.do(t, http.MethodGet, "/api/list/", h.token, "")
	if resp.StatusCode != 200 {
		t.Fatalf("/api/list/ must route (got %d)", resp.StatusCode)
	}

	// 双斜杠不匹配
	resp, _ = h.do(t, http.MethodGet, "/api//list", h.token, "")
	if resp.StatusCode != 404 {
		t.Fatalf("/api//list = %d", resp.StatusCode)
	}

	// 百分号编码不解码 → 404
	resp, _ = h.do(t, http.MethodGet, "/api/%6cist", h.token, "")
	if resp.StatusCode != 404 {
		t.Fatalf("encoded path = %d", resp.StatusCode)
	}

	// 方法不允许
	resp, raw = h.do(t, http.MethodPut, "/api/create", h.token, "{}")
	if resp.StatusCode != 405 || resp.Header.Get("Allow") != "POST" {
		t.Fatalf("PUT create = %d allow=%q", resp.StatusCode, resp.Header.Get("Allow"))
	}
	if m := decodeBody(t, raw); m["code"] != "method_not_allowed" {
		t.Fatalf("body = %s", raw)
	}
}

func TestWebUIOptions(t *testing.T) {
	h := newHarness(t)
	resp, raw := h.do(t, http.MethodOptions, "/api/create", "", "")
	if resp.StatusCode != 204 || raw != "" {
		t.Fatalf("OPTIONS = %d %q", resp.StatusCode, raw)
	}
	if resp.Header.Get("Access-Control-Allow-Methods") != "GET, POST, OPTIONS" {
		t.Fatalf("cors methods = %q", resp.Header.Get("Access-Control-Allow-Methods"))
	}
	if resp.Header.Get("Access-Control-Allow-Headers") != "Content-Type, X-Token" {
		t.Fatalf("cors headers = %q", resp.Header.Get("Access-Control-Allow-Headers"))
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://s.example.com" {
		t.Fatalf("cors origin = %q", got)
	}
	if resp.Header.Get("Vary") != "Origin" {
		t.Fatalf("vary = %q", resp.Header.Get("Vary"))
	}
}

func TestWebUIAuthErrors(t *testing.T) {
	h := newHarness(t)

	resp, raw := h.do(t, http.MethodPost, "/api/create", "", `{"url":"https://example.com"}`)
	if resp.StatusCode != 401 {
		t.Fatalf("missing key = %d", resp.StatusCode)
	}
	if m := decodeBody(t, raw); m["code"] != "missing_key" || m["error"] != "缺少密钥" {
		t.Fatalf("body = %s", raw)
	}

	resp, raw = h.do(t, http.MethodPost, "/api/create", "su_bad", `{"url":"https://example.com"}`)
	if resp.StatusCode != 401 {
		t.Fatalf("invalid key = %d", resp.StatusCode)
	}
	if m := decodeBody(t, raw); m["code"] != "invalid_key" {
		t.Fatalf("body = %s", raw)
	}

	// onetime 不能用于 delete
	resp, raw = h.do(t, http.MethodPost, "/api/delete", h.onetime, `{"code":"abc1"}`)
	if resp.StatusCode != 403 {
		t.Fatalf("onetime delete = %d", resp.StatusCode)
	}
	if m := decodeBody(t, raw); m["code"] != "wrong_channel" {
		t.Fatalf("body = %s", raw)
	}

	// service 不能用于 WebUI
	resp, raw = h.do(t, http.MethodGet, "/api/list", h.service, "")
	if resp.StatusCode != 403 {
		t.Fatalf("service list = %d", resp.StatusCode)
	}
	if m := decodeBody(t, raw); m["code"] != "wrong_channel" {
		t.Fatalf("body = %s", raw)
	}
}

func TestCreateAndQuery(t *testing.T) {
	h := newHarness(t)

	resp, raw := h.do(t, http.MethodPost, "/api/create", h.token, `{"url":"https://example.com"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("create = %d %s", resp.StatusCode, raw)
	}
	m := decodeBody(t, raw)
	su, _ := m["short_url"].(string)
	if !strings.HasPrefix(su, "https://s.example.com/") || len(strings.TrimPrefix(su, "https://s.example.com/")) != 4 {
		t.Fatalf("short_url = %v", su)
	}
	if m["exp"] != nil || m["dedup"] != false || m["synced"] != true || m["key_consumed"] != false {
		t.Fatalf("create body = %s", raw)
	}

	// 去重
	resp, raw = h.do(t, http.MethodPost, "/api/create", h.token, `{"url":"https://example.com"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("dedup create = %d %s", resp.StatusCode, raw)
	}
	m = decodeBody(t, raw)
	if m["dedup"] != true {
		t.Fatalf("dedup body = %s", raw)
	}

	// 列表
	resp, raw = h.do(t, http.MethodGet, "/api/list", h.token, "")
	if resp.StatusCode != 200 {
		t.Fatalf("list = %d %s", resp.StatusCode, raw)
	}
	var grouped struct {
		Permanent []map[string]any `json:"permanent"`
		Temporary []map[string]any `json:"temporary"`
	}
	if err := json.Unmarshal([]byte(raw), &grouped); err != nil {
		t.Fatal(err)
	}
	if len(grouped.Permanent) != 1 || len(grouped.Temporary) != 0 {
		t.Fatalf("list = %s", raw)
	}
	if grouped.Permanent[0]["exp"] != "permanent" {
		t.Fatalf("exp = %v", grouped.Permanent[0]["exp"])
	}

	// 统计
	resp, raw = h.do(t, http.MethodGet, "/api/stat", h.token, "")
	if resp.StatusCode != 200 {
		t.Fatalf("stat = %d %s", resp.StatusCode, raw)
	}
	m = decodeBody(t, raw)
	if m["perm_count"] != float64(1) || m["hot_available"] != true {
		t.Fatalf("stat = %s", raw)
	}
	if m["hot_perm_count"] != float64(1) {
		t.Fatalf("hot count = %v", m["hot_perm_count"])
	}

	// 删除
	code, _ := grouped.Permanent[0]["id"].(string)
	resp, raw = h.do(t, http.MethodPost, "/api/delete", h.token, `{"code":"`+code+`"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("delete = %d %s", resp.StatusCode, raw)
	}
	m = decodeBody(t, raw)
	if m["ok"] != true || m["synced"] != true || m["warning"] != nil {
		t.Fatalf("delete body = %s", raw)
	}

	resp, raw = h.do(t, http.MethodPost, "/api/delete", h.token, `{"code":"`+code+`"}`)
	if resp.StatusCode != 404 {
		t.Fatalf("redelete = %d %s", resp.StatusCode, raw)
	}
}

func TestCreateValidation(t *testing.T) {
	h := newHarness(t)
	cases := []struct {
		body string
		code string
		want int
	}{
		{``, "bad_request", 400},
		{`null`, "bad_request", 400},
		{`"str"`, "bad_request", 400},
		{`123`, "bad_request", 400},
		{`{"url":"https://example.com"}garbage`, "bad_request", 400},
		{`[]`, "missing_url", 400},
		{`{}`, "missing_url", 400},
		{`{"url":123}`, "missing_url", 400},
		{`{"url":"   "}`, "missing_url", 400},
		{`{"url":"javascript:alert(1)"}`, "invalid_url", 400},
		{`{"url":"https://127.0.0.1/a"}`, "private_url", 400},
		{`{"url":"https://example.com","ttl":"5"}`, "invalid_ttl", 400},
		{`{"url":"https://example.com","ttl":null}`, "invalid_ttl", 400},
		{`{"url":"https://example.com","ttl":1.5}`, "invalid_ttl", 400},
		{`{"url":"https://example.com","ttl":1e3}`, "invalid_ttl", 400},
		{`{"url":"https://example.com","ttl":-1}`, "ttl_exceeded", 400},
		{`{"url":"https://example.com","code":"ABC!"}`, "invalid_code", 400},
		{`{"url":"https://example.com","code":"api"}`, "reserved_code", 400},
	}
	for _, c := range cases {
		resp, raw := h.do(t, http.MethodPost, "/api/create", h.token, c.body)
		if resp.StatusCode != c.want {
			t.Fatalf("body %q => %d %s, want %d", c.body, resp.StatusCode, raw, c.want)
		}
		m := decodeBody(t, raw)
		if m["code"] != c.code {
			t.Fatalf("body %q => code %v, want %s", c.body, m["code"], c.code)
		}
	}

	// code "0" 视为未提供 → 自动生成
	resp, raw := h.do(t, http.MethodPost, "/api/create", h.token,
		`{"url":"https://example.org/zero","code":"0"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("code 0 = %d %s", resp.StatusCode, raw)
	}
	m := decodeBody(t, raw)
	if m["dedup"] != false {
		t.Fatalf("code 0 body = %s", raw)
	}

	// 自定义短码大写归一
	resp, raw = h.do(t, http.MethodPost, "/api/create", h.token,
		`{"url":"https://example.org/upper","code":"AbC9"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("custom code = %d %s", resp.StatusCode, raw)
	}
	m = decodeBody(t, raw)
	if m["short_url"] != "https://s.example.com/abc9" {
		t.Fatalf("custom code body = %s", raw)
	}

	// 冲突
	resp, raw = h.do(t, http.MethodPost, "/api/create", h.token,
		`{"url":"https://example.org/other","code":"AbC9"}`)
	if resp.StatusCode != 409 {
		t.Fatalf("conflict = %d %s", resp.StatusCode, raw)
	}
	if m := decodeBody(t, raw); m["code"] != "conflict" {
		t.Fatalf("body = %s", raw)
	}
}

func TestHeadlessChannels(t *testing.T) {
	h := newHarness(t)

	// OPTIONS 405 空体
	resp, raw := h.doHeadless(t, http.MethodOptions, "/headless/api/create", "", "")
	if resp.StatusCode != 405 || raw != "" || resp.Header.Get("Allow") != "GET, POST" {
		t.Fatalf("headless OPTIONS = %d %q allow=%q", resp.StatusCode, raw, resp.Header.Get("Allow"))
	}

	// 404 / 405 在认证之前
	resp, raw = h.doHeadless(t, http.MethodGet, "/headless/api/nope", "", "")
	if resp.StatusCode != 404 {
		t.Fatalf("404 = %d", resp.StatusCode)
	}
	if m := decodeBody(t, raw); m["error"] != "not_found" || m["message"] != "未知的端点" {
		t.Fatalf("body = %s", raw)
	}

	resp, _ = h.doHeadless(t, http.MethodPut, "/headless/api/list", h.service, "")
	if resp.StatusCode != 405 || resp.Header.Get("Allow") != "GET" {
		t.Fatalf("wrong method = %d allow=%q", resp.StatusCode, resp.Header.Get("Allow"))
	}

	// missing_key 提示
	resp, raw = h.doHeadless(t, http.MethodGet, "/headless/api/list", "", "")
	if resp.StatusCode != 401 {
		t.Fatalf("missing = %d", resp.StatusCode)
	}
	if m := decodeBody(t, raw); m["error"] != "missing_key" || !strings.Contains(m["message"].(string), "缺少密钥") {
		t.Fatalf("body = %s", raw)
	}

	req, _ := http.NewRequest(http.MethodGet, h.srv.URL+"/headless/api/list", nil)
	req.Header.Set("X-Token", h.token)
	r2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw2, _ := io.ReadAll(r2.Body)
	r2.Body.Close()
	m := decodeBody(t, string(raw2))
	if !strings.Contains(m["message"].(string), "X-Token") {
		t.Fatalf("hint missing: %s", raw2)
	}

	// service key 走 headless
	resp, raw = h.doHeadless(t, http.MethodPost, "/headless/api/create", h.service,
		`{"url":"https://example.net/svc","ttl":0,"len":5}`)
	if resp.StatusCode != 200 {
		t.Fatalf("svc create = %d %s", resp.StatusCode, raw)
	}
	m = decodeBody(t, raw)
	su := m["short_url"].(string)
	if len(strings.TrimPrefix(su, "https://s.example.com/")) != 5 {
		t.Fatalf("svc code length = %s", su)
	}

	code := strings.TrimPrefix(su, "https://s.example.com/")
	resp, raw = h.doHeadless(t, http.MethodGet, "/headless/api/get/"+code, h.service, "")
	if resp.StatusCode != 200 {
		t.Fatalf("get = %d %s", resp.StatusCode, raw)
	}
	m = decodeBody(t, raw)
	if m["id"] != code || m["exp"] != "permanent" {
		t.Fatalf("get body = %s", raw)
	}

	// headless delete 不做格式校验（大写归一后命中）
	resp, raw = h.doHeadless(t, http.MethodPost, "/headless/api/delete", h.service, `{"code":"`+strings.ToUpper(code)+`"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("headless delete = %d %s", resp.StatusCode, raw)
	}

	// 一次性 Key 管理被拒
	resp, raw = h.doHeadless(t, http.MethodGet, "/headless/api/list", h.onetime, "")
	if resp.StatusCode != 403 {
		t.Fatalf("onetime list = %d %s", resp.StatusCode, raw)
	}
	if m := decodeBody(t, raw); m["error"] != "forbidden_key_type" {
		t.Fatalf("body = %s", raw)
	}
}

func TestPanelDisabled(t *testing.T) {
	h := newHarness(t)
	h.cfg.Site.PanelEnabled = false

	// ping 不受影响
	resp, _ := h.do(t, http.MethodGet, "/api/ping", "", "")
	if resp.StatusCode != 200 {
		t.Fatalf("ping = %d", resp.StatusCode)
	}

	resp, raw := h.do(t, http.MethodGet, "/api/list", h.token, "")
	if resp.StatusCode != 403 {
		t.Fatalf("panel disabled = %d", resp.StatusCode)
	}
	if m := decodeBody(t, raw); m["code"] != "panel_disabled" {
		t.Fatalf("body = %s", raw)
	}

	// headless 不受面板开关影响
	resp, _ = h.doHeadless(t, http.MethodGet, "/headless/api/list", h.service, "")
	if resp.StatusCode != 200 {
		t.Fatalf("headless with panel off = %d", resp.StatusCode)
	}
}

func TestHotFailureStrictMode(t *testing.T) {
	h := newHarness(t)
	h.lua.mu.Lock()
	h.lua.fail = true
	h.lua.mu.Unlock()

	resp, raw := h.do(t, http.MethodPost, "/api/create", h.token, `{"url":"https://example.com/hotfail"}`)
	if resp.StatusCode != 503 {
		t.Fatalf("hot failure = %d %s", resp.StatusCode, raw)
	}
	if m := decodeBody(t, raw); m["code"] != "hot_store_unavailable" {
		t.Fatalf("body = %s", raw)
	}

	// 冷存储不应被写入
	resp, raw = h.do(t, http.MethodGet, "/api/stat", h.token, "")
	if resp.StatusCode != 200 {
		t.Fatalf("stat = %d", resp.StatusCode)
	}
	m := decodeBody(t, raw)
	if m["perm_count"] != float64(0) {
		t.Fatalf("cold store must stay empty: %s", raw)
	}
}

func TestHotFailureColdFallback(t *testing.T) {
	h := newHarness(t)
	h.cfg.Policy.ColdFallback = true
	h.lua.mu.Lock()
	h.lua.fail = true
	h.lua.mu.Unlock()

	resp, raw := h.do(t, http.MethodPost, "/api/create", h.token, `{"url":"https://example.com/fallback"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("fallback create = %d %s", resp.StatusCode, raw)
	}
	m := decodeBody(t, raw)
	if m["synced"] != false || m["warning"] == nil {
		t.Fatalf("body = %s", raw)
	}
}

func TestOnetimeConsumption(t *testing.T) {
	h := newHarness(t)

	resp, raw := h.do(t, http.MethodPost, "/api/create", h.onetime, `{"url":"https://example.com/once"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("onetime create = %d %s", resp.StatusCode, raw)
	}
	if m := decodeBody(t, raw); m["key_consumed"] != true {
		t.Fatalf("body = %s", raw)
	}

	// 同一 Key 不可复用
	resp, raw = h.do(t, http.MethodPost, "/api/create", h.onetime, `{"url":"https://example.com/once2"}`)
	if resp.StatusCode != 401 {
		t.Fatalf("reused onetime = %d %s", resp.StatusCode, raw)
	}
}

func TestPayloadTooLarge(t *testing.T) {
	h := newHarness(t)
	h.cfg.Server.MaxBodyBytes = 64
	big := `{"url":"https://example.com/` + strings.Repeat("a", 200) + `"}`
	resp, raw := h.do(t, http.MethodPost, "/api/create", h.token, big)
	if resp.StatusCode != 413 {
		t.Fatalf("payload too large = %d %s", resp.StatusCode, raw)
	}
	if m := decodeBody(t, raw); m["code"] != "payload_too_large" {
		t.Fatalf("body = %s", raw)
	}
}
