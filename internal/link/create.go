package link

import (
	"log/slog"
	"math/rand"
	"time"

	"github.com/UeCook/Short_NURL/internal/auth"
	"github.com/UeCook/Short_NURL/internal/config"
	"github.com/UeCook/Short_NURL/internal/isoutil"
	"github.com/UeCook/Short_NURL/internal/store"
	"github.com/UeCook/Short_NURL/internal/urlguard"
)

const genAlphabet = "23456789abcdefghjkmnpqrstuvwxyz"

// Create 执行创建短链（对齐 CreateLinkService::execute）。
func (s *Services) Create(authCtx *auth.Context, params map[string]any) BizResponse {
	cfg := s.Cfg
	if cfg.QuotaTotal() > 30000 {
		return Error("config_error", "配置的总容量超过 30000", 500)
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

	now := time.Now().Unix()
	domain := cfg.Site.Domain
	tz := cfg.Site.TzOffset
	expStr := "0"
	if isTemp {
		formatted, err := isoutil.Format(now+ttl, tz)
		if err != nil {
			slog.Error("[create] 时区配置无效", "err", err)
			return Error("config_error", "时区配置无效", 500)
		}
		expStr = formatted
	}
	shortURL := domain + "/" + code
	entry := map[string]any{"id": code, "url": urlStr, "lurl": shortURL}
	if isTemp {
		entry["t"] = expStr
	}

	targetStore, otherStore := s.Perm, s.Temp
	if isTemp {
		targetStore, otherStore = s.Temp, s.Perm
	}
	storePath := targetStore.Path()
	otherPath := otherStore.Path()

	var errorResponse *BizResponse
	var dedupEntry map[string]any
	var dedupCode string
	synced := false
	hotWriteAttempted := false
	mutationStarted := false
	var operationLock *opLock
	var urlLock *opLock

	// 仅「永久 + 自动短码」持有 URL 操作锁
	if !isTemp && !isCustom {
		urlLock = acquireOpLock(s.Perm.Path(), "url:"+urlStr)
		if urlLock == nil {
			return Error("operation_in_progress", "该目标链接正在创建，请稍后重试", 409)
		}
	}

	fail := func(resp BizResponse) BizResponse {
		if mutationStarted {
			s.Hot.EndMutation(code)
			mutationStarted = false
		}
		if operationLock != nil {
			operationLock.release()
			operationLock = nil
		}
		if urlLock != nil {
			urlLock.release()
			urlLock = nil
		}
		return resp
	}

	// ── 阶段 1：双 Store 锁内只做选择与校验，不写入 ──────────────
	_, err := store.WithBothLocks(s.Perm, s.Temp, func(all map[string]store.Data) (*store.TxResult, error) {
		data := all[storePath]
		other := all[otherPath]

		if isTemp {
			store.CleanExpired(data)
		}

		if !isTemp && !isCustom {
			for _, existingCode := range store.SortedCodes(data) {
				existing, ok := data.Entry(existingCode)
				if !ok {
					continue
				}
				eu, hasURL := store.FieldString(existing, "url")
				_, hasLURL := store.FieldString(existing, "lurl")
				if !hasURL || !hasLURL || eu != urlStr {
					continue
				}
				code = existingCode
				operationLock = acquireOpLock(s.Perm.Path(), "code:"+code)
				if operationLock == nil {
					errorResponse = ptr(Error("operation_in_progress", "该短码正在操作，请稍后重试", 409))
					return &store.TxResult{Write: []string{}, Result: false}, nil
				}
				dedupCode = existingCode
				dedupEntry = existing
				return &store.TxResult{Write: []string{}, Result: true}, nil
			}
		}

		if isCustom {
			if data.Has(code) || other.Has(code) {
				errorResponse = ptr(Error("conflict", "已占用", 409))
			}
		} else {
			generated := false
			for i := 0; i < 8; i++ {
				candidate := randomCode(svcCodeLength)
				if isReserved(cfg.Codes.ReservedCodes, candidate) {
					continue
				}
				if data.Has(candidate) || other.Has(candidate) {
					continue
				}
				code = candidate
				generated = true
				break
			}
			if !generated {
				errorResponse = ptr(Error("gen_failed", "无法生成后缀", 500))
			} else {
				shortURL = domain + "/" + code
				entry["id"] = code
				entry["lurl"] = shortURL
			}
		}

		if errorResponse == nil {
			activeCount := store.CountActive(data)
			limit := quotaLimit(cfg, isServiceKey, isTemp)
			if activeCount >= limit {
				errorResponse = ptr(Error("quota_exceeded", "已达上限", 429))
			}
		}

		if errorResponse == nil {
			operationLock = acquireOpLock(s.Perm.Path(), "code:"+code)
			if operationLock == nil {
				errorResponse = ptr(Error("operation_in_progress", "该短码正在操作，请稍后重试", 409))
				return &store.TxResult{Write: []string{}, Result: false}, nil
			}
			return &store.TxResult{Write: []string{}, Result: true}, nil
		}
		return &store.TxResult{Write: []string{}, Result: false}, nil
	})
	if err != nil {
		return fail(Error("write_failed", "服务器内部错误", 500))
	}
	if errorResponse != nil {
		return fail(*errorResponse)
	}

	// ── 阶段 2：热同步 ─────────────────────────────────────────
	mutationStarted = s.Hot.BeginMutation(code)
	if !mutationStarted && !cfg.Policy.ColdFallback {
		return fail(Error("hot_store_unavailable", "热存储暂不可用，请稍后重试", 503))
	}
	if !mutationStarted {
		slog.Warn("[create] mutation 标记未确认，已启用冷存储兜底", "code", code)
	}

	hotWriteAttempted = dedupEntry == nil
	remainingTtl := int64(0)
	if isTemp {
		remainingTtl = now + ttl - time.Now().Unix()
		if remainingTtl < 1 {
			remainingTtl = 1
		}
	}
	synced = s.Hot.SetWithRetry(code, urlStr, int(remainingTtl), expStr)
	if !synced && !cfg.Policy.ColdFallback {
		if hotWriteAttempted {
			hotType := "perm"
			if isTemp {
				hotType = "temp"
			}
			s.Hot.DeleteWithRetry(code, hotType)
		}
		return fail(Error("hot_store_unavailable", "热存储暂不可用，请稍后重试", 503))
	}

	// ── 阶段 3：冷提交 ─────────────────────────────────────────
	if dedupEntry == nil {
		_, err = store.WithBothLocks(s.Perm, s.Temp, func(all map[string]store.Data) (*store.TxResult, error) {
			data := all[storePath]
			if isTemp {
				store.CleanExpired(data)
			}
			if data.Has(code) || all[otherPath].Has(code) {
				errorResponse = ptr(Error("conflict", "已占用", 409))
				return &store.TxResult{Write: []string{}, Result: false}, nil
			}
			active := store.CountActive(data)
			limit := quotaLimit(cfg, isServiceKey, isTemp)
			if active >= limit {
				errorResponse = ptr(Error("quota_exceeded", "已达上限", 429))
				return &store.TxResult{Write: []string{}, Result: false}, nil
			}
			data[code] = entry
			return &store.TxResult{Write: []string{storePath}, Result: true}, nil
		})
		if err != nil {
			slog.Error("[create]", "err", err)
			if hotWriteAttempted {
				hotType := "perm"
				if isTemp {
					hotType = "temp"
				}
				s.Hot.DeleteWithRetry(code, hotType)
			}
			return fail(Error("write_failed", "服务器内部错误", 500))
		}
		if errorResponse != nil {
			hotType := "perm"
			if isTemp {
				hotType = "temp"
			}
			s.Hot.DeleteWithRetry(code, hotType)
		}
	}

	if errorResponse != nil {
		return fail(*errorResponse)
	}

	if mutationStarted {
		s.Hot.EndMutation(code)
		mutationStarted = false
	}
	if operationLock != nil {
		operationLock.release()
		operationLock = nil
	}
	if urlLock != nil {
		urlLock.release()
		urlLock = nil
	}

	if !synced {
		slog.Warn("[create] 冷存储记录已保留，热同步未确认，等待后台对账", "code", code)
	}
	var warning *string
	if !synced {
		w := "短链已保存到冷存储，但未确认热存储同步，可能暂时无法访问，等待后台对账恢复。"
		warning = &w
	}

	if dedupEntry != nil && dedupCode != "" {
		lurl, _ := store.FieldString(dedupEntry, "lurl")
		return Success(CreateOK{
			ShortURL:    lurl,
			Exp:         nil,
			Dedup:       true,
			Synced:      synced,
			Warning:     warning,
			KeyConsumed: authCtx.Consumed,
		})
	}

	var expOut *string
	if isTemp {
		expOut = &expStr
	}
	return Success(CreateOK{
		ShortURL:    shortURL,
		Exp:         expOut,
		Dedup:       false,
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
	for i := range b {
		b[i] = genAlphabet[rand.Intn(len(genAlphabet))]
	}
	return string(b)
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

func ptr[T any](v T) *T { return &v }
