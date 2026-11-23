package link

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/UeCook/Short_NURL/internal/auth"
	"github.com/UeCook/Short_NURL/internal/isoutil"
	"github.com/UeCook/Short_NURL/internal/store"
	"github.com/UeCook/Short_NURL/internal/urlguard"
)

// Delete 执行删除短链（对齐 DeleteLinkService::execute）。
func (s *Services) Delete(authCtx *auth.Context, code string) BizResponse {
	return s.DeleteContext(context.Background(), authCtx, code)
}

// DeleteContext 允许取消排队和未提交的删除。热存储补偿有独立的期限。
func (s *Services) DeleteContext(ctx context.Context, authCtx *auth.Context, code string) BizResponse {
	cfg := s.Cfg
	if authCtx.KeyType == "onetime" {
		return Error("onetime_forbidden", "一次性密钥无权进行短链管理", 403)
	}
	if isEmptyPHP(trimSpace(code)) {
		return Error("missing_code", "缺少短链后缀", 400)
	}
	code = lower(code)
	ctx, cancel := context.WithTimeout(ctx, mutationTimeout)
	defer cancel()

	var rejected BizResponse
	var deletedEntry map[string]any
	deletedType := ""
	writePath := ""
	hotAttempted, coldCommitted := false, false
	mutationStarted := false
	var operationLock *opLock

	defer func() {
		defer operationLock.release()
		if !hotAttempted && !mutationStarted {
			return
		}
		cleanup, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), coldLockWait)
		defer cancelCleanup()
		if hotAttempted && !coldCommitted {
			s.restoreHotEntry(cleanup, code, deletedType, deletedEntry)
		}
		if mutationStarted {
			s.Hot.EndMutationContext(cleanup, code)
		}
	}()

	permPath := s.Perm.Path()
	tempPath := s.Temp.Path()

	waitCtx, cancelWait := context.WithTimeout(ctx, coldLockWait)
	defer cancelWait()
	_, err := store.WithBothLocksContext(ctx, waitCtx, s.Perm, s.Temp, func(all map[string]store.Data) (*store.TxResult, error) {
		if all[tempPath].Has(code) {
			deletedType, writePath = "temp", tempPath
		} else if all[permPath].Has(code) {
			deletedType, writePath = "perm", permPath
		} else {
			rejected = Error("not_found", "该短链不存在", 404)
			return &store.TxResult{}, nil
		}
		entry, ok := all[writePath].Entry(code)
		if !ok {
			rejected = Error("data_corrupted", "数据条目损坏", 500)
			return &store.TxResult{}, nil
		}
		deletedEntry = entry
		var lockErr error
		operationLock, lockErr = acquireOpLock(s.Perm.Path(), "code:"+code)
		if lockErr != nil {
			rejected = lockFailure(lockErr)
		}
		return &store.TxResult{}, nil
	})
	if err != nil {
		return deleteStoreError(ctx, err)
	}
	if rejected.Code != "" {
		return rejected
	}

	mutationStarted = s.Hot.BeginMutationContext(ctx, code)
	if err := ctx.Err(); err != nil {
		return deleteStoreError(ctx, err)
	}
	if !mutationStarted && !cfg.Policy.ColdFallback {
		return Error("hot_store_unavailable", "热存储暂不可用，短链未删除", 503)
	}
	if !mutationStarted {
		slog.Warn("[delete] mutation 未确认，继续冷删除兜底", "code", code)
	}

	hotAttempted = true
	synced := s.Hot.DeleteWithRetryContext(ctx, code, deletedType)
	if err := ctx.Err(); err != nil {
		return deleteStoreError(ctx, err)
	}
	if !synced && !cfg.Policy.ColdFallback {
		return Error("hot_store_unavailable", "热存储暂不可用，短链未删除", 503)
	}

	commitWait, cancelCommitWait := context.WithTimeout(ctx, coldLockWait)
	defer cancelCommitWait()
	_, err = store.WithBothLocksContext(ctx, commitWait, s.Perm, s.Temp, func(all map[string]store.Data) (*store.TxResult, error) {
		delete(all[writePath], code)
		if deletedType == "temp" {
			store.CleanExpired(all[writePath])
		}
		return &store.TxResult{Write: []string{writePath}}, nil
	})
	if err != nil {
		return deleteStoreError(ctx, err)
	}
	coldCommitted = true

	var warning *string
	if !synced {
		slog.Warn("[delete] 冷记录已删除，热同步未确认", "code", code)
		w := "冷存储记录已删除，但未确认热存储同步，原短链可能仍可访问。"
		warning = &w
	}
	return Success(DeleteOK{OK: true, Synced: synced, Warning: warning})
}

func deleteStoreError(ctx context.Context, err error) BizResponse {
	switch {
	case errors.Is(err, context.Canceled):
		return Error("delete_cancelled", "删除请求已取消", 408)
	case errors.Is(err, context.DeadlineExceeded):
		if ctx.Err() == nil {
			return Error("api_busy", "删除请求排队超时，请稍后重试", 429)
		}
		return Error("delete_timeout", "删除请求超时，请稍后重试", 504)
	default:
		slog.Error("[delete] 冷存储事务失败", "err", err)
		return Error("write_failed", "服务器内部错误", 500)
	}
}

func (s *Services) restoreHotEntry(ctx context.Context, code, typ string, entry map[string]any) {
	urlStr, ok := store.FieldString(entry, "url")
	if !ok || urlStr == "" {
		slog.Error("[delete] 无法恢复损坏的冷记录", "code", code)
		return
	}
	if !urlguard.ValidHotURL(urlStr) {
		slog.Error("[delete] 无法恢复非法 URL 的冷记录", "code", code)
		return
	}
	ttl := 0
	exp := "0"
	if typ == "temp" {
		t, ok := store.FieldString(entry, "t")
		if !ok {
			return
		}
		exp = t
		expiresAt, ok := isoutil.Parse(exp)
		remaining := expiresAt - time.Now().Unix()
		if !ok || remaining <= 0 {
			return
		}
		ttl = int(remaining)
	}
	if err := s.Hot.SetWithRetryContext(ctx, code, urlStr, ttl, exp); err != nil {
		slog.Error("[delete] 冷写失败后的热存储恢复失败", "code", code, "reason", err)
	}
}
