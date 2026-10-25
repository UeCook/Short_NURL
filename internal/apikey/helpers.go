package apikey

import (
	"bytes"
	"path/filepath"

	"github.com/UeCook/Short_NURL/internal/fslock"
	"github.com/UeCook/Short_NURL/internal/store"
)

func dirOf(path string) string {
	dir := filepath.Dir(path)
	if dir == "" {
		return "."
	}
	return dir
}

func lockFile(path string) (func() error, error) {
	return fslock.Acquire(path, true)
}

func encodePretty(v any) ([]byte, error) {
	return store.EncodePretty(v)
}

func trimSpace(b []byte) []byte { return bytes.TrimSpace(b) }
