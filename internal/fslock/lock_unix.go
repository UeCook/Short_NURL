//go:build !windows

package fslock

import (
	"os"
	"syscall"
)

func acquire(path string, blocking bool) (Unlock, error) {
	// 锁文件没有内容；只需读权限，便于和 OpenResty 共用。
	f, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	how := syscall.LOCK_EX
	if !blocking {
		how |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		f.Close()
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			return nil, ErrNotAcquired
		}
		return nil, err
	}
	return func() error {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return err
	}, nil
}
