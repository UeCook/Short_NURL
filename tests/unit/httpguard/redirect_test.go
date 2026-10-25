package httpguard_test

import (
	"net/http"
	"net/url"
	"testing"

	. "github.com/UeCook/Short_NURL/internal/httpguard"
)

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return u
}

func TestAllowed(t *testing.T) {
	cases := []struct {
		name string
		from string
		to   string
		want bool
	}{
		{"同源", "https://s.example.com", "https://s.example.com/headless/api/list", true},
		{"跨主机", "https://s.example.com", "https://evil.example.net/x", false},
		{"HTTPS 降级", "https://s.example.com", "http://s.example.com/x", false},
		{"HTTP 升级 HTTPS", "http://s.example.com", "https://s.example.com/x", true},
		{"HTTPS 改端口", "https://s.example.com", "https://s.example.com:8443/x", false},
		{"HTTP 改端口", "http://127.0.0.1:8080", "http://127.0.0.1:9090/x", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Allowed(mustParseURL(t, tc.from), mustParseURL(t, tc.to)); got != tc.want {
				t.Fatalf("Allowed(%s, %s) = %v, want %v", tc.from, tc.to, got, tc.want)
			}
		})
	}
}

func TestSameOriginOnly(t *testing.T) {
	policy := SameOriginOnly("https://s.example.com")

	same := &http.Request{URL: mustParseURL(t, "https://s.example.com/next")}
	if err := policy(same, nil); err != nil {
		t.Fatalf("同源重定向应允许：%v", err)
	}

	cross := &http.Request{URL: mustParseURL(t, "https://evil.example.net/x")}
	if err := policy(cross, nil); err == nil {
		t.Fatal("跨源重定向必须拒绝")
	}

	if err := policy(same, make([]*http.Request, 5)); err == nil {
		t.Fatal("超过 5 次重定向必须拒绝")
	}
}

func TestSameOriginOnlyInvalidOrigin(t *testing.T) {
	policy := SameOriginOnly("example.com")
	req := &http.Request{URL: mustParseURL(t, "https://example.com/x")}
	if err := policy(req, nil); err == nil {
		t.Fatal("无效源地址必须拒绝所有重定向")
	}
}
