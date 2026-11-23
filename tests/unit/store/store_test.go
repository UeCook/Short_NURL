package store_test

import (
	. "github.com/UeCook/Short_NURL/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadMissingAndWrite(t *testing.T) {
	dir := t.TempDir()
	st := New(filepath.Join(dir, "perm.json"), "+08:00")

	data, err := st.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatalf("want empty, got %v", data)
	}

	err = st.ReadWrite(func(d Data) error {
		d["abc1"] = map[string]any{"id": "abc1", "url": "https://example.com", "lurl": "https://s/abc1"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(st.Path())
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, `\/`) {
		t.Fatalf("must not escape slashes: %s", text)
	}
	if strings.HasSuffix(text, "\n") {
		t.Fatalf("must not end with newline")
	}
	if !strings.Contains(text, `"v": 1`) {
		t.Fatalf("envelope missing: %s", text)
	}

	data, err = st.Read()
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := data.Entry("abc1")
	if !ok {
		t.Fatal("entry missing")
	}
	if u, _ := FieldString(entry, "url"); u != "https://example.com" {
		t.Fatalf("url = %q", u)
	}
}

func TestEmptyFileIsCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "perm.json")
	if err := os.WriteFile(path, []byte("   "), 0o644); err != nil {
		t.Fatal(err)
	}
	st := New(path, "+08:00")
	if _, err := st.Read(); err == nil {
		t.Fatal("empty file must be corrupt")
	}
	if err := st.LockBegin(); err != nil {
		t.Fatal(err)
	}
	_, err := st.ReadLocked()
	st.LockEnd()
	if err == nil {
		t.Fatal("locked read of empty file must fail")
	}
}

func TestBadJSONIsCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "perm.json")
	if err := os.WriteFile(path, []byte(`{"v":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	st := New(path, "+08:00")
	if _, err := st.Read(); err == nil {
		t.Fatal("missing d must be corrupt")
	}
}

func TestDualStoreOrder(t *testing.T) {
	dir := t.TempDir()
	a := New(filepath.Join(dir, "a.json"), "+08:00")
	b := New(filepath.Join(dir, "b.json"), "+08:00")

	_, err := WithBothLocks(a, b, func(all map[string]Data) (*TxResult, error) {
		if _, ok := all[a.Path()]; !ok {
			t.Fatal("missing a")
		}
		if _, ok := all[b.Path()]; !ok {
			t.Fatal("missing b")
		}
		all[a.Path()]["k"] = map[string]any{"id": "k", "url": "https://e", "lurl": "l"}
		return &TxResult{Write: []string{a.Path()}, Result: "ok"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	data, err := a.Read()
	if err != nil {
		t.Fatal(err)
	}
	if !data.Has("k") {
		t.Fatal("write lost")
	}
	data, err = b.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatal("b must be untouched")
	}
}

func TestSharedStoreLockWaitsForPreviousOperation(t *testing.T) {
	st := New(filepath.Join(t.TempDir(), "perm.json"), "+08:00")
	if err := st.LockBegin(); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		err := st.LockBegin()
		if err == nil {
			err = st.LockEnd()
		}
		done <- err
	}()
	<-started

	var early error
	returnedEarly := false
	select {
	case early = <-done:
		returnedEarly = true
	case <-time.After(50 * time.Millisecond):
	}
	if err := st.LockEnd(); err != nil {
		t.Fatal(err)
	}
	if returnedEarly {
		t.Fatalf("concurrent LockBegin returned before release: %v", early)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("waiting operation did not acquire the lock")
	}
}

func TestCleanExpired(t *testing.T) {
	d := Data{
		"live": map[string]any{"id": "live", "url": "u", "lurl": "l", "t": "2999-01-01T00:00:00+00:00"},
		"dead": map[string]any{"id": "dead", "url": "u", "lurl": "l", "t": "2000-01-01T00:00:00+00:00"},
		"perm": map[string]any{"id": "perm", "url": "u", "lurl": "l"},
		"junk": "not-an-object",
	}
	if n := CleanExpired(d); n != 1 {
		t.Fatalf("removed %d", n)
	}
	if d.Has("dead") {
		t.Fatal("dead should be gone")
	}
	if !d.Has("junk") {
		t.Fatal("non-object entry must be kept")
	}
	if CountActive(d) != 3 {
		t.Fatalf("active = %d", CountActive(d))
	}
}

func TestDualStoreCommitUpdatesBothBackups(t *testing.T) {
	dir := t.TempDir()
	a, b := New(filepath.Join(dir, "a.json"), "+08:00"), New(filepath.Join(dir, "b.json"), "+08:00")
	before := []byte(`{"v":1,"d":{"old":{}}}`)
	for _, st := range []*Store{a, b} {
		if err := os.WriteFile(st.Path(), before, 0o640); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(st.Path()+".bak", []byte(`{"stale":true}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, err := WithBothLocks(b, a, func(all map[string]Data) (*TxResult, error) {
		all[a.Path()]["new"] = map[string]any{"id": "new"}
		all[b.Path()]["new"] = map[string]any{"id": "new"}
		return &TxResult{Write: []string{a.Path(), b.Path()}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range []*Store{a, b} {
		data, err := st.Read()
		if err != nil || !data.Has("new") {
			t.Fatalf("commit failed: %v, err=%v", data, err)
		}
		backup, err := os.ReadFile(st.Path() + ".bak")
		if err != nil || string(backup) != string(before) {
			t.Fatalf("backup is stale: %s, err=%v", backup, err)
		}
	}
}

func TestDualStoreNilDataWritesEmptyObject(t *testing.T) {
	dir := t.TempDir()
	a, b := New(filepath.Join(dir, "a.json"), "+08:00"), New(filepath.Join(dir, "b.json"), "+08:00")
	_, err := WithBothLocks(a, b, func(all map[string]Data) (*TxResult, error) {
		all[a.Path()] = nil
		return &TxResult{Write: []string{a.Path()}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := a.Read()
	if err != nil || len(data) != 0 {
		t.Fatalf("empty data did not round trip: %v, err=%v", data, err)
	}
}
