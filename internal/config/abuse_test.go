package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOldConfigGetsEnabledAbuseDefaults(t *testing.T) {
	t.Setenv("NURL_ABUSE_ENABLED", "")
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"site":{"domain":"https://s.example.com"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Abuse.Enabled || cfg.Abuse.MaxAuthFailures != 5 || cfg.Abuse.FailureWindowSeconds != 300 || cfg.Abuse.BanSeconds != 900 || cfg.Abuse.MaxBanSeconds != 86400 {
		t.Fatalf("old deployment did not inherit safe defaults: %+v", cfg.Abuse)
	}
	t.Setenv("NURL_ABUSE_ENABLED", "false")
	cfg, err = Load(path)
	if err != nil || cfg.Abuse.Enabled {
		t.Fatalf("explicit disable failed: %+v %v", cfg, err)
	}
}

func TestAbuseConfigRejectsUnsafeParameters(t *testing.T) {
	for _, change := range []func(*Abuse){
		func(a *Abuse) { a.RequestsPerMinute = 0 },
		func(a *Abuse) { a.Burst = -1 },
		func(a *Abuse) { a.GlobalRequestsPerSecond = 0 },
		func(a *Abuse) { a.GlobalBurst = 0 },
		func(a *Abuse) { a.MaxConcurrent = 0 },
		func(a *Abuse) { a.MaxClients = 0 },
		func(a *Abuse) { a.MaxAuthFailures = 101 },
		func(a *Abuse) { a.FailureWindowSeconds = -1 },
		func(a *Abuse) { a.BanSeconds = 0 },
		func(a *Abuse) { a.MaxBanSeconds = 1 },
		func(a *Abuse) { a.HistorySeconds = 0 },
		func(a *Abuse) { a.TrustedProxies = []string{"not-an-address"} },
	} {
		cfg := defaultAbuse()
		change(&cfg)
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "abuse.") {
			t.Errorf("unsafe configuration accepted: %+v", cfg)
		}
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"site":{"domain":"https://s.example.com"},"abuse":{"max_auth_failures":0}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NURL_ABUSE_ENABLED", "")
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "abuse.max_auth_failures") {
		t.Fatalf("invalid abuse config did not stop startup: %v", err)
	}
}
