package apikey_test

import (
	. "github.com/UeCook/Short_NURL/internal/apikey"
	"testing"
)

func TestBase58Encode(t *testing.T) {
	cases := []struct {
		in   []byte
		want string
	}{
		{nil, ""},
		{[]byte{}, ""},
		{[]byte{0}, "1"},
		{[]byte{0, 0}, "11"},
		{[]byte("a"), "2g"},
		{[]byte{0, 1}, "12"},
	}
	for _, c := range cases {
		if got := Base58Encode(c.in); got != c.want {
			t.Fatalf("Base58Encode(%v) = %q, want %q", c.in, got, c.want)
		}
	}
	// 32 字节随机密钥必须只含 Base58 字符集且长度稳定
	out := Base58Encode(make([]byte, 32))
	if out != "11111111111111111111111111111111" {
		t.Fatalf("all-zero 32 bytes = %q", out)
	}
}
