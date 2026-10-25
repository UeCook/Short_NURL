package httpapi_test

import (
	"encoding/json"
	. "github.com/UeCook/Short_NURL/internal/httpapi"
	"io"
	"log/slog"
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
	"github.com/UeCook/Short_NURL/internal/link"
)

// fakeLua 模拟 OpenResty 内部 API。
type fakeLua struct {
	mu       sync.Mutex
	items    map[string]map[string]any
	fail     bool
	setCalls int
}

func newFakeLua() *fakeLua {
	return &fakeLua{items: map[string]map[string]any{}}
}

func (f *fakeLua) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.fail {
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
			f.items[code] = p
			f.setCalls++
			w.Write([]byte(`{"ok":true}`))
		case "/internal/delete":
			var p map[string]any
			_ = json.Unmarshal(body, &p)
			code, _ := p["code"].(string)
			delete(f.items, code)
			w.Write([]byte(`{"ok":true}`))
		case "/internal/mutation":
			w.Write([]byte(`{"ok":true}`))
		case "/internal/stat":
			perm, temp := 0, 0
			for _, v := range f.items {
				if exp, _ := v["exp_str"].(string); exp == "0" {
					perm++
				} else {
					temp++
				}
			}
			w.Write([]byte(`{"perm_count":` + itoa(perm) + `,"temp_count":` + itoa(temp) + `}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

type harness struct {
	cfg     *config.Config
	srv     *httptest.Server
	lua     *fakeLua
	luaSrv  *httptest.Server
	keys    *apikey.KeyStore
	token   string
	onetime string
	service string
	dir     string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	lua := newFakeLua()
	luaSrv := httptest.NewServer(lua.handler())
	t.Cleanup(luaSrv.Close)

	cfg := config.Default()
	cfg.Site.Domain = "https://s.example.com"
	cfg.Site.TzOffset = "+08:00"
	cfg.Storage.BaseDir = dir
	cfg.PermPath = filepath.Join(dir, "perm.json")
	cfg.TempPath = filepath.Join(dir, "temp.json")
	cfg.KeysPath = filepath.Join(dir, "keys.json")
	cfg.InternalTokenPath = filepath.Join(dir, "internal_token")
	cfg.Internal.Host = luaSrv.URL
	cfg.Internal.TimeoutSec = 2.0

	if err := os.WriteFile(cfg.InternalTokenPath, []byte("aabbccdd"), 0o600); err != nil {
		t.Fatal(err)
	}

	keys := apikey.New(cfg.KeysPath, cfg.Site.TzOffset, cfg.Keys.TTLDays, 3)
	resident, err := keys.GenerateResident()
	if err != nil {
		t.Fatal(err)
	}
	onetime, err := keys.FillPool()
	if err != nil {
		t.Fatal(err)
	}
	svc, err := keys.GenerateService("default")
	if err != nil {
		t.Fatal(err)
	}

	authSvc := auth.New(keys)
	hotClient := hot.New(cfg.Internal.Host, cfg.InternalTokenPath,
		time.Duration(cfg.Internal.TimeoutSec*float64(time.Second)),
		cfg.Internal.MaxRetries, cfg.Internal.BackoffMS)
	links := link.NewServices(cfg, hotClient)
	srv := httptest.NewServer(NewServer(cfg, keys, authSvc, links, slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(srv.Close)

	h := &harness{cfg: cfg, srv: srv, lua: lua, luaSrv: luaSrv, keys: keys, dir: dir}
	h.token = resident
	h.onetime = onetime[0]
	h.service = svc
	return h
}

func (h *harness) do(t *testing.T, method, path, token, body string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, h.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("X-Token", token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, string(raw)
}

func (h *harness) doHeadless(t *testing.T, method, path, token, body string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, h.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("X-Headless-Token", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, string(raw)
}
