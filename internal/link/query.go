package link

import (
	"encoding/json"
	"log/slog"
	"strconv"

	"github.com/UeCook/Short_NURL/internal/auth"
	"github.com/UeCook/Short_NURL/internal/isoutil"
	"github.com/UeCook/Short_NURL/internal/store"
)

// List 列出所有短链（扁平列表，插入序 → 短码升序，偏差 P1）。
func (s *Services) List(authCtx *auth.Context) BizResponse {
	if authCtx.KeyType == "onetime" {
		return Error("onetime_forbidden", "一次性密钥无权进行短链管理", 403)
	}

	permData, err := s.Perm.Read()
	if err != nil {
		slog.Error("[list] 冷存储读取失败", "err", err)
		return Error("data_corrupted", "数据文件损坏", 500)
	}
	tempData, err := s.Temp.Read()
	if err != nil {
		slog.Error("[list] 冷存储读取失败", "err", err)
		return Error("data_corrupted", "数据文件损坏", 500)
	}

	list := []ListItem{}

	for _, code := range store.SortedCodes(permData) {
		item, _ := permData.Entry(code)
		list = append(list, ListItem{
			ID:   fieldOr(item, "id", code),
			URL:  fieldOr(item, "url", ""),
			LURL: fieldOr(item, "lurl", ""),
			Exp:  "permanent",
		})
	}

	for _, code := range store.SortedCodes(tempData) {
		item, _ := tempData.Entry(code)
		var t any
		if item != nil {
			t = item["t"]
		}
		if isoutil.IsExpired(t) {
			continue
		}
		list = append(list, ListItem{
			ID:   fieldOr(item, "id", code),
			URL:  fieldOr(item, "url", ""),
			LURL: fieldOr(item, "lurl", ""),
			Exp:  t,
		})
	}

	return Success(list)
}

// ListGrouped 转换为 v1.11.0 的分组结构。
func (s *Services) ListGrouped(authCtx *auth.Context) BizResponse {
	result := s.List(authCtx)
	if !result.OK {
		return result
	}
	items, _ := result.Data.([]ListItem)
	grouped := GroupedList{Permanent: []ListItem{}, Temporary: []ListItem{}}
	for _, item := range items {
		if s, ok := item.Exp.(string); ok && s == "permanent" {
			grouped.Permanent = append(grouped.Permanent, item)
		} else {
			grouped.Temporary = append(grouped.Temporary, item)
		}
	}
	return Success(grouped)
}

// Stat 配额与热存储计数。
func (s *Services) Stat(authCtx *auth.Context) BizResponse {
	cfg := s.Cfg
	if authCtx.KeyType == "onetime" {
		return Error("onetime_forbidden", "一次性密钥无权进行短链管理", 403)
	}

	permData, err := s.Perm.Read()
	if err != nil {
		slog.Error("[stat] 冷存储读取失败", "err", err)
		return Error("data_corrupted", "数据文件损坏", 500)
	}
	tempData, err := s.Temp.Read()
	if err != nil {
		slog.Error("[stat] 冷存储读取失败", "err", err)
		return Error("data_corrupted", "数据文件损坏", 500)
	}
	permActive := store.CountActive(permData)
	tempActive := store.CountActive(tempData)

	isServiceKey := authCtx.KeyType == "service"
	permLimit := cfg.Quota.PermLimit
	tempLimit := cfg.Quota.TempLimit
	if isServiceKey {
		permLimit = cfg.Quota.SvcPermLimit
		tempLimit = cfg.Quota.SvcTempLimit
	}

	var hotPermCount, hotTempCount any
	hotAvailable := false
	if p, t, ok := s.Hot.Stat(); ok {
		hotPermCount, hotTempCount = p, t
		hotAvailable = true
		if toInt(p) != permActive || toInt(t) != tempActive {
			slog.Warn("[stat] 热冷计数漂移",
				"hot_perm", p, "cold_perm", permActive,
				"hot_temp", t, "cold_temp", tempActive)
		}
	} else {
		slog.Warn("[stat] 热存储不可用，使用冷存储计数",
			"cold_perm", permActive, "cold_temp", tempActive)
	}

	return Success(StatOK{
		PermCount:    permActive,
		PermLimit:    permLimit,
		TempCount:    tempActive,
		TempLimit:    tempLimit,
		HotPermCount: hotPermCount,
		HotTempCount: hotTempCount,
		HotAvailable: hotAvailable,
	})
}

// Get 单条查询。
func (s *Services) Get(authCtx *auth.Context, code string) BizResponse {
	if authCtx.KeyType == "onetime" {
		return Error("onetime_forbidden", "一次性密钥无权进行短链管理", 403)
	}
	if isEmptyPHP(trimSpace(code)) {
		return Error("missing_code", "缺少短链后缀", 400)
	}
	code = lower(code)

	permData, err := s.Perm.Read()
	if err != nil {
		slog.Error("[get] 冷存储读取失败", "err", err)
		return Error("data_corrupted", "数据文件损坏", 500)
	}
	if permData.Has(code) {
		item, ok := permData.Entry(code)
		id, hasID := store.FieldString(item, "id")
		u, hasURL := store.FieldString(item, "url")
		l, hasLURL := store.FieldString(item, "lurl")
		if !ok || !hasID || !hasURL || !hasLURL {
			return Error("data_corrupted", "数据条目损坏", 500)
		}
		return Success(GetOK{ID: id, URL: u, LURL: l, Exp: "permanent"})
	}

	tempData, err := s.Temp.Read()
	if err != nil {
		slog.Error("[get] 冷存储读取失败", "err", err)
		return Error("data_corrupted", "数据文件损坏", 500)
	}
	if tempData.Has(code) {
		item, ok := tempData.Entry(code)
		id, hasID := store.FieldString(item, "id")
		u, hasURL := store.FieldString(item, "url")
		l, hasLURL := store.FieldString(item, "lurl")
		if !ok || !hasID || !hasURL || !hasLURL {
			return Error("data_corrupted", "数据条目损坏", 500)
		}
		if isoutil.IsExpired(item["t"]) {
			return Error("expired", "该短链已过期", 404)
		}
		return Success(GetOK{ID: id, URL: u, LURL: l, Exp: item["t"]})
	}

	return Error("not_found", "该短链不存在", 404)
}

func fieldOr(m map[string]any, key, fallback string) string {
	if v, ok := store.FieldString(m, key); ok {
		return v
	}
	return fallback
}

func toInt(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	case json.Number:
		n, err := strconv.Atoi(t.String())
		if err != nil {
			return -1
		}
		return n
	default:
		return -1
	}
}
