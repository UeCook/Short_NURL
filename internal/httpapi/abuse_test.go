package httpapi

import (
	"net/http/httptest"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/UeCook/Short_NURL/internal/config"
)

func testGuard(t *testing.T) (*apiGuard, *time.Time) {
	t.Helper()
	cfg := config.Default().Abuse
	// 状态机测试单独覆盖封禁，不让访问频率掩盖认证失败。
	cfg.RequestsPerMinute, cfg.Burst = 100000, 100000
	cfg.GlobalRequestsPerSecond, cfg.GlobalBurst = 100000, 100000
	g := newAPIGuard(cfg)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	g.now = func() time.Time { return now }
	return g, &now
}

func enterTestClient(g *apiGuard, address string) (netip.Addr, *guardDenial) {
	r := httptest.NewRequest("GET", "/api/list", nil)
	r.RemoteAddr = address
	return g.enter(r)
}

func failTestClient(t *testing.T, g *apiGuard, address string) authWarning {
	t.Helper()
	ip, denied := enterTestClient(g, address)
	if denied != nil {
		t.Fatalf("unexpected pre-auth denial: %+v", denied)
	}
	defer g.leave(ip)
	return g.authFailure(ip)
}

func TestAuthBanWarningsEscalationAndCap(t *testing.T) {
	g, now := testGuard(t)
	const address = "192.0.2.1:4321"
	for attempt := 1; attempt < 5; attempt++ {
		warning := failTestClient(t, g, address)
		if warning.denial != nil || warning.failures != attempt || warning.remaining != 5-attempt || warning.nextBan != 15*time.Minute {
			t.Fatalf("attempt %d: %+v", attempt, warning)
		}
	}
	warning := failTestClient(t, g, address)
	if warning.denial == nil || warning.denial.retryAfter != 15*time.Minute || warning.denial.banLevel != 1 {
		t.Fatalf("fifth error must immediately block: %+v", warning)
	}
	for _, duration := range []time.Duration{30 * time.Minute, time.Hour, 2 * time.Hour, 4 * time.Hour, 8 * time.Hour, 16 * time.Hour, 24 * time.Hour, 24 * time.Hour} {
		*now = now.Add(warning.denial.retryAfter)
		warning = failTestClient(t, g, address)
		if warning.denial == nil || warning.denial.retryAfter != duration {
			t.Fatalf("one error after expiry must block for %s: %+v", duration, warning)
		}
	}
}

func TestActiveBanDoesNotExtendOrResetOnSuccess(t *testing.T) {
	g, now := testGuard(t)
	for i := 0; i < 5; i++ {
		failTestClient(t, g, "192.0.2.1:1")
	}
	ip := netip.MustParseAddr("192.0.2.1")
	deadline := g.clients[ip].bannedUntil
	*now = now.Add(time.Minute)
	_, denied := enterTestClient(g, "192.0.2.1:2")
	if denied == nil || denied.retryAfter != 14*time.Minute {
		t.Fatalf("port changes must not bypass a ban: %+v", denied)
	}
	if warning := g.authFailure(ip); warning.denial == nil || warning.denial.banLevel != 1 {
		t.Fatalf("inflight failures must not escalate an active ban: %+v", warning)
	}
	if g.authSuccess(ip) == nil || g.clients[ip].bannedUntil != deadline {
		t.Fatal("inflight success must not erase an active ban")
	}
	other, denied := enterTestClient(g, "192.0.2.2:1")
	if denied != nil {
		t.Fatalf("other source was blocked: %+v", denied)
	}
	g.leave(other)
	*now = deadline
	ip, denied = enterTestClient(g, "192.0.2.1:3")
	if denied != nil || g.authSuccess(ip) != nil {
		t.Fatalf("correct authentication after expiry must succeed: %+v", denied)
	}
	g.leave(ip)
	if warning := failTestClient(t, g, "192.0.2.1:3"); warning.denial != nil || warning.remaining != 4 {
		t.Fatalf("successful authentication must restore initial failure policy: %+v", warning)
	}
}

func TestAuthFailureWindowSlidesAndHistoryExpires(t *testing.T) {
	g, now := testGuard(t)
	failTestClient(t, g, "192.0.2.1:1")
	*now = now.Add(4 * time.Minute)
	failTestClient(t, g, "192.0.2.1:1")
	*now = now.Add(time.Minute + time.Nanosecond)
	if warning := failTestClient(t, g, "192.0.2.1:1"); warning.failures != 2 {
		t.Fatalf("old failures must expire individually: %+v", warning)
	}
	*now = now.Add(6 * time.Minute)
	for i := 0; i < 5; i++ {
		failTestClient(t, g, "192.0.2.1:1")
	}
	*now = now.Add(15*time.Minute + 24*time.Hour)
	if warning := failTestClient(t, g, "192.0.2.1:1"); warning.denial != nil || warning.remaining != 4 {
		t.Fatalf("expired escalation history must restore initial chances: %+v", warning)
	}
}

func TestAPIRateLimitsAreSharedAndRefill(t *testing.T) {
	g, now := testGuard(t)
	g.cfg.RequestsPerMinute, g.cfg.Burst = 30, 2
	for i := 0; i < 2; i++ {
		ip, denied := enterTestClient(g, "192.0.2.1:1")
		if denied != nil {
			t.Fatal(denied)
		}
		g.leave(ip)
	}
	_, denied := enterTestClient(g, "192.0.2.1:2")
	if denied == nil || denied.code != "rate_limited" || denied.retryAfter != 2*time.Second {
		t.Fatalf("expected per-IP refill wait: %+v", denied)
	}
	ip, denied := enterTestClient(g, "192.0.2.2:1")
	if denied != nil {
		t.Fatal("throttled source consumed another source's allowance")
	}
	g.leave(ip)
	*now = now.Add(2 * time.Second)
	ip, denied = enterTestClient(g, "192.0.2.1:3")
	if denied != nil {
		t.Fatal("per-IP allowance did not refill")
	}
	g.leave(ip)

	g, now = testGuard(t)
	g.cfg.GlobalRequestsPerSecond, g.cfg.GlobalBurst = 5, 2
	for _, address := range []string{"192.0.2.1:1", "192.0.2.2:1"} {
		ip, denied := enterTestClient(g, address)
		if denied != nil {
			t.Fatal(denied)
		}
		g.leave(ip)
	}
	if _, denied := enterTestClient(g, "192.0.2.3:1"); denied == nil || denied.code != "rate_limited" {
		t.Fatalf("rotating IP bypassed global protection: %+v", denied)
	}
	*now = now.Add(time.Second)
	ip, denied = enterTestClient(g, "192.0.2.3:1")
	if denied != nil {
		t.Fatal("global allowance did not refill")
	}
	g.leave(ip)
}

func TestTrackedClientsStayBoundedWithoutEvictingBans(t *testing.T) {
	g, now := testGuard(t)
	g.cfg.MaxClients = 2
	for i := 0; i < 5; i++ {
		failTestClient(t, g, "192.0.2.1:1")
	}
	ip, _ := enterTestClient(g, "192.0.2.2:1")
	g.leave(ip)
	if _, denied := enterTestClient(g, "192.0.2.3:1"); denied == nil || len(g.clients) != 2 {
		t.Fatal("tracking table overflowed")
	}
	if _, denied := enterTestClient(g, "192.0.2.1:1"); denied == nil || denied.code != "auth_blocked" {
		t.Fatal("capacity pressure evicted an active ban")
	}
	*now = now.Add(25 * time.Hour)
	ip, denied := enterTestClient(g, "192.0.2.3:1")
	if denied != nil || len(g.clients) != 1 {
		t.Fatalf("expired tracking entries did not clean up: %+v", denied)
	}
	g.leave(ip)
}

func TestCleanupDoesNotResetPartiallyRefilledBucket(t *testing.T) {
	g, now := testGuard(t)
	g.cfg.RequestsPerMinute, g.cfg.Burst = 1, 10
	for i := 0; i < 10; i++ {
		ip, denied := enterTestClient(g, "192.0.2.1:1")
		if denied != nil {
			t.Fatal(denied)
		}
		g.leave(ip)
	}
	*now = now.Add(6 * time.Minute) // 超过失败窗口，但只补回 6 个令牌。
	for i := 0; i < 6; i++ {
		ip, denied := enterTestClient(g, "192.0.2.1:1")
		if denied != nil {
			t.Fatal(denied)
		}
		g.leave(ip)
	}
	if _, denied := enterTestClient(g, "192.0.2.1:1"); denied == nil || denied.retryAfter != time.Minute {
		t.Fatalf("cleanup granted a fresh burst before refill completed: %+v", denied)
	}
}

func TestTrustedProxyCannotBeSpoofedByDirectClient(t *testing.T) {
	g, _ := testGuard(t)
	for _, tc := range []struct{ peer, header, want string }{
		{"192.0.2.1:1", "198.51.100.1", "192.0.2.1"},
		{"127.0.0.1:1", "198.51.100.1", "198.51.100.1"},
		{"[::1]:1", "2001:db8::1", "2001:db8::1"},
		{"127.0.0.1:1", "::ffff:192.0.2.1", "192.0.2.1"},
		{"127.0.0.1:1", "192.0.2.1, 192.0.2.2", "127.0.0.1"},
		{"127.0.0.1:1", "0.0.0.0", "127.0.0.1"},
	} {
		r := httptest.NewRequest("GET", "/api/list", nil)
		r.RemoteAddr = tc.peer
		r.Header.Set("X-Real-IP", tc.header)
		r.Header.Set("X-Forwarded-For", "203.0.113.1")
		r.Header.Set("CF-Connecting-IP", "203.0.113.2")
		if got := g.clientIP(r).String(); got != tc.want {
			t.Errorf("peer %s, header %s: got %s, want %s", tc.peer, tc.header, got, tc.want)
		}
	}
	r := httptest.NewRequest("GET", "/api/list", nil)
	r.RemoteAddr = "127.0.0.1:1"
	r.Header.Add("X-Real-IP", "192.0.2.1")
	r.Header.Add("X-Real-IP", "192.0.2.2")
	if got := g.clientIP(r).String(); got != "127.0.0.1" {
		t.Fatalf("multiple real-IP values were trusted: %s", got)
	}
}

func TestConcurrentFailuresOnlyTriggerOneBan(t *testing.T) {
	g, _ := testGuard(t)
	start := make(chan struct{})
	var wg sync.WaitGroup
	var bans atomic.Int32
	for i := 0; i < 8; i++ {
		ip, denied := enterTestClient(g, "192.0.2.1:1")
		if denied != nil {
			t.Fatal(denied)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer g.leave(ip)
			<-start
			if warning := g.authFailure(ip); warning.denial != nil && warning.denial.newBan {
				bans.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if bans.Load() != 1 || g.clients[netip.MustParseAddr("192.0.2.1")].banLevel != 1 || g.active != 0 {
		t.Fatal("concurrent failures escalated or leaked an active slot")
	}
}

func TestConcurrencyLimitDoesNotAllocateRejectedClients(t *testing.T) {
	g, _ := testGuard(t)
	g.cfg.MaxConcurrent = 2
	a, _ := enterTestClient(g, "192.0.2.1:1")
	b, _ := enterTestClient(g, "192.0.2.2:1")
	if _, denied := enterTestClient(g, "192.0.2.3:1"); denied == nil || denied.code != "api_busy" || len(g.clients) != 2 {
		t.Fatal("concurrency rejection must not allocate another client")
	}
	g.leave(a)
	c, denied := enterTestClient(g, "192.0.2.3:1")
	if denied != nil {
		t.Fatal("slot not released")
	}
	g.leave(b)
	g.leave(c)
}
