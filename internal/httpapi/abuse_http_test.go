package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UeCook/Short_NURL/internal/apikey"
	"github.com/UeCook/Short_NURL/internal/auth"
	"github.com/UeCook/Short_NURL/internal/config"
	"github.com/UeCook/Short_NURL/internal/fslock"
	"github.com/UeCook/Short_NURL/internal/hot"
	"github.com/UeCook/Short_NURL/internal/link"
)

type protectedFixture struct {
	server   *Server
	resident string
	onetime  string
	service  string
	now      *time.Time
}

func newProtectedFixture(t *testing.T) *protectedFixture {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Site.Domain = "https://s.example.com"
	cfg.PermPath, cfg.TempPath, cfg.KeysPath = filepath.Join(dir, "perm.json"), filepath.Join(dir, "temp.json"), filepath.Join(dir, "keys.json")
	for _, path := range []string{cfg.PermPath, cfg.TempPath} {
		if err := os.WriteFile(path, []byte(`{"v":1,"at":"2026-01-01T00:00:00+08:00","d":{}}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	keys := apikey.New(cfg.KeysPath, "+08:00", 7, 1)
	resident, err := keys.GenerateResident()
	if err != nil {
		t.Fatal(err)
	}
	onetime, err := keys.FillPool()
	if err != nil {
		t.Fatal(err)
	}
	service, err := keys.GenerateService("test")
	if err != nil {
		t.Fatal(err)
	}
	hotClient := hot.New("http://127.0.0.1:1", "", time.Millisecond, 1, 0)
	s := NewServer(cfg, keys, auth.New(keys), link.NewServices(cfg, hotClient), slog.New(slog.NewTextHandler(io.Discard, nil)))
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.guard.now = func() time.Time { return now }
	return &protectedFixture{s, resident, onetime[0], service, &now}
}

func serveProtected(t *testing.T, s *Server, path, key string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.RemoteAddr = "192.0.2.1:12345"
	header := "X-Token"
	if strings.HasPrefix(path, "/headless") {
		header = "X-Headless-Token"
	}
	if key != "" {
		r.Header.Set(header, key)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func responseFields(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &fields); err != nil {
		t.Fatalf("invalid response: %s", w.Body.String())
	}
	return fields
}

type unreadableBody struct{}

func (unreadableBody) Read([]byte) (int, error) { panic("blocked request body must not be read") }
func (unreadableBody) Close() error             { return nil }

func TestProtectedHTTPProgressiveWarningsSharedAcrossChannels(t *testing.T) {
	f := newProtectedFixture(t)
	for attempt := 1; attempt <= 5; attempt++ {
		path := "/api/list"
		if attempt%2 == 0 || attempt == 5 {
			path = "/headless/api/list"
		}
		w := serveProtected(t, f.server, path, fmt.Sprintf("su_bad_%d", attempt))
		fields := responseFields(t, w)
		if attempt < 5 {
			message := fields["error"]
			if strings.HasPrefix(path, "/headless") {
				message = fields["message"]
			}
			if w.Code != 401 || !strings.Contains(fmt.Sprint(message), fmt.Sprintf("第 %d 次失败", attempt)) || !strings.Contains(fmt.Sprint(message), fmt.Sprintf("还可尝试 %d 次", 5-attempt)) {
				t.Fatalf("warning %d: %d %s", attempt, w.Code, w.Body.String())
			}
			if attempt == 4 && !strings.Contains(fmt.Sprint(message), "再错一次将封禁 15 分钟") {
				t.Fatalf("fourth warning lacks lockout notice: %s", w.Body.String())
			}
		} else if w.Code != 429 || fields["error"] != "auth_blocked" || w.Header().Get("Retry-After") != "900" {
			t.Fatalf("fifth error did not immediately block: %d %s", w.Code, w.Body.String())
		}
	}

	// 即使数据路径不可访问，已封禁请求也必须在文件检查和 body 读取前返回。
	originalPath := f.server.Cfg.KeysPath
	f.server.Cfg.KeysPath = filepath.Join(originalPath, "not-a-directory")
	r := httptest.NewRequest(http.MethodPost, "/api/create", nil)
	r.RemoteAddr, r.Body = "192.0.2.1:9999", unreadableBody{}
	r.Header.Set("X-Token", f.onetime)
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	if w.Code != 429 || w.Header().Get("Access-Control-Expose-Headers") != "Retry-After" {
		t.Fatalf("blocked request reached business processing: %d %s", w.Code, w.Body.String())
	}
	f.server.Cfg.KeysPath = originalPath
	status, err := f.server.Keys.Status()
	if err != nil || status.OnetimeCount != 1 || status.OnetimeReservedCount != 0 {
		t.Fatalf("blocked request consumed/reserved one-time key: %+v %v", status, err)
	}
	*f.now = f.now.Add(15 * time.Minute)
	w = serveProtected(t, f.server, "/headless/api/list", "another-invalid-key")
	if w.Code != 429 || w.Header().Get("Retry-After") != "1800" {
		t.Fatalf("first error after expiry did not escalate: %d %s", w.Code, w.Body.String())
	}
	*f.now = f.now.Add(30 * time.Minute)
	w = serveProtected(t, f.server, "/api/list", f.resident)
	if w.Code != 200 {
		t.Fatalf("valid key after expiry failed: %d %s", w.Code, w.Body.String())
	}
	w = serveProtected(t, f.server, "/api/list", "invalid-after-success")
	if w.Code != 401 || !strings.Contains(w.Body.String(), "还可尝试 4 次") {
		t.Fatalf("success did not reset failure history: %d %s", w.Code, w.Body.String())
	}
}

func TestMissingWrongChannelAndBackendErrorsDoNotCountAsBadKeys(t *testing.T) {
	f := newProtectedFixture(t)
	f.server.guard.cfg.Burst, f.server.guard.cfg.GlobalBurst = 100, 100
	for i := 0; i < 6; i++ {
		if w := serveProtected(t, f.server, "/api/list", ""); w.Code != 401 {
			t.Fatal("missing key became an authentication ban")
		}
		if w := serveProtected(t, f.server, "/api/list", f.service); w.Code != 403 {
			t.Fatal("wrong-channel key became an authentication ban")
		}
	}
	if err := os.WriteFile(f.server.Cfg.KeysPath, []byte("corrupted JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if w := serveProtected(t, f.server, "/api/list", f.resident); w.Code != 503 {
			t.Fatalf("backend failure counted as bad key: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestDefaultRateLimitRejectsBeforeOneTimeReservation(t *testing.T) {
	f := newProtectedFixture(t)
	f.server.guard.cfg.Burst = 1
	if w := serveProtected(t, f.server, "/api/list", "bad-key"); w.Code != 401 {
		t.Fatal(w.Body.String())
	}
	w := serveProtected(t, f.server, "/headless/api/list", f.onetime)
	if w.Code != 429 || responseFields(t, w)["error"] != "rate_limited" || w.Header().Get("Retry-After") != "2" {
		t.Fatalf("per-IP limit not shared: %d %s", w.Code, w.Body.String())
	}
	status, err := f.server.Keys.Status()
	if err != nil || status.OnetimeCount != 1 || status.OnetimeReservedCount != 0 {
		t.Fatalf("rate limit changed one-time key state: %+v %v", status, err)
	}
}

func TestGuardDoesNotBlockHealthPreflightAndReleasesAfterPanic(t *testing.T) {
	f := newProtectedFixture(t)
	for i := 0; i < 5; i++ {
		serveProtected(t, f.server, "/api/list", "bad-key")
	}
	if w := serveProtected(t, f.server, "/api/ping", ""); w.Code != 200 {
		t.Fatalf("ban blocked health check: %d", w.Code)
	}
	r := httptest.NewRequest(http.MethodOptions, "/api/create", nil)
	r.RemoteAddr = "192.0.2.1:1"
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatalf("ban blocked CORS preflight: %d", w.Code)
	}
	*f.now = f.now.Add(15 * time.Minute)
	f.server.Links = nil // 验证恢复路径也会释放并发额度。
	w = serveProtected(t, f.server, "/api/list", f.resident)
	if w.Code != 500 || f.server.guard.active != 0 {
		t.Fatalf("panic leaked concurrency slot: status=%d active=%d", w.Code, f.server.guard.active)
	}
}

func TestCanceledAuthReleasesConcurrencySlot(t *testing.T) {
	f := newProtectedFixture(t)
	unlock, err := fslock.Acquire(f.server.Cfg.KeysPath+".lock", false)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest(http.MethodGet, "/api/list", nil).WithContext(ctx)
	r.RemoteAddr = "192.0.2.1:12345"
	r.Header.Set("X-Token", f.resident)
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		f.server.ServeHTTP(w, r)
		finished <- w
	}()

	deadline := time.Now().Add(time.Second)
	for {
		f.server.guard.mu.Lock()
		active := f.server.guard.active
		f.server.guard.mu.Unlock()
		if active == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("request did not enter API guard")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case w := <-finished:
		if w.Code != http.StatusServiceUnavailable || responseFields(t, w)["code"] != "auth_unavailable" {
			t.Fatalf("auth response: %d %s", w.Code, w.Body)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled request is still waiting for the key lock")
	}
	if active := f.server.guard.active; active != 0 {
		t.Fatalf("active requests = %d, want 0", active)
	}
}
