// Package apikey 实现 KeyStore（SHA-256 哈希 + Base58 明文 + 一次性预约状态机）。
package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/UeCook/Short_NURL/internal/isoutil"
	"github.com/UeCook/Short_NURL/internal/store"
)

// Resident 常驻 Key 槽位。
type Resident struct {
	KeyHash   string `json:"key_hash"`
	KeyPrefix string `json:"key_prefix"`
	Created   string `json:"created"`
	Expires   string `json:"expires"`
}

// Service 服务 Key 槽位。
type Service struct {
	KeyHash   string `json:"key_hash"`
	KeyPrefix string `json:"key_prefix"`
	Created   string `json:"created"`
	Label     string `json:"label"`
}

// PoolSlot 一次性 Key 槽位。
type PoolSlot struct {
	KeyHash          *string `json:"key_hash"`
	KeyPrefix        string  `json:"key_prefix"`
	Created          string  `json:"created"`
	ReservedAt       *int64  `json:"reserved_at,omitempty"`
	ReservationToken *string `json:"reservation_token,omitempty"`
}

// File keys.json 的根结构。
type File struct {
	Resident    *Resident   `json:"resident"`
	OnetimePool []*PoolSlot `json:"onetime_pool"`
	Service     *Service    `json:"service"`
}

// KeyStore 密钥存储。
type KeyStore struct {
	path     string
	tz       string
	ttlDays  int
	poolSize int
	now      func() int64
}

// New 构造 KeyStore。
func New(path, tz string, ttlDays, poolSize int) *KeyStore {
	return &KeyStore{
		path:     path,
		tz:       tz,
		ttlDays:  ttlDays,
		poolSize: poolSize,
		now:      func() int64 { return time.Now().Unix() },
	}
}

// Path 返回 keys.json 路径。
func (k *KeyStore) Path() string { return k.path }

// VerifyResult verify() 的返回值。
type VerifyResult struct {
	OK               bool
	KeyType          string
	Error            string // invalid / wrong_channel / backend_error
	ReservationToken string
}

func invalidResult() VerifyResult      { return VerifyResult{Error: "invalid"} }
func wrongChannelResult() VerifyResult { return VerifyResult{Error: "wrong_channel"} }
func backendErrorResult() VerifyResult { return VerifyResult{Error: "backend_error"} }
func successResult(t string, token string) VerifyResult {
	return VerifyResult{OK: true, KeyType: t, ReservationToken: token}
}

func hashHex(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func generateRawKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "su_" + Base58Encode(buf), nil
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// Verify 校验原始 Key，并在同一把锁内完成类型检查与一次性预约。
func (k *KeyStore) Verify(rawKey string, allowedTypes []string) VerifyResult {
	if rawKey == "" {
		return invalidResult()
	}
	if _, err := os.Stat(k.path); err != nil {
		return invalidResult()
	}

	hash := hashHex(rawKey)

	unlock, err := k.lock()
	if err != nil {
		return backendErrorResult()
	}
	defer unlock()

	now := k.now() // 获取锁可能等待，预约时限从实际验证时刻计算。
	keys, err := k.readLocked()
	if err != nil {
		return backendErrorResult()
	}
	if reapReservations(keys, now) {
		if err := k.writeLocked(keys); err != nil {
			return backendErrorResult()
		}
	}

	// 常驻 Key
	if keys.Resident != nil && keys.Resident.KeyHash != "" &&
		constantTimeEqual(keys.Resident.KeyHash, hash) {
		if !contains(allowedTypes, "resident") {
			return wrongChannelResult()
		}
		if keys.Resident.Expires == "" {
			return invalidResult()
		}
		ts, ok := isoutil.Parse(keys.Resident.Expires)
		if ok && ts > now {
			return successResult("resident", "")
		}
		return invalidResult()
	}

	// 服务 Key
	if keys.Service != nil && keys.Service.KeyHash != "" &&
		constantTimeEqual(keys.Service.KeyHash, hash) {
		if !contains(allowedTypes, "service") {
			return wrongChannelResult()
		}
		return successResult("service", "")
	}

	// 一次性 Key 池
	for _, slot := range keys.OnetimePool {
		if slot == nil || slot.KeyHash == nil || *slot.KeyHash == "" {
			continue
		}
		if !constantTimeEqual(*slot.KeyHash, hash) {
			continue
		}
		if !contains(allowedTypes, "onetime") {
			return wrongChannelResult()
		}
		if slot.ReservedAt != nil && *slot.ReservedAt > 0 {
			return invalidResult()
		}
		token, err := randomHex(16)
		if err != nil {
			return backendErrorResult()
		}
		at := now
		slot.ReservedAt = &at
		slot.ReservationToken = &token
		if err := k.writeLocked(keys); err != nil {
			return backendErrorResult()
		}
		return successResult("onetime", token)
	}

	return invalidResult()
}

// CompleteOnetime 完成一次性 Key 预约；success 时消费，失败时释放。
func (k *KeyStore) CompleteOnetime(reservationToken string, success bool) bool {
	if reservationToken == "" {
		return false
	}
	found := false
	err := k.withLock(func(keys *File) (bool, error) {
		for _, slot := range keys.OnetimePool {
			if slot == nil || slot.ReservationToken == nil ||
				!constantTimeEqual(*slot.ReservationToken, reservationToken) {
				continue
			}
			if success || (k.now()-slotReservedAt(slot) >= 60) {
				empty := ""
				slot.KeyHash = &empty
			}
			slot.ReservedAt = nil
			slot.ReservationToken = nil
			found = true
			return true, k.writeLocked(keys)
		}
		return false, nil
	})
	return err == nil && found
}

// GenerateResident 生成新的常驻 Key，返回明文。
func (k *KeyStore) GenerateResident() (string, error) {
	var raw string
	err := k.withLock(func(keys *File) (bool, error) {
		var err error
		raw, err = generateRawKey()
		if err != nil {
			return false, err
		}
		now := k.now()
		created, err := isoutil.Format(now, k.tz)
		if err != nil {
			return false, err
		}
		expires, err := isoutil.Format(now+int64(k.ttlDays)*86400, k.tz)
		if err != nil {
			return false, err
		}
		keys.Resident = &Resident{
			KeyHash:   hashHex(raw),
			KeyPrefix: prefix8(raw),
			Created:   created,
			Expires:   expires,
		}
		return true, k.writeLocked(keys)
	})
	if err != nil {
		return "", err
	}
	return raw, nil
}

// FillPool 补充一次性 Key 池，返回新生成的明文列表。
func (k *KeyStore) FillPool() ([]string, error) {
	var newKeys []string
	err := k.withLock(func(keys *File) (bool, error) {
		reapReservations(keys, k.now())
		for _, slot := range keys.OnetimePool {
			if slot == nil || slot.KeyHash == nil || *slot.KeyHash == "" || *slot.KeyHash == "0" {
				raw, err := generateRawKey()
				if err != nil {
					return false, err
				}
				created, err := isoutil.Format(k.now(), k.tz)
				if err != nil {
					return false, err
				}
				h := hashHex(raw)
				p := prefix8(raw)
				slot.KeyHash = &h
				slot.KeyPrefix = p
				slot.Created = created
				newKeys = append(newKeys, raw)
			}
		}
		for len(keys.OnetimePool) < k.poolSize {
			raw, err := generateRawKey()
			if err != nil {
				return false, err
			}
			created, err := isoutil.Format(k.now(), k.tz)
			if err != nil {
				return false, err
			}
			h := hashHex(raw)
			keys.OnetimePool = append(keys.OnetimePool, &PoolSlot{
				KeyHash:   &h,
				KeyPrefix: prefix8(raw),
				Created:   created,
			})
			newKeys = append(newKeys, raw)
		}
		return true, k.writeLocked(keys)
	})
	if err != nil {
		return nil, err
	}
	return newKeys, nil
}

// ErrServiceExists 服务密钥已存在。
var ErrServiceExists = errors.New("服务密钥已存在，请先撤销旧密钥")

// GenerateService 生成服务密钥。
func (k *KeyStore) GenerateService(label string) (string, error) {
	var raw string
	err := k.withLock(func(keys *File) (bool, error) {
		if keys.Service != nil {
			return false, ErrServiceExists
		}
		var err error
		raw, err = generateRawKey()
		if err != nil {
			return false, err
		}
		created, err := isoutil.Format(k.now(), k.tz)
		if err != nil {
			return false, err
		}
		if label == "" {
			label = "default"
		}
		keys.Service = &Service{
			KeyHash:   hashHex(raw),
			KeyPrefix: prefix8(raw),
			Created:   created,
			Label:     label,
		}
		return true, k.writeLocked(keys)
	})
	if err != nil {
		return "", err
	}
	return raw, nil
}

// Revoke 吊销指定类型的 Key：all / resident / onetime / service。
func (k *KeyStore) Revoke(kind string) error {
	return k.withLock(func(keys *File) (bool, error) {
		switch kind {
		case "all":
			keys.Resident = nil
			keys.OnetimePool = nil
			keys.Service = nil
		case "resident":
			keys.Resident = nil
		case "onetime":
			keys.OnetimePool = nil
		case "service":
			keys.Service = nil
		default:
			return false, fmt.Errorf("无效的吊销类型: %s", kind)
		}
		return true, k.writeLocked(keys)
	})
}

// StatusItem 一次性 Key 池展示项。
type StatusItem struct {
	Prefix   string
	Created  string
	Consumed bool
	Reserved bool
}

// Status 密钥状态（不含明文）。
type Status struct {
	Resident             *Resident
	Service              *Service
	OnetimeCount         int
	OnetimeReservedCount int
	OnetimePoolSize      int
	OnetimePool          []StatusItem
}

// Status 读取当前密钥状态。
func (k *KeyStore) Status() (*Status, error) {
	var keys *File
	err := k.withLock(func(f *File) (bool, error) {
		if reapReservations(f, k.now()) {
			if err := k.writeLocked(f); err != nil {
				return false, err
			}
		}
		keys = f
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	if keys == nil {
		return nil, errors.New("keys.json 读取失败")
	}
	st := &Status{
		Resident:        keys.Resident,
		Service:         keys.Service,
		OnetimePoolSize: k.poolSize,
	}
	for _, slot := range keys.OnetimePool {
		if slot == nil {
			continue
		}
		consumed := slot.KeyHash == nil || *slot.KeyHash == "" || *slot.KeyHash == "0"
		reserved := !consumed && slot.ReservedAt != nil && *slot.ReservedAt > 0
		if reserved {
			st.OnetimeReservedCount++
		}
		if !consumed && !reserved {
			st.OnetimeCount++
		}
		prefix := slot.KeyPrefix
		if prefix == "" {
			prefix = "-"
		}
		created := slot.Created
		if created == "" {
			created = "-"
		}
		st.OnetimePool = append(st.OnetimePool, StatusItem{
			Prefix:   prefix,
			Created:  created,
			Consumed: consumed,
			Reserved: reserved,
		})
	}
	return st, nil
}

// ── 内部 ────────────────────────────────────────────────────────

func (k *KeyStore) lock() (func() error, error) {
	if err := os.MkdirAll(dirOf(k.path), 0o755); err != nil {
		return nil, err
	}
	return lockFile(k.path + ".lock")
}

// lastWithLockOK 记录 withLock 回调是否要求写入成功。
// 简化：complete 的返回值以回调结果为准。
func (k *KeyStore) withLock(fn func(*File) (bool, error)) error {
	if err := os.MkdirAll(dirOf(k.path), 0o755); err != nil {
		return err
	}
	unlock, err := lockFile(k.path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()

	raw, err := os.ReadFile(k.path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		raw = nil
	}
	keys, err := decodeFile(raw, k.path)
	if err != nil {
		return err
	}
	_, err = fn(keys)
	return err
}

func (k *KeyStore) readLocked() (*File, error) {
	raw, err := os.ReadFile(k.path)
	if err != nil {
		return nil, err
	}
	return decodeFile(raw, k.path)
}

func (k *KeyStore) writeLocked(keys *File) error {
	if keys.OnetimePool == nil {
		keys.OnetimePool = []*PoolSlot{}
	}
	payload, err := encodePretty(keys)
	if err != nil {
		return err
	}
	// keys.json 含认证摘要，只允许属主读写。不继承旧文件的宽松权限：
	// 历史版本首次写入为 0644，收紧时不能继续沿用。
	mode := os.FileMode(0o600)
	return store.WriteFileAtomic(k.path, payload, mode, true)
}

func decodeFile(raw []byte, path string) (*File, error) {
	if raw == nil {
		return &File{OnetimePool: []*PoolSlot{}}, nil
	}
	trimmed := string(trimSpace(raw))
	if trimmed == "" {
		if _, err := os.Stat(path); err != nil {
			return &File{OnetimePool: []*PoolSlot{}}, nil
		}
		return nil, errors.New("keys.json 文件为空")
	}
	var keys File
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil, errors.New("keys.json 数据损坏")
	}
	if keys.OnetimePool == nil {
		keys.OnetimePool = []*PoolSlot{}
	}
	return &keys, nil
}

func reapReservations(keys *File, now int64) bool {
	changed := false
	for _, slot := range keys.OnetimePool {
		if slot == nil {
			continue
		}
		at := slotReservedAt(slot)
		if at > 0 && now-at >= 60 {
			empty := ""
			slot.KeyHash = &empty
			slot.ReservedAt = nil
			slot.ReservationToken = nil
			changed = true
		}
	}
	return changed
}

func slotReservedAt(slot *PoolSlot) int64 {
	if slot.ReservedAt == nil {
		return 0
	}
	return *slot.ReservedAt
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func prefix8(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}
