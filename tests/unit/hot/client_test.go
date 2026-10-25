package hot_test

import (
	"encoding/json"
	. "github.com/UeCook/Short_NURL/internal/hot"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestSetAndRetry(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.Header.Get("LPA-Key") != "secret" {
			t.Errorf("LPA-Key = %q", r.Header.Get("LPA-Key"))
		}
		if r.URL.Path != "/internal/set" {
			t.Errorf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		if m["exp_str"] != "0" || m["ttl"] != float64(0) {
			t.Errorf("body = %s", body)
		}
		if atomic.LoadInt32(&calls) < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	tokPath := filepath.Join(dir, "internal_token")
	if err := os.WriteFile(tokPath, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	c := New(srv.URL, tokPath, 2*time.Second, 3, 1)
	if !c.SetWithRetry("abc", "https://e.com", 0, "0") {
		t.Fatalf("set failed: %v", c.LastError())
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestStatValidation(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		headers map[string]string
		status  int
		ok      bool
	}{
		{"good", `{"perm_count":1,"temp_count":2}`, map[string]string{"Content-Type": "application/json"}, 200, true},
		{"missing-field", `{"perm_count":1}`, map[string]string{"Content-Type": "application/json"}, 200, false},
		{"null-field", `{"perm_count":null,"temp_count":2}`, map[string]string{"Content-Type": "application/json"}, 200, false},
		{"bad-json", `not-json`, map[string]string{"Content-Type": "application/json"}, 200, false},
		{"bad-ct", `{"perm_count":1,"temp_count":2}`, map[string]string{"Content-Type": "text/html"}, 200, false},
		{"http-err", `{"perm_count":1,"temp_count":2}`, map[string]string{"Content-Type": "application/json"}, 500, false},
		{"empty-body", ``, map[string]string{"Content-Type": "application/json"}, 200, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tc.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			c := New(srv.URL, "", 2*time.Second, 1, 1)
			_, _, ok := c.Stat()
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (err=%v)", ok, tc.ok, c.LastError())
			}
		})
	}
}

func TestPostRequiresOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "", 2*time.Second, 1, 1)
	if _, err := c.Delete("abc", "perm"); err == nil {
		t.Fatal("ok:false must fail")
	}
	if c.LastError() == nil || c.LastError().Type != "response" {
		t.Fatalf("last error = %+v", c.LastError())
	}
}

func TestMutationNoRetry(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL, "", 2*time.Second, 3, 1)
	if c.BeginMutation("abc") {
		t.Fatal("begin should fail")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("mutation must not retry, calls = %d", calls)
	}
}
