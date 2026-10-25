// Package link 实现短链的创建、删除与查询业务（对齐 v2.0.3 的三个 Service）。
package link

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/UeCook/Short_NURL/internal/config"
	"github.com/UeCook/Short_NURL/internal/hot"
	"github.com/UeCook/Short_NURL/internal/store"
)

// Services 业务服务集合。
type Services struct {
	Cfg  *config.Config
	Perm *store.Store
	Temp *store.Store
	Hot  *hot.Client
}

// NewServices 构造业务服务。
func NewServices(cfg *config.Config, hotClient *hot.Client) *Services {
	return &Services{
		Cfg:  cfg,
		Perm: store.New(cfg.PermPath, cfg.Site.TzOffset),
		Temp: store.New(cfg.TempPath, cfg.Site.TzOffset),
		Hot:  hotClient,
	}
}

// BizResponse 业务层标准响应。
type BizResponse struct {
	OK         bool
	Data       any
	Code       string
	Message    string
	HTTPStatus int
}

// Success 成功响应。
func Success(data any) BizResponse {
	return BizResponse{OK: true, Data: data}
}

// Error 失败响应。
func Error(code, message string, status int) BizResponse {
	return BizResponse{Code: code, Message: message, HTTPStatus: status}
}

// ── 响应结构（保持字段顺序与 PHP json_encode 一致）──────────────

// CreateOK 创建成功响应。
type CreateOK struct {
	ShortURL    string  `json:"short_url"`
	Exp         *string `json:"exp"`
	Dedup       bool    `json:"dedup"`
	Synced      bool    `json:"synced"`
	Warning     *string `json:"warning"`
	KeyConsumed bool    `json:"key_consumed"`
}

// DeleteOK 删除成功响应。
type DeleteOK struct {
	OK      bool    `json:"ok"`
	Synced  bool    `json:"synced"`
	Warning *string `json:"warning"`
}

// ListItem 列表条目。
type ListItem struct {
	ID   string `json:"id"`
	URL  string `json:"url"`
	LURL string `json:"lurl"`
	Exp  any    `json:"exp"`
}

// GroupedList 分组列表。
type GroupedList struct {
	Permanent []ListItem `json:"permanent"`
	Temporary []ListItem `json:"temporary"`
}

// StatOK 配额状态响应。
type StatOK struct {
	PermCount    int  `json:"perm_count"`
	PermLimit    int  `json:"perm_limit"`
	TempCount    int  `json:"temp_count"`
	TempLimit    int  `json:"temp_limit"`
	HotPermCount any  `json:"hot_perm_count"`
	HotTempCount any  `json:"hot_temp_count"`
	HotAvailable bool `json:"hot_available"`
}

// GetOK 单条查询响应。
type GetOK struct {
	ID   string `json:"id"`
	URL  string `json:"url"`
	LURL string `json:"lurl"`
	Exp  any    `json:"exp"`
}

// ── 参数解析（复刻 PHP 的弱类型怪癖）───────────────────────────

// phpIntval 宽松整数转换（对齐 PHP intval）。
func phpIntval(v any) int64 {
	switch t := v.(type) {
	case nil:
		return 0
	case bool:
		if t {
			return 1
		}
		return 0
	case json.Number:
		if n, err := strconv.ParseInt(t.String(), 10, 64); err == nil {
			return n
		}
		if f, err := strconv.ParseFloat(t.String(), 64); err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) {
			return int64(f)
		}
		return 0
	case string:
		s := strings.TrimSpace(t)
		neg := false
		i := 0
		if i < len(s) && (s[i] == '-' || s[i] == '+') {
			neg = s[i] == '-'
			i++
		}
		start := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if start == i {
			return 0
		}
		n, err := strconv.ParseInt(s[:i], 10, 64)
		if err != nil {
			return 0
		}
		if neg {
			return -n
		}
		return n
	default:
		return 0
	}
}

// isPHPInt 判定 JSON 值是否为 PHP 意义上的整数（json_decode 的 int）。
func isPHPInt(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	s := n.String()
	if strings.ContainsAny(s, ".eE") {
		return 0, false
	}
	out, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return out, true
}

// isEmptyPHP 对齐 PHP empty()（仅用于字符串短码）。
func isEmptyPHP(s string) bool {
	return s == "" || s == "0"
}
