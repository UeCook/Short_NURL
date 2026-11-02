package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInternalTokenUsesAtomicStorage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "internal_token")
	if err := writeInternalToken(path, "first-token"); err != nil {
		t.Fatal(err)
	}
	if err := writeInternalToken(path, "second-token"); err != nil {
		t.Fatal(err)
	}
	if got := readTokenFile(path); got != "second-token" {
		t.Fatalf("token=%q", got)
	}
	backup, err := os.ReadFile(path + ".bak")
	if err != nil || string(backup) != "first-token" {
		t.Fatalf("backup=%q err=%v", backup, err)
	}
}
