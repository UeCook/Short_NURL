// nurl-key — Short_NURL 远程短链管理工具（走无头 API）。
package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/UeCook/Short_NURL/internal/config"
	"github.com/UeCook/Short_NURL/internal/httpguard"
)

var version = "3.0.1_GitHub"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "错误："+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		showHelp()
		return nil
	}

	has := func(flag string) bool {
		for _, a := range args {
			if a == flag {
				return true
			}
		}
		return false
	}
	hasPrefix := func(prefix string) bool {
		for _, a := range args {
			if strings.HasPrefix(a, prefix) {
				return true
			}
		}
		return false
	}

	hasH := has("-h") || has("-help")
	hasV := has("-v")
	hasList := has("-list")
	hasStat := has("-stat")
	hasGet := has("-get") || hasPrefix("-get:")
	hasAdd := has("-add")
	hasRm := has("-rm")
	insecureMode := has("-insecure")
	allowPlaintextHTTP := has("-allow-http")
	keyFromStdin := has("-key-stdin")

	key := extractValue(args, "-key")
	keyFile := extractValue(args, "-key-file")
	getCode := extractValue(args, "-get")
	addURL := extractValue(args, "-url")
	addTTL := extractValue(args, "-ttl")
	addCode := extractValue(args, "-code")
	addLen := extractValue(args, "-len")
	rmCode := extractValue(args, "-code")

	if hasH {
		showHelp()
		return nil
	}
	if hasV {
		fmt.Println("nurl-key v" + version)
		return nil
	}

	// 只有网络操作需要有效域名；帮助和版本查询应始终可用。
	for _, flag := range []string{"-key", "-key-file", "-get", "-url", "-ttl", "-code", "-len"} {
		if has(flag) && extractValue(args, flag) == nil {
			return fmt.Errorf("%s 缺少参数值。", flag)
		}
	}

	domain, err := loadDomain()
	if err != nil {
		return err
	}
	if strings.Contains(domain, "{") {
		return fmt.Errorf("config.json 的 domain 尚未配置（仍含占位符），请先修改。")
	}

	keyVal, err := resolveKey(key, keyFile, keyFromStdin)
	if err != nil {
		return err
	}
	if keyVal == "" {
		return fmt.Errorf("缺少密钥。可用 -key \"su_xxx\"、-key-file <文件>、-key-stdin 或环境变量 NURL_KEY 提供 API Key。")
	}

	if insecureMode {
		fmt.Fprintln(os.Stderr, "警告：-insecure 已关闭 TLS 证书校验，仅应在受控测试环境使用。")
	}
	if u, err := url.Parse(domain); err == nil &&
		strings.EqualFold(u.Scheme, "http") && !isLoopbackHost(u.Hostname()) {
		// 认证头是持有即授权的凭证，拒绝默认经明文链路发送（F-01 修复）。
		if !allowPlaintextHTTP {
			return fmt.Errorf("目标为明文 HTTP 且不是回环地址，API Key 将在未加密链路中传输；请改用 HTTPS，确需继续请显式加 -allow-http。")
		}
		fmt.Fprintln(os.Stderr, "警告：-allow-http 已允许在明文 HTTP 上传输 API Key，仅应在受控网络中继续。")
	}

	switch {
	case hasList:
		return doList(domain, keyVal, insecureMode)
	case hasStat:
		return doStat(domain, keyVal, insecureMode)
	case hasGet && getCode != nil && *getCode != "":
		return doGet(domain, keyVal, *getCode, insecureMode)
	case hasAdd:
		return doAdd(domain, keyVal, addURL, addTTL, addCode, addLen, insecureMode)
	case hasRm && rmCode != nil && *rmCode != "":
		return doRm(domain, keyVal, *rmCode, insecureMode)
	}
	fmt.Println("未知命令。使用 -h 查看帮助。")
	os.Exit(1)
	return nil
}

const maxKeyInputBytes = 4096

// resolveKey 按 -key、-key-file、-key-stdin、NURL_KEY 的顺序取密钥。
// 命令行参数会留在进程列表与 shell 历史中，故提供文件与标准输入两种更安全的来源。
func resolveKey(arg, file *string, fromStdin bool) (string, error) {
	if arg != nil {
		if k := strings.TrimSpace(*arg); k != "" {
			return k, nil
		}
	}
	if file != nil {
		path := strings.TrimSpace(*file)
		if path != "" {
			f, err := os.Open(path)
			if err != nil {
				return "", fmt.Errorf("读取密钥文件失败：%s", err.Error())
			}
			defer f.Close()
			data, err := io.ReadAll(io.LimitReader(f, maxKeyInputBytes+1))
			if err != nil {
				return "", fmt.Errorf("读取密钥文件失败：%s", err.Error())
			}
			if len(data) > maxKeyInputBytes {
				return "", fmt.Errorf("密钥文件过大（上限 %d 字节）：%s", maxKeyInputBytes, path)
			}
			k := strings.TrimSpace(string(data))
			if k == "" {
				return "", fmt.Errorf("密钥文件为空：%s", path)
			}
			return k, nil
		}
	}
	if fromStdin {
		data, err := io.ReadAll(io.LimitReader(os.Stdin, maxKeyInputBytes+1))
		if err != nil {
			return "", fmt.Errorf("读取标准输入失败：%s", err.Error())
		}
		if len(data) > maxKeyInputBytes {
			return "", fmt.Errorf("标准输入内容过长（上限 %d 字节）", maxKeyInputBytes)
		}
		k := strings.TrimSpace(string(data))
		if k == "" {
			return "", fmt.Errorf("标准输入中没有密钥")
		}
		return k, nil
	}
	return strings.TrimSpace(os.Getenv("NURL_KEY")), nil
}

// responseSizeLimit 返回单次响应允许读入内存的最大字节数（默认 8 MiB），
// 可用 NURL_MAX_RESPONSE_BYTES 覆盖（1024 ~ 1 GiB）。
func responseSizeLimit() int64 {
	const def int64 = 8 << 20
	if v := strings.TrimSpace(os.Getenv("NURL_MAX_RESPONSE_BYTES")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 1024 && n <= 1<<30 {
			return n
		}
	}
	return def
}

func loadDomain() (string, error) {
	path, err := config.FindConfigFile("")
	if err != nil {
		return "", err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return "", err
	}
	return cfg.Site.Domain, nil
}

// extractValue 支持 -flag value 与 -flag:value 两种写法。
func extractValue(args []string, flag string) *string {
	for i, arg := range args {
		if strings.HasPrefix(arg, flag+":") {
			v := arg[len(flag)+1:]
			return &v
		}
		if arg == flag {
			if i+1 < len(args) {
				next := args[i+1]
				if !flagLikeRe.MatchString(next) {
					return &next
				}
			}
			return nil
		}
	}
	return nil
}

var flagLikeRe = regexp.MustCompile(`^-[a-zA-Z]`)

func apiRequest(domain, key, method, path string, body any, insecure bool) (map[string]any, error) {
	var reader io.Reader
	if method == http.MethodPost && body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, domain+path, reader)
	if err != nil {
		return nil, fmt.Errorf("cURL 初始化失败")
	}
	req.Header.Set("X-Headless-Token", key)
	req.Header.Set("Content-Type", "application/json")

	transport := &http.Transport{
		DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
	}
	if insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
		// 认证头是持有即授权的凭证：只允许同源重定向，绝不把
		// X-Headless-Token 转发到其他主机，也不允许 HTTPS 降级。
		CheckRedirect: httpguard.SameOriginOnly(domain),
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cURL 错误：%s", err.Error())
	}
	defer resp.Body.Close()
	// 响应大小有上限：避免被控制的服务端或中间人用超大响应耗尽客户端内存（F-05 修复）。
	limit := responseSizeLimit()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败：%s", err.Error())
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("响应超过 %d 字节上限，已中止；如需放宽请设置 NURL_MAX_RESPONSE_BYTES", limit)
	}

	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("响应解析失败 (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		msg := "未知错误"
		if m, ok := data["message"].(string); ok && m != "" {
			msg = m
		} else if m, ok := data["error"].(string); ok && m != "" {
			msg = m
		}
		return nil, fmt.Errorf("%s (HTTP %d)", msg, resp.StatusCode)
	}
	return data, nil
}

// isLoopbackHost 判断目标主机是否为回环地址（用于明文 HTTP 告警）。
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func doList(domain, key string, insecure bool) error {
	result, err := apiRequest(domain, key, http.MethodGet, "/headless/api/list", nil, insecure)
	if err != nil {
		return err
	}

	var permanent, temporary []map[string]any
	if _, ok := result["permanent"]; ok {
		permanent = toMaps(result["permanent"])
	}
	if _, ok := result["temporary"]; ok {
		temporary = toMaps(result["temporary"])
	}
	if _, hasP := result["permanent"]; !hasP {
		if _, hasT := result["temporary"]; !hasT {
			for _, item := range toMaps(result) {
				if exp, _ := item["exp"].(string); exp == "permanent" {
					permanent = append(permanent, item)
				} else {
					temporary = append(temporary, item)
				}
			}
		}
	}

	fmt.Println("短链列表")
	fmt.Println(strings.Repeat("─", 60))
	fmt.Println()

	fmt.Printf("永久短链（%d 条）：\n", len(permanent))
	if len(permanent) == 0 {
		fmt.Println("  （空）")
	} else {
		for i, item := range permanent {
			fmt.Printf("  %d. %s  →  %s\n", i+1, str(item["id"]), str(item["url"]))
			fmt.Printf("     %s\n", str(item["lurl"]))
		}
	}
	fmt.Println()

	fmt.Printf("临时短链（%d 条）：\n", len(temporary))
	if len(temporary) == 0 {
		fmt.Println("  （空）")
	} else {
		for i, item := range temporary {
			fmt.Printf("  %d. %s  →  %s\n", i+1, str(item["id"]), str(item["url"]))
			fmt.Printf("     %s\n", str(item["lurl"]))
			exp := str(item["exp"])
			if exp == "" {
				exp = "未知"
			}
			fmt.Printf("     过期：%s\n", exp)
		}
	}
	fmt.Println()
	return nil
}

func doStat(domain, key string, insecure bool) error {
	result, err := apiRequest(domain, key, http.MethodGet, "/headless/api/stat", nil, insecure)
	if err != nil {
		return err
	}
	fmt.Println("配额状态：")
	fmt.Println("────────────────────────────────────────")
	fmt.Printf("永久短链：%v/%v\n", result["perm_count"], result["perm_limit"])
	fmt.Printf("临时短链：%v/%v\n", result["temp_count"], result["temp_limit"])
	if _, ok := result["hot_perm_count"]; ok {
		fmt.Println("────────────────────────────────────────")
		fmt.Println("热存储（诊断）：")
		fmt.Printf("  永久：%v\n", result["hot_perm_count"])
		fmt.Printf("  临时：%v\n", result["hot_temp_count"])
	}
	return nil
}

func doGet(domain, key, code string, insecure bool) error {
	result, err := apiRequest(domain, key, http.MethodGet, "/headless/api/get/"+url.PathEscape(code), nil, insecure)
	if err != nil {
		return err
	}
	fmt.Println("短链信息：")
	fmt.Println("────────────────────────────────────────")
	fmt.Printf("短码：%v\n", result["id"])
	fmt.Printf("目标：%v\n", result["url"])
	fmt.Printf("短链：%v\n", result["lurl"])
	fmt.Printf("过期：%v\n", result["exp"])
	return nil
}

func doAdd(domain, key string, u, ttl, code, length *string, insecure bool) error {
	if u == nil || *u == "" {
		return fmt.Errorf("缺少 -url 参数。")
	}
	if ttl != nil {
		if ok, err := validTTL(*ttl); err != nil || !ok {
			return fmt.Errorf("-ttl 必须为可表示的非负整数。")
		}
	}
	if length != nil && *length != "4" && *length != "5" {
		return fmt.Errorf("-len 必须为 4 或 5。")
	}
	body := map[string]any{"url": *u, "ttl": 0}
	if ttl != nil {
		n, _ := strconv.ParseInt(*ttl, 10, 64)
		body["ttl"] = n
	}
	if code != nil && *code != "" {
		body["code"] = *code
	}
	if length != nil && *length != "" {
		n, _ := strconv.Atoi(*length)
		body["len"] = n
	}

	result, err := apiRequest(domain, key, http.MethodPost, "/headless/api/create", body, insecure)
	if err != nil {
		return err
	}
	fmt.Println("短链创建成功：")
	fmt.Println("────────────────────────────────────────")
	fmt.Printf("短链：%v\n", result["short_url"])
	expires := "永久"
	if v, ok := result["exp"]; ok && v != nil {
		expires = fmt.Sprintf("%v", v)
	}
	fmt.Printf("过期：%s\n", expires)
	if b, _ := result["dedup"].(bool); b {
		fmt.Println("（已存在相同目标的短链，返回已有短码）")
	}
	if v, ok := result["synced"]; ok {
		if b, isBool := v.(bool); isBool && !b {
			warning := "短链已保存，但未确认热存储同步，可能暂时无法访问"
			if s, ok := result["warning"].(string); ok && s != "" {
				warning = s
			}
			fmt.Fprintln(os.Stderr, "警告："+warning)
		}
	}
	if b, _ := result["key_consumed"].(bool); b {
		fmt.Println("注：本次请求已消费一次性 Key，该 Key 不可再次使用。")
	}
	return nil
}

func doRm(domain, key, code string, insecure bool) error {
	result, err := apiRequest(domain, key, http.MethodPost, "/headless/api/delete",
		map[string]any{"code": code}, insecure)
	if err != nil {
		return err
	}
	fmt.Printf("短链已删除：%s\n", code)
	if v, ok := result["synced"]; ok {
		if b, isBool := v.(bool); isBool && !b {
			warning := "冷记录已删除，但热存储未确认同步，原短链可能仍可访问"
			if s, ok := result["warning"].(string); ok && s != "" {
				warning = s
			}
			fmt.Fprintln(os.Stderr, "警告："+warning)
		}
	}
	return nil
}

// validTTL 对齐 PHP 的 ctype_digit + int64 上限判断。
func validTTL(s string) (bool, error) {
	if s == "" {
		return false, nil
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false, nil
		}
	}
	trimmed := strings.TrimLeft(s, "0")
	maxStr := strconv.FormatInt(int64(^uint64(0)>>1), 10)
	if len(trimmed) > len(maxStr) {
		return false, nil
	}
	if len(trimmed) == len(maxStr) && trimmed > maxStr {
		return false, nil
	}
	return true, nil
}

func toMaps(v any) []map[string]any {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(arr))
	for _, item := range arr {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func str(v any) string {
	if v == nil {
		return "-"
	}
	if s, ok := v.(string); ok {
		if s == "" {
			return "-"
		}
		return s
	}
	return fmt.Sprintf("%v", v)
}

func showHelp() {
	fmt.Println(`nurl-key — Short_NURL 远程短链管理工具

用法：nurl-key [密钥来源] <操作> [参数]

密钥来源（按优先级，取第一个非空值）：
  -key "su_xxx"            直接传参：会留在进程列表与命令历史，仅建议临时使用
  -key-file <文件>         从文件读取（建议 chmod 600）
  -key-stdin               从标准输入读取，适合管道与自动化
  NURL_KEY                 环境变量，不进入命令历史

操作：
  -list                    查看所有短链
  -stat                    查看配额状态
  -get "code"              查看单条短链
  -add -url "https://..." -ttl 0 [-code "abc"]
                           创建短链（-code 可选，不填自动生成）
  -rm -code "abc"          删除短链
  -h / -help               显示帮助信息
  -v                       查看版本

选项：
  -insecure                跳过 SSL 证书验证（仅用于测试环境）
  -allow-http              允许向非回环的明文 HTTP 目标发送密钥（默认直接拒绝）
  -len 4|5                 服务密钥生成的短码长度
  参数支持 -flag value 和 -flag:value 两种写法。
  响应体超过 NURL_MAX_RESPONSE_BYTES（默认 8 MiB）时报错退出。

所有操作通过无头 API（headless）远程执行，需要网络连接。
本地管理请使用：nurl`)
}
