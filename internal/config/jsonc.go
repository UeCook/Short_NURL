package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// StripJSONC 去掉 // 与 /* */ 注释以及尾逗号，得到纯 JSON。
func StripJSONC(src []byte) ([]byte, error) {
	var out bytes.Buffer
	out.Grow(len(src))
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '"':
			// 字符串字面量原样拷贝（处理转义）
			out.WriteByte(c)
			i++
			for i < len(src) {
				out.WriteByte(src[i])
				if src[i] == '\\' {
					i++
					if i < len(src) {
						out.WriteByte(src[i])
						i++
					}
					continue
				}
				if src[i] == '"' {
					i++
					break
				}
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			i += 2
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			if i+1 < len(src) {
				i += 2
			} else {
				return nil, errors.New("未闭合的块注释")
			}
		default:
			out.WriteByte(c)
			i++
		}
	}

	// 去掉尾逗号：, 后面只允许空白，然后是 } 或 ]
	cleaned := out.Bytes()
	var out2 bytes.Buffer
	out2.Grow(len(cleaned))
	for i := 0; i < len(cleaned); i++ {
		if cleaned[i] == ',' {
			j := i + 1
			for j < len(cleaned) && (cleaned[j] == ' ' || cleaned[j] == '\t' || cleaned[j] == '\n' || cleaned[j] == '\r') {
				j++
			}
			if j < len(cleaned) && (cleaned[j] == '}' || cleaned[j] == ']') {
				continue
			}
		}
		out2.WriteByte(cleaned[i])
	}
	return out2.Bytes(), nil
}

// UnmarshalWithDefaults 解析 JSON 到已填充默认值的结构体（缺失字段保留默认）。
func UnmarshalWithDefaults(data []byte, cfg *Config) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return errors.New("配置文件为空")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw map[string]json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return fmt.Errorf("JSON 解析失败: %w", err)
	}
	// 重新用标准解码覆盖默认值
	if err := json.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("配置结构不匹配: %w", err)
	}
	return nil
}
