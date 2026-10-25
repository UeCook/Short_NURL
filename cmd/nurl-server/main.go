// nurl-server — Short_NURL v3 API 服务（Go）。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/UeCook/Short_NURL/internal/apikey"
	"github.com/UeCook/Short_NURL/internal/auth"
	"github.com/UeCook/Short_NURL/internal/config"
	"github.com/UeCook/Short_NURL/internal/hot"
	"github.com/UeCook/Short_NURL/internal/httpapi"
	"github.com/UeCook/Short_NURL/internal/isoutil"
	"github.com/UeCook/Short_NURL/internal/link"
	"github.com/UeCook/Short_NURL/internal/store"
)

// version 由构建脚本通过 -ldflags -X 注入。
var version = "3.0.1_GitHub"

func main() {
	var (
		configPath string
		check      bool
		verifyData bool
		showVer    bool
	)
	flag.StringVar(&configPath, "config", "", "配置文件路径（默认 config/config.json）")
	flag.BoolVar(&check, "check", false, "自检后退出")
	flag.BoolVar(&verifyData, "verify-data", false, "按 Lua 冷启动规则校验冷存储后退出")
	flag.BoolVar(&showVer, "version", false, "显示版本")
	flag.Parse()

	if showVer {
		fmt.Println(version)
		return
	}

	path, err := config.FindConfigFile(configPath)
	if err != nil {
		fail(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		fail(err)
	}

	if check {
		os.Exit(runCheck(cfg))
	}
	if verifyData {
		os.Exit(runVerifyData(cfg))
	}

	logger := newLogger(cfg)
	// 让 internal/* 里的 slog.Warn/Error 也走同一 handler，
	// 否则它们会落到默认 logger（格式与 log.level/format 配置不一致）。
	slog.SetDefault(logger)
	for _, w := range cfg.Warnings() {
		logger.Warn("[config]", "warning", w)
	}
	warnLooseFileModes(logger, cfg)
	if !config.CheckFileAccess(cfg.InternalTokenPath) {
		logger.Warn("[config] internal_token 不可读", "path", cfg.InternalTokenPath)
	}

	keys := apikey.New(cfg.KeysPath, cfg.Site.TzOffset, cfg.Keys.TTLDays, cfg.Keys.OnetimePoolSize)
	authSvc := auth.New(keys)
	hotClient := hot.New(
		cfg.Internal.Host,
		cfg.InternalTokenPath,
		time.Duration(cfg.Internal.TimeoutSec*float64(time.Second)),
		cfg.Internal.MaxRetries,
		cfg.Internal.BackoffMS,
	)
	links := link.NewServices(cfg, hotClient)

	srv := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           httpapi.NewServer(cfg, keys, authSvc, links, logger),
		ReadHeaderTimeout: time.Duration(cfg.Server.ReadHeaderTimeoutSec) * time.Second,
		ReadTimeout:       time.Duration(cfg.Server.ReadTimeoutSec) * time.Second,
		WriteTimeout:      time.Duration(cfg.Server.WriteTimeoutSec) * time.Second,
		IdleTimeout:       time.Duration(cfg.Server.IdleTimeoutSec) * time.Second,
	}

	logger.Info("nurl-server 启动",
		"version", version,
		"listen", cfg.Server.Listen,
		"perm_path", cfg.PermPath,
		"temp_path", cfg.TempPath,
		"keys_path", cfg.KeysPath,
		"cold_fallback", cfg.Policy.ColdFallback,
	)

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		fail(err)
	case sig := <-sigCh:
		logger.Info("收到退出信号，开始优雅停机", "signal", sig.String())
	}

	ctx, cancel := context.WithTimeout(context.Background(),
		time.Duration(cfg.Server.ShutdownTimeoutSec)*time.Second)
	defer cancel()
	inflight := 0
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("优雅停机失败", "err", err)
	}
	logger.Info("已退出", "inflight", inflight)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "错误："+err.Error())
	os.Exit(1)
}

// warnLooseFileModes 提示敏感文件权限过宽。Windows 的 os.FileMode 不表达 ACL，
// 因此只在非 Windows 平台检查。
func warnLooseFileModes(logger *slog.Logger, cfg *config.Config) {
	if runtime.GOOS == "windows" {
		return
	}
	for _, p := range []string{cfg.KeysPath, cfg.InternalTokenPath} {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		if perm := fi.Mode().Perm(); perm&0o077 != 0 {
			logger.Warn("[config] 敏感文件权限过宽，建议 0600", "path", p, "mode", perm.String())
		}
	}
}

func newLogger(cfg *config.Config) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(cfg.Log.Level) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if strings.ToLower(cfg.Log.Format) == "json" {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		handler = slog.NewTextHandler(os.Stderr, opts)
	}
	return slog.New(handler)
}

// runCheck 校验配置、路径、令牌与时区。
func runCheck(cfg *config.Config) int {
	ok := true
	report := func(good bool, format string, args ...any) {
		mark := "[ok]  "
		if !good {
			mark = "[fail]"
			ok = false
		}
		fmt.Printf(mark+" "+format+"\n", args...)
	}

	report(true, "配置文件：%s", cfg.ConfigPath)
	report(config.ValidateListen(cfg.Server.Listen) == nil, "监听地址：%s", cfg.Server.Listen)
	report(isoutil.NormalizeOffset(cfg.Site.TzOffset) == nil, "时区：%s", cfg.Site.TzOffset)
	report(!strings.Contains(cfg.Site.Domain, "{"), "域名：%s", cfg.Site.Domain)
	report(cfg.QuotaTotal() <= 30000, "配额合计：%d（上限 30000）", cfg.QuotaTotal())

	for _, p := range []string{cfg.PermPath, cfg.TempPath, cfg.KeysPath} {
		report(config.CheckFileAccess(p), "数据文件可读写：%s", p)
	}
	if raw, err := os.ReadFile(cfg.InternalTokenPath); err != nil {
		fmt.Printf("[warn] 内部令牌暂不可读：%s（可稍后用 nurl -itk 生成）\n", cfg.InternalTokenPath)
	} else if len(strings.TrimSpace(string(raw))) == 0 {
		fmt.Printf("[warn] 内部令牌为空：%s（可稍后用 nurl -itk 生成）\n", cfg.InternalTokenPath)
	} else {
		report(true, "内部令牌可读：%s", cfg.InternalTokenPath)
	}

	if ok {
		fmt.Println("自检通过。")
		return 0
	}
	fmt.Println("自检未通过。")
	return 1
}

// runVerifyData 按 init.lua 的加载规则校验冷存储。
func runVerifyData(cfg *config.Config) int {
	failed := false

	permData, err := readStore(cfg.PermPath)
	if err != nil {
		fmt.Printf("perm.json: 读取失败（%s）\n", err)
		failed = true
	} else {
		total, loadable := verifyEntries(permData, false)
		skipped := total - loadable
		fmt.Printf("perm.json: %d entries, %d loadable, %d skipped\n", total, loadable, skipped)
		if skipped > 0 {
			failed = true
		}
	}

	tempData, err := readStore(cfg.TempPath)
	if err != nil {
		fmt.Printf("temp.json: 读取失败（%s）\n", err)
		failed = true
	} else {
		total, loadable, expired, skipped := verifyTemp(tempData)
		line := fmt.Sprintf("temp.json: %d entries, %d loadable, %d expired", total, loadable, expired)
		if skipped > 0 {
			line += fmt.Sprintf(", %d skipped", skipped)
		}
		fmt.Println(line)
		if loadable != total {
			failed = true
		}
	}

	keys := apikey.New(cfg.KeysPath, cfg.Site.TzOffset, cfg.Keys.TTLDays, cfg.Keys.OnetimePoolSize)
	if st, err := keys.Status(); err != nil {
		fmt.Printf("keys.json: 读取失败（%s）\n", err)
		failed = true
	} else {
		resident, service := "no", "no"
		if st.Resident != nil {
			resident = "yes"
		}
		if st.Service != nil {
			service = "yes"
		}
		fmt.Printf("keys.json: ok (resident=%s, service=%s, pool=%d/%d)\n",
			resident, service, st.OnetimeCount, st.OnetimePoolSize)
	}

	if raw, err := os.ReadFile(cfg.InternalTokenPath); err != nil {
		fmt.Printf("internal_token: 不可读（%s）\n", err)
		failed = true
	} else {
		fmt.Printf("internal_token: readable (%d chars)\n", len(strings.TrimSpace(string(raw))))
	}

	if failed {
		return 1
	}
	return 0
}

func readStore(path string) (store.Data, error) {
	st := store.New(path, "+00:00")
	return st.Read()
}

// verifyEntries 复刻 init.lua 的 store_entry 校验（permanent）。
func verifyEntries(d store.Data, temp bool) (total, loadable int) {
	total = len(d)
	for code, v := range d {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		urlStr, ok := store.FieldString(m, "url")
		if !ok {
			continue
		}
		if luaStoreEntry(code, urlStr) {
			loadable++
		}
	}
	return total, loadable
}

// verifyTemp 复刻 init.lua 对临时条目的加载规则。
func verifyTemp(d store.Data) (total, loadable, expired, skipped int) {
	total = len(d)
	now := time.Now().Unix()
	for code, v := range d {
		m, ok := v.(map[string]any)
		if !ok {
			skipped++
			continue
		}
		urlStr, ok := store.FieldString(m, "url")
		if !ok {
			skipped++
			continue
		}
		t, isStr := store.FieldString(m, "t")
		if !isStr || t == "0" {
			expired++
			continue
		}
		expEpoch, ok := isoutil.ParseStrict(t)
		if !ok || expEpoch-now <= 0 {
			expired++
			continue
		}
		if luaStoreEntry(code, urlStr) {
			loadable++
		} else {
			skipped++
		}
	}
	return total, loadable, expired, skipped
}

// luaStoreEntry 对齐 init.lua 的 store_entry 冷启动校验。
func luaStoreEntry(code, urlStr string) bool {
	if code == "" || urlStr == "" {
		return false
	}
	lower := strings.ToLower(urlStr)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return false
	}
	for i := 0; i < len(urlStr); i++ {
		c := urlStr[i]
		if c <= 0x20 || c == 0x7f || c == '"' || c == '<' || c == '>' {
			return false
		}
	}
	return true
}
