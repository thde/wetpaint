// Package names cleans up player-supplied names.
package names

import (
	"regexp"
	"strings"
	"unicode"
)

const (
	// MaxLen is the longest name, in characters.
	MaxLen = 16
	// Fallback is used when nothing printable is left.
	Fallback = "a little drop"
)

var tag = regexp.MustCompile(`<[^>]*>`)

// blocked is a deliberately small list; the game shows names to strangers.
var blocked = []string{
	"fuck", "shit", "bitch", "cunt", "nigger", "nigga", "faggot",
	"whore", "slut", "nazi", "hitler",
}

// leet folds common look-alike characters before matching blocked words.
var leet = strings.NewReplacer("0", "o", "1", "i", "3", "e", "4", "a", "5", "s", "@", "a", "$", "s", "!", "i")

// Sanitize strips HTML and control characters, collapses whitespace, masks
// blocked words and truncates to MaxLen characters. It never returns an empty
// string.
func Sanitize(name string) string {
	name = strings.ToValidUTF8(name, "")
	name = tag.ReplaceAllString(name, "")
	name = strings.Map(func(r rune) rune {
		switch {
		case r == '<' || r == '>' || r == '&' || r == '"' || r == '\'' || r == '`':
			return -1
		case unicode.IsSpace(r):
			return ' '
		case !unicode.IsPrint(r):
			return -1
		}
		return r
	}, name)

	runes := []rune(strings.Join(strings.Fields(name), " "))
	if len(runes) > MaxLen {
		runes = runes[:MaxLen]
	}
	runes = mask(runes)

	if name = strings.TrimSpace(string(runes)); name == "" {
		return Fallback
	}
	return name
}

// mask replaces every blocked word in runes with asterisks, in place.
func mask(runes []rune) []rune {
	// Fold rune by rune so indexes in folded line up with runes.
	folded := make([]rune, len(runes))
	for i, r := range runes {
		f := []rune(leet.Replace(string(unicode.ToLower(r))))
		folded[i] = f[0]
	}
	for _, word := range blocked {
		w := []rune(word)
		for i := 0; i+len(w) <= len(folded); i++ {
			if string(folded[i:i+len(w)]) != word {
				continue
			}
			for j := range w {
				runes[i+j], folded[i+j] = '*', '*'
			}
		}
	}
	return runes
}
