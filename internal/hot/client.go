// Package hot 实现 Go → OpenResty Lua 内部 API 客户端（/internal/*）。
//
// 契约见文档 02 第 8 节：LPA-Key 头、重试与退避、错误分类与补偿。
package hot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// Error 内部请求失败信息（仅用于日志与 last_error）。
type Error struct {
	Type     string // transport / http / content_type / json / response
	Message  string
	HTTPCode int
}

func (e *Error) String() string {
	if e == nil {
		return "未知错误"
	}
	return e.Message
}

// Error 实现 error 接口。
func (e *Error) Error() string { return e.String() }

// Client 内部 API 客户端。
type Client struct {
	host       string
	tokenPath  string
	timeout    time.Duration
	maxRetries int
	backoff    time.Duration
	http       *http.Client
	lastError  *Error
}

// New 构造内部 API 客户端。
func New(host, tokenPath string, timeout time.Duration, maxRetries, backoffMS int) *Client {
	if maxRetries <= 0 {
		maxRetries = 3
	}
	if backoffMS <= 0 {
		backoffMS = 100
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	// 显式指定 Transport：不读取 HTTP(S)_PROXY，避免 LPA-Key 被环境代理截获；
	// 连接超时与整体超时保持一致。
	transport := &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout:   timeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		ExpectContinueTimeout: time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          16,
	}
	return &Client{
		host:       strings.TrimRight(host, "/"),
		tokenPath:  tokenPath,
		timeout:    timeout,
		maxRetries: maxRetries,
		backoff:    time.Duration(backoffMS) * time.Millisecond,
		http: &http.Client{
			Timeout:   timeout,
			Transport: transport,
			// 内部 API 不返回重定向。绝不跟随，避免把 LPA-Key 转发给 3xx 指向的其他主机；
			// 返回原始响应，由 checkResponse 归类为 HTTP 3xx 错误。
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// LastError 返回最近一次内部请求的错误。
func (c *Client) LastError() *Error { return c.lastError }

// SetResult 内部接口返回的 JSON 对象。
type SetResult map[string]any

func (c *Client) readToken() string {
	if c.tokenPath == "" {
		return ""
	}
	raw, err := os.ReadFile(c.tokenPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func (c *Client) buildHeaders(req *http.Request, contentType bool) {
	if contentType {
		req.Header.Set("Content-Type", "application/json")
	}
	if token := c.readToken(); token != "" {
		req.Header.Set("LPA-Key", token)
	}
}

// Set 写入热存储。
func (c *Client) Set(code, url string, ttl int, expStr string) (SetResult, error) {
	return c.post("/internal/set", map[string]any{
		"code":    code,
		"url":     url,
		"ttl":     ttl,
		"exp_str": expStr,
	})
}

// SetWithRetry 带重试地写入热存储。
func (c *Client) SetWithRetry(code, url string, ttl int, expStr string) bool {
	for i := 0; i < c.maxRetries; i++ {
		if _, err := c.Set(code, url, ttl, expStr); err == nil {
			return true
		}
		if i < c.maxRetries-1 {
			time.Sleep(c.backoff * (1 << i))
		}
	}
	return false
}

// Delete 删除热存储条目。
func (c *Client) Delete(code, typ string) (SetResult, error) {
	params := map[string]any{"code": code}
	if typ != "" {
		params["type"] = typ
	}
	return c.post("/internal/delete", params)
}

// DeleteWithRetry 带重试地删除热存储条目。
func (c *Client) DeleteWithRetry(code, typ string) bool {
	for i := 0; i < c.maxRetries; i++ {
		if _, err := c.Delete(code, typ); err == nil {
			return true
		}
		if i < c.maxRetries-1 {
			time.Sleep(c.backoff * (1 << i))
		}
	}
	return false
}

// BeginMutation 标记短码进入提交阶段。
func (c *Client) BeginMutation(code string) bool {
	_, err := c.post("/internal/mutation", map[string]any{"code": code, "action": "begin"})
	return err == nil
}

// EndMutation 结束提交标记（不关心结果）。
func (c *Client) EndMutation(code string) {
	_, _ = c.post("/internal/mutation", map[string]any{"code": code, "action": "end"})
}

// Stat 读取热存储计数。
func (c *Client) Stat() (permCount, tempCount any, ok bool) {
	c.lastError = nil
	req, err := http.NewRequest(http.MethodGet, c.host+"/internal/stat", nil)
	if err != nil {
		c.lastError = &Error{Type: "transport", Message: "内部请求构建失败"}
		return nil, nil, false
	}
	c.buildHeaders(req, false)

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := c.http.Do(req)
	if err != nil {
		c.lastError = &Error{Type: "transport", Message: "cURL 错误：" + err.Error()}
		return nil, nil, false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if err := c.checkResponse(resp, body); err != nil {
		c.lastError = err
		return nil, nil, false
	}
	var d map[string]any
	if err := json.Unmarshal(body, &d); err != nil {
		c.lastError = &Error{Type: "json", Message: "响应 JSON 解析失败：" + err.Error(), HTTPCode: resp.StatusCode}
		return nil, nil, false
	}
	p, hasP := d["perm_count"]
	t, hasT := d["temp_count"]
	if !hasP || !hasT || p == nil || t == nil {
		c.lastError = &Error{Type: "response", Message: "内部统计响应结构无效", HTTPCode: resp.StatusCode}
		return nil, nil, false
	}
	return p, t, true
}

func (c *Client) post(path string, params map[string]any) (SetResult, error) {
	c.lastError = nil
	payload, err := json.Marshal(params)
	if err != nil {
		c.lastError = &Error{Type: "json", Message: "内部请求编码失败"}
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, c.host+path, bytes.NewReader(payload))
	if err != nil {
		c.lastError = &Error{Type: "transport", Message: "内部请求构建失败"}
		return nil, err
	}
	c.buildHeaders(req, true)

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := c.http.Do(req)
	if err != nil {
		c.lastError = &Error{Type: "transport", Message: "cURL 错误：" + err.Error()}
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if err := c.checkResponse(resp, body); err != nil {
		c.lastError = err
		return nil, err
	}
	var d map[string]any
	if err := json.Unmarshal(body, &d); err != nil {
		c.lastError = &Error{Type: "json", Message: "响应 JSON 解析失败：" + err.Error(), HTTPCode: resp.StatusCode}
		return nil, err
	}
	if ok, _ := d["ok"].(bool); !ok {
		c.lastError = &Error{Type: "response", Message: "内部接口未确认操作成功", HTTPCode: resp.StatusCode}
		return nil, fmt.Errorf("内部接口未确认操作成功")
	}
	return d, nil
}

func (c *Client) checkResponse(resp *http.Response, body []byte) *Error {
	if len(bytes.TrimSpace(body)) == 0 {
		return &Error{Type: "http", Message: fmt.Sprintf("HTTP %d", resp.StatusCode), HTTPCode: resp.StatusCode}
	}
	if resp.StatusCode != 200 {
		reason := ""
		if resp.StatusCode == 403 {
			reason = "（IP 不在 allow 列表）"
		} else if resp.StatusCode == 500 {
			reason = "（OpenResty 内部错误）"
		}
		return &Error{Type: "http", Message: fmt.Sprintf("HTTP %d%s", resp.StatusCode, reason), HTTPCode: resp.StatusCode}
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.Contains(ct, "application/json") {
		return &Error{Type: "content_type", Message: "非 JSON 响应：" + ct, HTTPCode: resp.StatusCode}
	}
	if !json.Valid(body) {
		return &Error{Type: "json", Message: "响应 JSON 解析失败：invalid json", HTTPCode: resp.StatusCode}
	}
	return nil
}
