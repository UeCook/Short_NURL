// Package fslock 提供跨进程文件锁，语义对齐 v2.0.3 的 flock(LOCK_EX)。
//
// 锁文件永不删除，避免 inode 更换导致两个进程锁到不同对象。
package fslock

import "errors"

// ErrNotAcquired 非阻塞获取失败。
var ErrNotAcquired = errors.New("lock not acquired")

// Unlock 释放锁。
type Unlock func() error

// Acquire 获取 path 上的排他锁。blocking 为 false 时拿不到立刻返回 ErrNotAcquired。
func Acquire(path string, blocking bool) (Unlock, error) {
	return acquire(path, blocking)
}
