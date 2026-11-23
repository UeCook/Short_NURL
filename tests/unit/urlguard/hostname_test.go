package urlguard_test

import (
	"github.com/UeCook/Short_NURL/internal/urlguard"
	"testing"
)

func TestHostNormalizationBoundary(t *testing.T) {
	for _, raw := range []string{
		"http://l%6fcalhost/", "http://%31%32%37.0.0.1/",
		"http://127%2e0%2e0%2e1/", "http://localhost%2e/",
		"http://user:pass@l%6fcalhost:8080/", "http://ｌｏｃａｌｈｏｓｔ/",
		"http://127。0。0。1/", "http://[fe80::1%25eth0]/",
	} {
		if !urlguard.IsPrivateLiteral(raw) || urlguard.ValidHotURL(raw) {
			t.Errorf("unsafe host allowed: %s", raw)
		}
		if urlguard.Check(raw) == nil {
			t.Errorf("create accepted unsafe host: %s", raw)
		}
	}
	for _, raw := range []string{
		"https://example.com/a%20b?q=%31%32%37#%20",
		"https://user%40name:pass%25word@example.com/path",
		"https://example.com/中文?q=中文", "https://xn--fiqs8s.example/",
	} {
		if urlguard.IsPrivateLiteral(raw) || !urlguard.ValidHotURL(raw) {
			t.Errorf("safe host rejected: %s", raw)
		}
	}
}
