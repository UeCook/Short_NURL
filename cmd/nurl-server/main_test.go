package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UeCook/Short_NURL/internal/apikey"
	"github.com/UeCook/Short_NURL/internal/config"
	"github.com/UeCook/Short_NURL/internal/store"
)

func TestVerifyDataDistinguishesExpiryFromCorruption(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.PermPath = filepath.Join(dir, "perm.json")
	cfg.TempPath = filepath.Join(dir, "temp.json")
	cfg.KeysPath = filepath.Join(dir, "keys.json")
	cfg.InternalTokenPath = filepath.Join(dir, "internal_token")

	perm := store.New(cfg.PermPath, cfg.Site.TzOffset)
	if err := perm.ReadWrite(func(store.Data) error { return nil }); err != nil {
		t.Fatal(err)
	}
	temp := store.New(cfg.TempPath, cfg.Site.TzOffset)
	if err := temp.ReadWrite(func(d store.Data) error {
		d["old"] = map[string]any{"url": "https://example.com/old", "t": "2000-01-01T00:00:00+00:00"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := apikey.New(cfg.KeysPath, cfg.Site.TzOffset, cfg.Keys.TTLDays, cfg.Keys.OnetimePoolSize).GenerateResident(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.InternalTokenPath, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := runVerifyData(cfg); got != 0 {
		t.Fatalf("expired entry is valid cold storage: exit %d", got)
	}
	if err := temp.ReadWrite(func(d store.Data) error {
		d["bad"] = map[string]any{"url": "https://example.com/bad", "t": "not-a-time"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := runVerifyData(cfg); got != 1 {
		t.Fatalf("malformed expiry must fail verification: exit %d", got)
	}
	if err := temp.ReadWrite(func(d store.Data) error {
		delete(d, "bad")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.InternalTokenPath, []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := runVerifyData(cfg); got != 1 {
		t.Fatalf("empty internal token must fail verification: exit %d", got)
	}
}

func TestVerifyEntriesUsesHotURLContract(t *testing.T) {
	data := store.Data{"ok": map[string]any{"url": "https://8.8.8.8/"}}
	for i, target := range []string{"https://127.0.0.1/", "https://", "https://8.8.8.8:65536/", "https://8.8.8.8/" + strings.Repeat("a", 2050)} {
		data[string(rune('a'+i))] = map[string]any{"url": target}
	}
	total, loadable := verifyEntries(data)
	if total != 5 || loadable != 1 {
		t.Fatalf("verification: total=%d loadable=%d", total, loadable)
	}
	for _, entry := range data {
		entry.(map[string]any)["t"] = "2999-01-01T00:00:00+00:00"
	}
	total, loadable, expired, skipped := verifyTemp(data)
	if total != 5 || loadable != 1 || expired != 0 || skipped != 4 {
		t.Fatalf("temporary verification: total=%d loadable=%d expired=%d skipped=%d", total, loadable, expired, skipped)
	}
}
