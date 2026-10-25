package link

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"

	"github.com/UeCook/Short_NURL/internal/fslock"
)

// opLock 同一短码/目标的操作串行；锁文件保留，避免 unlink 后不同进程锁住不同 inode。
type opLock struct {
	unlock fslock.Unlock
}

func acquireOpLock(storePath, identity string) *opLock {
	dir := filepath.Join(filepath.Dir(storePath), ".operations")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil
	}
	sum := sha256.Sum256([]byte(identity))
	name := hex.EncodeToString(sum[:]) + ".lock"
	unlock, err := fslock.Acquire(filepath.Join(dir, name), false)
	if err != nil {
		return nil
	}
	return &opLock{unlock: unlock}
}

func (l *opLock) release() {
	if l == nil || l.unlock == nil {
		return
	}
	_ = l.unlock()
	l.unlock = nil
}
