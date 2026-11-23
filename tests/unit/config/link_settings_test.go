package config_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/UeCook/Short_NURL/internal/config"
)

func TestLoadRejectsInvalidLinkSettings(t *testing.T) {
	t.Setenv("NURL_DOMAIN", "")
	type testCase struct {
		name   string
		change func(*config.Config)
		want   string
	}
	tests := []testCase{
		{"quota-over-limit", func(c *config.Config) { c.Quota.PermLimit = 19001 }, "总容量超过 30000"},
	}
	for _, length := range []int{-1, 0, 1, 3, 6, 1000000} {
		tests = append(tests, testCase{
			fmt.Sprintf("service-code-length-%d", length),
			func(c *config.Config) { c.Codes.SvcCodeLength = length },
			"codes.svc_code_length",
		})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Site.Domain = "http://127.0.0.1:38127"
			cfg.Server.Listen = "127.0.0.1:38127"
			tt.change(cfg)
			raw, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := config.Load(path); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("invalid settings were not rejected: err=%v, want %q", err, tt.want)
			}
		})
	}
}

func TestLoadAcceptsLinkSettingBoundaries(t *testing.T) {
	t.Setenv("NURL_DOMAIN", "")
	for _, length := range []int{4, 5} {
		t.Run(fmt.Sprint(length), func(t *testing.T) {
			cfg := config.Default()
			cfg.Site.Domain = "http://127.0.0.1:38127"
			cfg.Server.Listen = "127.0.0.1:38127"
			cfg.Codes.SvcCodeLength = length
			cfg.Quota.PermLimit = 19000
			raw, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			loaded, err := config.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if loaded.QuotaTotal() != config.MaxQuotaTotal || loaded.Codes.SvcCodeLength != length {
				t.Fatalf("boundary settings changed: %+v", loaded)
			}
		})
	}
}

func TestCheckFileAccessIgnoresLegacyProbe(t *testing.T) {
	dir := t.TempDir()
	probe := filepath.Join(dir, ".nurl-write-probe")
	content := []byte("existing probe must remain untouched")
	if err := os.WriteFile(probe, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if !config.CheckFileAccess(filepath.Join(dir, "missing.json")) {
		t.Fatal("a legacy probe made a writable directory appear inaccessible")
	}
	after, err := os.ReadFile(probe)
	if err != nil || string(after) != string(content) {
		t.Fatalf("legacy probe changed: %q, err=%v", after, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != ".nurl-write-probe" {
		t.Fatalf("temporary probes were left behind: %v", entries)
	}
}

func TestCheckFileAccessConcurrentProbes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.json")
	const callers = 32
	results := make(chan bool, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- config.CheckFileAccess(path)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for ok := range results {
		if !ok {
			t.Error("a concurrent probe rejected a writable directory")
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary probes were left behind: %v", entries)
	}
}
