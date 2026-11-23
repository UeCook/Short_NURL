package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/UeCook/Short_NURL/internal/fslock"
	"github.com/UeCook/Short_NURL/internal/store"
)

func TestDeleteCancellationLeavesCommittedLinkIntact(t *testing.T) {
	for _, afterHotDelete := range []bool{false, true} {
		name := "before hot delete"
		if afterHotDelete {
			name = "after hot delete"
		}
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			const code = "abcd"
			const target = "https://8.8.8.8/keep"
			resp, raw := h.do(t, http.MethodPost, "/api/create", h.token,
				`{"url":"`+target+`","code":"`+code+`"}`)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("seed link: %d %s", resp.StatusCode, raw)
			}
			holdLock := func() fslock.Unlock {
				unlock, err := fslock.Acquire(h.cfg.TempPath+".lock", false)
				if err != nil {
					t.Error(err)
				}
				return unlock
			}
			var unlock fslock.Unlock
			hotDeleted := make(chan fslock.Unlock, 1)
			if afterHotDelete {
				h.lua.mu.Lock()
				h.lua.onDelete = func() { hotDeleted <- holdLock() }
				h.lua.mu.Unlock()
			} else {
				unlock = holdLock()
			}
			defer func() {
				if unlock != nil {
					_ = unlock()
				}
			}()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req := httptest.NewRequest(http.MethodPost, "/headless/api/delete", strings.NewReader(
				`{"code":"`+code+`"}`)).WithContext(ctx)
			req.Header.Set("X-Headless-Token", h.token)
			finished := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				w := httptest.NewRecorder()
				h.srv.Config.Handler.ServeHTTP(w, req)
				finished <- w
			}()

			if afterHotDelete {
				select {
				case unlock = <-hotDeleted:
				case <-time.After(3 * time.Second):
					t.Fatal("request did not reach hot deletion")
				}
			}
			if unlock == nil {
				t.Fatal("could not hold second cold lock")
			}
			deadline := time.Now().Add(time.Second)
			for {
				release, err := fslock.Acquire(h.cfg.PermPath+".lock", false)
				if errors.Is(err, fslock.ErrNotAcquired) {
					break // The request holds the first cold lock and is waiting for the second.
				}
				if err != nil {
					t.Fatal(err)
				}
				_ = release()
				if time.Now().After(deadline) {
					t.Fatal("request did not start waiting for the cold lock")
				}
				time.Sleep(5 * time.Millisecond)
			}
			cancel()
			select {
			case w := <-finished:
				if w.Code != http.StatusRequestTimeout || !strings.Contains(w.Body.String(), "delete_cancelled") {
					t.Fatalf("cancel response: %d %s", w.Code, w.Body)
				}
			case <-time.After(time.Second):
				t.Fatal("canceled request is still waiting for the cold lock")
			}

			data, err := store.New(h.cfg.PermPath, h.cfg.Site.TzOffset).Read()
			if err != nil || !data.Has(code) {
				t.Fatalf("cold data: %v, %v", data, err)
			}
			h.lua.mu.Lock()
			got := h.lua.items[code]["url"]
			h.lua.mu.Unlock()
			if got != target {
				t.Fatalf("hot URL = %v, want %s", got, target)
			}
		})
	}
}
