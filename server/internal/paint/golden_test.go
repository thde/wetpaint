package paint

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"flag"
	"math/rand/v2"
	"os"
	"testing"
)

var update = flag.Bool("update", false, "rewrite protocol/paint_vectors.json")

const (
	vectorsPath     = "../../../protocol/paint_vectors.json"
	vectorFadeTicks = 6000
)

type vectorOp struct {
	Tick     uint32   `json:"tick"`
	X        uint16   `json:"x"`
	Y        uint16   `json:"y"`
	Radius2  uint8    `json:"radius2"`
	Color    [3]uint8 `json:"color"`
	Strength uint8    `json:"strength"`
}

type vectorCase struct {
	Name    string     `json:"name"`
	GridW   int        `json:"gridW"`
	GridH   int        `json:"gridH"`
	Ops     []vectorOp `json:"ops"`
	RGBA    string     `json:"rgba"`
	T       string     `json:"t"`
	EffTick uint32     `json:"effTick"`
	CRC     uint32     `json:"crc"` // Grid.Checksum at EffTick
	Eff     string     `json:"eff"`
}

type vectorFile struct {
	FadeTicks uint32       `json:"fadeTicks"`
	Cases     []vectorCase `json:"cases"`
}

// vectorInputs returns the golden cases without their expected results.
func vectorInputs() []vectorCase {
	red, blue, yellow := [3]uint8{226, 88, 96}, [3]uint8{80, 130, 220}, [3]uint8{245, 205, 90}
	cases := []vectorCase{
		{Name: "single stamp", EffTick: 3100, Ops: []vectorOp{
			{Tick: 100, X: 3200, Y: 2400, Radius2: 60, Color: red, Strength: 200},
		}},
		{Name: "two colours overlapping", EffTick: 500, Ops: []vectorOp{
			{Tick: 10, X: 2800, Y: 2400, Radius2: 70, Color: blue, Strength: 180},
			{Tick: 12, X: 3500, Y: 2500, Radius2: 70, Color: yellow, Strength: 180},
		}},
		{Name: "repaint over partially faded paint", EffTick: 5000, Ops: []vectorOp{
			{Tick: 0, X: 3000, Y: 2000, Radius2: 90, Color: blue, Strength: 255},
			{Tick: 3000, X: 3300, Y: 2100, Radius2: 60, Color: red, Strength: 120},
			{Tick: 4500, X: 3300, Y: 2100, Radius2: 30, Color: red, Strength: 120},
		}},
		{Name: "erase", EffTick: 400, Ops: []vectorOp{
			{Tick: 5, X: 3200, Y: 2400, Radius2: 100, Color: yellow, Strength: 220},
			{Tick: 50, X: 3200, Y: 2400, Radius2: 40, Strength: 0},
			{Tick: 90, X: 3500, Y: 2400, Radius2: 30, Color: blue, Strength: 160},
			{Tick: 91, X: 1000, Y: 1000, Radius2: 50, Strength: 0},
		}},
		{Name: "stamps clipped at grid edges", EffTick: 60, Ops: []vectorOp{
			{Tick: 1, X: 0, Y: 0, Radius2: 80, Color: red, Strength: 200},
			{Tick: 2, X: 6399, Y: 4799, Radius2: 80, Color: blue, Strength: 200},
			{Tick: 3, X: 6500, Y: 2400, Radius2: 40, Color: yellow, Strength: 200},
			{Tick: 4, X: 65535, Y: 65535, Radius2: 255, Color: red, Strength: 255},
			{Tick: 5, X: 3200, Y: 0, Radius2: 255, Color: blue, Strength: 90},
			{Tick: 6, X: 7900, Y: 5500, Radius2: 255, Color: yellow, Strength: 255},
		}},
		{Name: "strength and radius extremes", EffTick: 1000, Ops: []vectorOp{
			{Tick: 0, X: 800, Y: 800, Radius2: 60, Color: red, Strength: 1},
			{Tick: 0, X: 2400, Y: 800, Radius2: 60, Color: red, Strength: 255},
			{Tick: 1, X: 2400, Y: 800, Radius2: 60, Color: blue, Strength: 255},
			{Tick: 2, X: 4000, Y: 800, Radius2: 0, Color: blue, Strength: 255},
			{Tick: 2, X: 4080, Y: 2480, Radius2: 1, Color: blue, Strength: 255},
			{Tick: 2, X: 5040, Y: 2480, Radius2: 2, Color: yellow, Strength: 255},
			{Tick: 3, X: 800, Y: 3200, Radius2: 255, Color: [3]uint8{0, 0, 0}, Strength: 2},
			{Tick: 4, X: 800, Y: 3200, Radius2: 120, Color: [3]uint8{255, 255, 255}, Strength: 3},
		}},
		{Name: "fully faded then repainted", EffTick: 12000, Ops: []vectorOp{
			{Tick: 0, X: 3200, Y: 2400, Radius2: 80, Color: blue, Strength: 255},
			{Tick: 5999, X: 2000, Y: 2400, Radius2: 80, Color: yellow, Strength: 100},
			{Tick: 6000, X: 3200, Y: 2400, Radius2: 50, Color: red, Strength: 100},
			{Tick: 9000, X: 4000, Y: 2400, Radius2: 80, Color: yellow, Strength: 30},
		}},
	}

	rng := rand.New(rand.NewPCG(1, 2))
	soup := vectorCase{Name: "random soup"}
	var tick uint32
	for range 400 {
		tick += uint32(rng.IntN(60))
		op := vectorOp{
			Tick:     tick,
			X:        uint16(rng.IntN(7200)),
			Y:        uint16(rng.IntN(5600)),
			Radius2:  uint8(rng.IntN(120)),
			Color:    [3]uint8{uint8(rng.IntN(256)), uint8(rng.IntN(256)), uint8(rng.IntN(256))},
			Strength: uint8(rng.IntN(256)),
		}
		if rng.IntN(8) == 0 {
			op.Strength = 0
		}
		soup.Ops = append(soup.Ops, op)
	}
	soup.EffTick = tick + 2000
	cases = append(cases, soup)

	for i := range cases {
		cases[i].GridW, cases[i].GridH = 40, 30
	}
	return cases
}

// solve fills in the expected results of c using the Go implementation.
func solve(c vectorCase, fadeTicks uint32) vectorCase {
	g := NewGrid(c.GridW, c.GridH, fadeTicks, 40)
	for _, op := range c.Ops {
		g.Apply(Op{
			X: op.X, Y: op.Y, Radius2: op.Radius2,
			R: op.Color[0], G: op.Color[1], B: op.Color[2],
			Strength: op.Strength,
		}, op.Tick)
	}
	n := c.GridW * c.GridH
	t := make([]byte, 0, n*4)
	eff := make([]byte, n)
	for i := range n {
		t = binary.LittleEndian.AppendUint32(t, g.Times()[i])
		eff[i] = g.Eff(i, c.EffTick)
	}
	c.RGBA = base64.StdEncoding.EncodeToString(g.RGBA())
	c.T = base64.StdEncoding.EncodeToString(t)
	c.Eff = base64.StdEncoding.EncodeToString(eff)
	c.CRC = g.Checksum(c.EffTick)
	return c
}

// TestGoldenVectors checks the paint algorithm against the vectors shared
// with the TypeScript client. Run with -update after an intentional change to
// the algorithm (and the protocol document).
func TestGoldenVectors(t *testing.T) {
	t.Parallel()
	if *update {
		file := vectorFile{FadeTicks: vectorFadeTicks}
		for _, c := range vectorInputs() {
			file.Cases = append(file.Cases, solve(c, file.FadeTicks))
		}
		data, err := json.MarshalIndent(file, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(vectorsPath, append(data, '\n'), 0o644); err != nil { //nolint:gosec // checked-in fixture, world-readable like the rest of the repo
			t.Fatal(err)
		}
	}

	data, err := os.ReadFile(vectorsPath)
	if err != nil {
		t.Fatalf("read vectors (generate with -update): %v", err)
	}
	var file vectorFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Cases) == 0 {
		t.Fatal("no cases in vector file")
	}
	for _, want := range file.Cases {
		t.Run(want.Name, func(t *testing.T) {
			t.Parallel()
			got := solve(want, file.FadeTicks)
			if got.RGBA != want.RGBA {
				t.Error("rgba differs")
			}
			if got.T != want.T {
				t.Error("t differs")
			}
			if got.Eff != want.Eff {
				t.Error("eff differs")
			}
			if got.CRC != want.CRC {
				t.Errorf("crc = %#08x, want %#08x", got.CRC, want.CRC)
			}
		})
	}
}
