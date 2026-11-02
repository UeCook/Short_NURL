package urlguard_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UeCook/Short_NURL/internal/urlguard"
)

func TestHotURLContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "url-contract.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		expected, target, ok := strings.Cut(line, "\t")
		if !ok || (expected != "0" && expected != "1") {
			t.Fatalf("invalid fixture: %q", line)
		}
		if got := urlguard.ValidHotURL(target); got != (expected == "1") {
			t.Errorf("ValidHotURL(%q)=%v, want %s", target, got, expected)
		}
	}
}

func TestPortValidationBeforeHotSync(t *testing.T) {
	for _, target := range []string{"https://8.8.8.8:65536/", "https://8.8.8.8:/", "https://8.8.8.8:0/", "https://[2606:4700:4700::1111]:65536/"} {
		if err := urlguard.Check(target); err == nil || err.Code != urlguard.CodeInvalidURL {
			t.Errorf("Check(%q)=%v, want invalid_url", target, err)
		}
	}
	for _, target := range []string{"https://8.8.8.8:1/", "https://8.8.8.8:65535/"} {
		if err := urlguard.Check(target); err != nil {
			t.Errorf("valid port rejected: %s: %v", target, err)
		}
	}
}
