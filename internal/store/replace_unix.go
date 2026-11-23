//go:build !windows

package store

import (
	"errors"
	"os"
	"path/filepath"
)

func replaceFile(from, to string) error { return os.Rename(from, to) }

func retryReplace(error) bool { return false }

func syncParent(path string) error {
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
