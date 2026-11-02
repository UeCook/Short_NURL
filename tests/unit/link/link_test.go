package link_test

import (
	"encoding/json"
	. "github.com/UeCook/Short_NURL/internal/link"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UeCook/Short_NURL/internal/apikey"
	"github.com/UeCook/Short_NURL/internal/auth"
	"github.com/UeCook/Short_NURL/internal/config"
	"github.com/UeCook/Short_NURL/internal/hot"
)

type stubLua struct {
	mu    sync.Mutex
	items map[string]map[string]any
	fail  bool
}

func (s *stubLua) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	body, _ := io.ReadAll(r.Body)
	switch r.URL.Path {
	case "/internal/set":
		var p map[string]any
		_ = json.Unmarshal(body, &p)
		code, _ := p["code"].(string)
		s.items[code] = p
		_, _ = w.Write([]byte(`{"ok":true}`))
	case "/internal/delete":
		var p map[string]any
		_ = json.Unmarshal(body, &p)
		code, _ := p["code"].(string)
		delete(s.items, code)
		_, _ = w.Write([]byte(`{"ok":true}`))
	case "/internal/mutation":
		_, _ = w.Write([]byte(`{"ok":true}`))
	case "/internal/stat":
		_, _ = w.Write([]byte(`{"perm_count":0,"temp_count":0}`))
	}
}

// jsonNum 造一个 json.Number，模拟 JSON 整数字段。
func jsonNum(s string) any {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	_ = dec.Decode(&v)
	return v
}

type fixture struct {
	svc     *Services
	cfg     *config.Config
	lua     *stubLua
	ctx     *auth.Context
	onetime *auth.Context
	svcCtx  *auth.Context
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	lua := &stubLua{items: map[string]map[string]any{}}
	srv := httptest.NewServer(lua)
	t.Cleanup(srv.Close)

	cfg := config.Default()
	cfg.Site.Domain = "https://s.example.com"
	cfg.PermPath = filepath.Join(dir, "perm.json")
	cfg.TempPath = filepath.Join(dir, "temp.json")
	cfg.KeysPath = filepath.Join(dir, "keys.json")
	cfg.InternalTokenPath = filepath.Join(dir, "internal_token")
	cfg.Internal.Host = srv.URL
	if err := os.WriteFile(cfg.InternalTokenPath, []byte("tok"), 0o600); err != nil {
		t.Fatal(err)
	}

	keys := apikey.New(cfg.KeysPath, cfg.Site.TzOffset, 7, 1)
	resident, err := keys.GenerateResident()
	if err != nil {
		t.Fatal(err)
	}
	onetimeKeys, err := keys.FillPool()
	if err != nil {
		t.Fatal(err)
	}
	svcKey, err := keys.GenerateService("s")
	if err != nil {
		t.Fatal(err)
	}

	authSvc := auth.New(keys)
	hotClient := hot.New(cfg.Internal.Host, cfg.InternalTokenPath,
		time.Duration(cfg.Internal.TimeoutSec*float64(time.Second)), 3, 1)

	rRes := authSvc.Authenticate(resident, []string{"resident"})
	rOnce := authSvc.Authenticate(onetimeKeys[0], []string{"onetime"})
	rSvc := authSvc.Authenticate(svcKey, []string{"service"})
	if !rRes.Success || !rOnce.Success || !rSvc.Success {
		t.Fatal("auth failed")
	}

	return &fixture{
		svc:     NewServices(cfg, hotClient),
		cfg:     cfg,
		lua:     lua,
		ctx:     rRes.Ctx,
		onetime: rOnce.Ctx,
		svcCtx:  rSvc.Ctx,
	}
}

func TestCreateTempAndNoDedup(t *testing.T) {
	f := newFixture(t)
	res := f.svc.Create(f.ctx, map[string]any{
		"url": "https://example.com/tmp",
		"ttl": jsonNum("60"),
	})
	if !res.OK {
		t.Fatalf("create temp: %+v", res)
	}
	out := res.Data.(CreateOK)
	if out.Exp == nil || !strings.Contains(*out.Exp, "+08:00") {
		t.Fatalf("exp = %v", out.Exp)
	}

	grouped := f.svc.ListGrouped(f.ctx).Data.(GroupedList)
	if len(grouped.Temporary) != 1 || len(grouped.Permanent) != 0 {
		t.Fatalf("grouped = %+v", grouped)
	}

	res2 := f.svc.Create(f.ctx, map[string]any{
		"url": "https://example.com/tmp",
		"ttl": jsonNum("60"),
	})
	if !res2.OK || res2.Data.(CreateOK).Dedup {
		t.Fatalf("temp must not dedup: %+v", res2)
	}
}

func TestQuotaExceeded(t *testing.T) {
	f := newFixture(t)
	f.cfg.Quota.PermLimit = 1
	if res := f.svc.Create(f.ctx, map[string]any{"url": "https://example.com/1"}); !res.OK {
		t.Fatalf("first create: %+v", res)
	}
	res := f.svc.Create(f.ctx, map[string]any{"url": "https://example.com/2"})
	if res.OK || res.Code != "quota_exceeded" || res.HTTPStatus != 429 {
		t.Fatalf("want quota_exceeded, got %+v", res)
	}
}

func TestConfigCapacityGuard(t *testing.T) {
	f := newFixture(t)
	f.cfg.Quota.PermLimit = 30000
	f.cfg.Quota.SvcPermLimit = 1
	res := f.svc.Create(f.ctx, map[string]any{"url": "https://example.com/x"})
	if res.OK || res.Code != "config_error" || res.HTTPStatus != 500 {
		t.Fatalf("want config_error, got %+v", res)
	}
}

func TestTimezoneError(t *testing.T) {
	f := newFixture(t)
	f.cfg.Site.TzOffset = "bogus"
	res := f.svc.Create(f.ctx, map[string]any{
		"url": "https://example.com/x",
		"ttl": jsonNum("60"),
	})
	if res.OK || res.Code != "config_error" {
		t.Fatalf("want config_error, got %+v", res)
	}
}

func TestServiceCodeLength(t *testing.T) {
	f := newFixture(t)
	f.cfg.Codes.SvcCodeLength = 5

	res := f.svc.Create(f.svcCtx, map[string]any{"url": "https://example.com/long"})
	if !res.OK {
		t.Fatalf("create: %+v", res)
	}
	code := strings.TrimPrefix(res.Data.(CreateOK).ShortURL, "https://s.example.com/")
	if len(code) != 5 {
		t.Fatalf("code = %q", code)
	}

	res = f.svc.Create(f.svcCtx, map[string]any{
		"url": "https://example.com/len4",
		"len": jsonNum("4"),
	})
	if !res.OK {
		t.Fatalf("create len: %+v", res)
	}
	code = strings.TrimPrefix(res.Data.(CreateOK).ShortURL, "https://s.example.com/")
	if len(code) != 4 {
		t.Fatalf("code = %q", code)
	}

	res = f.svc.Create(f.svcCtx, map[string]any{
		"url":  "https://example.com/c5",
		"code": "abcde",
	})
	if !res.OK || res.Data.(CreateOK).ShortURL != "https://s.example.com/abcde" {
		t.Fatalf("custom 5: %+v", res)
	}

	res = f.svc.Create(f.ctx, map[string]any{
		"url":  "https://example.com/c5b",
		"code": "abcde",
	})
	if res.OK || res.Code != "invalid_code" {
		t.Fatalf("want invalid_code, got %+v", res)
	}
}

func TestOnetimeForbiddenForManagement(t *testing.T) {
	f := newFixture(t)
	results := map[string]BizResponse{
		"delete": f.svc.Delete(f.onetime, "abc"),
		"list":   f.svc.List(f.onetime),
		"stat":   f.svc.Stat(f.onetime),
		"get":    f.svc.Get(f.onetime, "abc"),
	}
	for name, res := range results {
		if res.OK || res.Code != "onetime_forbidden" || res.HTTPStatus != 403 {
			t.Fatalf("%s: %+v", name, res)
		}
	}
}

func TestDeleteValidation(t *testing.T) {
	f := newFixture(t)
	if res := f.svc.Delete(f.ctx, ""); res.OK || res.Code != "missing_code" {
		t.Fatalf("empty code: %+v", res)
	}
	if res := f.svc.Delete(f.ctx, "0"); res.OK || res.Code != "missing_code" {
		t.Fatalf(`code "0" is empty in PHP: %+v`, res)
	}
	if res := f.svc.Delete(f.ctx, "nope"); res.OK || res.Code != "not_found" || res.HTTPStatus != 404 {
		t.Fatalf("not found: %+v", res)
	}
}

func TestDeleteColdFallback(t *testing.T) {
	f := newFixture(t)
	f.cfg.Policy.ColdFallback = true
	if res := f.svc.Create(f.ctx, map[string]any{"url": "https://example.com/del"}); !res.OK {
		t.Fatal(res.Message)
	}
	items := f.svc.List(f.ctx).Data.([]ListItem)
	if len(items) != 1 {
		t.Fatalf("items = %+v", items)
	}

	f.lua.mu.Lock()
	f.lua.fail = true
	f.lua.mu.Unlock()

	res := f.svc.Delete(f.ctx, items[0].ID)
	if !res.OK {
		t.Fatalf("fallback delete: %+v", res)
	}
	out := res.Data.(DeleteOK)
	if out.Synced || out.Warning == nil {
		t.Fatalf("body = %+v", out)
	}
}

func TestDeleteStrictModeHotFailure(t *testing.T) {
	f := newFixture(t)
	if res := f.svc.Create(f.ctx, map[string]any{"url": "https://example.com/del2"}); !res.OK {
		t.Fatal(res.Message)
	}
	items := f.svc.List(f.ctx).Data.([]ListItem)

	f.lua.mu.Lock()
	f.lua.fail = true
	f.lua.mu.Unlock()

	res := f.svc.Delete(f.ctx, items[0].ID)
	if res.OK || res.Code != "hot_store_unavailable" || res.HTTPStatus != 503 {
		t.Fatalf("want hot_store_unavailable, got %+v", res)
	}

	// 冷记录必须保留
	remaining := f.svc.List(f.ctx).Data.([]ListItem)
	if len(remaining) != 1 {
		t.Fatalf("cold entry must survive: %+v", remaining)
	}
}
