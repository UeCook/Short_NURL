package link

import (
	"log/slog"
	"time"

	"github.com/UeCook/Short_NURL/internal/auth"
	"github.com/UeCook/Short_NURL/internal/isoutil"
	"github.com/UeCook/Short_NURL/internal/store"
	"github.com/UeCook/Short_NURL/internal/urlguard"
)

// Delete 执行删除短链（对齐 DeleteLinkService::execute）。
func (s *Services) Delete(authCtx *auth.Context, code string) BizResponse {
	cfg := s.Cfg
	if authCtx.KeyType == "onetime" {
		return Error("onetime_forbidden", "一次性密钥无权进行短链管理", 403)
	}
	if isEmptyPHP(trimSpace(code)) {
		return Error("missing_code", "缺少短链后缀", 400)
	}
	code = lower(code)

	var errorResponse *BizResponse
	var deletedEntry map[string]any
	deletedType := ""
	hotDeleted := false
	mutationStarted := false
	var operationLock *opLock
	synced := false

	permPath := s.Perm.Path()
	tempPath := s.Temp.Path()

	fail := func(resp BizResponse) BizResponse {
		if mutationStarted {
			s.Hot.EndMutation(code)
			mutationStarted = false
		}
		if operationLock != nil {
			operationLock.release()
			operationLock = nil
		}
		return resp
	}

	_, err := store.WithBothLocks(s.Perm, s.Temp, func(all map[string]store.Data) (*store.TxResult, error) {
		if all[tempPath].Has(code) {
			deletedType = "temp"
		} else if all[permPath].Has(code) {
			deletedType = "perm"
		} else {
			errorResponse = ptr(Error("not_found", "该短链不存在", 404))
			return &store.TxResult{Write: []string{}, Result: false}, nil
		}
		writePath := permPath
		if deletedType == "temp" {
			writePath = tempPath
		}
		entry, ok := all[writePath].Entry(code)
		if !ok {
			errorResponse = ptr(Error("data_corrupted", "数据条目损坏", 500))
			return &store.TxResult{Write: []string{}, Result: false}, nil
		}
		deletedEntry = entry
		operationLock = acquireOpLock(s.Perm.Path(), "code:"+code)
		if operationLock == nil {
			errorResponse = ptr(Error("operation_in_progress", "该短码正在操作，请稍后重试", 409))
			return &store.TxResult{Write: []string{}, Result: false}, nil
		}
		return &store.TxResult{Write: []string{}, Result: true}, nil
	})
	if err != nil {
		return fail(Error("write_failed", "服务器内部错误", 500))
	}
	if errorResponse != nil {
		return fail(*errorResponse)
	}

	mutationStarted = s.Hot.BeginMutation(code)
	if !mutationStarted && !cfg.Policy.ColdFallback {
		return fail(Error("hot_store_unavailable", "热存储暂不可用，短链未删除", 503))
	}
	if !mutationStarted {
		slog.Warn("[delete] mutation 未确认，继续冷删除兜底", "code", code)
	}

	hotDeleted = true
	synced = s.Hot.DeleteWithRetry(code, deletedType)
	if !synced && !cfg.Policy.ColdFallback {
		s.restoreHotEntry(code, deletedType, deletedEntry)
		return fail(Error("hot_store_unavailable", "热存储暂不可用，短链未删除", 503))
	}

	_, err = store.WithBothLocks(s.Perm, s.Temp, func(all map[string]store.Data) (*store.TxResult, error) {
		writePath := permPath
		if deletedType == "temp" {
			writePath = tempPath
		}
		delete(all[writePath], code)
		if deletedType == "temp" {
			store.CleanExpired(all[writePath])
		}
		return &store.TxResult{Write: []string{writePath}, Result: true}, nil
	})
	if err != nil {
		slog.Error("[delete] 冷存储提交失败", "err", err)
		if hotDeleted && deletedEntry != nil {
			if _, ok := store.FieldString(deletedEntry, "url"); ok {
				s.restoreHotEntry(code, deletedType, deletedEntry)
			}
		}
		return fail(Error("write_failed", "服务器内部错误", 500))
	}

	if mutationStarted {
		s.Hot.EndMutation(code)
		mutationStarted = false
	}
	if operationLock != nil {
		operationLock.release()
		operationLock = nil
	}

	if !synced {
		slog.Warn("[delete] 冷记录已删除，热同步未确认", "code", code)
	}
	var warning *string
	if !synced {
		w := "冷存储记录已删除，但未确认热存储同步，原短链可能仍可访问。"
		warning = &w
	}
	return Success(DeleteOK{OK: true, Synced: synced, Warning: warning})
}

func (s *Services) restoreHotEntry(code, typ string, entry map[string]any) {
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
		if !ok || expiresAt <= time.Now().Unix() {
			return
		}
		remaining := expiresAt - time.Now().Unix()
		if remaining < 1 {
			remaining = 1
		}
		ttl = int(remaining)
	}
	if !s.Hot.SetWithRetry(code, urlStr, ttl, exp) {
		detail := s.Hot.LastError()
		slog.Error("[delete] 冷写失败后的热存储恢复失败", "code", code, "reason", detail.String())
	}
}
