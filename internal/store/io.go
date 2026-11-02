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

type fileWrite struct {
	path       string
	payload    []byte
	mode       os.FileMode // 0 表示保留原文件权限，新文件使用 0644。
	verifyJSON bool
}

// pending 保留本次写入前的快照，回滚不依赖可能来自历史操作的 .bak。
type pending struct {
	fileWrite
	tmp          string
	before       []byte
	existed      bool
	originalMode os.FileMode
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

// WriteFileAtomic 是单文件安全写入入口；调用方负责持有该文件的操作锁。
// mode 为 0 时保留权限；verifyJSON 为 true 时拒绝非法或 null JSON。
func WriteFileAtomic(path string, payload []byte, mode os.FileMode, verifyJSON bool) error {
	return writeFilesAtomic([]fileWrite{{path: path, payload: payload, mode: mode, verifyJSON: verifyJSON}})
}

func writeFilesAtomic(writes []fileWrite) error {
	prepared, err := prepareWrites(writes)
	if err != nil {
		return err
	}
	return commitWrites(prepared)
}

// prepareWrites 在替换任何目标前完成快照、备份和临时文件的落盘。
func prepareWrites(writes []fileWrite) (prepared []pending, err error) {
	defer func() {
		if err != nil {
			cleanupPrepared(prepared)
		}
	}()
	seen := make(map[string]bool, len(writes))
	for _, write := range writes {
		if seen[write.path] {
			return prepared, fmt.Errorf("重复的写入路径: %s", write.path)
		}
		seen[write.path] = true
		if write.verifyJSON && (!json.Valid(write.payload) || bytes.Equal(bytes.TrimSpace(write.payload), []byte("null"))) {
			return prepared, fmt.Errorf("待写入 JSON 无效: %s", write.path)
		}
		if err := os.MkdirAll(filepath.Dir(write.path), 0o755); err != nil {
			return prepared, fmt.Errorf("无法创建数据目录: %w", err)
		}
		p := pending{fileWrite: write, originalMode: 0o644}
		info, statErr := os.Stat(write.path)
		if statErr == nil {
			p.existed = true
			p.originalMode = info.Mode().Perm()
			p.before, err = os.ReadFile(write.path)
			if err != nil {
				return prepared, fmt.Errorf("读取写入前快照失败: %w", err)
			}
		} else if !os.IsNotExist(statErr) {
			return prepared, statErr
		}
		if p.mode == 0 {
			p.mode = p.originalMode
		}
		if p.existed {
			backupTmp, err := writeTempFile(p.path+".bak", p.before, 0o600)
			if err != nil {
				return prepared, err
			}
			if err := renameTemp(backupTmp, p.path+".bak"); err != nil {
				_ = os.Remove(backupTmp)
				return prepared, fmt.Errorf("备份失败: %w", err)
			}
		}
		p.tmp, err = writeTempFile(p.path, p.payload, p.mode)
		if err != nil {
			return prepared, err
		}
		prepared = append(prepared, p)
	}
	return prepared, nil
}

// commitWrites 依次替换并回读，任一步失败都恢复本次已提交文件。
// 多文件替换之间有可见窗口，此处保证的是同步错误回滚。
func commitWrites(prepared []pending) error {
	defer cleanupPrepared(prepared)
	for i, p := range prepared {
		if err := renameTemp(p.tmp, p.path); err != nil {
			return errors.Join(err, rollbackWrites(prepared[:i]))
		}
		raw, err := os.ReadFile(p.path)
		if err == nil && !bytes.Equal(raw, p.payload) {
			err = errors.New("回读内容与写入内容不一致")
		}
		if err != nil {
			return errors.Join(fmt.Errorf("写入验证失败: %s: %w", p.path, err), rollbackWrites(prepared[:i+1]))
		}
	}
	return nil
}

func rollbackWrites(committed []pending) error {
	var failures []error
	for i := len(committed) - 1; i >= 0; i-- {
		p := committed[i]
		var err error
		if !p.existed {
			err = os.Remove(p.path)
			if os.IsNotExist(err) {
				err = nil
			}
		} else {
			var tmp string
			tmp, err = writeTempFile(p.path, p.before, p.originalMode)
			if err == nil {
				err = renameTemp(tmp, p.path)
				_ = os.Remove(tmp)
			}
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("回滚失败: %s: %w", p.path, err))
		}
	}
	return errors.Join(failures...)
}

func cleanupPrepared(prepared []pending) {
	for _, p := range prepared {
		_ = os.Remove(p.tmp)
	}
}

// writeTempFile 使用同目录唯一临时文件，设置权限并 fsync 后再交给提交阶段。
func writeTempFile(path string, payload []byte, mode os.FileMode) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".go.tmp-*")
	if err != nil {
		return "", fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmp := f.Name()
	if _, err = f.Write(payload); err == nil {
		err = f.Chmod(mode)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err = errors.Join(err, closeErr); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("写入临时文件失败: %w", err)
	}
	return tmp, nil
}

// renameTemp 原子替换；Windows 上共享读句柄可能暂时阻止替换，做有限重试。
func renameTemp(tmp, path string) error {
	var err error
	for i := 0; i < 20; i++ {
		if err = os.Rename(tmp, path); err == nil {
			return nil
		}
		time.Sleep(time.Millisecond)
	}
	return fmt.Errorf("rename 失败: %s → %s: %w", tmp, path, err)
}
