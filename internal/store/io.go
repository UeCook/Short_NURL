package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// pending 双 Store 提交阶段的临时文件记录。
type pending struct {
	st  *Store
	tmp string
}

func nowUnix() int64 { return time.Now().Unix() }

// EncodePretty 生成 PHP JSON_PRETTY_PRINT|UNESCAPED_SLASHES|UNESCAPED_UNICODE 的等价输出：
// 4 空格缩进、不转义 `/` 与 `<>&`、末尾无换行。
func EncodePretty(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "    ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// EncodeCompact 生成 HTTP 响应用的紧凑 JSON（不转义 `/` 与 HTML 字符）。
func EncodeCompact(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func decodeData(raw []byte, path string) (Data, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("数据文件 JSON 损坏: %s", path)
	}
	rawD, ok := root["d"]
	if !ok {
		return nil, fmt.Errorf("数据文件 JSON 损坏: %s", path)
	}
	d, err := decodeDataField(rawD)
	if err != nil {
		return nil, fmt.Errorf("数据文件 JSON 损坏: %s", path)
	}
	return d, nil
}

// decodeDataField 兼容 PHP 的 `d` 形态：对象或数组（PHP 的 [] 与 {} 都解为数组）。
func decodeDataField(raw json.RawMessage) (Data, error) {
	trim := bytes.TrimSpace(raw)
	if bytes.Equal(trim, []byte("null")) {
		return nil, errors.New("invalid d")
	}
	if bytes.Equal(trim, []byte("[]")) || bytes.Equal(trim, []byte("{}")) {
		return Data{}, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(trim, &obj); err == nil {
		if obj == nil {
			obj = map[string]any{}
		}
		return Data(obj), nil
	}
	var arr []any
	if err := json.Unmarshal(trim, &arr); err == nil {
		d := Data{}
		for i, v := range arr {
			d[fmt.Sprintf("%d", i)] = v
		}
		return d, nil
	}
	return nil, errors.New("invalid d")
}

// writeAtomic 执行「备份 → 写 tmp → fsync → rename → 回读校验 → 失败回滚」。
func writeAtomic(path string, payload []byte) error {
	// 备份当前文件（.bak 仅供 Go 回滚，不参与 Lua 读取，收紧为 0600）
	if current, err := os.ReadFile(path); err == nil && len(current) > 0 {
		bak := path + ".bak"
		if err := os.WriteFile(bak, current, 0o600); err != nil {
			return fmt.Errorf("备份失败：%s", bak)
		}
		_ = os.Chmod(bak, 0o600)
	}

	tmp, err := writeTempFile(path, payload)
	if err != nil {
		return err
	}
	if err := renameTemp(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := verifyFile(path); err != nil {
		if rerr := restoreBackup(path); rerr != nil {
			return fmt.Errorf("写入验证失败且回滚失败：%s", path)
		}
		return fmt.Errorf("写入验证失败，已回滚：%s", path)
	}
	return nil
}

// writeTempFile 写 <path>.go.tmp 并 fsync，保留原文件权限。
func writeTempFile(path string, payload []byte) (string, error) {
	tmp := path + ".go.tmp"
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return "", fmt.Errorf("写入临时文件失败：%s", tmp)
	}
	if _, err := f.Write(payload); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return "", fmt.Errorf("写入临时文件失败：%s", tmp)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return "", fmt.Errorf("写入临时文件失败：%s", tmp)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("写入临时文件失败：%s", tmp)
	}
	_ = os.Chmod(tmp, mode)
	return tmp, nil
}

// renameTemp 原子替换（Windows 上并发读句柄会让 rename 偶发失败，做有限重试）。
func renameTemp(tmp, path string) error {
	var err error
	for i := 0; i < 20; i++ {
		err = os.Rename(tmp, path)
		if err == nil {
			return nil
		}
		time.Sleep(time.Millisecond)
	}
	_ = os.Remove(tmp)
	return fmt.Errorf("rename 失败：%s → %s", tmp, path)
}

func verifyFile(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("写入验证失败：%s", path)
	}
	trim := bytes.TrimSpace(raw)
	if len(trim) == 0 || bytes.Equal(trim, []byte("null")) || !json.Valid(trim) {
		return fmt.Errorf("写入验证失败：%s", path)
	}
	return nil
}

// restoreBackup 用 .bak 回滚。
func restoreBackup(path string) error {
	bak, err := os.ReadFile(path + ".bak")
	if err != nil {
		return err
	}
	tmp, err := writeTempFile(path, bak)
	if err != nil {
		return err
	}
	return renameTemp(tmp, path)
}

// WriteFileAtomic 把内容写入指定文件；verifyJSON 为 true 时按 JSON 校验（keys.json）。
func WriteFileAtomic(path string, payload []byte, mode os.FileMode, verifyJSON bool) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("无法创建目录: %s", dir)
		}
	}
	if current, err := os.ReadFile(path); err == nil && len(current) > 0 {
		bak := path + ".bak"
		_ = os.WriteFile(bak, current, mode)
		// 已存在的 .bak 不会被 WriteFile 改权限，显式收紧。
		_ = os.Chmod(bak, mode)
	}
	tmp := fmt.Sprintf("%s.go.tmp", path)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("写入临时文件失败：%s", tmp)
	}
	if _, err := f.Write(payload); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("写入临时文件失败：%s", tmp)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("写入临时文件失败：%s", tmp)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	_ = os.Chmod(tmp, mode)
	if err := renameTemp(tmp, path); err != nil {
		return err
	}
	if verifyJSON {
		if err := verifyFile(path); err != nil {
			if rerr := restoreBackup(path); rerr != nil {
				return fmt.Errorf("写入验证失败且回滚失败：%s", path)
			}
			return fmt.Errorf("写入验证失败，已回滚：%s", path)
		}
	}
	return nil
}
