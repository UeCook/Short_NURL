package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UeCook/Short_NURL/internal/fslock"
	"github.com/UeCook/Short_NURL/internal/store"
)

func TestBothCreateChannelsCancelWhileColdStoreIsLocked(t *testing.T) {
	for _, path := range []string{"/api/create", "/headless/api/create"} {
		t.Run(path, func(t *testing.T) {
			h := newHarness(t)
			unlock, err := fslock.Acquire(h.cfg.TempPath+".lock", false)
			if err != nil {
				t.Fatal(err)
			}
			defer unlock()
			checked := make(chan struct{})
			h.lua.mu.Lock()
			h.lua.onCheck = func() { close(checked) }
			h.lua.mu.Unlock()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(
				`{"url":"https://8.8.8.8/cancel","code":"abcd"}`)).WithContext(ctx)
			req.Header.Set("X-Token", h.onetime)
			req.Header.Set("X-Headless-Token", h.service)
			finished := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				w := httptest.NewRecorder()
				h.srv.Config.Handler.ServeHTTP(w, req)
				finished <- w
			}()

			select {
			case <-checked:
			case <-time.After(3 * time.Second):
				t.Fatal("request did not reach hot readiness check")
			}
			deadline := time.Now().Add(time.Second)
			for {
				release, err := fslock.Acquire(h.cfg.PermPath+".lock", false)
				if errors.Is(err, fslock.ErrNotAcquired) {
					break // The transaction holds the first lock and is waiting for TempPath.
				}
				if err != nil {
					t.Fatal(err)
				}
				_ = release()
				if time.Now().After(deadline) {
					t.Fatal("transaction did not start waiting for the second lock")
				}
				time.Sleep(5 * time.Millisecond)
			}
			cancel()
			select {
			case w := <-finished:
				if w.Code != http.StatusRequestTimeout || !strings.Contains(w.Body.String(), "create_cancelled") {
					t.Fatalf("cancel response: %d %s", w.Code, w.Body)
				}
			case <-time.After(time.Second):
				t.Fatal("canceled request is still waiting for the cold lock")
			}

			h.lua.mu.Lock()
			sets := h.lua.setCalls
			h.lua.mu.Unlock()
			if sets != 0 {
				t.Errorf("canceled create sent %d hot writes", sets)
			}
			data, err := store.New(h.cfg.PermPath, h.cfg.Site.TzOffset).Read()
			if err != nil || data.Has("abcd") {
				t.Fatalf("canceled create persisted: %v, %v", data, err)
			}
		})
	}
}

func TestCreateColdQueueReturnsRetryableBusy(t *testing.T) {
	h := newHarness(t)
	unlock, err := fslock.Acquire(h.cfg.PermPath+".lock", false)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	resp, raw := h.do(t, http.MethodPost, "/api/create", h.token,
		`{"url":"https://8.8.8.8/queued","code":"abcd"}`)
	if resp.StatusCode != 429 || decodeBody(t, raw)["code"] != "api_busy" || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("queue response: %d %s, headers %v", resp.StatusCode, raw, resp.Header)
	}
}

func TestCreatePublishesAfterColdCommitAndReportsPostCommitFailure(t *testing.T) {
	h := newHarness(t)
	perm := store.New(h.cfg.PermPath, h.cfg.Site.TzOffset)
	h.lua.mu.Lock()
	h.lua.failSet = true
	h.lua.onSet = func(code string) {
		data, err := perm.Read()
		if err != nil || !data.Has(code) {
			t.Errorf("hot write for %s preceded cold commit: %v, %v", code, data, err)
		}
	}
	h.lua.mu.Unlock()

	resp, raw := h.do(t, http.MethodPost, "/api/create", h.token,
		`{"url":"https://8.8.8.8/persisted","code":"abcd"}`)
	result := decodeBody(t, raw)
	if resp.StatusCode != 200 || result["synced"] != false || result["warning"] == nil {
		t.Fatalf("post-commit failure: %d %s", resp.StatusCode, raw)
	}
	h.lua.mu.Lock()
	h.lua.failSet = false
	h.lua.mu.Unlock()
	resp, raw = h.do(t, http.MethodPost, "/api/create", h.token,
		`{"url":"https://8.8.8.8/persisted"}`)
	result = decodeBody(t, raw)
	if resp.StatusCode != 200 || result["synced"] != true || result["dedup"] != true ||
		result["short_url"] != "https://s.example.com/abcd" {
		t.Fatalf("retry: %d %s", resp.StatusCode, raw)
	}
}

func TestCreateColdFailureNeverPublishes(t *testing.T) {
	h := newHarness(t)
	if err := os.WriteFile(h.cfg.PermPath, []byte(`{"d":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(h.cfg.PermPath+".bak", 0o700); err != nil {
		t.Fatal(err)
	}
	resp, raw := h.do(t, http.MethodPost, "/api/create", h.token,
		`{"url":"https://8.8.8.8/backup-failure","code":"abcd"}`)
	if resp.StatusCode != 500 || decodeBody(t, raw)["code"] != "write_failed" {
		t.Fatalf("backup error: %d %s", resp.StatusCode, raw)
	}
	h.lua.mu.Lock()
	defer h.lua.mu.Unlock()
	if h.lua.setCalls != 0 {
		t.Fatalf("cold failure reached hot publication %d times", h.lua.setCalls)
	}
}

func TestOperationLockIOFailureIsNotConflict(t *testing.T) {
	h := newHarness(t)
	if err := os.WriteFile(filepath.Join(h.dir, ".operations"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	resp, raw := h.do(t, http.MethodPost, "/api/create", h.token,
		`{"url":"https://8.8.8.8/lock-failure","code":"abcd"}`)
	if resp.StatusCode != 500 || decodeBody(t, raw)["code"] != "write_failed" {
		t.Fatalf("operation lock error: %d %s", resp.StatusCode, raw)
	}
}
