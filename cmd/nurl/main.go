// nurl — Short_NURL 本地管理工具。
package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/UeCook/Short_NURL/internal/apikey"
	"github.com/UeCook/Short_NURL/internal/config"
	"github.com/UeCook/Short_NURL/internal/store"
)

var version = "3.0.1_GitHub"

func versionBlock() string {
	return "Short_NURL v" + version + "\n---------------------------\nGitHub：https://github.com/UeCook/Short_NURL"
}

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
	hasNew := has("-new")
	hasLong := has("-long")
	hasFull := has("-full")
	hasSvc := has("-svc")
	hasItk := has("-itk")
	hasLook := has("-look")
	hasDrop := has("-drop")
	hasSweep := has("-sweep")
	hasV := has("-v")
	hasH := has("-h") || has("-help")

	if hasH {
		showHelp()
		return nil
	}
	if hasV {
		fmt.Println(versionBlock())
		return nil
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	keys := apikey.New(cfg.KeysPath, cfg.Site.TzOffset, cfg.Keys.TTLDays, cfg.Keys.OnetimePoolSize)
	tokenPath := cfg.InternalTokenPath

	if hasSweep {
		return doSweep(cfg)
	}

	if hasDrop {
		targets := 0
		for _, b := range []bool{hasLong, hasFull, hasSvc, hasItk} {
			if b {
				targets++
			}
		}
		if targets > 1 {
			return fmt.Errorf("-drop 一次只能指定一个目标。")
		}
		switch {
		case hasLong:
			if err := keys.Revoke("resident"); err != nil {
				return err
			}
			fmt.Println("常驻密钥已撤销。")
		case hasFull:
			if err := keys.Revoke("onetime"); err != nil {
				return err
			}
			fmt.Println("一次性密钥池已清空。")
		case hasSvc:
			if err := keys.Revoke("service"); err != nil {
				return err
			}
			fmt.Println("服务密钥已撤销。")
		case hasItk:
			if fileExists(tokenPath) {
				if err := os.Remove(tokenPath); err != nil {
					return fmt.Errorf("无法删除内部通信令牌: %s", tokenPath)
				}
				fmt.Println("内部通信令牌已删除。")
			} else {
				fmt.Println("内部通信令牌不存在。")
			}
		default:
			if err := keys.Revoke("all"); err != nil {
				return err
			}
			if fileExists(tokenPath) {
				if err := os.Remove(tokenPath); err != nil {
					return fmt.Errorf("无法删除内部通信令牌: %s", tokenPath)
				}
			}
			fmt.Println("所有密钥和内部通信令牌已撤销。")
		}
		return nil
	}

	// 保持 v1 优先级：drop > svc > itk > look > new/long/full。
	if hasSvc {
		label := "default"
		for _, arg := range args {
			if strings.HasPrefix(arg, "-label:") {
				label = arg[len("-label:"):]
				break
			}
		}
		serviceKey, err := keys.GenerateService(label)
		if err != nil {
			return fmt.Errorf("%s\n请先使用 nurl -drop -svc 撤销旧密钥。", err.Error())
		}
		fmt.Printf("新服务密钥：%s\n标签：%s\n", serviceKey, label)
		return nil
	}

	if hasItk {
		token, err := randomToken()
		if err != nil {
			return err
		}
		if err := writeInternalToken(tokenPath, token); err != nil {
			return err
		}
		fmt.Printf("新内部通信令牌：%s\n", token)
		return nil
	}

	if hasLook {
		return doLook(keys, tokenPath)
	}

	if hasLong && hasFull {
		return fmt.Errorf("-long 和 -full 不能同时使用。")
	}

	if hasNew {
		var residentKey string
		if !hasFull {
			k, err := keys.GenerateResident()
			if err != nil {
				return err
			}
			residentKey = k
		}
		var onetimeKeys []string
		if !hasLong {
			ks, err := keys.FillPool()
			if err != nil {
				return err
			}
			onetimeKeys = ks
		}
		var newToken string
		if !hasLong && !hasFull {
			existing := readTokenFile(tokenPath)
			if existing == "" {
				t, err := randomToken()
				if err != nil {
					return err
				}
				newToken = t
				if err := writeInternalToken(tokenPath, newToken); err != nil {
					return err
				}
			}
		}
		if residentKey != "" {
			fmt.Printf("新常驻密钥：%s\n", residentKey)
		}
		if len(onetimeKeys) > 0 {
			fmt.Println("新一次性密钥：")
			for i, k := range onetimeKeys {
				fmt.Printf("  [%d] %s\n", i, k)
			}
		}
		if newToken != "" {
			fmt.Printf("新内部通信令牌：%s\n", newToken)
		}
		return nil
	}

	if hasLong {
		k, err := keys.GenerateResident()
		if err != nil {
			return err
		}
		fmt.Printf("新常驻密钥：%s\n", k)
		return nil
	}

	if hasFull {
		onetimeKeys, err := keys.FillPool()
		if err != nil {
			return err
		}
		if len(onetimeKeys) > 0 {
			fmt.Println("新一次性密钥：")
			for i, k := range onetimeKeys {
				fmt.Printf("  [%d] %s\n", i, k)
			}
		} else {
			fmt.Println("一次性密钥池已满。")
		}
		return nil
	}

	fmt.Println("未知命令。使用 -h 查看帮助。")
	os.Exit(1)
	return nil
}

func loadConfig() (*config.Config, error) {
	path, err := config.FindConfigFile("")
	if err != nil {
		return nil, err
	}
	return config.Load(path)
}

func doLook(keys *apikey.KeyStore, tokenPath string) error {
	st, err := keys.Status()
	if err != nil {
		return err
	}
	fmt.Println("密钥状态：")
	fmt.Println("────────────────────────────────────────")
	fmt.Println("常驻密钥：")
	if st.Resident != nil {
		fmt.Printf("  前缀：%s\n", dash(st.Resident.KeyPrefix))
		fmt.Printf("  创建：%s\n", dash(st.Resident.Created))
		fmt.Printf("  过期：%s\n", dash(st.Resident.Expires))
	} else {
		fmt.Println("  （未设置）")
	}
	fmt.Println("────────────────────────────────────────")
	fmt.Println("服务密钥：")
	if st.Service != nil {
		fmt.Printf("  前缀：%s\n", dash(st.Service.KeyPrefix))
		fmt.Printf("  创建：%s\n", dash(st.Service.Created))
		fmt.Printf("  标签：%s\n", dash(st.Service.Label))
	} else {
		fmt.Println("  （未设置）")
	}
	fmt.Println("────────────────────────────────────────")
	fmt.Printf("一次性密钥池：%d/%d 可用\n", st.OnetimeCount, st.OnetimePoolSize)
	for i, slot := range st.OnetimePool {
		state := "可用"
		if slot.Consumed {
			state = "已消耗"
		} else if slot.Reserved {
			state = "预约中"
		}
		fmt.Printf("  [%d] %s (%s)\n", i, slot.Prefix, state)
	}
	fmt.Printf("预约中：%d\n", st.OnetimeReservedCount)
	token := readTokenFile(tokenPath)
	if token == "" {
		fmt.Println("内部通信令牌（LPA-Key）：（未设置）")
	} else {
		fmt.Printf("内部通信令牌（LPA-Key）：%s...（已设置）\n", first8(token))
	}
	return nil
}

func doSweep(cfg *config.Config) error {
	tempPath := cfg.TempPath
	if !fileExists(tempPath) {
		fmt.Println("临时存储文件不存在。")
		return nil
	}
	st := store.New(tempPath, cfg.Site.TzOffset)
	if err := st.LockBegin(); err != nil {
		return err
	}
	defer st.LockEnd()
	data, err := st.ReadLocked()
	if err != nil {
		return err
	}
	removed := store.CleanExpired(data)
	if removed > 0 {
		if err := st.WriteLocked(data); err != nil {
			return err
		}
		fmt.Printf("已清理 %d 条过期临时短链。\n", removed)
	} else {
		fmt.Println("没有过期的临时短链。")
	}
	return nil
}

func writeInternalToken(path, token string) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("无法创建目录: %s", dir)
		}
	}
	suffix, err := randomHex(6)
	if err != nil {
		return err
	}
	tmp := path + ".tmp." + suffix
	if err := os.WriteFile(tmp, []byte(token), 0o600); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("无法写入内部通信令牌: %s", tmp)
	}
	_ = os.Chmod(tmp, 0o600)
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("无法替换内部通信令牌: %s", path)
	}
	return nil
}

func readTokenFile(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func randomToken() (string, error) { return randomHex(32) }

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func first8(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func showHelp() {
	fmt.Println(`nurl — Short_NURL 本地管理工具

用法：nurl <标志>

标志：
  -new          生成常驻密钥 + 补充一次性密钥池
  -new -long    仅轮换常驻密钥
  -new -full    仅补充一次性密钥池
  -long         仅轮换常驻密钥
  -full         仅补充一次性密钥池
  -svc          生成服务密钥（永不过期，用于服务间集成）
  -svc -label:X 指定服务密钥标签（默认 default）
  -itk          生成/轮换内部通信令牌（LPA-Key）
  -look         显示密钥状态（不显示明文）
  -drop         撤销所有密钥（含内部令牌）
  -drop -long   仅删除常驻密钥
  -drop -full   仅删除所有一次性密钥
  -drop -svc    仅删除服务密钥
  -drop -itk    仅删除内部通信令牌
  -sweep        清理过期临时短链
  -v            查看版本
  -h / -help    显示帮助信息

所有操作均为纯本地文件读写，不依赖网络。
短链远程操作请使用：nurl-key`)
}
