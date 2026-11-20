package link

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/UeCook/Short_NURL/internal/fslock"
)

// opLock 分别为短码和 URL 使用 4096 个固定锁槽，文件保留以避免更换 inode。
type opLock struct {
	unlock fslock.Unlock
}

func acquireOpLock(storePath, identity string) (*opLock, error) {
	dir := filepath.Join(filepath.Dir(storePath), ".operations")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(identity))
	namespace, _, _ := strings.Cut(identity, ":")
	bucket := uint16(sum[0])<<4 | uint16(sum[1]>>4)
	name := fmt.Sprintf("%s-%03x.lock", namespace, bucket)
	unlock, err := fslock.Acquire(filepath.Join(dir, name), false)
	if err != nil {
		return nil, err
	}
	return &opLock{unlock: unlock}, nil
}

func lockFailure(err error) BizResponse {
	if errors.Is(err, fslock.ErrNotAcquired) {
		return Error("operation_in_progress", "有其他操作占用存储锁，请稍后重试", 409)
	}
	slog.Error("[link] 无法打开操作锁", "err", err)
	return Error("write_failed", "服务器内部错误", 500)
}

func (l *opLock) release() {
	if l == nil || l.unlock == nil {
		return
	}
	_ = l.unlock()
	l.unlock = nil
}
