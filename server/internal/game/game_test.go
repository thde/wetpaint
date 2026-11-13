package game

import (
	"bytes"
	"math"
	"math/rand/v2"
	"reflect"
	"testing"

	"github.com/thde/wetpaint/server/internal/paint"
	"github.com/thde/wetpaint/server/internal/protocol"
)

// calm is a world with nothing in it but drops.
var calm = Options{NoWater: true, NoHazards: true}

func newTestWorld(opts Options) *World {
	return NewWorld(rand.New(rand.NewPCG(7, 11)), opts)
}

// addDrop joins a player and puts its drop at a known spot.
func addDrop(w *World, name string, x, y, wetness float64) *Player {
	p := w.AddPlayer()
	w.Join(p, name, 0)
	p.X, p.Y, p.Wetness = x, y, wetness
	return p
}

// dye gives a drop a colour of its own, as if it had spawned with it.
func dye(p *Player, c Color) { p.Color, p.ownColor = c, c }

// hueTurn is how far, in radians, the hue of to lies from the hue of from.
func hueTurn(from, to Color) float64 {
	return math.Remainder(to.oklch().h-from.oklch().h, 2*math.Pi)
}

func stepSeconds(w *World, seconds float64) (deaths []DeathReport, events []protocol.Event) {
	for range Ticks(seconds) {
		out := w.Step()
		deaths = append(deaths, out.Deaths...)
		events = append(events, out.Events...)
	}
	return deaths, events
}

func near(got, want, tol float64) bool { return math.Abs(got-want) <= tol }

func TestDryRate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		moving, blotter bool
		want            float64
	}{
		{false, false, 1.1},
		{true, false, 1.4},
		{false, true, 4.4},
		{true, true, 5.6},
	}
	for _, tt := range tests {
		if got := dryRate(tt.moving, tt.blotter); !near(got, tt.want, 1e-9) {
			t.Errorf("dryRate(moving=%v, blotter=%v) = %v, want %v", tt.moving, tt.blotter, got, tt.want)
		}
	}
}

func TestLifeLength(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		dx     int8
		wantAt float64 // seconds
	}{
		{"resting drop lasts about 91 s", 0, MaxWetness / DryPerSec},
		{"moving drop lasts about 71 s", 127, MaxWetness / (DryPerSec + DryMovingPerSec)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := newTestWorld(calm)
			p := addDrop(w, "solo", WorldW/2, WorldH/2, MaxWetness)
			var report *DeathReport
			for tick := 0; tick < Ticks2(120) && report == nil; tick++ {
				// Pace up and down the page so the drop never rests on an edge.
				dx := tt.dx
				if tick/(8*TickRate)%2 == 1 {
					dx = -dx
				}
				w.SetInput(p, uint32(tick), dx, 0)
				if out := w.Step(); len(out.Deaths) > 0 {
					report = &out.Deaths[0]
				}
			}
			if report == nil {
				t.Fatal("drop never dried out")
			}
			if report.Dead.Cause != protocol.CauseDried {
				t.Errorf("cause = %d, want dried", report.Dead.Cause)
			}
			if got := float64(report.Dead.SecondsAlive); !near(got, tt.wantAt, 2.5) {
				t.Errorf("lived %.1f s, want about %.1f s", got, tt.wantAt)
			}
			if p.Alive {
				t.Error("player still alive after its death report")
			}
			if tt.dx != 0 && report.Dead.CellsPainted == 0 {
				t.Error("a moving drop painted nothing")
			}
		})
	}
}

// Ticks2 is Ticks as an int, for loop bounds.
func Ticks2(seconds float64) int { return int(Ticks(seconds)) }

func TestBlotterDriesFourTimesFaster(t *testing.T) {
	t.Parallel()
	w := newTestWorld(calm)
	w.blotters = []protocol.Rect{{X: 1000, Y: 1000, W: 400, H: 300}}
	on := addDrop(w, "on", 1200, 1150, MaxWetness)
	off := addDrop(w, "off", 3000, 2000, MaxWetness)
	stepSeconds(w, 10)
	lostOn, lostOff := MaxWetness-on.Wetness, MaxWetness-off.Wetness
	if !near(lostOn/lostOff, BlotterDryMultiplier, 0.01) {
		t.Errorf("blotter drop lost %.2f, other lost %.2f; want ratio %v", lostOn, lostOff, BlotterDryMultiplier)
	}
}

func TestSmallDropsAreNimbleAndBigOnesSlow(t *testing.T) {
	t.Parallel()
	w := newTestWorld(calm)
	small := addDrop(w, "small", 500, 500, 20)
	big := addDrop(w, "big", 500, 2000, 100)
	for i := range Ticks2(3) {
		w.SetInput(small, uint32(i), 127, 0)
		w.SetInput(big, uint32(i), 127, 0)
		w.Step()
	}
	if !(small.Radius() < big.Radius()) {
		t.Fatalf("radius: small %v, big %v", small.Radius(), big.Radius())
	}
	if !(small.Speed() > big.Speed()+50) {
		t.Errorf("speed: small %.0f should clearly beat big %.0f", small.Speed(), big.Speed())
	}
	if small.Speed() > SpeedMax || big.Speed() < SpeedMin*0.9 {
		t.Errorf("speeds outside [SpeedMin, SpeedMax]: %.0f, %.0f", small.Speed(), big.Speed())
	}
}

func TestMovementHasInertiaAndStopsAtEdges(t *testing.T) {
	t.Parallel()
	w := newTestWorld(calm)
	p := addDrop(w, "p", 2000, 1500, 50)

	w.SetInput(p, 1, 127, 0)
	w.Step()
	first := p.VX
	if first <= 0 || first > 0.25*SpeedMax {
		t.Errorf("after one tick vx = %.1f; want a gentle start", first)
	}
	if p.AckSeq != 1 {
		t.Errorf("AckSeq = %d, want 1", p.AckSeq)
	}

	w.SetInput(p, 2, 0, 0)
	w.Step()
	if !(p.VX > 0 && p.VX < first) {
		t.Errorf("after releasing, vx = %.1f; want it to glide (0 < vx < %.1f)", p.VX, first)
	}

	// An over-long input vector is clamped to full throttle.
	w.SetInput(p, 3, -128, -128)
	stepSeconds(w, 30)
	// The drop was clamped with the radius it had before this tick's drying.
	r := p.Radius()
	if !near(p.X, r, 0.05) || !near(p.Y, r, 0.05) {
		t.Errorf("drop at (%.2f, %.2f), want pinned in the corner at (%.2f, %.2f)", p.X, p.Y, r, r)
	}
	if p.VX != 0 || p.VY != 0 {
		t.Errorf("velocity (%.2f, %.2f) not zeroed against the edge", p.VX, p.VY)
	}
}

func TestCanAbsorb(t *testing.T) {
	t.Parallel()
	drop := func(wetness float64) *Player { return &Player{Wetness: wetness, Alive: true} }
	tests := []struct {
		name       string
		big, small float64 // wetness
		dist       float64
		want       bool
	}{
		{"much bigger, covering the centre", 100, 10, 20, true},
		{"much bigger, centre outside", 100, 10, 36, false},
		{"touching rims only", 100, 10, 45, false},
		{"just under 1.3× the radius", 60, 37, 0, false}, // 25 vs 19.25 → 1.299
		{"just over 1.3× the radius", 60, 36, 0, true},   // 25 vs 19 → 1.316
		{"equal sizes never absorb", 80, 80, 0, false},
		{"the smaller cannot absorb the larger", 10, 100, 0, false},
	}
	for _, tt := range tests {
		if got := canAbsorb(drop(tt.big), drop(tt.small), tt.dist); got != tt.want {
			t.Errorf("%s: canAbsorb = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestAbsorption(t *testing.T) {
	t.Parallel()
	w := newTestWorld(calm)
	big := addDrop(w, "big", 1000, 1000, 80)
	small := addDrop(w, "small", 1010, 1000, 20)
	red, blue := Color{200, 0, 0}, Color{0, 0, 100}
	dye(big, red)
	dye(small, blue)

	out := w.Step()

	if !big.Alive || small.Alive {
		t.Fatalf("alive: big=%v small=%v", big.Alive, small.Alive)
	}
	// Both dried for one tick before meeting.
	wantWet := (80 - DryPerSec*dt) + (20-DryPerSec*dt)*AbsorbWetness
	if !near(big.Wetness, wantWet, 1e-9) {
		t.Errorf("big wetness = %v, want %v", big.Wetness, wantWet)
	}
	if want := red.Mix(blue, AbsorbBlend); big.Color != want {
		t.Errorf("big colour = %+v, want %+v (30%% toward the absorbed drop)", big.Color, want)
	}
	if big.ownColor != red {
		t.Errorf("absorbing changed the drop's own colour to %+v", big.ownColor)
	}
	if len(out.Deaths) != 1 || out.Deaths[0].Player != small ||
		out.Deaths[0].Dead.Cause != protocol.CauseAbsorbed || out.Deaths[0].Dead.Killer != big.ID {
		t.Errorf("death report = %+v", out.Deaths)
	}
	if len(out.Paint) == 0 {
		t.Error("absorbed drop left no bloom")
	}
	if len(w.blooms) != 1 || w.blooms[0].Name != "small" {
		t.Errorf("bloom labels = %+v", w.blooms)
	}
}

func TestAbsorbedWetnessIsCapped(t *testing.T) {
	t.Parallel()
	w := newTestWorld(calm)
	big := addDrop(w, "big", 1000, 1000, 100)
	addDrop(w, "small", 1000, 1000, 40)
	w.Step()
	if big.Wetness != MaxWetness {
		t.Errorf("wetness = %v, want capped at %v", big.Wetness, MaxWetness)
	}
}

func TestColorToward(t *testing.T) {
	t.Parallel()
	a, b := Color{200, 100, 0}, Color{0, 100, 255}
	if got := a.Toward(b, 0); got != a {
		t.Errorf("Toward(0) = %+v", got)
	}
	if got := a.Toward(b, 1); got != b {
		t.Errorf("Toward(1) = %+v", got)
	}
	if got, want := a.Toward(b, 0.25), (Color{150, 100, 63.75}); got != want {
		t.Errorf("Toward(0.25) = %+v, want %+v", got, want)
	}
	if got, want := (Color{-3, 127.5, 300}).RGB(), (protocol.RGB{R: 0, G: 128, B: 255}); got != want {
		t.Errorf("RGB = %+v, want %+v", got, want)
	}
}

func TestColorMixEndpoints(t *testing.T) {
	t.Parallel()
	a, b := Color{200, 100, 0}, Color{0, 100, 255}
	if got := a.Mix(b, 0); got != a {
		t.Errorf("Mix(0) = %+v", got)
	}
	if got := a.Mix(b, 1); got != b {
		t.Errorf("Mix(1) = %+v", got)
	}
	if got := a.Mix(a, 0.5); got != a {
		t.Errorf("a colour mixed with itself = %+v, want %+v", got, a)
	}
}

func TestColorSurvivesOKLCHRoundTrip(t *testing.T) {
	t.Parallel()
	colors := []Color{black, white, {128, 128, 128}, {255, 0, 0}, {0, 255, 0}, {0, 0, 255}, {12.5, 200.25, 99.75}}
	for _, c := range Palette() {
		colors = append(colors, colorOf(c))
	}
	for _, c := range colors {
		got := c
		// A long life of mixing must not walk the colour away.
		for range 5000 {
			got = got.oklch().color()
		}
		if got.Dist(c) > 1e-6 {
			t.Errorf("%+v became %+v after 5000 round trips", c, got)
		}
	}
}

// The reason for mixing in OKLCH: two saturated colours must not meet in
// grey, however far apart their hues are.
func TestColorMixKeepsChroma(t *testing.T) {
	t.Parallel()
	palette := Palette()
	for i, pa := range palette {
		for _, pb := range palette[i+1:] {
			a, b := colorOf(pa), colorOf(pb)
			floor := 0.8 * min(a.oklch().c, b.oklch().c)
			for _, f := range []float64{0.25, 0.5, 0.75} {
				if got := a.Mix(b, f).oklch().c; got < floor {
					t.Errorf("%v mixed %.2f into %v has chroma %.3f, want at least %.3f", pa, f, pb, got, floor)
				}
			}
		}
	}

	// Complementary primaries are the worst case for an RGB average.
	red, cyan := Color{255, 0, 0}, Color{0, 255, 255}
	if rgb, mix := red.Toward(cyan, 0.5).oklch().c, red.Mix(cyan, 0.5).oklch().c; rgb > 0.01 || mix < 0.1 {
		t.Errorf("red+cyan chroma: RGB average %.3f, Mix %.3f; want grey and clearly coloured", rgb, mix)
	}
}

func TestColorMixStaysInGamut(t *testing.T) {
	t.Parallel()
	primaries := []Color{{255, 0, 0}, {255, 255, 0}, {0, 255, 0}, {0, 255, 255}, {0, 0, 255}, {255, 0, 255}}
	for _, a := range primaries {
		for _, b := range primaries {
			for f := 0.0; f <= 1; f += 0.125 {
				got := a.Mix(b, f)
				if min(got.R, got.G, got.B) < 0 || max(got.R, got.G, got.B) > 255 {
					t.Errorf("%+v mixed %.3f into %+v = %+v, outside sRGB", a, f, b, got)
				}
			}
		}
	}
}

func TestColorMixWithGreyTakesTheOtherHue(t *testing.T) {
	t.Parallel()
	grey, blue := Color{128, 128, 128}, Color{98, 136, 216}
	mid := grey.Mix(blue, 0.5)
	if turn := hueTurn(blue, mid); math.Abs(turn) > 0.05 {
		t.Errorf("grey mixed into blue is %.2f rad off blue's hue: %+v", turn, mid)
	}
	if c := mid.oklch().c; c >= blue.oklch().c || c <= grey.oklch().c {
		t.Errorf("chroma %.3f is not between grey's and blue's", c)
	}
}

func TestTouchingDropsBleedAndEarnMixBonus(t *testing.T) {
	t.Parallel()
	w := newTestWorld(calm)
	a := addDrop(w, "a", 1000, 1000, 50)
	b := addDrop(w, "b", 1030, 1000, 50)
	red, blue := Color{255, 0, 0}, Color{0, 0, 255}
	dye(a, red)
	dye(b, blue)

	w.out.Events = nil // forget the two Spawns
	out := w.Step()
	wantWet := 50 - DryPerSec*dt + MixBonus
	if !near(a.Wetness, wantWet, 1e-9) || !near(b.Wetness, wantWet, 1e-9) {
		t.Errorf("wetness after first touch = %v, %v; want %v", a.Wetness, b.Wetness, wantWet)
	}
	if a.ColorsMixed != 1 || b.ColorsMixed != 1 {
		t.Errorf("ColorsMixed = %d, %d; want 1, 1", a.ColorsMixed, b.ColorsMixed)
	}
	if want := []protocol.Event{protocol.Mix{A: a.ID, B: b.ID}}; !reflect.DeepEqual(out.Events, want) {
		t.Errorf("events = %#v, want %#v", out.Events, want)
	}

	// One second of contact in total: each colour has moved about 25% (a bit
	// less, because the other colour is approaching too). No second bonus.
	_, events := stepSeconds(w, 1-dt)
	if len(events) != 0 || a.ColorsMixed != 1 {
		t.Errorf("bonus repeated within the cooldown: %#v", events)
	}
	gap := hueTurn(red, blue)
	shiftA, shiftB := hueTurn(red, a.Color)/gap, hueTurn(b.Color, blue)/gap
	if shiftA < 0.18 || shiftA > 0.25 {
		t.Errorf("a's hue moved %.3f of the way to b in 1 s, want just under %.2f", shiftA, BleedPerSec)
	}
	if !near(shiftA, shiftB, 1e-3) {
		t.Errorf("bleeding is not symmetric: a moved %.4f, b moved %.4f", shiftA, shiftB)
	}
	if a.ownColor != red || b.ownColor != blue {
		t.Errorf("bleeding changed the drops' own colours: %+v, %+v", a.ownColor, b.ownColor)
	}

	// Still touching when the cooldown ends: the bonus comes round again.
	_, events = stepSeconds(w, MixCooldownSecs)
	if len(events) != 1 || a.ColorsMixed != 2 {
		t.Errorf("after the cooldown: events = %#v, mixed = %d; want one more Mix", events, a.ColorsMixed)
	}
}

func TestUntouchedDropKeepsItsColour(t *testing.T) {
	t.Parallel()
	w := newTestWorld(calm)
	p := addDrop(w, "p", 1000, 1000, 100)
	own := Color{12.5, 200.25, 99.75}
	dye(p, own)
	stepSeconds(w, 60)
	if p.Color != own {
		t.Errorf("colour drifted to %+v with nobody near, want exactly %+v", p.Color, own)
	}
}

func TestBleedWearsOffWhenDropsPart(t *testing.T) {
	t.Parallel()
	w := newTestWorld(calm)
	a := addDrop(w, "a", 1000, 1000, 100)
	b := addDrop(w, "b", 1030, 1000, 100)
	rose, viridian := colorOf(Palette()[0]), colorOf(Palette()[4])
	dye(a, rose)
	dye(b, viridian)

	stepSeconds(w, 5)
	picked := a.Color.Dist(rose)
	if picked < 20 {
		t.Fatalf("after 5 s of contact a is only %.1f from its own colour; the test needs a visible bleed", picked)
	}

	b.X = 2000 // out of reach
	stepSeconds(w, 5)
	if d := a.Color.Dist(rose); d >= picked || d < 1 {
		t.Errorf("5 s apart: a is %.1f from its own colour, want part-way back from %.1f", d, picked)
	}
	stepSeconds(w, 85)
	if a.Color != rose || b.Color != viridian {
		t.Errorf("a minute and a half apart: colours %+v, %+v; want exactly their own %+v, %+v", a.Color, b.Color, rose, viridian)
	}
}

func TestAbsorbedColourWearsOff(t *testing.T) {
	t.Parallel()
	w := newTestWorld(calm)
	big := addDrop(w, "big", 1000, 1000, 80)
	small := addDrop(w, "small", 1010, 1000, 20)
	rose, viridian := colorOf(Palette()[0]), colorOf(Palette()[4])
	dye(big, rose)
	dye(small, viridian)

	w.Step()
	changed := big.Color.Dist(rose)
	if small.Alive || changed < 20 {
		t.Fatalf("small alive = %v, big changed by %.1f; want absorbed and visibly tinted", small.Alive, changed)
	}
	stepSeconds(w, 3)
	if d := big.Color.Dist(rose); d < changed/2 {
		t.Errorf("3 s after absorbing, the tint has already faded from %.1f to %.1f", changed, d)
	}
	stepSeconds(w, 90)
	if big.Color != rose {
		t.Errorf("a minute and a half after absorbing: colour %+v, want its own %+v", big.Color, rose)
	}
}

// A huddle converges on one colour; that must not be permanent.
func TestHuddleDoesNotLeaveEveryoneTheSameColour(t *testing.T) {
	t.Parallel()
	w := newTestWorld(calm)
	palette := Palette()
	drops := make([]*Player, len(palette))
	for i, c := range palette {
		// Equal sizes on a tight ring: everyone overlaps, nobody is absorbed.
		angle := 2 * math.Pi * float64(i) / float64(len(palette))
		drops[i] = addDrop(w, "d", 1000+8*math.Cos(angle), 1000+8*math.Sin(angle), 100)
		dye(drops[i], colorOf(c))
	}
	for range Ticks(60) {
		for _, p := range drops {
			p.Wetness = 100 // keep them alive and the same size
		}
		w.Step()
	}
	spread := func() (widest float64) {
		for i, a := range drops {
			for _, b := range drops[i+1:] {
				widest = max(widest, a.Color.Dist(b.Color))
			}
		}
		return widest
	}
	if s := spread(); s > 5 {
		t.Fatalf("after a minute huddled the colours still differ by %.1f; the test needs them merged", s)
	}
	if c := drops[0].Color.oklch().c; c < 0.05 {
		t.Errorf("the huddle's shared colour %+v is grey (chroma %.3f)", drops[0].Color, c)
	}

	for i, p := range drops {
		p.X, p.Y = 300+300*float64(i), 1500
	}
	for range Ticks(90) {
		for _, p := range drops {
			p.Wetness = 100
		}
		w.Step()
	}
	for i, p := range drops {
		if !p.Alive {
			t.Fatalf("drop %d died during the test", i)
		}
		if want := colorOf(palette[i]); p.Color != want {
			t.Errorf("drop %d is %+v a minute and a half after the huddle, want its own %+v", i, p.Color, want)
		}
	}
}

func TestTrailFadesAway(t *testing.T) {
	t.Parallel()
	w := newTestWorld(calm)
	p := addDrop(w, "painter", 2000, 1500, MaxWetness)
	for i := range Ticks2(2) {
		w.SetInput(p, uint32(i), 127, 0)
		w.Step()
	}
	w.RemovePlayer(p)
	painted := func() (n int) {
		for i := range GridW * GridH {
			if w.Grid().Eff(i, w.Tick()) > 0 {
				n++
			}
		}
		return n
	}
	if painted() == 0 {
		t.Fatal("moving left no trail")
	}
	stepSeconds(w, FadeSeconds/2)
	if painted() == 0 {
		t.Error("trail gone after half the fade time")
	}
	stepSeconds(w, FadeSeconds/2)
	if n := painted(); n != 0 {
		t.Errorf("%d cells still visible after the full fade time", n)
	}
}

func TestDropletRestoresWetness(t *testing.T) {
	t.Parallel()
	w := newTestWorld(calm)
	p := addDrop(w, "thirsty", 1000, 1000, 40)
	w.droplets = []droplet{{id: 5, x: 1005, y: 1000, expires: 1000}, {id: 6, x: 3000, y: 100, expires: 2}}
	w.opts.NoWater = false
	w.nextPuddleTick = math.MaxUint32
	w.cloud = cloud{x: cloudMinX, y: cloudMinY}

	out := w.Step()
	if want := 40 - DryPerSec*dt + DropletWetness; !near(p.Wetness, want, 1e-9) {
		t.Errorf("wetness = %v, want %v", p.Wetness, want)
	}
	gone := map[protocol.DropletGone]bool{}
	for _, e := range out.Events {
		if g, ok := e.(protocol.DropletGone); ok {
			gone[g] = true
		}
	}
	if !gone[protocol.DropletGone{ID: 5, By: p.ID}] {
		t.Errorf("no DropletGone for the drunk droplet in %#v", out.Events)
	}
	out = w.Step()
	found := false
	for _, e := range out.Events {
		if e == (protocol.DropletGone{ID: 6, By: 0}) {
			found = true
		}
	}
	if !found {
		t.Errorf("stale droplet did not evaporate: %#v", out.Events)
	}
}

func TestPuddleRestoresUntilEmpty(t *testing.T) {
	t.Parallel()
	w := newTestWorld(calm)
	p := addDrop(w, "wader", 1000, 1000, 10)
	w.opts.NoWater = false
	w.nextPuddleTick = math.MaxUint32
	w.cloud = cloud{x: cloudMaxX, y: cloudMaxY, vx: 0, vy: 0}
	w.puddles = []puddle{{id: 1, x: 1020, y: 1000, remaining: PuddleCapacity, expires: math.MaxUint32}}

	stepSeconds(w, 1)
	if want := 10 + PuddleRestorePerSec - DryPerSec; !near(p.Wetness, want, 0.01) {
		t.Errorf("after 1 s wetness = %v, want %v", p.Wetness, want)
	}
	var st protocol.State
	w.FillState(&st, p)
	if len(st.Puddles) != 1 || st.Puddles[0].Remaining != uint8(PuddleCapacity-PuddleRestorePerSec) {
		t.Errorf("puddle state = %+v", st.Puddles)
	}

	stepSeconds(w, PuddleCapacity/PuddleRestorePerSec)
	if len(w.puddles) != 0 {
		t.Errorf("puddle not used up: %+v", w.puddles)
	}
	total := PuddleCapacity/PuddleRestorePerSec + 1
	if want := 10 + PuddleCapacity - DryPerSec*total; !near(p.Wetness, want, 0.01) {
		t.Errorf("wetness = %v, want %v (the whole puddle, minus drying)", p.Wetness, want)
	}
}

func TestBrushWarnsThenSoaksUpDrops(t *testing.T) {
	t.Parallel()
	w := newTestWorld(Options{NoWater: true})
	w.beetles, w.blotters = nil, nil
	w.nextBrushTick = w.tick + 1

	out := w.Step()
	var warn *protocol.Brush
	for _, e := range out.Events {
		if bw, ok := e.(protocol.BrushWarn); ok {
			warn = &bw.Brush
		}
	}
	if warn == nil {
		t.Fatalf("no BrushWarn in %#v", out.Events)
	}
	if got := warn.StartTick - w.Tick(); got != Ticks(BrushWarnSeconds) {
		t.Errorf("warned %d ticks ahead, want %d", got, Ticks(BrushWarnSeconds))
	}
	var welcome protocol.Welcome
	w.FillWelcome(&welcome)
	if welcome.Brush == nil || *welcome.Brush != *warn {
		t.Error("Welcome does not carry the pending brush")
	}

	// One drop sits on the brush's centre line, one far to the side of it.
	mx, my := float64(warn.X0+warn.X1)/2, float64(warn.Y0+warn.Y1)/2
	nx, ny := float64(warn.Y1-warn.Y0), -float64(warn.X1-warn.X0)
	nl := math.Hypot(nx, ny)
	victim := addDrop(w, "victim", mx, my, MaxWetness)
	sideX, sideY := mx+nx/nl*3*BrushHalfWidth, my+ny/nl*3*BrushHalfWidth
	if sideX < 100 || sideX > WorldW-100 || sideY < 100 || sideY > WorldH-100 {
		sideX, sideY = mx-nx/nl*3*BrushHalfWidth, my-ny/nl*3*BrushHalfWidth
	}
	bystander := addDrop(w, "bystander", sideX, sideY, MaxWetness)

	stepSeconds(w, BrushWarnSeconds-0.1)
	if !victim.Alive {
		t.Fatal("brush struck before its warning ran out")
	}
	deaths, _ := stepSeconds(w, BrushSeconds+0.2)
	if victim.Alive || len(deaths) != 1 || deaths[0].Dead.Cause != protocol.CauseBrush {
		t.Errorf("victim alive=%v, deaths=%+v", victim.Alive, deaths)
	}
	if !bystander.Alive {
		t.Error("bystander outside the band was soaked up")
	}
	if w.brush != nil {
		t.Error("brush still active after its sweep")
	}
	if w.Grid().Eff(int(my/CellSize)*GridW+int(mx/CellSize), w.Tick()) == 0 {
		t.Error("brush left no stroke on its centre line")
	}
}

func TestBeetleBumpsAndEatsPaint(t *testing.T) {
	t.Parallel()
	w := newTestWorld(Options{NoWater: true})
	w.blotters, w.nextBrushTick = nil, math.MaxUint32
	w.beetles = []beetle{{id: 1, x: 1000, y: 1000, tx: 1000, ty: 1000, retarget: math.MaxUint32}}
	w.stamp(1000, 1000, 30, protocol.RGB{R: 200}, 200)
	p := addDrop(w, "poked", 1030, 1000, 50)

	out := w.Step()
	if !near(p.Wetness, 50-DryPerSec*dt-BeetleDrain, 1e-9) {
		t.Errorf("wetness = %v, want a %v drain", p.Wetness, BeetleDrain)
	}
	if p.VX <= 0 || !near(p.Speed(), BeetleKnockback, 1) {
		t.Errorf("velocity (%.0f, %.0f), want a knock-back away from the beetle", p.VX, p.VY)
	}
	bumps := 0
	for _, e := range out.Events {
		if _, ok := e.(protocol.BeetleBump); ok {
			bumps++
		}
	}
	if bumps != 1 {
		t.Errorf("%d BeetleBump events, want 1", bumps)
	}

	before := p.Wetness
	p.X, p.Y = w.beetles[0].x+5, w.beetles[0].y
	w.Step()
	if p.Wetness < before-1 {
		t.Error("bumped again during the immunity window")
	}

	stepSeconds(w, 0.2)
	b := w.beetles[0]
	if w.Grid().Eff(int(b.y/CellSize)*GridW+int(b.x/CellSize), w.Tick()) != 0 {
		t.Error("beetle did not eat the paint under it")
	}
}

func TestViewportCulling(t *testing.T) {
	t.Parallel()
	w := newTestWorld(calm)
	me := addDrop(w, "me", 2000, 1500, 50)
	nearby := addDrop(w, "near", 2600, 1500, 50)
	far := addDrop(w, "far", 3700, 1500, 50)
	w.SetView(me, 1600, 1000)

	var st protocol.State
	w.FillState(&st, me)
	if len(st.Drops) != 1 || st.Drops[0].ID != nearby.ID {
		t.Errorf("living player sees %+v, want only the nearby drop %d", st.Drops, nearby.ID)
	}
	if !st.Alive || st.Self.X != 2000 || st.Drops[0].Wetness2 != 100 {
		t.Errorf("state = %+v", st)
	}

	spectator := w.AddPlayer()
	w.FillState(&st, spectator)
	if st.Alive || len(st.Drops) != 3 {
		t.Errorf("spectator sees %d drops (alive=%v), want all 3", len(st.Drops), st.Alive)
	}
	_ = far

	w.SetView(me, 60000, 1)
	if me.viewW != WorldW || me.viewH != MinView {
		t.Errorf("view clamped to %v × %v", me.viewW, me.viewH)
	}
}

func TestJoinRespawnAndLeave(t *testing.T) {
	t.Parallel()
	w := newTestWorld(calm)
	p := w.AddPlayer()
	if p.ID == 0 {
		t.Fatal("player ID 0 is reserved")
	}
	w.Respawn(p, 1)
	if p.Alive {
		t.Fatal("Respawn before Join spawned a drop")
	}

	w.Join(p, "pip", 4)
	if !p.Alive || p.Wetness != MaxWetness || p.Color != colorOf(Palette()[4]) {
		t.Fatalf("after Join: %+v", p)
	}
	x, y := p.X, p.Y
	w.Join(p, "other", 2)
	if p.Name != "pip" || p.X != x || p.Y != y {
		t.Error("Join while alive was not ignored")
	}

	p.Wetness = 0.01
	deaths, events := stepSeconds(w, 0.1)
	if len(deaths) != 1 || p.Alive {
		t.Fatalf("deaths = %+v", deaths)
	}
	if want := (protocol.Spawn{ID: p.ID, Name: "pip"}); events[0] != want {
		t.Errorf("first event = %#v, want %#v", events[0], want)
	}

	w.Respawn(p, protocol.ColorRandom)
	if !p.Alive || p.Color != colorOf(Palette()[4]) || p.Name != "pip" || p.CellsPainted != 0 {
		t.Errorf("after Respawn keeping colour: %+v", p)
	}

	id := p.ID
	w.RemovePlayer(p)
	out := w.Step()
	left := false
	for _, e := range out.Events {
		if d, ok := e.(protocol.Death); ok && d.ID == id && d.Cause == protocol.CauseLeft {
			left = true
		}
	}
	if !left || len(out.Deaths) != 0 || len(out.Paint) != 0 {
		t.Errorf("leaving: events=%#v deaths=%d paint=%d; want a quiet Death(left)", out.Events, len(out.Deaths), len(out.Paint))
	}
	if len(w.Players()) != 0 {
		t.Error("seat not freed")
	}
}

func TestWireConstantsMatchProtocolOrder(t *testing.T) {
	t.Parallel()
	c := WireConstants()
	if len(c) != 20 {
		t.Fatalf("%d constants, protocol v1 lists 20", len(c))
	}
	if c[0] != 4000 || c[1] != 3000 || c[2] != 20 || c[3] != 10 || c[12] != 100 || c[19] != 1.3 {
		t.Errorf("constants out of order: %v", c)
	}
	if len(Palette()) != 10 {
		t.Errorf("palette has %d colours, want 10", len(Palette()))
	}
}

// crowd fills a world with wandering drops driven by rng.
type crowd struct {
	w       *World
	rng     *rand.Rand
	players []*Player
}

func newCrowd(seed uint64, n int) *crowd {
	c := &crowd{
		w:   NewWorld(rand.New(rand.NewPCG(seed, 1)), Options{}),
		rng: rand.New(rand.NewPCG(seed, 2)),
	}
	for range n {
		p := c.w.AddPlayer()
		c.w.Join(p, "bot", protocol.ColorRandom)
		c.players = append(c.players, p)
	}
	return c
}

func (c *crowd) step() Output {
	for _, p := range c.players {
		if !p.Alive {
			c.w.Respawn(p, protocol.ColorRandom)
		}
		if c.rng.IntN(10) == 0 {
			c.w.SetInput(p, c.w.Tick(), int8(c.rng.IntN(255)-127), int8(c.rng.IntN(255)-127))
		}
	}
	return c.w.Step()
}

// TestCrowdedPageStaysConsistent runs a full page for a few minutes and
// checks the invariants clients rely on, above all that replaying the paint
// ops reproduces the server's grid exactly.
func TestCrowdedPageStaysConsistent(t *testing.T) {
	t.Parallel()
	c := newCrowd(42, PageCap)
	replica := paint.NewGrid(GridW, GridH, fadeTicks, freshTicks)
	causes := map[protocol.Cause]int{}
	kinds := map[reflect.Type]int{}

	for range Ticks2(240) {
		out := c.step()
		for _, op := range out.Paint {
			replica.Apply(op, out.Tick)
		}
		for _, d := range out.Deaths {
			causes[d.Dead.Cause]++
		}
		for _, e := range out.Events {
			kinds[reflect.TypeOf(e)]++
		}

		msg := &protocol.Events{Tick: out.Tick, Paint: out.Paint, Events: out.Events}
		back, err := protocol.DecodeServer(msg.Append(nil))
		if err != nil {
			t.Fatalf("tick %d: events do not decode: %v", out.Tick, err)
		}
		if got, ok := back.(*protocol.Events); !ok || len(got.Paint) != len(out.Paint) || len(got.Events) != len(out.Events) {
			t.Fatalf("tick %d: events changed in transit", out.Tick)
		}

		for _, p := range c.players {
			if !p.Alive {
				continue
			}
			// A drop may grow after it was clamped, so only the smallest
			// radius is guaranteed to be inside.
			const r = RadiusBase
			if p.Wetness < 0 || p.Wetness > MaxWetness ||
				p.X < r || p.X > WorldW-r || p.Y < r || p.Y > WorldH-r {
				t.Fatalf("tick %d: drop out of range: %+v", out.Tick, p)
			}
		}
	}

	if !bytes.Equal(replica.RGBA(), c.w.Grid().RGBA()) || !reflect.DeepEqual(replica.Times(), c.w.Grid().Times()) {
		t.Error("grid replayed from paint ops differs from the server's")
	}
	for _, cause := range []protocol.Cause{protocol.CauseDried, protocol.CauseAbsorbed, protocol.CauseBrush} {
		if causes[cause] == 0 {
			t.Errorf("nobody died of cause %d in four crowded minutes: %v", cause, causes)
		}
	}
	for _, e := range []protocol.Event{protocol.Spawn{}, protocol.Death{}, protocol.DropletSpawn{}, protocol.DropletGone{}, protocol.BrushWarn{}, protocol.Mix{}, protocol.BeetleBump{}} {
		if kinds[reflect.TypeOf(e)] == 0 {
			t.Errorf("no %T event in four crowded minutes", e)
		}
	}
	if n := len(c.w.blotters); n < BlottersMin || n > BlottersMax {
		t.Errorf("%d blotters", n)
	}
	if n := len(c.w.beetles); n < BeetlesMin || n > BeetlesMax {
		t.Errorf("%d beetles", n)
	}
}

func TestSameSeedReplaysIdentically(t *testing.T) {
	t.Parallel()
	a, b := newCrowd(9, 20), newCrowd(9, 20)
	for range Ticks2(60) {
		a.step()
		b.step()
	}
	if !bytes.Equal(a.w.Grid().RGBA(), b.w.Grid().RGBA()) {
		t.Error("two worlds with the same seed painted different pictures")
	}
}

func TestWorldResumesFromStartTick(t *testing.T) {
	t.Parallel()
	w := newTestWorld(Options{StartTick: 123456})
	if w.Tick() != 123456 {
		t.Fatalf("Tick = %d", w.Tick())
	}
	if out := w.Step(); out.Tick != 123457 {
		t.Errorf("first step is tick %d", out.Tick)
	}
	if w.brush != nil {
		t.Error("brush fired immediately after a resume")
	}
}

func BenchmarkStepFullPage(b *testing.B) {
	c := newCrowd(1, PageCap)
	var st protocol.State
	var buf []byte
	for b.Loop() {
		c.step()
		for _, p := range c.players {
			c.w.FillState(&st, p)
			buf = st.Append(buf[:0])
		}
	}
}
