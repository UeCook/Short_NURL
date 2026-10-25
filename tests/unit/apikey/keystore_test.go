package apikey_test

import (
	. "github.com/UeCook/Short_NURL/internal/apikey"
	"os"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) (*KeyStore, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")
	return New(path, "+08:00", 7, 2), path
}

func TestKeyLifecycle(t *testing.T) {
	k, path := newTestStore(t)

	raw, err := k.GenerateResident()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 10 || raw[:3] != "su_" {
		t.Fatalf("unexpected key %q", raw)
	}

	res := k.Verify(raw, []string{"resident"})
	if !res.OK || res.KeyType != "resident" {
		t.Fatalf("verify resident: %+v", res)
	}

	if r := k.Verify(raw, []string{"onetime"}); r.Error != "wrong_channel" {
		t.Fatalf("want wrong_channel, got %+v", r)
	}
	if r := k.Verify("su_nope", []string{"resident"}); r.Error != "invalid" {
		t.Fatalf("want invalid, got %+v", r)
	}
	if r := k.Verify("", []string{"resident"}); r.Error != "invalid" {
		t.Fatalf("empty key should be invalid")
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestOnetimeReservation(t *testing.T) {
	k, _ := newTestStore(t)
	keys, err := k.FillPool()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("pool size %d", len(keys))
	}

	res := k.Verify(keys[0], []string{"onetime"})
	if !res.OK || res.ReservationToken == "" {
		t.Fatalf("reservation failed: %+v", res)
	}

	// 预约中的 Key 不能重复使用
	if r := k.Verify(keys[0], []string{"onetime"}); r.Error != "invalid" {
		t.Fatalf("reserved key should be invalid, got %+v", r)
	}

	// 失败释放 → 可重试
	if !k.CompleteOnetime(res.ReservationToken, false) {
		t.Fatal("complete(false) failed")
	}
	res2 := k.Verify(keys[0], []string{"onetime"})
	if !res2.OK {
		t.Fatalf("should be reusable: %+v", res2)
	}

	// 成功消费 → 不可复用
	if !k.CompleteOnetime(res2.ReservationToken, true) {
		t.Fatal("complete(true) failed")
	}
	if r := k.Verify(keys[0], []string{"onetime"}); r.Error != "invalid" {
		t.Fatalf("consumed key should be invalid, got %+v", r)
	}

	st, err := k.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.OnetimeCount != 1 || st.OnetimePool[0].Consumed != true {
		t.Fatalf("status: %+v", st)
	}
}

func TestRevoke(t *testing.T) {
	k, _ := newTestStore(t)
	raw, err := k.GenerateResident()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.GenerateService("default"); err != nil {
		t.Fatal(err)
	}
	if _, err := k.GenerateService("again"); err == nil {
		t.Fatal("duplicate service key must fail")
	}
	if err := k.Revoke("service"); err != nil {
		t.Fatal(err)
	}
	if r := k.Verify(raw, []string{"resident"}); !r.OK {
		t.Fatal("resident must survive service revoke")
	}
	if err := k.Revoke("all"); err != nil {
		t.Fatal(err)
	}
	if r := k.Verify(raw, []string{"resident"}); r.Error != "invalid" {
		t.Fatalf("after revoke all: %+v", r)
	}
}
