package store

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBatchRollbackUsesCurrentSnapshot(t *testing.T) {
	for _, existed := range []bool{true, false} {
		name := "existing-first-file"
		if !existed {
			name = "new-first-file"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			a, b := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")
			before := []byte(`{"current":true}`)
			if existed {
				if err := os.WriteFile(a, before, 0o640); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(b, before, 0o640); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(a+".bak", []byte(`{"stale":true}`), 0o600); err != nil {
				t.Fatal(err)
			}
			prepared, err := prepareWrites([]fileWrite{
				{path: a, payload: []byte(`{"new":1}`), verifyJSON: true},
				{path: b, payload: []byte(`{"new":2}`), verifyJSON: true},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { cleanupPrepared(prepared) })
			if existed {
				assertFileBytes(t, a+".bak", before)
			}
			// Preparation is complete. Block the second replacement after the first can commit.
			if err := os.Remove(b); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(b, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(b, "block"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := commitWrites(prepared); err == nil || !strings.Contains(err.Error(), b) {
				t.Fatalf("expected the second replacement to fail: %v", err)
			}
			if existed {
				assertFileBytes(t, a, before)
			} else if _, err := os.Stat(a); !os.IsNotExist(err) {
				t.Fatalf("new first file survived rollback: %v", err)
			}
			assertNoTemps(t, dir)
		})
	}
}

func TestReadbackFailureRollsBackAllCommittedFiles(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")
	before := []byte(`{"before":true}`)
	for _, path := range []string{a, b} {
		if err := os.WriteFile(path, before, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	prepared, err := prepareWrites([]fileWrite{
		{path: a, payload: []byte(`{"after":1}`), verifyJSON: true},
		{path: b, payload: []byte(`{"after":2}`), verifyJSON: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupPrepared(prepared) })
	// Simulate changed bytes between preparation and readback without replacing production I/O.
	if err := os.WriteFile(prepared[1].tmp, []byte(`{"corrupted":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := commitWrites(prepared); err == nil || !strings.Contains(err.Error(), "写入验证失败") {
		t.Fatalf("expected readback verification failure: %v", err)
	}
	assertFileBytes(t, a, before)
	assertFileBytes(t, b, before)
	assertNoTemps(t, dir)
}

func TestBackupFailurePreventsReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")
	before := []byte(`{"before":true}`)
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".bak", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path+".bak", "block"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte(`{"after":true}`), 0o600, true); err == nil {
		t.Fatal("backup failure was ignored")
	}
	assertFileBytes(t, path, before)
	assertNoTemps(t, dir)
}

func TestAtomicWritesValidateJSONAndPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	before := []byte(`{"before":true}`)
	if err := os.WriteFile(path, before, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"null", "", "{broken"} {
		if err := WriteFileAtomic(path, []byte(invalid), 0o600, true); err == nil {
			t.Errorf("invalid JSON accepted: %q", invalid)
		}
		assertFileBytes(t, path, before)
	}
	after := []byte(`{"after":true}`)
	if err := WriteFileAtomic(path, after, 0o600, true); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, path, after)
	assertFileBytes(t, path+".bak", before)
	if runtime.GOOS != "windows" {
		for _, file := range []string{path, path + ".bak"} {
			info, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Errorf("mode of %s: %v", file, info.Mode().Perm())
			}
		}
	}
}

func assertFileBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s: got %s, want %s", path, got, want)
	}
}

func assertNoTemps(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".go.tmp-") {
			t.Errorf("temporary file leaked: %s", entry.Name())
		}
	}
}
