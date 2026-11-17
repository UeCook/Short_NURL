package link_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UeCook/Short_NURL/internal/fslock"
	"github.com/UeCook/Short_NURL/internal/link"
)

func TestCreateRejectsInvalidServiceCodeLengthBeforeLocking(t *testing.T) {
	for _, length := range []int{-1, 0, 1, 3, 6, 1000000} {
		t.Run(fmt.Sprint(length), func(t *testing.T) {
			f := newFixture(t)
			f.cfg.Codes.SvcCodeLength = length
			result := f.svc.Create(f.svcCtx, map[string]any{"url": "https://8.8.8.8/invalid-length"})
			if result.OK || result.Code != "config_error" || result.HTTPStatus != 500 {
				t.Fatalf("invalid length was accepted: %+v", result)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(f.cfg.PermPath), ".operations")); !os.IsNotExist(err) {
				t.Fatalf("invalid length reached operation locking: %v", err)
			}
			data, err := f.svc.Perm.Read()
			if err != nil || len(data) != 0 {
				t.Fatalf("invalid length committed cold data: %v, err=%v", data, err)
			}
		})
	}
}

func TestCreatePanicReleasesOperationLocks(t *testing.T) {
	f := newFixture(t)
	client := f.svc.Hot
	f.svc.Hot = nil
	target := "https://8.8.8.8/create-panic"
	expectPanic(t, func() {
		f.svc.Create(f.ctx, map[string]any{"url": target})
	})

	assertOperationLocksReleased(t, f.cfg.PermPath)
	f.svc.Hot = client
	if result := f.svc.Create(f.ctx, map[string]any{"url": target}); !result.OK {
		t.Fatalf("retry after panic failed: %+v", result)
	}
}

func TestDeletePanicReleasesOperationLock(t *testing.T) {
	f := newFixture(t)
	created := f.svc.Create(f.ctx, map[string]any{"url": "https://8.8.8.8/delete-panic"})
	if !created.OK {
		t.Fatalf("create: %+v", created)
	}
	code := strings.TrimPrefix(created.Data.(link.CreateOK).ShortURL, f.cfg.Site.Domain+"/")
	client := f.svc.Hot
	f.svc.Hot = nil
	expectPanic(t, func() {
		f.svc.Delete(f.ctx, code)
	})

	assertOperationLocksReleased(t, f.cfg.PermPath)
	f.svc.Hot = client
	if result := f.svc.Delete(f.ctx, code); !result.OK {
		t.Fatalf("retry after panic failed: %+v", result)
	}
}

func expectPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("expected an injected hot-client panic")
		}
	}()
	fn()
}

func assertOperationLocksReleased(t *testing.T, storePath string) {
	t.Helper()
	dir := filepath.Join(filepath.Dir(storePath), ".operations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("panic was injected before any operation lock was acquired")
	}
	for _, entry := range entries {
		unlock, err := fslock.Acquire(filepath.Join(dir, entry.Name()), false)
		if err != nil {
			t.Errorf("operation lock remained held after panic: %s: %v", entry.Name(), err)
			continue
		}
		if err := unlock(); err != nil {
			t.Fatal(err)
		}
	}
}
