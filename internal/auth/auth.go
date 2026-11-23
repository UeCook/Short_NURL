// Package auth 把请求凭证解析为 AuthContext，并管理一次性 Key 预约的提交/释放。
package auth

import (
	"context"
	"strings"

	"github.com/UeCook/Short_NURL/internal/apikey"
)

// Context 可信进程内的认证上下文。
type Context struct {
	KeyType          string // resident / onetime / service
	Consumed         bool   // 是否为已预约、成功后将消费的 onetime
	KeyPrefix        string
	ReservationToken string
}

// Result 认证结果。
type Result struct {
	Success bool
	Ctx     *Context
	Reason  string // missing / invalid / wrong_channel / backend_error
}

// Auth 认证服务。
type Auth struct {
	keys *apikey.KeyStore
}

// New 构造认证服务。
func New(keys *apikey.KeyStore) *Auth { return &Auth{keys: keys} }

// Authenticate 校验凭证并返回认证结果。
func (a *Auth) Authenticate(credential string, allowedTypes []string) Result {
	return a.AuthenticateContext(context.Background(), credential, allowedTypes)
}

// AuthenticateContext 允许调用方取消等待密钥存储锁。
func (a *Auth) AuthenticateContext(ctx context.Context, credential string, allowedTypes []string) Result {
	if credential == "" {
		return Result{Reason: "missing"}
	}
	if !validCredentialFormat(credential) {
		return Result{Reason: "invalid"}
	}
	v := a.keys.VerifyContext(ctx, credential, allowedTypes)
	if v.OK {
		return Result{
			Success: true,
			Ctx: &Context{
				KeyType:          v.KeyType,
				Consumed:         v.KeyType == "onetime",
				KeyPrefix:        prefix8(credential),
				ReservationToken: v.ReservationToken,
			},
		}
	}
	reason := v.Error
	if reason == "" {
		reason = "invalid"
	}
	return Result{Reason: reason}
}

// 32 字节 Base58 Key 的编码为 32–44 位；先拒绝畸形值，避免读取/锁定 keys.json。
func validCredentialFormat(raw string) bool {
	if len(raw) < 35 || len(raw) > 47 || !strings.HasPrefix(raw, "su_") {
		return false
	}
	const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	for _, c := range raw[3:] {
		if !strings.ContainsRune(alphabet, c) {
			return false
		}
	}
	return true
}

// CompleteContext 提交或释放一次性 Key 预约，允许取消锁等待。
func (a *Auth) CompleteContext(ctx context.Context, reservation *Context, success bool) bool {
	if reservation == nil || reservation.KeyType != "onetime" {
		return true
	}
	if reservation.ReservationToken == "" {
		return false
	}
	return a.keys.CompleteOnetimeContext(ctx, reservation.ReservationToken, success)
}

// AllowedKeyTypes 返回端点允许的密钥类型（对齐 Helpers::allowedKeyTypes）。
func AllowedKeyTypes(source, action string) ([]string, bool) {
	table := map[string]map[string][]string{
		"webui": {
			"create": {"resident", "onetime"},
			"delete": {"resident"},
			"list":   {"resident"},
			"stat":   {"resident"},
		},
		"headless": {
			"create": {"resident", "onetime", "service"},
			"delete": {"resident", "service"},
			"list":   {"resident", "service"},
			"stat":   {"resident", "service"},
			"get":    {"resident", "service"},
		},
	}
	m, ok := table[source]
	if !ok {
		return nil, false
	}
	types, ok := m[action]
	return types, ok
}

func prefix8(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 8 {
		return s[:8]
	}
	return s
}
