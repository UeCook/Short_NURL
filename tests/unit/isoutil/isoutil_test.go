package isoutil_test

import (
	. "github.com/UeCook/Short_NURL/internal/isoutil"
	"testing"
)

func TestFormatOffset(t *testing.T) {
	// 1761280000 = 2025-10-24T02:26:40Z
	got, err := Format(1761280000, "+08:00")
	if err != nil {
		t.Fatal(err)
	}
	want := "2025-10-24T12:26:40+08:00"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	got, err = Format(1761280000, "-05:00")
	if err != nil {
		t.Fatal(err)
	}
	if got != "2025-10-23T23:26:40-05:00" {
		t.Fatalf("got %q", got)
	}
}

func TestFormatZone(t *testing.T) {
	got, err := Format(1761280000, "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	if got != "2025-10-24T12:26:40+08:00" {
		t.Fatalf("got %q", got)
	}
}

func TestFormatInvalid(t *testing.T) {
	for _, tz := range []string{"", "08:00", "+8:00", "+24:00", "+12:60", "Nope/Zone"} {
		if _, err := Format(0, tz); err == nil {
			t.Fatalf("expected error for tz %q", tz)
		}
	}
}

func TestParseStrict(t *testing.T) {
	ts, ok := ParseStrict("2025-10-24T12:26:40+08:00")
	if !ok {
		t.Fatal("parse failed")
	}
	if ts != 1761280000 {
		t.Fatalf("got %d", ts)
	}
	for _, bad := range []string{
		"", "0", "2025-13-01T00:00:00+08:00", "2025-02-30T00:00:00+08:00",
		"2025-10-24T25:00:00+08:00", "2025-10-24T10:26:40", "2025-10-24 10:26:40+08:00",
	} {
		if _, ok := ParseStrict(bad); ok {
			t.Fatalf("expected failure for %q", bad)
		}
	}
}

func TestIsExpired(t *testing.T) {
	cases := []struct {
		in   any
		want bool
	}{
		{nil, false},
		{"", false},
		{"0", false},
		{"1970-01-01T00:00:01+00:00", true},
		{"not-a-date", true},
		{"2999-01-01T00:00:00+00:00", false},
	}
	for _, c := range cases {
		if got := IsExpired(c.in); got != c.want {
			t.Fatalf("IsExpired(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestNormalizeOffset(t *testing.T) {
	if err := NormalizeOffset("+08:00"); err != nil {
		t.Fatal(err)
	}
	if err := NormalizeOffset("Asia/Shanghai"); err != nil {
		t.Fatal(err)
	}
	if err := NormalizeOffset("+99:00"); err == nil {
		t.Fatal("expected error")
	}
}
