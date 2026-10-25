// Package store 实现 JSON 冷存储（perm.json / temp.json）与 keys.json 的安全写入。
//
// 数据格式：{"v":1, "at":"ISO8601", "d":{"code": {entry}, ...}}
// 写入协议：备份 .bak → 写 .go.tmp → fsync → rename → 回读校验 → 失败用 .bak 回滚。
package store

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/UeCook/Short_NURL/internal/fslock"
	"github.com/UeCook/Short_NURL/internal/isoutil"
)

// Data 是 `d` 字段的内容：code → entry。
type Data map[string]any

type envelope struct {
	V  int    `json:"v"`
	At string `json:"at"`
	D  Data   `json:"d"`
}

// Store 单个 JSON 数据文件。
type Store struct {
	path string
	tz   string
	lock *locked
}

type locked struct {
	unlock fslock.Unlock
}

// New 构造 Store，必要时创建目录。
func New(path, tz string) *Store {
	return &Store{path: path, tz: tz}
}

// Path 返回数据文件路径。
func (s *Store) Path() string { return s.path }

// Tz 返回时区配置。
func (s *Store) Tz() string { return s.tz }

// Read 读取 `d`（不加锁）。
func (s *Store) Read() (Data, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return Data{}, nil
		}
		return nil, fmt.Errorf("数据文件读取失败: %s", s.path)
	}
	return decodeData(raw, s.path)
}

// LockBegin 获取存储排他锁（阻塞）。
func (s *Store) LockBegin() error {
	if s.lock != nil {
		return errors.New("重复加锁")
	}
	dir := filepath.Dir(s.path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("无法创建目录: %s", dir)
		}
	}
	unlock, err := fslock.Acquire(s.path+".lock", true)
	if err != nil {
		return fmt.Errorf("无法获取排他锁: %s.lock", s.path)
	}
	s.lock = &locked{unlock: unlock}
	return nil
}

// LockEnd 释放存储锁。
func (s *Store) LockEnd() error {
	if s.lock == nil {
		return nil
	}
	err := s.lock.unlock()
	s.lock = nil
	return err
}

// ReadLocked 在持锁状态下读取 `d`。
func (s *Store) ReadLocked() (Data, error) {
	if s.lock == nil {
		return nil, errors.New("未持有锁，请先调用 LockBegin()")
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return Data{}, nil
		}
		return nil, fmt.Errorf("数据文件读取失败: %s", s.path)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, fmt.Errorf("数据文件为空: %s", s.path)
	}
	return decodeData(raw, s.path)
}

// WriteLocked 在持锁状态下安全写入 `d`。
func (s *Store) WriteLocked(data Data) error {
	if s.lock == nil {
		return errors.New("未持有锁，请先调用 LockBegin()")
	}
	if data == nil {
		data = Data{}
	}
	at, err := isoutil.Format(nowUnix(), s.tz)
	if err != nil {
		return err
	}
	payload, err := EncodePretty(envelope{V: 1, At: at, D: data})
	if err != nil {
		return fmt.Errorf("数据 JSON 编码失败: %w", err)
	}
	return writeAtomic(s.path, payload)
}

// ReadWrite 读-改-写的便捷封装（自带存储锁）。
func (s *Store) ReadWrite(fn func(Data) error) error {
	if err := s.LockBegin(); err != nil {
		return err
	}
	defer s.LockEnd()
	data, err := s.ReadLocked()
	if err != nil {
		return err
	}
	if err := fn(data); err != nil {
		return err
	}
	return s.WriteLocked(data)
}

// TxResult 双 Store 事务回调的返回值。
type TxResult struct {
	Write  []string
	Result any
}

// WithBothLocks 同时锁定两个 Store，按绝对路径升序加锁后执行回调。
// 回调只声明需要写入的路径，由本函数统一提交。
func WithBothLocks(first, second *Store, fn func(map[string]Data) (*TxResult, error)) (any, error) {
	stores := []*Store{first, second}
	if stores[0].path > stores[1].path {
		stores[0], stores[1] = stores[1], stores[0]
	}
	if err := stores[0].LockBegin(); err != nil {
		return nil, err
	}
	defer stores[0].LockEnd()
	if err := stores[1].LockBegin(); err != nil {
		return nil, err
	}
	defer stores[1].LockEnd()

	data := make(map[string]Data, 2)
	for _, st := range stores {
		d, err := st.ReadLocked()
		if err != nil {
			return nil, err
		}
		data[st.path] = d
	}

	res, err := fn(data)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, errors.New("双 Store 事务回调必须返回写入路径")
	}

	// 提交前先把所有待写文件的 tmp 全部落盘，再依次 rename。
	var prepared []pending
	for _, path := range res.Write {
		var st *Store
		switch path {
		case stores[0].path:
			st = stores[0]
		case stores[1].path:
			st = stores[1]
		default:
			rollbackPrepared(prepared)
			return nil, errors.New("双 Store 事务返回了未知路径")
		}
		at, ferr := isoutil.Format(nowUnix(), st.tz)
		if ferr != nil {
			rollbackPrepared(prepared)
			return nil, ferr
		}
		payload, ferr := EncodePretty(envelope{V: 1, At: at, D: data[path]})
		if ferr != nil {
			rollbackPrepared(prepared)
			return nil, ferr
		}
		tmp, ferr := writeTempFile(st.path, payload)
		if ferr != nil {
			rollbackPrepared(prepared)
			return nil, ferr
		}
		prepared = append(prepared, pending{st: st, tmp: tmp})
	}

	// 依次 rename；任一失败用各自的 .bak 回滚已提交的文件。
	var committed []pending
	for _, p := range prepared {
		if err := renameTemp(p.tmp, p.st.path); err != nil {
			for _, c := range committed {
				_ = restoreBackup(c.st.path)
			}
			_ = os.Remove(p.tmp)
			return nil, fmt.Errorf("write_failed: %w", err)
		}
		committed = append(committed, p)
		if err := verifyFile(p.st.path); err != nil {
			_ = restoreBackup(p.st.path)
			return nil, err
		}
	}
	return res.Result, nil
}

func rollbackPrepared(prepared []pending) {
	for _, p := range prepared {
		_ = os.Remove(p.tmp)
	}
}

// Entry 取出 code 对应的条目对象（PHP 的 is_array 判定）。
func (d Data) Entry(code string) (map[string]any, bool) {
	v, ok := d[code]
	if !ok || v == nil {
		return nil, false
	}
	m, ok := v.(map[string]any)
	return m, ok
}

// Has 报告 code 是否存在有效条目（PHP isset 语义：null 视为不存在）。
func (d Data) Has(code string) bool {
	v, ok := d[code]
	return ok && v != nil
}

// CleanExpired 原地移除过期条目，返回移除数。
func CleanExpired(d Data) int {
	removed := 0
	for k, v := range d {
		if m, ok := v.(map[string]any); ok {
			if isoutil.IsExpired(m["t"]) {
				delete(d, k)
				removed++
			}
		}
	}
	return removed
}

// CountActive 统计未过期条目数。
func CountActive(d Data) int {
	count := 0
	for _, v := range d {
		if m, ok := v.(map[string]any); ok {
			if isoutil.IsExpired(m["t"]) {
				continue
			}
		}
		count++
	}
	return count
}

// SortedCodes 返回升序短码列表（偏差 P1：确定性输出）。
func SortedCodes(d Data) []string {
	codes := make([]string, 0, len(d))
	for k := range d {
		codes = append(codes, k)
	}
	sort.Strings(codes)
	return codes
}

// FieldString 读取条目里的字符串字段。
func FieldString(m map[string]any, key string) (string, bool) {
	if m == nil {
		return "", false
	}
	v, ok := m[key]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}
