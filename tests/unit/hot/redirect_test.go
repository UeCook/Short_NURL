package hot_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/UeCook/Short_NURL/internal/hot"
)

// 内部 API 返回 3xx 时必须失败，且不得把 LPA-Key 转发给 Location 指向的主机。
func TestRedirectIsNotFollowed(t *testing.T) {
	var leaked int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&leaked, 1)
		if key := r.Header.Get("LPA-Key"); key != "" {
			t.Errorf("LPA-Key 被转发到重定向目标：%q", key)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer target.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	}))
	defer origin.Close()

	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "internal_token")
	if err := os.WriteFile(tokenPath, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	c := New(origin.URL, tokenPath, 2*time.Second, 1, 1)
	if _, err := c.Set("abc", "https://example.com", 0, "0"); err == nil {
		t.Fatal("3xx 必须作为失败返回")
	}
	if n := atomic.LoadInt32(&leaked); n != 0 {
		t.Fatalf("重定向目标收到 %d 次请求，期望 0", n)
	}
	if c.LastError() == nil || c.LastError().HTTPCode != http.StatusFound {
		t.Fatalf("last error = %+v", c.LastError())
	}
}
