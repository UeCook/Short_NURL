package link

import (
	"fmt"
	"strings"
	"testing"
)

func TestConfusableCodePairs(t *testing.T) {
	for _, c := range genAlphabet {
		if !confusableCodePair(byte(c), byte(c)) {
			t.Errorf("adjacent repeated character %q must be excluded", c)
		}
	}
	for _, pair := range []string{"vv", "ww", "uv", "vu", "uw", "wu", "vw", "wv", "rn", "rm", "nr", "nm", "mn", "mr"} {
		if !confusableCodePair(pair[0], pair[1]) {
			t.Errorf("visually ambiguous pair %q must be excluded", pair)
		}
	}
	for _, pair := range []string{"a2", "2a", "ab", "bc", "mu", "nu", "rv", "wr", "wm", "vn", "mj"} {
		if confusableCodePair(pair[0], pair[1]) {
			t.Errorf("readable pair %q must remain available", pair)
		}
	}
}

func TestRandomCodeReadability(t *testing.T) {
	for _, length := range []int{4, 5} {
		t.Run(fmt.Sprint(length), func(t *testing.T) {
			for sample := 0; sample < 10000; sample++ {
				code := randomCode(length)
				if len(code) != length {
					t.Fatalf("code %q has length %d, want %d", code, len(code), length)
				}
				for i := range code {
					if !strings.ContainsRune("23456789abcdefghjkmnpqrstuvwxyz", rune(code[i])) {
						t.Fatalf("code %q contains an excluded character", code)
					}
					if i > 0 && code[i-1] == code[i] {
						t.Fatalf("code %q contains adjacent repeated characters", code)
					}
				}
				for _, pair := range []string{"mn", "mr", "nm", "nr", "rm", "rn", "uv", "uw", "vu", "vw", "wu", "wv"} {
					if strings.Contains(code, pair) {
						t.Fatalf("code %q contains the visually ambiguous pair %q", code, pair)
					}
				}
			}
		})
	}
}
