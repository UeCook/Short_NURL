package store_test

import (
	. "github.com/UeCook/Short_NURL/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPHPFixturesRead 读取 PHP JsonStore 真实写出的样本。
func TestPHPFixturesRead(t *testing.T) {
	fix := filepath.Join("..", "..", "fixtures")

	perm := New(filepath.Join(fix, "perm.json"), "+08:00")
	data, err := perm.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 2 {
		t.Fatalf("perm entries = %d", len(data))
	}
	entry, ok := data.Entry("abc1")
	if !ok {
		t.Fatal("abc1 missing")
	}
	if u, _ := FieldString(entry, "url"); u != "https://example.com/path?a=1" {
		t.Fatalf("url = %q", u)
	}
	if _, ok := data.Entry("中文"); !ok {
		t.Fatal("chinese key missing")
	}

	temp := New(filepath.Join(fix, "temp.json"), "+08:00")
	tdata, err := temp.Read()
	if err != nil {
		t.Fatal(err)
	}
	if CountActive(tdata) != 1 {
		t.Fatalf("active temp = %d", CountActive(tdata))
	}
	if n := CleanExpired(tdata); n != 1 {
		t.Fatalf("clean removed %d", n)
	}

	// PHP 空集合写的是 "d": []
	empty := New(filepath.Join(fix, "perm-empty.json"), "+08:00")
	edata, err := empty.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(edata) != 0 {
		t.Fatalf("empty d = %v", edata)
	}
}

// TestRoundTripReadable 由 Go 写出的文件必须仍可被 PHP 语义读取。
func TestRoundTripReadable(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join("..", "..", "fixtures", "perm.json")
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "perm.json")
	if err := os.WriteFile(dst, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	st := New(dst, "+08:00")
	err = st.ReadWrite(func(d Data) error {
		d["zz99"] = map[string]any{
			"id": "zz99", "url": "https://example.net/x",
			"lurl": "https://s.example.net/zz99", "t": "2099-01-01T00:00:00+08:00",
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	out, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	// PHP 的 JSON_UNESCAPED_SLASHES / UNESCAPED_UNICODE 等价
	if strings.Contains(text, `\/`) {
		t.Fatalf("slashes escaped:\n%s", text)
	}
	if !strings.Contains(text, "https://example.org/中文路径") {
		t.Fatalf("unicode lost:\n%s", text)
	}
	if strings.HasSuffix(text, "\n") {
		t.Fatalf("trailing newline:\n%s", text)
	}

	// 重新读取
	data, err := st.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 3 {
		t.Fatalf("entries = %d", len(data))
	}
	if !data.Has("zz99") || !data.Has("abc1") {
		t.Fatal("round trip lost entries")
	}
}
