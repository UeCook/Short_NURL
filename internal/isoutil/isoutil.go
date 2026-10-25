// Package isoutil 提供 ISO 8601 格式化、解析与过期判定，
// 行为与 v2.0.3 的 core/src/Helpers.php（formatIso8601 / isExpired）对齐。
package isoutil

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	// Windows 等无系统 tz 数据库的平台也能按 IANA 名称解析时区。
	_ "time/tzdata"
)

var (
	tzNameRe   = regexp.MustCompile(`^[A-Za-z]`)
	tzOffsetRe = regexp.MustCompile(`^([+-])(\d{2}):(\d{2})$`)
)

// ErrInvalidTimezone 时区配置无效。
var ErrInvalidTimezone = errors.New("无效时区")

// Format 将 Unix 秒格式化为带时区偏移的 ISO 8601 字符串。
// tz 支持 "+HH:MM" 偏移与 IANA 时区名（如 "Asia/Shanghai"）。
func Format(ts int64, tz string) (string, error) {
	if tzNameRe.MatchString(tz) {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			return "", fmt.Errorf("无效时区名称: %s", tz)
		}
		return time.Unix(ts, 0).In(loc).Format("2006-01-02T15:04:05-07:00"), nil
	}

	m := tzOffsetRe.FindStringSubmatch(tz)
	if m == nil {
		return "", fmt.Errorf("无效时区格式: %s", tz)
	}
	hh, _ := strconv.Atoi(m[2])
	mm, _ := strconv.Atoi(m[3])
	if hh > 23 || mm > 59 {
		return "", fmt.Errorf("无效时区偏移: %s", tz)
	}
	offset := hh*3600 + mm*60
	if m[1] == "-" {
		offset = -offset
	}
	return time.Unix(ts+int64(offset), 0).UTC().Format("2006-01-02T15:04:05") + tz, nil
}

// MustFormat 同 Format，失败时 panic（仅用于已通过校验的时区）。
func MustFormat(ts int64, tz string) string {
	s, err := Format(ts, tz)
	if err != nil {
		panic(err)
	}
	return s
}

var luaISORe = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})([+-])(\d{2}):(\d{2})$`)

// ParseStrict 按 Lua 冷启动的严格规则解析 `YYYY-MM-DDTHH:MM:SS±HH:MM`。
func ParseStrict(s string) (int64, bool) {
	m := luaISORe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	year, _ := strconv.Atoi(m[1])
	month, _ := strconv.Atoi(m[2])
	day, _ := strconv.Atoi(m[3])
	hour, _ := strconv.Atoi(m[4])
	min, _ := strconv.Atoi(m[5])
	sec, _ := strconv.Atoi(m[6])
	oh, _ := strconv.Atoi(m[8])
	om, _ := strconv.Atoi(m[9])

	if month < 1 || month > 12 || day < 1 || day > daysInMonth(year, month) ||
		hour > 23 || min > 59 || sec > 59 ||
		oh > 14 || om > 59 || (oh == 14 && om != 0) {
		return 0, false
	}
	offset := oh*3600 + om*60
	if m[7] == "-" {
		offset = -offset
	}
	t := time.Date(year, time.Month(month), day, hour, min, sec, 0, time.UTC)
	return t.Unix() - int64(offset), true
}

// Parse 宽松解析（对齐 PHP strtotime 的常见形态）。
func Parse(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	layouts := []string{
		"2006-01-02T15:04:05-07:00",
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Unix(), true
		}
		if layout == "2006-01-02T15:04:05-07:00" {
			if t, err := time.Parse("2006-01-02T15:04:05-0700", s); err == nil {
				return t.Unix(), true
			}
		}
	}
	return 0, false
}

// IsExpired 判断临时条目是否已过期；nil / "" / "0" 表示永久。
func IsExpired(exp any) bool {
	var s string
	switch v := exp.(type) {
	case nil:
		return false
	case string:
		s = v
	default:
		s = fmt.Sprint(v)
	}
	if s == "0" || s == "" {
		return false
	}
	ts, ok := Parse(s)
	if !ok {
		return true
	}
	return ts <= time.Now().Unix()
}

func isLeapYear(year int) bool {
	return (year%4 == 0 && year%100 != 0) || year%400 == 0
}

func daysInMonth(year, month int) int {
	days := []int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	if month == 2 && isLeapYear(year) {
		return 29
	}
	if month < 1 || month > 12 {
		return 0
	}
	return days[month-1]
}

// NormalizeOffset 校验 "+HH:MM" 形式偏移；非法返回错误。
func NormalizeOffset(tz string) error {
	if tzNameRe.MatchString(tz) {
		if _, err := time.LoadLocation(tz); err != nil {
			return fmt.Errorf("无效时区名称: %s", tz)
		}
		return nil
	}
	m := tzOffsetRe.FindStringSubmatch(tz)
	if m == nil {
		return fmt.Errorf("无效时区格式: %s", tz)
	}
	hh, _ := strconv.Atoi(m[2])
	mm, _ := strconv.Atoi(m[3])
	if hh > 23 || mm > 59 {
		return fmt.Errorf("无效时区偏移: %s", tz)
	}
	return nil
}

// IsOffset 报告 tz 是否为偏移形式（而非 IANA 名称）。
func IsOffset(tz string) bool {
	return !tzNameRe.MatchString(tz) && tzOffsetRe.MatchString(tz)
}

// String helper for logs.
func String(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}
