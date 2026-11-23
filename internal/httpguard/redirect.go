// Package httpguard 提供携带凭证的 HTTP 客户端的重定向策略。
//
// 背景：Authorization/Cookie 之外的自定义认证头（如 X-Headless-Token、LPA-Key）
// 不受标准库跨域保护，默认跟随 3xx 会把凭证转发给 Location 指向的主机。
package httpguard

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// SameOriginOnly 返回一个 http.Client.CheckRedirect：只允许同源重定向，
// 以及同主机从 HTTP 升级到 HTTPS；跨主机、HTTPS 降级、端口变化一律拒绝。
func SameOriginOnly(origin string) func(*http.Request, []*http.Request) error {
	base, err := url.Parse(origin)
	if err != nil || base.Host == "" {
		return func(*http.Request, []*http.Request) error {
			return fmt.Errorf("拒绝重定向：站点地址无效")
		}
	}
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("拒绝重定向：超过 5 次")
		}
		if !Allowed(base, req.URL) {
			return fmt.Errorf("拒绝跨源重定向（%s → %s），避免泄露认证头",
				base.Redacted(), req.URL.Redacted())
		}
		return nil
	}
}

// Allowed 判断从 from 到 to 的重定向是否在允许范围内。
func Allowed(from, to *url.URL) bool {
	if !strings.EqualFold(from.Hostname(), to.Hostname()) {
		return false
	}
	fromPort, toPort := effectivePort(from), effectivePort(to)
	if strings.EqualFold(from.Scheme, "https") {
		return strings.EqualFold(to.Scheme, "https") && fromPort == toPort
	}
	if strings.EqualFold(to.Scheme, "https") {
		// 同主机的 HTTP → HTTPS 升级；常见形态是 80 → 443。
		return (fromPort == "80" && toPort == "443") || fromPort == toPort
	}
	return strings.EqualFold(from.Scheme, "http") &&
		strings.EqualFold(to.Scheme, "http") && fromPort == toPort
}

func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	}
	return ""
}
