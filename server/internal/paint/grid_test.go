package paint

import (
	"bytes"
	"testing"
)

const testFade = 6000

func centreOp(strength, r, g, b uint8) Op {
	// Centre of cell (5, 5) on a 10×10 grid.
	return Op{X: 5*cell + halfCell, Y: 5*cell + halfCell, Radius2: 50, R: r, G: g, B: b, Strength: strength}
}

func TestPaintFadesLinearlyToZero(t *testing.T) {
	t.Parallel()
	g := NewGrid(10, 10, testFade, 40)
	g.Apply(centreOp(200, 10, 20, 30), 1000)
	i := 5*10 + 5

	tests := []struct {
		now  uint32
		want uint8
	}{
		{1000, 200},
		{1000 + testFade/4, 150},
		{1000 + testFade/2, 100},
		{1000 + testFade - 1, 0},
		{1000 + testFade, 0},
		{1000 + 10*testFade, 0},
	}
	for _, tt := range tests {
		if got := g.Eff(i, tt.now); got != tt.want {
			t.Errorf("Eff at +%d ticks = %d, want %d", tt.now-1000, got, tt.want)
		}
	}
}

func TestStampHasSoftEdge(t *testing.T) {
	t.Parallel()
	g := NewGrid(10, 10, testFade, 40)
	g.Apply(centreOp(200, 255, 0, 0), 0)
	centre, edge := g.Eff(5*10+5, 0), g.Eff(5*10+7, 0)
	if centre != 200 {
		t.Errorf("centre alpha = %d, want full strength 200", centre)
	}
	if edge == 0 || edge >= centre {
		t.Errorf("edge alpha = %d, want between 0 and %d", edge, centre)
	}
	if got := g.Eff(5*10+8, 0); got != 0 {
		t.Errorf("cell outside radius has alpha %d", got)
	}
}

func TestRepaintingSameColourIsIdempotent(t *testing.T) {
	t.Parallel()
	g := NewGrid(10, 10, testFade, 40)
	for now := range uint32(50) {
		g.Apply(centreOp(120, 200, 150, 90), now)
	}
	px := g.RGBA()[(5*10+5)*4:]
	if px[0] != 200 || px[1] != 150 || px[2] != 90 {
		t.Errorf("colour drifted to %v", px[:3])
	}
	if px[3] < 250 {
		t.Errorf("alpha = %d, want saturation near 255", px[3])
	}
}

func TestMixKeepsOverlapsBright(t *testing.T) {
	t.Parallel()
	g := NewGrid(10, 10, testFade, 40)
	g.Apply(centreOp(200, 240, 220, 40), 0) // yellow
	g.Apply(centreOp(200, 40, 90, 230), 0)  // blue over it
	px := g.RGBA()[(5*10+5)*4:]

	// Equal weights give k = 192: the newer colour dominates.
	if px[2] <= px[0] {
		t.Errorf("newer blue should dominate, got %v", px[:3])
	}
	// Squared mixing lands above the linear average for every channel.
	linear := func(older, newer int) int { return (older*(255-192) + newer*192) / 255 }
	for c, pair := range [][2]int{{240, 40}, {220, 90}, {40, 230}} {
		if int(px[c]) < linear(pair[0], pair[1]) {
			t.Errorf("channel %d = %d is darker than linear blend %d", c, px[c], linear(pair[0], pair[1]))
		}
	}
}

func TestEraseClearsAlphaButKeepsTime(t *testing.T) {
	t.Parallel()
	g := NewGrid(10, 10, testFade, 40)
	g.Apply(centreOp(200, 1, 2, 3), 7)
	if !g.Painted(centreOp(0, 0, 0, 0), 8) {
		t.Fatal("Painted = false after stamping")
	}
	g.Apply(centreOp(0, 0, 0, 0), 9)
	i := 5*10 + 5
	if g.Eff(i, 9) != 0 {
		t.Error("cell still visible after erase")
	}
	if g.Times()[i] != 7 {
		t.Errorf("erase changed t to %d", g.Times()[i])
	}
	if g.Painted(centreOp(0, 0, 0, 0), 9) {
		t.Error("Painted = true after erase")
	}
}

func TestApplyCountsFreshCells(t *testing.T) {
	t.Parallel()
	g := NewGrid(10, 10, testFade, 40)
	first := g.Apply(centreOp(200, 1, 2, 3), 0)
	if first == 0 {
		t.Fatal("first stamp covered no cells")
	}
	if again := g.Apply(centreOp(200, 1, 2, 3), 10); again != 0 {
		t.Errorf("immediate repaint counted %d fresh cells", again)
	}
	if later := g.Apply(centreOp(200, 1, 2, 3), 50); later != first {
		t.Errorf("repaint after freshTicks counted %d cells, want %d", later, first)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	t.Parallel()
	const now = 70000
	g := NewGrid(10, 10, testFade, 40)
	g.Apply(centreOp(200, 10, 20, 30), now-testFade-5) // fully faded: dropped
	g.Apply(Op{X: 300, Y: 300, Radius2: 30, R: 9, G: 8, B: 7, Strength: 99}, now-100)

	raw := g.AppendSnapshot(nil, now)
	back, err := Decompress(Compress(raw), len(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, back) {
		t.Fatal("zlib round trip changed the planes")
	}

	h := NewGrid(10, 10, testFade, 40)
	if err := h.LoadSnapshot(back, now); err != nil {
		t.Fatal(err)
	}
	for i := range 100 {
		for _, at := range []uint32{now, now + 1000, now + testFade} {
			if g.Eff(i, at) != h.Eff(i, at) {
				t.Fatalf("cell %d differs at tick %d", i, at)
			}
		}
	}
	if err := h.LoadSnapshot(raw[1:], now); err == nil {
		t.Error("LoadSnapshot accepted a short snapshot")
	}
	if _, err := Decompress(Compress(raw), len(raw)-1); err == nil {
		t.Error("Decompress accepted an oversized stream")
	}
}

func TestFloorDiv(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ a, b, want int }{{7, 2, 3}, {-7, 2, -4}, {-8, 2, -4}, {0, 5, 0}, {-1, 160, -1}} {
		if got := floorDiv(tt.a, tt.b); got != tt.want {
			t.Errorf("floorDiv(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func BenchmarkAppendSnapshotFullPage(b *testing.B) {
	g := NewGrid(400, 300, testFade, 40)
	for i := range 3000 {
		g.Apply(Op{X: uint16(i * 7919 % 64000), Y: uint16(i * 104729 % 48000), Radius2: 60, R: 200, G: 100, B: 50, Strength: 200}, uint32(i))
	}
	var buf []byte
	for b.Loop() {
		buf = g.AppendSnapshot(buf[:0], 3000)
	}
}

// A grid rebuilt from a snapshot stores different ticks for its cells, yet
// must keep producing the same checksum as the grid it was copied from, with
// the same ops applied to both afterwards.
func TestChecksumSurvivesSnapshot(t *testing.T) {
	t.Parallel()
	const w, h = 40, 30
	live := NewGrid(w, h, testFade, 40)
	stamp := func(g *Grid, i int, now uint32) {
		op := Op{
			X: uint16(i * 7919 % (w * cell)), Y: uint16(i * 104729 % (h * cell)),
			Radius2: uint8(20 + i%60), R: uint8(i * 37), G: uint8(i * 91), B: uint8(i * 13),
			Strength: uint8(1 + i*53%255),
		}
		if i%7 == 0 {
			op.Strength = 0 // erase
		}
		g.Apply(op, now)
	}

	// Paint for one and a half fade times, so cells are fresh, partially
	// faded, fully faded and erased when the snapshots are taken.
	var copies []*Grid
	now := uint32(100_000)
	for i := range 900 {
		now += 10
		stamp(live, i, now)
		for _, c := range copies {
			stamp(c, i, now)
		}
		if i%150 == 149 {
			c := NewGrid(w, h, testFade, 40)
			if err := c.LoadSnapshot(live.AppendSnapshot(nil, now), now); err != nil {
				t.Fatal(err)
			}
			copies = append(copies, c)
		}
		if i%25 != 0 {
			continue
		}
		for _, at := range []uint32{now, now + 1, now + testFade/3} {
			want := live.Checksum(at)
			for n, c := range copies {
				if got := c.Checksum(at); got != want {
					t.Fatalf("after op %d, at tick %d: copy %d has checksum %#08x, live grid %#08x", i, at, n, got, want)
				}
			}
		}
	}
	if len(copies) != 6 {
		t.Fatalf("took %d snapshots, want 6", len(copies))
	}

	blank := NewGrid(w, h, testFade, 40)
	if live.Checksum(now) == blank.Checksum(now) {
		t.Error("painted grid has the checksum of a blank one")
	}
	// Once everything has faded the grid is indistinguishable from blank.
	if got, want := live.Checksum(now+testFade), blank.Checksum(0); got != want {
		t.Errorf("faded grid checksum = %#08x, want the blank grid's %#08x", got, want)
	}
}

func TestChecksumDetectsOneDifferentCell(t *testing.T) {
	t.Parallel()
	a, b := NewGrid(10, 10, testFade, 40), NewGrid(10, 10, testFade, 40)
	a.Apply(centreOp(200, 10, 20, 30), 50)
	b.Apply(centreOp(200, 10, 20, 31), 50)
	if a.Checksum(60) == b.Checksum(60) {
		t.Error("grids differing in one channel share a checksum")
	}
	b.Apply(centreOp(0, 0, 0, 0), 55)
	a.Apply(centreOp(0, 0, 0, 0), 55)
	if a.Checksum(60) != b.Checksum(60) {
		t.Error("erased cells still affect the checksum")
	}
}

//nolint:paralleltest // testing.AllocsPerRun panics in parallel tests.
func TestChecksumDoesNotAllocateAfterFirstUse(t *testing.T) {
	g := NewGrid(40, 30, testFade, 40)
	g.Apply(centreOp(200, 10, 20, 30), 5)
	g.Checksum(6)
	if n := testing.AllocsPerRun(10, func() { g.Checksum(7) }); n != 0 {
		t.Errorf("Checksum allocates %v times per call", n)
	}
}

func BenchmarkChecksumFullPage(b *testing.B) {
	g := NewGrid(400, 300, testFade, 40)
	for i := range 3000 {
		g.Apply(Op{X: uint16(i * 7919 % 64000), Y: uint16(i * 104729 % 48000), Radius2: 60, R: 200, G: 100, B: 50, Strength: 200}, uint32(i))
	}
	for b.Loop() {
		g.Checksum(3000)
	}
}
