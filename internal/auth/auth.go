// Package auth 把请求凭证解析为 AuthContext，并管理一次性 Key 预约的提交/释放。
package auth

import (
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
	if credential == "" {
		return Result{Reason: "missing"}
	}
	v := a.keys.Verify(credential, allowedTypes)
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

// Complete 提交或释放一次性 Key 预约。
func (a *Auth) Complete(ctx *Context, success bool) bool {
	if ctx == nil {
		return true
	}
	if ctx.KeyType != "onetime" {
		return true
	}
	if ctx.ReservationToken == "" {
		return false
	}
	return a.keys.CompleteOnetime(ctx.ReservationToken, success)
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
