//go:build windows

package fslock

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

var (
	modkernel32     = syscall.NewLazyDLL("kernel32.dll")
	procCreateFileW = modkernel32.NewProc("CreateFileW")
)

const (
	genericRead  = 0x80000000
	genericWrite = 0x40000000
	openAlways   = 4
)

// errSharingViolation 被其他进程独占打开。
var errSharingViolation = syscall.Errno(32)

// acquire 用「独占打开」近似 flock(LOCK_EX)：dwShareMode=0，关闭即释放。
func acquire(path string, blocking bool) (Unlock, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	tryOpen := func() (syscall.Handle, error) {
		h, _, callErr := procCreateFileW.Call(
			uintptr(unsafe.Pointer(p)),
			uintptr(genericRead|genericWrite),
			0,
			0,
			uintptr(openAlways),
			uintptr(syscall.FILE_ATTRIBUTE_NORMAL),
			0,
		)
		if h == uintptr(syscall.InvalidHandle) {
			if callErr != nil && !errors.Is(callErr, syscall.Errno(0)) {
				return 0, callErr
			}
			return 0, syscall.EINVAL
		}
		return syscall.Handle(h), nil
	}

	h, err := tryOpen()
	if err == nil {
		return makeUnlock(h), nil
	}
	if !errors.Is(err, errSharingViolation) {
		return nil, err
	}
	if !blocking {
		return nil, ErrNotAcquired
	}
	for {
		time.Sleep(10 * time.Millisecond)
		h, err = tryOpen()
		if err == nil {
			return makeUnlock(h), nil
		}
		if !errors.Is(err, errSharingViolation) {
			return nil, err
		}
	}
}

func makeUnlock(h syscall.Handle) Unlock {
	return func() error {
		return syscall.CloseHandle(h)
	}
}
