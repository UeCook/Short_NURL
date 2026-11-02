package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListResponseContract(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"grouped-empty", `{"permanent":[],"temporary":[]}`, true},
		{"grouped", `{"permanent":[{"id":"abc","url":"https://8.8.8.8/","lurl":"https://s.example/abc"}],"temporary":[]}`, true},
		{"flat-unsupported", `[{"id":"abc"}]`, false},
		{"missing-groups", `{}`, false},
		{"malformed-item", `{"permanent":[null],"temporary":[]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Headless-Token") != "review-key" {
					t.Error("headless credential missing")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			if err := doList(srv.URL, "review-key", false); (err == nil) != tc.valid {
				t.Fatalf("valid=%v, err=%v", tc.valid, err)
			}
		})
	}
}
