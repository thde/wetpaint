// Package names generates player names. Players cannot choose their own: the
// game shows names to strangers, and a fixed vocabulary needs no moderation.
package names

import "math/rand/v2"

// MaxLen is the longest generated name, in characters.
const MaxLen = 16

// Adjectives and nouns are combined as "Adjective Noun". Each adjective is at
// most 7 letters and each noun at most 8, so every name fits in MaxLen. Colour
// words are left out on purpose: "Cobalt" on a red drop reads as a bug.
var (
	adjectives = []string{
		"Bashful", "Bold", "Bouncy", "Brave", "Breezy", "Bubbly", "Busy", "Calm",
		"Cheery", "Chilly", "Clever", "Cloudy", "Cosy", "Curious", "Daring", "Dapper",
		"Damp", "Dewy", "Dizzy", "Dreamy", "Drifty", "Drippy", "Dusky", "Fancy",
		"Fizzy", "Floaty", "Foggy", "Frosty", "Fuzzy", "Gentle", "Giddy", "Glassy",
		"Glossy", "Hazy", "Humble", "Jolly", "Lazy", "Little", "Lucky", "Mellow",
		"Merry", "Misty", "Nimble", "Plucky", "Quiet", "Rainy", "Runny", "Shiny",
		"Shy", "Silky", "Sleepy", "Smudgy", "Snowy", "Soft", "Soggy", "Splashy",
		"Spry", "Stormy", "Sunny", "Swift", "Tiny", "Watery", "Wobbly", "Zippy",
	}
	nouns = []string{
		"Bead", "Blob", "Bloom", "Blot", "Bristle", "Brook", "Brush", "Bubble",
		"Canvas", "Cloud", "Comet", "Dab", "Dewdrop", "Doodle", "Dot", "Dribble",
		"Drip", "Drizzle", "Drop", "Droplet", "Easel", "Fern", "Fleck", "Fountain",
		"Glaze", "Hue", "Inkblot", "Inkwell", "Lily", "Mist", "Moss", "Palette",
		"Paper", "Pebble", "Petal", "Pigment", "Pond", "Puddle", "Puff", "Rain",
		"Raindrop", "Ripple", "Scribble", "Sketch", "Smudge", "Speck", "Splash", "Splatter",
		"Splotch", "Spray", "Sprinkle", "Spritz", "Squiggle", "Streak", "Stream", "Swirl",
		"Teardrop", "Tide", "Tint", "Trickle", "Wash", "Wave", "Whirl", "Wisp",
	}
)

// Count is the number of distinct names.
var Count = len(adjectives) * len(nouns)

// attempts bounds the search for an unused name. With a few dozen names taken
// out of thousands, the first draw almost always succeeds.
const attempts = 16

// FromSeed returns the name seed stands for. A client keeps its seed to keep
// its name across visits. If that name is taken, the seed's next choices are
// tried, so the same seed still gets the same fallback.
func FromSeed(seed uint32, taken func(string) bool) string {
	return Generate(rand.New(rand.NewPCG(uint64(seed), seedStream)), taken)
}

// seedStream fixes the mapping from seeds to names. Changing it renames every
// returning player.
const seedStream = 0x7765747061696e74 // "wetpaint"

// Generate returns a random name for which taken reports false, if one turns
// up within a few draws; otherwise it returns a name that may be in use.
// taken may be nil.
func Generate(rng *rand.Rand, taken func(string) bool) string {
	var name string
	for range attempts {
		name = adjectives[rng.IntN(len(adjectives))] + " " + nouns[rng.IntN(len(nouns))]
		if taken == nil || !taken(name) {
			break
		}
	}
	return name
}
