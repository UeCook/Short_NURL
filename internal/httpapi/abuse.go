package httpapi

import (
	"fmt"
	"math"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/UeCook/Short_NURL/internal/config"
)

type apiBucket struct {
	tokens float64
	at     time.Time
}

func (b *apiBucket) refill(now time.Time, rate float64, burst int) {
	if b.at.IsZero() {
		b.tokens = float64(burst)
	} else if now.After(b.at) {
		b.tokens = math.Min(float64(burst), b.tokens+now.Sub(b.at).Seconds()*rate)
	}
	b.at = now
}

func (b *apiBucket) wait(rate float64) time.Duration {
	return time.Duration(math.Ceil((1 - b.tokens) / rate * float64(time.Second)))
}

type apiClient struct {
	bucket       apiBucket
	failures     []time.Time
	banLevel     int
	bannedUntil  time.Time
	historyUntil time.Time
	lastSeen     time.Time
	inflight     int
}

type guardDenial struct {
	code       string
	retryAfter time.Duration
	banLevel   int
	newBan     bool
}

type authWarning struct {
	failures, remaining int
	nextBan             time.Duration
	denial              *guardDenial
}

// apiGuard 只保存有界的进程内状态，不写业务文件、不创建每 IP 后台任务。
type apiGuard struct {
	mu        sync.Mutex
	cfg       config.Abuse
	trusted   []netip.Prefix
	clients   map[netip.Addr]*apiClient
	global    apiBucket
	active    int
	nextSweep time.Time
	now       func() time.Time
	initErr   error
}

func newAPIGuard(cfg config.Abuse) *apiGuard {
	if !cfg.Enabled {
		return nil
	}
	g := &apiGuard{cfg: cfg, clients: make(map[netip.Addr]*apiClient), now: time.Now, initErr: cfg.Validate()}
	if g.initErr == nil {
		for _, raw := range cfg.TrustedProxies {
			prefix, _ := netip.ParsePrefix(raw)
			g.trusted = append(g.trusted, prefix.Masked())
		}
	}
	return g
}

func (g *apiGuard) clientIP(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, _ := netip.ParseAddr(host)
	peer = peer.Unmap()
	for _, prefix := range g.trusted {
		if !prefix.Contains(peer) {
			continue
		}
		// 代理必须覆盖 X-Real-IP。忽略可追加/伪造的 X-Forwarded-For 链。
		values := r.Header.Values("X-Real-IP")
		if len(values) == 1 {
			if ip, err := netip.ParseAddr(strings.TrimSpace(values[0])); err == nil && !ip.IsUnspecified() && !ip.IsMulticast() {
				return ip.Unmap()
			}
		}
		break
	}
	return peer
}

// enter 在读取请求体、访问业务文件和预约一次性 Key 之前执行。
func (g *apiGuard) enter(r *http.Request) (netip.Addr, *guardDenial) {
	ip := g.clientIP(r)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.initErr != nil {
		return ip, &guardDenial{code: "abuse_unavailable", retryAfter: time.Second}
	}
	now := g.now()
	g.sweep(now)
	c := g.clients[ip]
	if c != nil && now.Before(c.bannedUntil) {
		return ip, blockedClient(c, now)
	}
	g.global.refill(now, float64(g.cfg.GlobalRequestsPerSecond), g.cfg.GlobalBurst)
	if g.global.tokens < 1 {
		return ip, &guardDenial{code: "rate_limited", retryAfter: g.global.wait(float64(g.cfg.GlobalRequestsPerSecond))}
	}
	if g.active >= g.cfg.MaxConcurrent {
		return ip, &guardDenial{code: "api_busy", retryAfter: time.Second}
	}
	if c == nil {
		if len(g.clients) >= g.cfg.MaxClients {
			// 不驱逐有效封禁；表满时拒绝新来源，防止换 IP 撑爆内存。
			return ip, &guardDenial{code: "rate_limited", retryAfter: g.nextSweep.Sub(now)}
		}
		c = &apiClient{}
		g.clients[ip] = c
	}
	c.lastSeen = now
	rate := float64(g.cfg.RequestsPerMinute) / 60
	c.bucket.refill(now, rate, g.cfg.Burst)
	if c.bucket.tokens < 1 {
		return ip, &guardDenial{code: "rate_limited", retryAfter: c.bucket.wait(rate)}
	}
	// 仅获准进入业务处理的请求消耗令牌，已封禁/被限流来源不能耗尽其他人的额度。
	g.global.tokens--
	c.bucket.tokens--
	c.inflight++
	g.active++
	return ip, nil
}

func (g *apiGuard) leave(ip netip.Addr) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.active--
	g.clients[ip].inflight-- // sweep 不移除仍有在途请求的来源。
}

func (g *apiGuard) sweep(now time.Time) {
	if now.Before(g.nextSweep) {
		return
	}
	g.nextSweep = now.Add(time.Minute)
	idle := max(time.Minute, time.Duration(g.cfg.FailureWindowSeconds)*time.Second)
	// 自定义大突发/低频率时，不能在令牌桶尚未补满前清理并重新送出整桶额度。
	refillTime := time.Duration(math.Ceil(float64(g.cfg.Burst) * 60 / float64(g.cfg.RequestsPerMinute) * float64(time.Second)))
	idle = max(idle, refillTime)
	for ip, c := range g.clients {
		if c.inflight == 0 && !now.Before(c.bannedUntil) && now.Sub(c.lastSeen) >= idle && (c.banLevel == 0 || !now.Before(c.historyUntil)) {
			delete(g.clients, ip)
		}
	}
}

func blockedClient(c *apiClient, now time.Time) *guardDenial {
	return &guardDenial{code: "auth_blocked", retryAfter: c.bannedUntil.Sub(now), banLevel: c.banLevel}
}

func (g *apiGuard) banDuration(level int) time.Duration {
	duration := time.Duration(g.cfg.BanSeconds) * time.Second
	maximum := time.Duration(g.cfg.MaxBanSeconds) * time.Second
	for i := 1; i < level && duration < maximum; i++ {
		if duration > maximum/2 {
			return maximum
		}
		duration *= 2
	}
	return min(duration, maximum)
}

func (g *apiGuard) authFailure(ip netip.Addr) authWarning {
	if g == nil {
		return authWarning{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	c := g.clients[ip]
	now := g.now()
	if now.Before(c.bannedUntil) {
		return authWarning{denial: blockedClient(c, now)}
	}
	c.lastSeen = now
	if c.banLevel > 0 && !now.Before(c.historyUntil) {
		c.banLevel, c.failures = 0, nil
	}
	cutoff := now.Add(-time.Duration(g.cfg.FailureWindowSeconds) * time.Second)
	first := 0
	for first < len(c.failures) && !c.failures[first].After(cutoff) {
		first++
	}
	c.failures = c.failures[:copy(c.failures, c.failures[first:])]
	c.failures = append(c.failures, now)
	if c.banLevel == 0 && len(c.failures) < g.cfg.MaxAuthFailures {
		return authWarning{failures: len(c.failures), remaining: g.cfg.MaxAuthFailures - len(c.failures), nextBan: g.banDuration(1)}
	}
	// 解封后再错一次即升档；并行的失败响应在封禁期间不重复升档。
	if c.banLevel == 0 || g.banDuration(c.banLevel) < time.Duration(g.cfg.MaxBanSeconds)*time.Second {
		c.banLevel++
	}
	c.bannedUntil = now.Add(g.banDuration(c.banLevel))
	c.historyUntil = c.bannedUntil.Add(time.Duration(g.cfg.HistorySeconds) * time.Second)
	c.failures = nil
	denial := blockedClient(c, now)
	denial.newBan = true
	return authWarning{denial: denial}
}

func (g *apiGuard) authSuccess(ip netip.Addr) *guardDenial {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	c := g.clients[ip]
	if now := g.now(); now.Before(c.bannedUntil) {
		// 已在途的正确密钥也不能撤销刚触发的封禁。
		return blockedClient(c, now)
	}
	c.failures = nil
	c.banLevel = 0
	c.bannedUntil, c.historyUntil = time.Time{}, time.Time{}
	return nil
}

func waitText(wait time.Duration) string {
	minutes := max(1, int(math.Ceil(wait.Minutes())))
	if minutes%60 == 0 {
		return fmt.Sprintf("%d 小时", minutes/60)
	}
	return fmt.Sprintf("%d 分钟", minutes)
}

func (st *requestState) writeGuardDenial(w http.ResponseWriter, denial *guardDenial) {
	seconds := max(1, int(math.Ceil(denial.retryAfter.Seconds())))
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	status := http.StatusTooManyRequests
	message := fmt.Sprintf("请求过于频繁，请在 %d 秒后重试", seconds)
	switch denial.code {
	case "auth_blocked":
		maximum := time.Duration(st.server.guard.cfg.MaxBanSeconds) * time.Second
		next := "解封后再输错一次将升级封禁"
		if st.server.guard.banDuration(denial.banLevel) >= maximum {
			next = "已达封禁时长上限，解封后再输错将再次封禁"
		}
		message = fmt.Sprintf("请求来源已封禁，请在约 %s后重试；%s（最高 %s）", waitText(denial.retryAfter), next, waitText(maximum))
		if denial.newBan {
			st.server.Log.Warn("[abuse] 密钥验证失败，已封禁来源", "ip", st.clientIP.String(), "level", denial.banLevel, "seconds", seconds)
		}
	case "api_busy":
		message = "API 正忙，请稍后重试"
	case "abuse_unavailable":
		status, message = http.StatusServiceUnavailable, "API 防护配置无效，暂时拒绝请求"
	}
	writeError(w, st.channel, status, denial.code, message)
}

func (st *requestState) writeInvalidKey(w http.ResponseWriter) {
	warning := st.server.guard.authFailure(st.clientIP)
	if warning.denial != nil {
		st.writeGuardDenial(w, warning.denial)
		return
	}
	message := "密钥失效或不正确"
	if warning.failures > 0 {
		window := waitText(time.Duration(st.server.guard.cfg.FailureWindowSeconds) * time.Second)
		message += fmt.Sprintf("。%s内第 %d 次失败，还可尝试 %d 次", window, warning.failures, warning.remaining)
		if warning.remaining == 1 {
			message += fmt.Sprintf("；再错一次将封禁 %s", waitText(warning.nextBan))
		}
	}
	writeError(w, st.channel, http.StatusUnauthorized, "invalid_key", message)
}

func (st *requestState) acceptAuthenticated(w http.ResponseWriter) bool {
	if denial := st.server.guard.authSuccess(st.clientIP); denial != nil {
		st.completeSilently(false)
		st.writeGuardDenial(w, denial)
		return false
	}
	return true
}
