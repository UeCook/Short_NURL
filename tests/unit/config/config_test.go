package config_test

import (
	"encoding/json"
	"fmt"
	. "github.com/UeCook/Short_NURL/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripJSONC(t *testing.T) {
	src := []byte(`{
  // 行注释
  "a": 1, /* 块注释 */
  "b": "含//与/*的字符串",
  "c": [1, 2, 3,],
}`)
	out, err := StripJSONC(src)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("result is not valid JSON: %v\n%s", err, out)
	}
	if m["b"] != "含//与/*的字符串" {
		t.Fatalf("string literal corrupted: %v", m["b"])
	}
	if fmt.Sprint(m["c"]) != "[1 2 3]" {
		t.Fatalf("array corrupted: %v", m["c"])
	}
	if v, ok := m["a"].(float64); !ok || v != 1 {
		t.Fatalf("a = %v", m["a"])
	}
}

func TestLoadDefaultsAndResolve(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfgDir, "config.json")
	body := `{
  "site": { "domain": "https://s.example.com", "tz_offset": "+08:00" },
  "storage": { "base_dir": ".." }
}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Keys.TTLDays != 7 {
		t.Fatalf("default lost: %+v", cfg.Keys)
	}
	if cfg.Quota.PermLimit != 10000 {
		t.Fatalf("default lost: %+v", cfg.Quota)
	}
	if len(cfg.Codes.ReservedCodes) != 8 {
		t.Fatalf("reserved codes: %v", cfg.Codes.ReservedCodes)
	}
	want := filepath.Clean(filepath.Join(dir, "backend/data/perm.json"))
	if cfg.PermPath != want {
		t.Fatalf("perm path = %s, want %s", cfg.PermPath, want)
	}
}

func TestValidateListen(t *testing.T) {
	if err := ValidateListen("127.0.0.1:8080"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateListen("nope"); err == nil {
		t.Fatal("expected error")
	}
	if err := ValidateListen(""); err == nil {
		t.Fatal("expected error")
	}
}

func TestCheckFileAccess(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.json")
	if !CheckFileAccess(p) {
		t.Fatal("missing file in writable dir should be accessible")
	}
	if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !CheckFileAccess(p) {
		t.Fatal("existing file should be accessible")
	}
	if CheckFileAccess("") {
		t.Fatal("empty path must fail")
	}
}

func TestWarningsListenScope(t *testing.T) {
	cfg := Default()
	cfg.Site.Domain = "https://s.example.com"

	cfg.Server.Listen = "0.0.0.0:8080"
	if !hasWarning(cfg.Warnings(), "未绑定回环") {
		t.Fatalf("0.0.0.0 应产生非回环告警：%v", cfg.Warnings())
	}

	cfg.Server.Listen = "127.0.0.1:8080"
	if hasWarning(cfg.Warnings(), "未绑定回环") {
		t.Fatalf("回环地址不应告警：%v", cfg.Warnings())
	}

	cfg.Server.Listen = "[::1]:8080"
	if hasWarning(cfg.Warnings(), "未绑定回环") {
		t.Fatalf("IPv6 回环不应告警：%v", cfg.Warnings())
	}
}

func hasWarning(list []string, sub string) bool {
	for _, w := range list {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}
