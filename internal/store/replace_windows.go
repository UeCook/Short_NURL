package store

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

var moveFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

func replaceFile(from, to string) error {
	src, err := syscall.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	dst, err := syscall.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	const replaceExisting, writeThrough = 1, 8
	result, _, callErr := moveFileEx.Call(uintptr(unsafe.Pointer(src)), uintptr(unsafe.Pointer(dst)), replaceExisting|writeThrough)
	if result == 0 {
		return &os.LinkError{Op: "MoveFileExW", Old: from, New: to, Err: callErr}
	}
	return nil
}

func retryReplace(err error) bool {
	const sharingViolation, lockViolation = syscall.Errno(32), syscall.Errno(33)
	return errors.Is(err, sharingViolation) || errors.Is(err, lockViolation)
}

// Windows 通过 MoveFileExW 的 WRITE_THROUGH 标志同步重命名。
func syncParent(string) error { return nil }
