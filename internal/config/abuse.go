package config

import (
	"fmt"
	"net/netip"
)

// Abuse 是单实例、进程内的 API 防刷配置，不依赖外部封禁服务。
type Abuse struct {
	Enabled                 bool     `json:"enabled"`
	RequestsPerMinute       int      `json:"requests_per_minute"`
	Burst                   int      `json:"burst"`
	GlobalRequestsPerSecond int      `json:"global_requests_per_second"`
	GlobalBurst             int      `json:"global_burst"`
	MaxConcurrent           int      `json:"max_concurrent"`
	FailureWindowSeconds    int      `json:"failure_window_seconds"`
	MaxAuthFailures         int      `json:"max_auth_failures"`
	BanSeconds              int      `json:"ban_seconds"`
	MaxBanSeconds           int      `json:"max_ban_seconds"`
	HistorySeconds          int      `json:"history_seconds"`
	MaxClients              int      `json:"max_clients"`
	TrustedProxies          []string `json:"trusted_proxies"`
}

func defaultAbuse() Abuse {
	return Abuse{
		Enabled: true, RequestsPerMinute: 30, Burst: 5,
		GlobalRequestsPerSecond: 5, GlobalBurst: 10, MaxConcurrent: 8,
		FailureWindowSeconds: 300, MaxAuthFailures: 5,
		BanSeconds: 900, MaxBanSeconds: 86400, HistorySeconds: 86400,
		MaxClients: 10000, TrustedProxies: []string{"127.0.0.1/32", "::1/128"},
	}
}

// Validate 拒绝无效或可能导致无界分配/时长溢出的防护参数。
func (a Abuse) Validate() error {
	if !a.Enabled {
		return nil
	}
	fields := []struct {
		name       string
		value, max int
	}{
		{"requests_per_minute", a.RequestsPerMinute, 1000000},
		{"burst", a.Burst, 1000000},
		{"global_requests_per_second", a.GlobalRequestsPerSecond, 1000000},
		{"global_burst", a.GlobalBurst, 1000000},
		{"max_concurrent", a.MaxConcurrent, 10000},
		{"failure_window_seconds", a.FailureWindowSeconds, 86400},
		{"max_auth_failures", a.MaxAuthFailures, 100},
		{"ban_seconds", a.BanSeconds, 604800},
		{"max_ban_seconds", a.MaxBanSeconds, 604800},
		{"history_seconds", a.HistorySeconds, 2592000},
		{"max_clients", a.MaxClients, 1000000},
	}
	for _, field := range fields {
		if field.value < 1 || field.value > field.max {
			return fmt.Errorf("abuse.%s 必须介于 1 和 %d 之间", field.name, field.max)
		}
	}
	if a.MaxBanSeconds < a.BanSeconds {
		return fmt.Errorf("abuse.max_ban_seconds 不能小于 abuse.ban_seconds")
	}
	for _, raw := range a.TrustedProxies {
		if _, err := netip.ParsePrefix(raw); err != nil {
			return fmt.Errorf("abuse.trusted_proxies 必须为 IP/CIDR 地址段，非法值: %q", raw)
		}
	}
	return nil
}
