package httpapi_test

import (
	"net/http"
	"testing"
)

func TestSecurityHeaders(t *testing.T) {
	h := newHarness(t)
	resp, _ := h.do(t, http.MethodGet, "/api/ping", "", "")

	want := map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "no-referrer",
		"Cache-Control":           "no-store",
		"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
		"Permissions-Policy":      "geolocation=(), camera=(), microphone=()",
	}
	for header, value := range want {
		if got := resp.Header.Get(header); got != value {
			t.Fatalf("%s = %q, want %q", header, got, value)
		}
	}
}
