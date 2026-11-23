package link

import (
	"context"
	"errors"
	"log/slog"
	"math/rand"
	"time"

	"github.com/UeCook/Short_NURL/internal/auth"
	"github.com/UeCook/Short_NURL/internal/config"
	"github.com/UeCook/Short_NURL/internal/fslock"
	"github.com/UeCook/Short_NURL/internal/isoutil"
	"github.com/UeCook/Short_NURL/internal/store"
	"github.com/UeCook/Short_NURL/internal/urlguard"
)

// 自动短码排除单字符歧义 0/1/i/l/o，相邻字形另由 confusableCodePair 约束。
const genAlphabet = "23456789abcdefghjkmnpqrstuvwxyz"

// Create 执行创建短链（对齐 CreateLinkService::execute）。
func (s *Services) Create(authCtx *auth.Context, params map[string]any) BizResponse {
	return s.CreateContext(context.Background(), authCtx, params)
}

// CreateContext 允许请求取消等待或中止尚未提交的创建。
func (s *Services) CreateContext(ctx context.Context, authCtx *auth.Context, params map[string]any) BizResponse {
	ctx, cancel := context.WithTimeout(ctx, mutationTimeout)
	defer cancel()
	cfg := s.Cfg
	if err := cfg.ValidateLinkSettings(); err != nil {
		return Error("config_error", err.Error(), 500)
	}

	// 目标链接
	rawURL, ok := params["url"]
	if !ok {
		return Error("missing_url", "目标链接必须为字符串", 400)
	}
	urlStr, isStr := rawURL.(string)
	if !isStr {
		return Error("missing_url", "目标链接必须为字符串", 400)
	}
	urlStr = trimSpace(urlStr)
	if urlStr == "" {
		return Error("missing_url", "缺少目标链接", 400)
	}
	if verr := urlguard.Check(urlStr); verr != nil {
		return Error(verr.Code, verr.Message, 400)
	}

	// TTL
	var ttl int64
	if v, exists := params["ttl"]; exists {
		n, isInt := isPHPInt(v)
		if !isInt {
			return Error("invalid_ttl", "TTL 必须为整数", 400)
		}
		ttl = n
	}
	if ttl < 0 || ttl > int64(cfg.Codes.TTLMax) {
		return Error("ttl_exceeded", "TTL 超限", 400)
	}
	isTemp := ttl > 0

	// 短码
	code := ""
	if v, exists := params["code"]; exists {
		if sv, ok := v.(string); ok {
			code = trimSpace(sv)
		}
	}
	isCustom := !isEmptyPHP(code)

	isServiceKey := authCtx.KeyType == "service"
	svcCodeLength := 4
	if isServiceKey {
		svcCodeLength = cfg.Codes.SvcCodeLength
		if v, exists := params["len"]; exists {
			if n := phpIntval(v); n == 4 || n == 5 {
				svcCodeLength = int(n)
			}
		}
	}
	maxCodeLen := 4
	if isServiceKey {
		maxCodeLen = 5
	}

	if isCustom {
		code = lower(code)
		if !validCustomCode(code, maxCodeLen) {
			return Error("invalid_code", "后缀格式错误", 400)
		}
		if isReserved(cfg.Codes.ReservedCodes, code) {
			return Error("reserved_code", "保留字", 400)
		}
	}

	// 冷库是创建的提交点。热存储检查和发布都在双库锁之外。
	var urlLock, codeLock *opLock
	defer func() {
		codeLock.release()
		urlLock.release()
	}()
	if !isTemp && !isCustom {
		var err error
		urlLock, err = acquireOpLock(cfg.PermPath, "url:"+urlStr)
		if err != nil {
			return lockFailure(err)
		}
	}

	hotReady := true
	if !cfg.Policy.ColdFallback {
		hotReady = s.Hot.CheckCreate(ctx) == nil
	}

	target, other := s.Perm, s.Temp
	if isTemp {
		target, other = s.Temp, s.Perm
	}
	var rejected BizResponse
	var dedupEntry map[string]any
	var expiresAt int64
	expStr := "0"
	waitCtx, cancelWait := context.WithTimeout(ctx, coldLockWait)
	defer cancelWait()
	_, err := store.WithBothLocksContext(ctx, waitCtx, s.Perm, s.Temp, func(all map[string]store.Data) (*store.TxResult, error) {
		data, otherData := all[target.Path()], all[other.Path()]
		readOnly := &store.TxResult{}

		if !isTemp && !isCustom {
			// 同 URL 的历史记录可能有多个短码，沿用字典序最小的结果。
			for existingCode := range data {
				if dedupEntry != nil && existingCode >= code {
					continue
				}
				entry, _ := data.Entry(existingCode)
				eu, _ := store.FieldString(entry, "url")
				_, hasLURL := store.FieldString(entry, "lurl")
				if hasLURL && eu == urlStr {
					code, dedupEntry = existingCode, entry
				}
			}
		}

		if dedupEntry == nil {
			if isTemp {
				store.CleanExpired(data)
			}
			if isCustom && (data.Has(code) || otherData.Has(code)) {
				rejected = Error("conflict", "已占用", 409)
				return readOnly, nil
			}
			if store.CountActive(data) >= quotaLimit(cfg, isServiceKey, isTemp) {
				rejected = Error("quota_exceeded", "已达上限", 429)
				return readOnly, nil
			}
		}

		if !hotReady {
			rejected = Error("hot_store_unavailable", "热存储暂不可用，请稍后重试", 503)
			return readOnly, nil
		}
		if dedupEntry != nil || isCustom {
			var lockErr error
			codeLock, lockErr = acquireOpLock(cfg.PermPath, "code:"+code)
			if lockErr != nil {
				rejected = lockFailure(lockErr)
				return readOnly, nil
			}
		} else {
			for i := 0; i < 8; i++ {
				candidate := randomCode(svcCodeLength)
				if isReserved(cfg.Codes.ReservedCodes, candidate) || data.Has(candidate) || otherData.Has(candidate) {
					continue
				}
				var lockErr error
				codeLock, lockErr = acquireOpLock(cfg.PermPath, "code:"+candidate)
				if errors.Is(lockErr, fslock.ErrNotAcquired) {
					continue
				}
				if lockErr != nil {
					rejected = lockFailure(lockErr)
					return readOnly, nil
				}
				code = candidate
				break
			}
			if codeLock == nil {
				rejected = Error("gen_failed", "无法生成后缀", 500)
				return readOnly, nil
			}
		}

		if dedupEntry != nil {
			return readOnly, nil
		}
		entry := map[string]any{"id": code, "url": urlStr, "lurl": cfg.Site.Domain + "/" + code}
		if isTemp {
			expiresAt = time.Now().Unix() + ttl
			formatted, formatErr := isoutil.Format(expiresAt, cfg.Site.TzOffset)
			if formatErr != nil {
				slog.Error("[create] 时区配置无效", "err", formatErr)
				rejected = Error("config_error", "时区配置无效", 500)
				return readOnly, nil
			}
			expStr = formatted
			entry["t"] = expStr
		}
		data[code] = entry
		return &store.TxResult{Write: []string{target.Path()}}, nil
	})
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return Error("create_cancelled", "创建请求已取消", 408)
		case errors.Is(err, context.DeadlineExceeded):
			if ctx.Err() == nil {
				return Error("api_busy", "创建请求排队超时，请稍后重试", 429)
			}
			return Error("create_timeout", "创建请求超时，请稍后重试", 504)
		default:
			slog.Error("[create] 冷存储事务失败", "err", err)
			return Error("write_failed", "服务器内部错误", 500)
		}
	}
	if rejected.Code != "" {
		return rejected
	}

	// 冷库已经提交。即使这时请求取消或 OpenResty 不可用，后台也能从冷库恢复。
	remainingTTL := int64(0)
	if isTemp {
		remainingTTL = expiresAt - time.Now().Unix()
	}
	synced := false
	if !isTemp || remainingTTL > 0 {
		synced = s.Hot.SetWithRetryContext(ctx, code, urlStr, int(remainingTTL), expStr) == nil
	}
	if !synced && dedupEntry != nil && !cfg.Policy.ColdFallback {
		return Error("hot_store_unavailable", "热存储暂不可用，请稍后重试", 503)
	}
	var warning *string
	if !synced {
		slog.Warn("[create] 冷存储记录已保留，热同步未确认", "code", code)
		w := "短链已保存到冷存储，但未确认热存储同步，可能暂时无法访问，等待后台对账恢复。"
		warning = &w
	}
	shortURL := cfg.Site.Domain + "/" + code
	if dedupEntry != nil {
		shortURL, _ = store.FieldString(dedupEntry, "lurl")
	}
	var expOut *string
	if isTemp {
		expOut = &expStr
	}
	return Success(CreateOK{
		ShortURL:    shortURL,
		Exp:         expOut,
		Dedup:       dedupEntry != nil,
		Synced:      synced,
		Warning:     warning,
		KeyConsumed: authCtx.Consumed,
	})
}

func quotaLimit(cfg *config.Config, isServiceKey, isTemp bool) int {
	if isServiceKey {
		if isTemp {
			return cfg.Quota.SvcTempLimit
		}
		return cfg.Quota.SvcPermLimit
	}
	if isTemp {
		return cfg.Quota.TempLimit
	}
	return cfg.Quota.PermLimit
}

func randomCode(n int) string {
	b := make([]byte, n)
	var allowed [len(genAlphabet)]byte
	for i := range b {
		count := 0
		for j := range genAlphabet {
			c := genAlphabet[j]
			if i > 0 && confusableCodePair(b[i-1], c) {
				continue
			}
			allowed[count] = c
			count++
		}
		// 直接从可用字符中选择，不占用外层的占用/保留字重试次数。
		b[i] = allowed[rand.Intn(count)]
	}
	return string(b)
}

// confusableCodePair 避免连续重复字符和容易粘连误读的字形组。
// m/n/r 可能拼成相似的竖笔画，u/v/w 可能拼成相似的谷形笔画。
// 仅限制自动生成，不改变自定义短码和已有短链的合法性。
func confusableCodePair(left, right byte) bool {
	if left == right {
		return true
	}
	switch left {
	case 'm', 'n', 'r':
		return right == 'm' || right == 'n' || right == 'r'
	case 'u', 'v', 'w':
		return right == 'u' || right == 'v' || right == 'w'
	default:
		return false
	}
}

func validCustomCode(code string, maxLen int) bool {
	if len(code) == 0 || len(code) > maxLen {
		return false
	}
	for i := 0; i < len(code); i++ {
		c := code[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || c == '-' {
			continue
		}
		return false
	}
	return true
}

func isReserved(list []string, code string) bool {
	for _, r := range list {
		if r == code {
			return true
		}
	}
	return false
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r' || s[start] == '\v' || s[start] == '\f') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r' || s[end-1] == '\v' || s[end-1] == '\f') {
		end--
	}
	return s[start:end]
}

func lower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}
