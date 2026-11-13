package names

import (
	"math/rand/v2"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestWordLists(t *testing.T) {
	t.Parallel()
	for _, list := range []struct {
		name   string
		words  []string
		maxLen int
	}{
		{"adjectives", adjectives, 7},
		{"nouns", nouns, 8},
	} {
		seen := make(map[string]bool, len(list.words))
		for _, w := range list.words {
			if seen[w] {
				t.Errorf("%s: %q appears twice", list.name, w)
			}
			seen[w] = true
			if n := utf8.RuneCountInString(w); n == 0 || n > list.maxLen {
				t.Errorf("%s: %q has %d letters, want 1..%d", list.name, w, n, list.maxLen)
			}
			for i, r := range w {
				if r > unicode.MaxASCII || !unicode.IsLetter(r) || (i == 0) != unicode.IsUpper(r) {
					t.Errorf("%s: %q is not a capitalised ASCII word", list.name, w)
					break
				}
			}
		}
	}
	if Count < 1000 {
		t.Errorf("only %d names", Count)
	}
}

func TestGenerate(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(1, 2))
	for range 1000 {
		name := Generate(rng, nil)
		if n := utf8.RuneCountInString(name); n > MaxLen {
			t.Fatalf("%q has %d characters, want at most %d", name, n, MaxLen)
		}
		if words := strings.Fields(name); len(words) != 2 {
			t.Fatalf("%q is not two words", name)
		}
	}
}

func TestGenerateAvoidsTakenNames(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(3, 4))
	taken := make(map[string]bool)
	for range 60 { // a full page
		name := Generate(rng, func(s string) bool { return taken[s] })
		if taken[name] {
			t.Fatalf("%q handed out twice", name)
		}
		taken[name] = true
	}
}

func TestGenerateGivesUpWhenEverythingIsTaken(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(5, 6))
	calls := 0
	name := Generate(rng, func(string) bool { calls++; return true })
	if name == "" || calls != attempts {
		t.Errorf("got %q after %d checks, want a name after %d", name, calls, attempts)
	}
}

func TestFromSeedIsStable(t *testing.T) {
	t.Parallel()
	for seed := range uint32(100) {
		if a, b := FromSeed(seed, nil), FromSeed(seed, nil); a != b {
			t.Fatalf("seed %d gave %q, then %q", seed, a, b)
		}
	}
	// Pinned so that a change to the mapping, which renames every returning
	// player, is deliberate.
	if got, want := FromSeed(1, nil), "Mellow Easel"; got != want {
		t.Errorf("FromSeed(1) = %q, want %q", got, want)
	}
}

func TestFromSeedFallsBackDeterministically(t *testing.T) {
	t.Parallel()
	first := FromSeed(9, nil)
	taken := func(s string) bool { return s == first }
	second := FromSeed(9, taken)
	if second == first || FromSeed(9, taken) != second {
		t.Errorf("with %q taken, seed 9 gave %q", first, second)
	}
}

func TestSeedsSpreadOverNames(t *testing.T) {
	t.Parallel()
	seen := make(map[string]bool)
	for seed := range uint32(2000) {
		seen[FromSeed(seed, nil)] = true
	}
	// 2000 draws from 4096 names leave about 1580 distinct ones.
	if len(seen) < 1400 {
		t.Errorf("2000 seeds gave only %d names", len(seen))
	}
}
