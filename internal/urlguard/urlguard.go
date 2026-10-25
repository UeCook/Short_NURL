// Package urlguard 提供目标链接校验与 SSRF（内网）拦截。
//
// 基线：PHP 的 filter_var(FILTER_VALIDATE_URL) + isPrivateUrl()；
// 偏差 D5/D6：额外拒绝 `"<>`、空白与控制字符，并把 CGNAT/组播/未指定纳入私网判定。
package urlguard

import (
	"context"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Error 业务校验错误。
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

const (
	CodeMissingURL = "missing_url"
	CodeInvalidURL = "invalid_url"
	CodeURLTooLong = "url_too_long"
	CodePrivateURL = "private_url"
)

var schemeRe = regexp.MustCompile(`(?i)^https?://`)

// forbiddenChars 拒绝控制字符、空白、双引号、尖括号与反斜杠。
// 反斜杠在浏览器与 Go 的 URL 解析中语义不同，保留它会造成同一字符串
// 被判为不同主机（解析差异），因此统一拒绝（偏差 D6，与 Lua 校验一致）。
var forbiddenChars = regexp.MustCompile("[\\x00-\\x1f\\x7f\\s\"<>\\\\]")

// Validate 按 PHP CreateLinkService 的顺序校验目标链接（含空值判定）。
func Validate(raw string) (string, *Error) {
	urlStr := strings.TrimSpace(raw)
	if urlStr == "" {
		return "", &Error{CodeMissingURL, "缺少目标链接"}
	}
	if err := Check(urlStr); err != nil {
		return "", err
	}
	return urlStr, nil
}

// Check 校验已 trim 的目标链接（合法性 → 长度 → 私网）。
func Check(urlStr string) *Error {
	if !validURL(urlStr) {
		return &Error{CodeInvalidURL, "目标链接无效"}
	}
	if len(urlStr) > 2048 {
		return &Error{CodeURLTooLong, "目标链接过长（最大 2048 字符）"}
	}
	if IsPrivate(urlStr) {
		return &Error{CodePrivateURL, "目标链接指向内网地址，已被拒绝"}
	}
	return nil
}

// validURL 对齐 filter_var(FILTER_VALIDATE_URL) 的常见形态，并施加 D6 字符集限制。
func validURL(u string) bool {
	if forbiddenChars.MatchString(u) {
		return false
	}
	if !schemeRe.MatchString(u) {
		return false
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	if strings.Contains(parsed.Host, " ") {
		return false
	}
	return true
}

// IsPrivate 判定 URL 是否指向内网/保留地址（true = 拒绝）。
// 创建路径使用此函数：先解析字面量，再对域名做 DNS 解析。
func IsPrivate(raw string) bool {
	host := hostname(raw)
	if host == "" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return isPrivateIP(ip)
	}
	// 浏览器会把 0177.0.0.1、127.1、0x7f000001 之类解释为 IPv4。
	if ip := parseBrowserIPv4(host); ip != nil {
		return isPrivateIP(ip)
	}
	// 含冒号却不是合法 IPv6：无法安全判断，按私网拒绝。
	if strings.Contains(host, ":") {
		return true
	}
	if isLocalHostname(host) || looksNumericHost(host) {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return true
	}
	for _, ip := range ips {
		if isPrivateIP(ip) {
			return true
		}
	}
	return false
}

// IsPrivateLiteral 只检查字面主机名，不做 DNS 解析。
// 供恢复/热存储写入等不能产生网络请求的路径使用。
func IsPrivateLiteral(raw string) bool {
	host := hostname(raw)
	if host == "" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return isPrivateIP(ip)
	}
	// 恢复路径没有 DNS 兜底，非标准 IPv4 写法必须在此拦截。
	if ip := parseBrowserIPv4(host); ip != nil {
		return isPrivateIP(ip)
	}
	if strings.Contains(host, ":") {
		return true
	}
	return isLocalHostname(host) || looksNumericHost(host)
}

// ValidHotURL 校验恢复写入热存储时的 URL 合法性（对齐 DeleteLinkService::restoreHotEntry）。
// 与创建路径共用长度、字符与字面量私网限制，但不做 DNS 解析。
func ValidHotURL(u string) bool {
	if u == "" || len(u) > 2048 {
		return false
	}
	if !schemeRe.MatchString(u) {
		return false
	}
	if forbiddenChars.MatchString(u) {
		return false
	}
	return !IsPrivateLiteral(u)
}

// parseBrowserIPv4 按浏览器（WHATWG URL）规则解析 IPv4 字面量。
// 支持 1~4 段、十进制、八进制（前导 0）与十六进制（0x）写法，最后一段可跨字节，
// 用于识别 0177.0.0.1、127.1、2130706433、0x7f000001 这类非规范写法。
// 解析失败返回 nil。
func parseBrowserIPv4(host string) net.IP {
	h := strings.ToLower(host)
	if h == "" {
		return nil
	}
	parts := strings.Split(h, ".")
	// WHATWG：忽略末尾单个空段，"127.0.0.1." 等价于 "127.0.0.1"。
	if len(parts) > 1 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) == 0 || len(parts) > 4 {
		return nil
	}
	nums := make([]uint64, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			return nil
		}
		base := uint64(10)
		digits := p
		switch {
		case strings.HasPrefix(p, "0x"):
			base, digits = 16, p[2:]
			if digits == "" {
				digits = "0"
			}
		case len(p) > 1 && p[0] == '0':
			base, digits = 8, p[1:]
		}
		v, ok := parseDigits(digits, base)
		if !ok {
			return nil
		}
		nums = append(nums, v)
	}
	last := len(nums) - 1
	// 最后一段可覆盖剩余字节，上限 256^(5-n)。
	if nums[last] >= uint64(1)<<(8*uint(5-len(nums))) {
		return nil
	}
	value := nums[last]
	for i := 0; i < last; i++ {
		if nums[i] > 255 {
			return nil
		}
		value += nums[i] << (8 * uint(3-i))
	}
	if value > 0xFFFFFFFF {
		return nil
	}
	return net.IPv4(byte(value>>24), byte(value>>16), byte(value>>8), byte(value))
}

// parseDigits 解析 base 进制数字串，拒绝空串、非法字符与溢出。
func parseDigits(s string, base uint64) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	var v uint64
	for i := 0; i < len(s); i++ {
		c := s[i]
		var d uint64
		switch {
		case c >= '0' && c <= '9':
			d = uint64(c - '0')
		case c >= 'a' && c <= 'f':
			d = uint64(c-'a') + 10
		default:
			return 0, false
		}
		if d >= base {
			return 0, false
		}
		v = v*base + d
		if v > 0xFFFFFFFF {
			return 0, false
		}
	}
	return v, true
}

func hostname(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.Trim(parsed.Hostname(), "[]")
	// Match Lua's ASCII-only host policy. Browsers decode percent escapes and
	// map Unicode hostnames; require callers to supply ASCII punycode instead.
	// Userinfo, paths and queries are intentionally outside this restriction.
	for i := 0; i < len(host); i++ {
		if host[i] == '%' || host[i] <= 0x20 || host[i] >= 0x7f {
			return ""
		}
	}
	return host
}

// isLocalHostname 拦截保留的本地域名（不依赖 DNS）。
func isLocalHostname(host string) bool {
	h := strings.TrimSuffix(strings.ToLower(host), ".")
	if h == "localhost" {
		return true
	}
	for _, suffix := range []string{".localhost", ".local", ".internal", ".home.arpa"} {
		if strings.HasSuffix(h, suffix) {
			return true
		}
	}
	return false
}

// looksNumericHost 拦截浏览器会当作 IPv4 解释、但不是标准点分十进制的写法，
// 例如 2130706433、127.1、0x7f000001。Go 的 DNS 解析会失败并拒绝创建；
// 恢复路径没有 DNS，需单独拦截。
func looksNumericHost(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if strings.HasPrefix(h, "0x") {
		rest := h[2:]
		return rest != "" && strings.Trim(rest, "0123456789abcdef") == ""
	}
	dots := 0
	for i := 0; i < len(h); i++ {
		switch {
		case h[i] == '.':
			dots++
		case h[i] >= '0' && h[i] <= '9':
		default:
			return false
		}
	}
	if dots == 0 {
		return true
	}
	return net.ParseIP(h) == nil
}

func isPrivateIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	// CGNAT 100.64/10
	if ip4[0] == 100 && ip4[1]&0xC0 == 64 {
		return true
	}
	// 0.0.0.0/8
	if ip4[0] == 0 {
		return true
	}
	// 240.0.0.0/4 保留段
	if ip4[0]&0xF0 == 0xF0 {
		return true
	}
	return false
}
