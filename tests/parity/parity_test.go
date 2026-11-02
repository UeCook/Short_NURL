// Package parity 是开发期的 PHP 黄金响应对照工具。
//
// 需要本机安装 PHP 并设置 PHP_BIN 与 PHP_SRC（v2.0.3 项目根）：
//
//	PHP_BIN=C:\...\php.exe PHP_SRC=D:\coding\URL\URL_PHP_v2.0.3 go test ./tests/parity -run TestParity -v
//
// 两者都未设置时自动 skip，不影响其他机器/CI。
package parity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/UeCook/Short_NURL/internal/apikey"
	"github.com/UeCook/Short_NURL/internal/auth"
	"github.com/UeCook/Short_NURL/internal/config"
	"github.com/UeCook/Short_NURL/internal/hot"
	"github.com/UeCook/Short_NURL/internal/httpapi"
	"github.com/UeCook/Short_NURL/internal/link"
)

type caseSpec struct {
	name    string
	method  string
	path    string
	channel string // webui / headless
	token   string
	body    string
}

// TestParity 对照 PHP 与 Go 的 HTTP 响应。
func TestParity(t *testing.T) {
	phpBin := os.Getenv("PHP_BIN")
	phpSrc := os.Getenv("PHP_SRC")
	if phpBin == "" || phpSrc == "" {
		t.Skip("PHP_BIN / PHP_SRC 未设置，跳过黄金对照")
	}
	if _, err := os.Stat(phpBin); err != nil {
		t.Skip("PHP_BIN 不存在")
	}
	if _, err := os.Stat(filepath.Join(phpSrc, "api", "api.php")); err != nil {
		t.Skip("PHP_SRC 不是 v2.0.3 项目根")
	}

	work := t.TempDir()

	// 共享的假 Lua
	lua := &parityLua{items: map[string]map[string]any{}}
	luaSrv := httptest.NewServer(lua)
	t.Cleanup(luaSrv.Close)

	// ── PHP 侧 ────────────────────────────────────────────
	phpDir := filepath.Join(work, "php")
	if err := copyTree(phpSrc, phpDir); err != nil {
		t.Fatalf("copy php: %v", err)
	}
	phpData := filepath.Join(phpDir, "backend", "data")
	_ = os.MkdirAll(phpData, 0o755)

	// 受限环境（如 Windows 文件沙箱）会让 PHP 的 is_writable() 误报 false，
	// 而实际写入是成功的；此时 v2 引导会直接 430，对照无法进行，跳过并提示。
	if reported, actual := phpWritableProbe(t, phpBin, phpData); reported != actual {
		t.Skipf("PHP is_writable() 误报（reported=%v actual=%v）：请在非沙箱/真实权限下运行对照", reported, actual)
	}

	// ── Go 侧 ────────────────────────────────────────────
	goData := filepath.Join(work, "go-data")
	_ = os.MkdirAll(goData, 0o755)

	// 两边共用同一份 keys.json / internal_token
	for _, dir := range []string{phpData, goData} {
		if err := os.WriteFile(filepath.Join(dir, "internal_token"), []byte("paritytoken"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.Default()
	cfg.Site.Domain = "https://s.example.com"
	cfg.Site.TzOffset = "+08:00"
	cfg.PermPath = filepath.Join(goData, "perm.json")
	cfg.TempPath = filepath.Join(goData, "temp.json")
	cfg.KeysPath = filepath.Join(goData, "keys.json")
	cfg.InternalTokenPath = filepath.Join(goData, "internal_token")
	cfg.Internal.Host = luaSrv.URL

	keys := apikey.New(cfg.KeysPath, cfg.Site.TzOffset, 7, 2)
	residentKey, onetimeKey, serviceKey, err := seedKeys(keys)
	if err != nil {
		t.Fatal(err)
	}
	rawKeys, err := os.ReadFile(cfg.KeysPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(phpData, "keys.json"), rawKeys, 0o644); err != nil {
		t.Fatal(err)
	}

	authSvc := auth.New(keys)
	hotClient := hot.New(cfg.Internal.Host, cfg.InternalTokenPath,
		time.Duration(cfg.Internal.TimeoutSec*float64(time.Second)), 3, 50)
	links := link.NewServices(cfg, hotClient)
	goSrv := httptest.NewServer(httpapi.NewServer(cfg, keys, authSvc, links, nil))
	t.Cleanup(goSrv.Close)

	// PHP 内置服务器 + 路由脚本
	router := filepath.Join(phpDir, "parity-router.php")
	if err := os.WriteFile(router, []byte(phpRouter), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgPHP := filepath.Join(phpDir, "core", "config.php")
	if err := os.WriteFile(cfgPHP, []byte(phpConfig(phpData, luaSrv.URL)), 0o644); err != nil {
		t.Fatal(err)
	}

	port := freePort(t)
	cmd := exec.Command(phpBin, "-S", "127.0.0.1:"+strconv.Itoa(port), router)
	cmd.Dir = phpDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start php: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	phpURL := "http://127.0.0.1:" + strconv.Itoa(port)
	if err := waitReady(phpURL+"/api/ping", 5*time.Second); err != nil {
		t.Fatalf("php not ready: %v\n%s", err, stderr.String())
	}
	t.Cleanup(func() {
		if t.Failed() && stderr.Len() > 0 {
			t.Logf("PHP stderr:\n%s", stderr.String())
		}
	})

	cases := []caseSpec{
		{"ping", "GET", "/api/ping", "webui", "", ""},
		{"ping-head", "HEAD", "/api/ping", "webui", "", ""},
		{"ping-post", "POST", "/api/ping", "webui", "", ""},
		{"options-create", "OPTIONS", "/api/create", "webui", "", ""},
		{"webui-404", "GET", "/api/nope", "webui", "", ""},
		{"webui-405", "PUT", "/api/create", "webui", "KEY", "{}"},
		{"webui-missing-key", "POST", "/api/create", "webui", "", `{"url":"https://example.com"}`},
		{"webui-bad-key", "POST", "/api/create", "webui", "su_bad", `{"url":"https://example.com"}`},
		{"bad-json", "POST", "/api/create", "webui", "KEY", `null`},
		{"empty-array", "POST", "/api/create", "webui", "KEY", `[]`},
		{"missing-url", "POST", "/api/create", "webui", "KEY", `{}`},
		{"url-type", "POST", "/api/create", "webui", "KEY", `{"url":123}`},
		{"url-space", "POST", "/api/create", "webui", "KEY", `{"url":"  "}`},
		{"url-js", "POST", "/api/create", "webui", "KEY", `{"url":"javascript:alert(1)"}`},
		{"url-private", "POST", "/api/create", "webui", "KEY", `{"url":"https://127.0.0.1/a"}`},
		{"ttl-string", "POST", "/api/create", "webui", "KEY", `{"url":"https://example.com","ttl":"5"}`},
		{"ttl-null", "POST", "/api/create", "webui", "KEY", `{"url":"https://example.com","ttl":null}`},
		{"ttl-float", "POST", "/api/create", "webui", "KEY", `{"url":"https://example.com","ttl":1.5}`},
		{"ttl-exp", "POST", "/api/create", "webui", "KEY", `{"url":"https://example.com","ttl":1e3}`},
		{"ttl-negative", "POST", "/api/create", "webui", "KEY", `{"url":"https://example.com","ttl":-1}`},
		{"code-bad", "POST", "/api/create", "webui", "KEY", `{"url":"https://example.com","code":"ABC!"}`},
		{"code-reserved", "POST", "/api/create", "webui", "KEY", `{"url":"https://example.com","code":"api"}`},
		{"create-ok", "POST", "/api/create", "webui", "KEY", `{"url":"https://example.com/parity","code":"par1"}`},
		{"create-conflict", "POST", "/api/create", "webui", "KEY", `{"url":"https://example.com/other","code":"par1"}`},
		{"delete-missing-code", "POST", "/api/delete", "webui", "KEY", `{}`},
		{"delete-empty-code", "POST", "/api/delete", "webui", "KEY", `{"code":""}`},
		{"delete-bad-code", "POST", "/api/delete", "webui", "KEY", `{"code":"toolong1"}`},
		{"delete-notfound", "POST", "/api/delete", "webui", "KEY", `{"code":"zzzz"}`},
		{"list", "GET", "/api/list", "webui", "KEY", ""},
		{"stat", "GET", "/api/stat", "webui", "KEY", ""},
		{"delete-ok", "POST", "/api/delete", "webui", "KEY", `{"code":"par1"}`},
		{"headless-options", "OPTIONS", "/headless/api/create", "headless", "", ""},
		{"headless-404", "GET", "/headless/api/nope", "headless", "", ""},
		{"headless-405", "PUT", "/headless/api/list", "headless", "SVC", ""},
		{"headless-missing", "GET", "/headless/api/list", "headless", "", ""},
		{"headless-onetime", "GET", "/headless/api/list", "headless", "ONCE", ""},
		{"headless-create", "POST", "/headless/api/create", "headless", "SVC", `{"url":"https://example.com/hl","code":"hl01","len":5}`},
		{"headless-get", "GET", "/headless/api/get/hl01", "headless", "SVC", ""},
		{"headless-get-404", "GET", "/headless/api/get/zzzz", "headless", "SVC", ""},
		{"headless-delete", "POST", "/headless/api/delete", "headless", "SVC", `{"code":"hl01"}`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			phpResp := doRequest(t, phpURL, c, residentKey, onetimeKey, serviceKey)
			goResp := doRequest(t, goSrv.URL, c, residentKey, onetimeKey, serviceKey)
			compare(t, c, phpResp, goResp)
		})
	}
}

type response struct {
	status      int
	contentType string
	allow       string
	body        string
}

func doRequest(t *testing.T, base string, c caseSpec, resident, onetime, service string) response {
	t.Helper()
	token := ""
	switch c.token {
	case "KEY":
		token = resident
	case "ONCE":
		token = onetime
	case "SVC":
		token = service
	}
	var body io.Reader
	if c.body != "" {
		body = strings.NewReader(c.body)
	}
	req, err := http.NewRequest(c.method, base+c.path, body)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		if c.channel == "headless" {
			req.Header.Set("X-Headless-Token", token)
		} else {
			req.Header.Set("X-Token", token)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return response{
		status:      resp.StatusCode,
		contentType: resp.Header.Get("Content-Type"),
		allow:       resp.Header.Get("Allow"),
		body:        string(raw),
	}
}

func compare(t *testing.T, c caseSpec, php, gov response) {
	t.Helper()
	if php.status != gov.status {
		t.Fatalf("status: php=%d go=%d\nphp=%s\ngo=%s", php.status, gov.status, php.body, gov.body)
	}
	if php.allow != gov.allow {
		t.Fatalf("allow: php=%q go=%q", php.allow, gov.allow)
	}
	if php.contentType != gov.contentType {
		t.Fatalf("content-type: php=%q go=%q", php.contentType, gov.contentType)
	}
	if php.body == "" && gov.body == "" {
		return
	}
	var pv, gv any
	perr := json.Unmarshal([]byte(php.body), &pv)
	gerr := json.Unmarshal([]byte(gov.body), &gv)
	if perr != nil || gerr != nil {
		if php.body != gov.body {
			t.Fatalf("body: php=%q go=%q", php.body, gov.body)
		}
		return
	}
	pj, _ := json.Marshal(pv)
	gj, _ := json.Marshal(gv)
	if string(pj) != string(gj) {
		t.Fatalf("json: php=%s go=%s", pj, gj)
	}
}

// ── 假 Lua ─────────────────────────────────────────────────────

type parityLua struct {
	items map[string]map[string]any
}

func (p *parityLua) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	body, _ := io.ReadAll(r.Body)
	switch r.URL.Path {
	case "/internal/set":
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		code, _ := m["code"].(string)
		p.items[code] = m
		_, _ = w.Write([]byte(`{"ok":true}`))
	case "/internal/delete":
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		code, _ := m["code"].(string)
		delete(p.items, code)
		_, _ = w.Write([]byte(`{"ok":true}`))
	case "/internal/mutation":
		_, _ = w.Write([]byte(`{"ok":true}`))
	case "/internal/stat":
		_, _ = w.Write([]byte(`{"perm_count":0,"temp_count":0}`))
	}
}

// ── 工具 ───────────────────────────────────────────────────────

func seedKeys(k *apikey.KeyStore) (resident, onetime, service string, err error) {
	resident, err = k.GenerateResident()
	if err != nil {
		return
	}
	pool, err := k.FillPool()
	if err != nil || len(pool) == 0 {
		return resident, "", "", fmt.Errorf("fill pool: %w", err)
	}
	onetime = pool[0]
	service, err = k.GenerateService("parity")
	return
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func waitReady(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s", url)
}

const phpRouter = `<?php
$uri = parse_url($_SERVER['REQUEST_URI'], PHP_URL_PATH);
if (strpos($uri, '/headless/') === 0) {
    require __DIR__ . '/headless/headless.php';
    return;
}
require __DIR__ . '/api/api.php';
`

// phpLiteral 生成 PHP 单引号字面量；路径统一转成 /，避免 Windows 反斜杠的转义歧义。
func phpLiteral(s string) string {
	s = strings.ReplaceAll(s, `\`, `/`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s + "'"
}

// phpWritableProbe 同时取 PHP 报告的 is_writable 与真实写入结果。
// 两者不一致说明环境受限（Windows 沙箱），对照应跳过而不是误报失败。
func phpWritableProbe(t *testing.T, phpBin, dir string) (reported, actual bool) {
	t.Helper()
	probe := filepath.Join(dir, ".parity-probe")
	code := `printf("R=%d A=%d", (is_writable(` + phpLiteral(dir) + `) ? 1 : 0), (file_put_contents(` + phpLiteral(probe) + `, "x") !== false ? 1 : 0)); @unlink(` + phpLiteral(probe) + `);`
	out, err := exec.Command(phpBin, "-r", code).CombinedOutput()
	if err != nil {
		t.Fatalf("PHP 写权限探测失败: %v\n%s", err, out)
	}
	var r, a int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "R=%d A=%d", &r, &a); err != nil {
		t.Fatalf("无法解析 PHP 探测输出 %q: %v", out, err)
	}
	return r == 1, a == 1
}

func phpConfig(dataDir, luaURL string) string {
	return `<?php
return [
    'domain' => 'https://s.example.com',
    'panel_origin' => '',
    'panel_enabled' => true,
    'tz_offset' => '+08:00',
    'perm_path' => ` + phpLiteral(filepath.Join(dataDir, "perm.json")) + `,
    'temp_path' => ` + phpLiteral(filepath.Join(dataDir, "temp.json")) + `,
    'keys_path' => ` + phpLiteral(filepath.Join(dataDir, "keys.json")) + `,
    'internal_token_path' => ` + phpLiteral(filepath.Join(dataDir, "internal_token")) + `,
    'key_ttl_days' => 7,
    'onetime_pool_size' => 2,
    'perm_limit' => 10000,
    'temp_limit' => 500,
    'svc_perm_limit' => 10000,
    'svc_temp_limit' => 500,
    'svc_code_length' => 4,
    'ttl_max' => 31536000,
    'reserved_codes' => ['api','stat','admin','data','lua','headless','internal','nurl'],
    'internal_host' => ` + phpLiteral(luaURL) + `,
    'internal_timeout' => 2.0,
    'cold_fallback' => false,
];
`
}
