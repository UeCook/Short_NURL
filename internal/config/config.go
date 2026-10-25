// Package config 加载 config/config.json（JSONC）、校验并解析路径。
//
// 优先级：内置默认值 < 配置文件 < 环境变量 < 命令行参数。
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/UeCook/Short_NURL/internal/isoutil"
)

// Server 服务端配置。
type Server struct {
	Listen               string `json:"listen"`
	MaxBodyBytes         int64  `json:"max_body_bytes"`
	ReadHeaderTimeoutSec int    `json:"read_header_timeout_seconds"`
	ReadTimeoutSec       int    `json:"read_timeout_seconds"`
	WriteTimeoutSec      int    `json:"write_timeout_seconds"`
	IdleTimeoutSec       int    `json:"idle_timeout_seconds"`
	ShutdownTimeoutSec   int    `json:"shutdown_timeout_seconds"`
}

// Site 站点配置。
type Site struct {
	Domain       string `json:"domain"`
	PanelOrigin  string `json:"panel_origin"`
	PanelEnabled bool   `json:"panel_enabled"`
	TzOffset     string `json:"tz_offset"`
}

// Storage 持久化路径配置。
type Storage struct {
	BaseDir           string `json:"base_dir"`
	PermPath          string `json:"perm_path"`
	TempPath          string `json:"temp_path"`
	KeysPath          string `json:"keys_path"`
	InternalTokenPath string `json:"internal_token_path"`
}

// Keys 密钥配置。
type Keys struct {
	TTLDays         int `json:"ttl_days"`
	OnetimePoolSize int `json:"onetime_pool_size"`
}

// Quota 配额配置。
type Quota struct {
	PermLimit    int `json:"perm_limit"`
	TempLimit    int `json:"temp_limit"`
	SvcPermLimit int `json:"svc_perm_limit"`
	SvcTempLimit int `json:"svc_temp_limit"`
}

// Codes 短码与 TTL 规则。
type Codes struct {
	SvcCodeLength int      `json:"svc_code_length"`
	TTLMax        int      `json:"ttl_max"`
	ReservedCodes []string `json:"reserved_codes"`
}

// Internal PHP↔Lua 内部接口配置。
type Internal struct {
	Host       string  `json:"host"`
	TimeoutSec float64 `json:"timeout_seconds"`
	MaxRetries int     `json:"max_retries"`
	BackoffMS  int     `json:"backoff_ms"`
}

// Policy 热存储故障策略。
type Policy struct {
	ColdFallback bool `json:"cold_fallback"`
}

// Log 日志配置。
type Log struct {
	Level     string `json:"level"`
	Format    string `json:"format"`
	AccessLog bool   `json:"access_log"`
}

// Config 完整配置。
type Config struct {
	Server   Server   `json:"server"`
	Site     Site     `json:"site"`
	Storage  Storage  `json:"storage"`
	Keys     Keys     `json:"keys"`
	Quota    Quota    `json:"quota"`
	Codes    Codes    `json:"codes"`
	Internal Internal `json:"internal"`
	Policy   Policy   `json:"policy"`
	Log      Log      `json:"log"`

	// 解析后的绝对路径。
	PermPath          string `json:"-"`
	TempPath          string `json:"-"`
	KeysPath          string `json:"-"`
	InternalTokenPath string `json:"-"`
	ConfigPath        string `json:"-"`
	ConfigDir         string `json:"-"`
}

// Default 返回内置默认值（与 v2.0.3 core/config.php 一致）。
func Default() *Config {
	return &Config{
		Server: Server{
			Listen:               "127.0.0.1:8080",
			MaxBodyBytes:         1048576,
			ReadHeaderTimeoutSec: 5,
			ReadTimeoutSec:       10,
			WriteTimeoutSec:      15,
			IdleTimeoutSec:       60,
			ShutdownTimeoutSec:   10,
		},
		Site: Site{
			Domain:       "{你的域名}",
			PanelOrigin:  "",
			PanelEnabled: true,
			TzOffset:     "+08:00",
		},
		Storage: Storage{
			BaseDir:           "..",
			PermPath:          "backend/data/perm.json",
			TempPath:          "backend/data/temp.json",
			KeysPath:          "backend/data/keys.json",
			InternalTokenPath: "backend/data/internal_token",
		},
		Keys: Keys{TTLDays: 7, OnetimePoolSize: 20},
		Quota: Quota{
			PermLimit:    10000,
			TempLimit:    500,
			SvcPermLimit: 10000,
			SvcTempLimit: 500,
		},
		Codes: Codes{
			SvcCodeLength: 4,
			TTLMax:        31536000,
			ReservedCodes: []string{"api", "stat", "admin", "data", "lua", "headless", "internal", "nurl"},
		},
		Internal: Internal{
			Host:       "http://127.0.0.1:18500",
			TimeoutSec: 2.0,
			MaxRetries: 3,
			BackoffMS:  100,
		},
		Policy: Policy{ColdFallback: false},
		Log:    Log{Level: "info", Format: "text", AccessLog: false},
	}
}

// FindConfigFile 定位配置文件：-config > NURL_CONFIG > ./config/config.json > <exe>/../config/config.json。
func FindConfigFile(flagPath string) (string, error) {
	if flagPath != "" {
		return flagPath, nil
	}
	if env := os.Getenv("NURL_CONFIG"); env != "" {
		return env, nil
	}
	if _, err := os.Stat("config/config.json"); err == nil {
		return "config/config.json", nil
	}
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), "..", "config", "config.json")
		if _, err := os.Stat(cand); err == nil {
			return cand, nil
		}
	}
	return "config/config.json", nil
}

// Load 加载并校验配置。
func Load(path string) (*Config, error) {
	cfg := Default()

	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	cfg.ConfigPath = abs
	cfg.ConfigDir = filepath.Dir(abs)

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("无法读取配置文件: %s", path)
	}
	stripped, err := StripJSONC(raw)
	if err != nil {
		return nil, fmt.Errorf("配置文件语法错误: %w", err)
	}
	if err := UnmarshalWithDefaults(stripped, cfg); err != nil {
		return nil, fmt.Errorf("配置文件解析失败: %w", err)
	}

	applyEnv(cfg)

	if err := cfg.resolvePaths(); err != nil {
		return nil, err
	}
	if err := ValidateListen(cfg.Server.Listen); err != nil {
		return nil, err
	}
	return cfg, nil
}

// ValidateListen 校验监听地址。
func ValidateListen(listen string) error {
	if strings.TrimSpace(listen) == "" {
		return errors.New("server.listen 不能为空")
	}
	if _, _, err := net.SplitHostPort(listen); err != nil {
		return fmt.Errorf("server.listen 非法: %s", listen)
	}
	return nil
}

// Warnings 返回启动期告警（时区、配额、domain 占位符、监听范围）。
func (c *Config) Warnings() []string {
	var out []string
	if err := isoutil.NormalizeOffset(c.Site.TzOffset); err != nil {
		out = append(out, "时区配置无效: "+err.Error())
	}
	if c.Quota.PermLimit+c.Quota.TempLimit+c.Quota.SvcPermLimit+c.Quota.SvcTempLimit > 30000 {
		out = append(out, "配置的总容量超过 30000")
	}
	if strings.Contains(c.Site.Domain, "{") {
		out = append(out, "site.domain 仍含占位符，创建接口将返回 config_error")
	}
	if !isLoopbackListen(c.Server.Listen) {
		out = append(out, "server.listen 未绑定回环地址；请确认只有受信任的反向代理可访问，"+
			"且公网入口已启用 HTTPS")
	}
	return out
}

// isLoopbackListen 判断监听地址是否只绑定回环。
func isLoopbackListen(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// QuotaTotal 四项配额之和。
func (c *Config) QuotaTotal() int {
	return c.Quota.PermLimit + c.Quota.TempLimit + c.Quota.SvcPermLimit + c.Quota.SvcTempLimit
}

func (c *Config) resolvePaths() error {
	base := c.Storage.BaseDir
	if base == "" {
		base = ".."
	}
	if !filepath.IsAbs(base) {
		base = filepath.Join(c.ConfigDir, base)
	}
	base = filepath.Clean(base)

	resolve := func(p string) string {
		if p == "" {
			return ""
		}
		if filepath.IsAbs(p) {
			return filepath.Clean(p)
		}
		return filepath.Clean(filepath.Join(base, p))
	}
	c.PermPath = resolve(c.Storage.PermPath)
	c.TempPath = resolve(c.Storage.TempPath)
	c.KeysPath = resolve(c.Storage.KeysPath)
	c.InternalTokenPath = resolve(c.Storage.InternalTokenPath)
	return nil
}

func applyEnv(c *Config) {
	setStr := func(dst *string, key string) {
		if v, ok := os.LookupEnv(key); ok && v != "" {
			*dst = v
		}
	}
	setStr(&c.Server.Listen, "NURL_LISTEN")
	setStr(&c.Site.Domain, "NURL_DOMAIN")
	setStr(&c.Site.PanelOrigin, "NURL_PANEL_ORIGIN")
	setStr(&c.Site.TzOffset, "NURL_TZ_OFFSET")
	setStr(&c.Storage.PermPath, "NURL_PERM_PATH")
	setStr(&c.Storage.TempPath, "NURL_TEMP_PATH")
	setStr(&c.Storage.KeysPath, "NURL_KEYS_PATH")
	setStr(&c.Storage.InternalTokenPath, "NURL_INTERNAL_TOKEN_PATH")
	setStr(&c.Internal.Host, "NURL_INTERNAL_HOST")
	setStr(&c.Log.Level, "NURL_LOG_LEVEL")
	setStr(&c.Log.Format, "NURL_LOG_FORMAT")

	if v, ok := os.LookupEnv("NURL_PANEL_ENABLED"); ok && v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.Site.PanelEnabled = b
		}
	}
	if v, ok := os.LookupEnv("NURL_COLD_FALLBACK"); ok && v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.Policy.ColdFallback = b
		}
	}
	if v, ok := os.LookupEnv("NURL_INTERNAL_TIMEOUT"); ok && v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.Internal.TimeoutSec = f
		}
	}
}

// CheckFileAccess 对齐 PHP 的 checkFileAccess()。
func CheckFileAccess(path string) bool {
	if path == "" {
		return false
	}
	fi, err := os.Stat(path)
	if err == nil {
		if fi.IsDir() {
			return false
		}
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			return false
		}
		_ = f.Close()
		return true
	}
	if !os.IsNotExist(err) {
		return false
	}
	dir := filepath.Dir(path)
	di, err := os.Stat(dir)
	if err != nil || !di.IsDir() {
		return false
	}
	return probeWritable(dir)
}

func probeWritable(dir string) bool {
	tmp := filepath.Join(dir, ".nurl-write-probe")
	f, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false
	}
	_ = f.Close()
	_ = os.Remove(tmp)
	return true
}
