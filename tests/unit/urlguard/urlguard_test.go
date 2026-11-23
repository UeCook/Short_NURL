package urlguard_test

import (
	. "github.com/UeCook/Short_NURL/internal/urlguard"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	ok := []string{
		"https://example.com",
		"http://example.com/path?a=1",
		"HTTPS://EXAMPLE.COM/x",
		"https://example.com:8443/中文路径",
	}
	for _, u := range ok {
		if _, err := Validate(u); err != nil {
			t.Fatalf("Validate(%q) = %v", u, err)
		}
	}

	bad := map[string]string{
		"":                          "missing_url",
		"   ":                       "missing_url",
		"javascript:alert(1)":       "invalid_url",
		"data:text/html,x":          "invalid_url",
		"file:///etc/passwd":        "invalid_url",
		"ftp://example.com":         "invalid_url",
		"example.com":               "invalid_url",
		"http://":                   "invalid_url",
		"https://example.com/a b":   "invalid_url",
		"https://example.com/a\"b":  "invalid_url",
		"https://example.com/a<b":   "invalid_url",
		"https://example.com/a\\b":  "invalid_url",
		"https://127.0.0.1/x":       "private_url",
		"https://10.1.2.3/x":        "private_url",
		"https://172.16.0.1/x":      "private_url",
		"https://192.168.1.1/x":     "private_url",
		"https://169.254.1.1/x":     "private_url",
		"https://100.64.0.1/x":      "private_url",
		"https://[::1]/x":           "private_url",
		"https://0.0.0.0/x":         "private_url",
		"https://224.0.0.1/x":       "private_url",
		"https://255.255.255.255/x": "private_url",
	}
	for u, code := range bad {
		_, err := Validate(u)
		if err == nil {
			t.Fatalf("Validate(%q) should fail", u)
		}
		if err.Code != code {
			t.Fatalf("Validate(%q) code = %s, want %s", u, err.Code, code)
		}
	}
}

func TestTooLong(t *testing.T) {
	long := "https://example.com/" + string(make([]byte, 3000))
	for i := range long[20:] {
		long = long[:20+i] + "a" + long[20+i+1:]
	}
	if len(long) <= 2048 {
		t.Fatal("setup")
	}
	_, err := Validate(long)
	if err == nil || err.Code != "url_too_long" {
		t.Fatalf("want url_too_long, got %v", err)
	}
}

func TestValidHotURL(t *testing.T) {
	if !ValidHotURL("https://example.com/x") {
		t.Fatal("should be valid")
	}
	if ValidHotURL("javascript:x") {
		t.Fatal("should be invalid")
	}
	if ValidHotURL("https://exa mple.com") {
		t.Fatal("space should be invalid")
	}
	if ValidHotURL("https://example.com/a\\b") {
		t.Fatal("backslash should be invalid")
	}
	if ValidHotURL("https://127.0.0.1/x") {
		t.Fatal("literal loopback should be invalid")
	}
	if ValidHotURL("https://example.com/" + strings.Repeat("a", 2050)) {
		t.Fatal("overlong URL should be invalid")
	}
}

func TestIsPrivateLiteral(t *testing.T) {
	bad := []string{
		"https://localhost/x",
		"https://app.localhost/x",
		"https://svc.internal/x",
		"https://printer.local/x",
		"https://127.0.0.1/x",
		"https://10.1.2.3/x",
		"https://172.16.0.1/x",
		"https://192.168.1.1/x",
		"https://169.254.169.254/latest",
		"https://100.64.0.1/x",
		"https://[::1]/x",
		"https://[fe80::1]/x",
		"https://[fd00::1]/x",
		"https://2130706433/x",
		"https://127.1/x",
		"https://0x7f000001/x",
	}
	for _, u := range bad {
		if !IsPrivateLiteral(u) {
			t.Fatalf("IsPrivateLiteral(%q) = false, want true", u)
		}
	}

	good := []string{
		"https://example.com/x",
		"https://8.8.8.8/x",
		"https://[2606:4700:4700::1111]/x",
		"https://s.example.com:8443/x",
	}
	for _, u := range good {
		if IsPrivateLiteral(u) {
			t.Fatalf("IsPrivateLiteral(%q) = true, want false", u)
		}
	}
}

// TestPrivateAddressEquivalents 覆盖 F-02 的等价写法：完整形式 IPv6 回环、
// IPv4-mapped 十六进制形式、IPv4 八进制/十六进制写法，判定必须与浏览器一致。
// 同一组用例在 backend/lua/shorturl/util/url.lua 中有对应实现（无 Lua 解释器时
// 由本测试锁定 Go 侧行为，Lua 侧保持同算法）。
func TestPrivateAddressEquivalents(t *testing.T) {
	bad := []string{
		"https://[0:0:0:0:0:0:0:0001]/x", // 完整形式回环
		"https://[0:0:0:0:0:0:0:1]/x",
		"https://[::ffff:7f00:1]/x",    // IPv4-mapped 十六进制
		"https://[::ffff:127.0.0.1]/x", // IPv4-mapped 点分
		"https://[::ffff:a00:1]/x",     // 10.0.0.1
		"https://[FE80::1]/x",
		"https://[fc00::1]/x",
		"https://[ff02::1]/x",
		"https://0177.0.0.1/x", // 八进制 127
		"https://0x7f.0.0.1/x", // 十六进制首段
		"https://0x7F.0.0.1/x",
		"https://127.0.1/x",
		"https://127.1/x",
		"https://2130706433/x",
	}
	for _, u := range bad {
		if _, err := Validate(u); err == nil || err.Code != "private_url" {
			t.Fatalf("Validate(%q) = %v, want private_url", u, err)
		}
		if !IsPrivateLiteral(u) {
			t.Fatalf("IsPrivateLiteral(%q) = false, want true", u)
		}
	}

	good := []string{
		"https://cafe.be/x",
		"https://8.8.8.8/x",
		"https://1.1.1.1/x",
		"https://[2606:4700:4700::1111]/x",
	}
	for _, u := range good {
		if IsPrivateLiteral(u) {
			t.Fatalf("IsPrivateLiteral(%q) = true, want false", u)
		}
	}
}
