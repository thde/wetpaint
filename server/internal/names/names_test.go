package names

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, in, want string
	}{
		{"plain", "Plip", "Plip"},
		{"trims and collapses whitespace", "  wet \t\n paint  ", "wet paint"},
		{"strips tags", "<b>bold</b> drop", "bold drop"},
		{"strips script", `<script>alert(1)</script>hi`, "alert(1)hi"},
		{"drops stray markup characters", `a<b & "c" > 'd'`, "a d"},
		{"unclosed tag", "x<img src=y", "ximg src=y"},
		{"truncates to 16 characters", "abcdefghijklmnopqrstuvwxyz", "abcdefghijklmnop"},
		{"counts characters not bytes", strings.Repeat("ü", 20), strings.Repeat("ü", 16)},
		{"keeps emoji", "drip 💧", "drip 💧"},
		{"control characters", "a\x00b\x1bc\u200b", "abc"},
		{"invalid utf-8", "ok\xff\xfe", "ok"},
		{"empty", "", Fallback},
		{"only markup", "<br>", Fallback},
		{"masks blocked words", "holy shit", "holy ****"},
		{"masks regardless of case", "FuCk this", "**** this"},
		{"masks leetspeak", "sh1t and 5hit", "**** and ****"},
		{"masks inside words", "xXnaziXx", "xX****Xx"},
		{"leaves innocent words", "grape juice", "grape juice"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Sanitize(tt.in)
			if got != tt.want {
				t.Errorf("Sanitize(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if n := utf8.RuneCountInString(got); n == 0 || n > MaxLen {
				t.Errorf("result has %d characters", n)
			}
		})
	}
}

func TestSanitizeIsIdempotent(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"<i>a</i>", "  x  y ", "sh1t", strings.Repeat("é ", 30), "<<b>>"} {
		once := Sanitize(in)
		if twice := Sanitize(once); twice != once {
			t.Errorf("Sanitize(%q) = %q, then %q", in, once, twice)
		}
	}
}
