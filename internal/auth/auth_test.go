package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UeCook/Short_NURL/internal/apikey"
)

func TestCredentialFormatBoundsAndAlphabet(t *testing.T) {
	for _, suffix := range []string{strings.Repeat("1", 32), strings.Repeat("z", 44), "123456789ABCDEFGHJKLMNPQRSTUVWXYZabc"} {
		if !validCredentialFormat("su_" + suffix) {
			t.Errorf("valid Base58 shape rejected: %q", suffix)
		}
	}
	for _, value := range []string{"", "su_bad", "su_" + strings.Repeat("1", 31), "su_" + strings.Repeat("1", 45), "xx_" + strings.Repeat("1", 32), "su_" + strings.Repeat("1", 31) + "0", "su_" + strings.Repeat("1", 31) + "O", "su_" + strings.Repeat("1", 31) + " ", "su_" + strings.Repeat("1", 31) + "密"} {
		if validCredentialFormat(value) {
			t.Errorf("invalid shape accepted: %q", value)
		}
	}
}

func TestMalformedCredentialDoesNotReadCorruptKeyStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	if err := os.WriteFile(path, []byte("not JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(apikey.New(path, "+08:00", 7, 20))
	if result := a.Authenticate("su_bad", []string{"resident"}); result.Reason != "invalid" {
		t.Fatalf("malformed credential reached storage: %+v", result)
	}
	if result := a.Authenticate("su_"+strings.Repeat("1", 44), []string{"resident"}); result.Reason != "backend_error" {
		t.Fatalf("well-formed credential must still verify against storage: %+v", result)
	}
}
