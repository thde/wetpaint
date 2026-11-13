package protocol

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
)

func TestClientRoundTrip(t *testing.T) {
	t.Parallel()
	msgs := []ClientMessage{
		Join{Seed: 0xCAFEF00D, Color: 3},
		Join{Seed: 0, Color: ColorRandom},
		Input{Seq: 0xDEADBEEF, DX: -127, DY: 127},
		Respawn{Color: ColorRandom},
		Ping{ClientTime: 123456},
		View{W: 1600, H: 1000},
		Resync{},
	}
	for _, want := range msgs {
		frame := want.Append(nil)
		got, err := DecodeClient(frame)
		if err != nil {
			t.Fatalf("%#v: %v", want, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %#v, want %#v", got, want)
		}
	}
}

func serverSamples() []ServerMessage {
	brush := Brush{X0: -165, Y0: 20.5, X1: 4165, Y1: 2900, HalfWidth: 110, StartTick: 900, DurationTicks: 60, Color: RGB{1, 2, 3}}
	return []ServerMessage{
		&Welcome{
			Version: Version, Page: 3, YourID: 17, Tick: 70000,
			Constants: []float32{4000, 3000, 20, 1.1},
			Palette:   []RGB{{1, 2, 3}, {250, 251, 252}},
			Blotters:  []Rect{{10, 20, 300, 200}},
			Roster:    []RosterEntry{{ID: 1, Name: "a"}, {ID: 2, Name: "Zoë"}},
			Droplets:  []Droplet{{ID: 9, X: 100, Y: 65535}},
			Blooms:    []Bloom{{X: 5, Y: 6, Tick: 69000, Name: "gone"}},
			Brush:     &brush,
			GridW:     400, GridH: 300,
			Grid: []byte{0x78, 0x9c, 1, 2, 3},
		},
		&Welcome{Version: Version, Page: 1, YourID: 1},
		&State{
			Tick: 5, AckSeq: 99, CloudX: 1000, CloudY: 2000, Alive: true,
			Self:    SelfState{X: 1.5, Y: -2.25, VX: 300, VY: -0.125, Wetness: 55.5, Color: RGB{9, 8, 7}},
			Drops:   []DropState{{ID: 2, X: 3, Y: 4, Wetness2: 200, Color: RGB{5, 6, 7}}},
			Beetles: []BeetleState{{ID: 1, X: 2, Y: 3}},
			Puddles: []PuddleState{{ID: 4, X: 5, Y: 6, Remaining: 60}},
		},
		&State{Tick: 6},
		&Events{
			Tick:  77,
			Paint: []PaintOp{{X: 1, Y: 2, Radius2: 3, R: 4, G: 5, B: 6, Strength: 7}, {Strength: 0}},
			Events: []Event{
				Spawn{ID: 3, Name: "new"},
				Death{ID: 3, Cause: CauseAbsorbed, Killer: 4, X: 100, Y: 200, Color: RGB{1, 1, 1}},
				DropletSpawn{ID: 8, X: 1, Y: 2},
				DropletGone{ID: 8, By: 3},
				BrushWarn{Brush: brush},
				Mix{A: 1, B: 2},
				BeetleBump{Player: 1, Beetle: 5},
			},
		},
		&Events{Tick: 78},
		&Dead{Cause: CauseBrush, Killer: 0, SecondsAlive: 61.25, CellsPainted: 4000, ColorsMixed: 3},
		&Leaderboard{Boards: [NumBoards][]Entry{
			BoardLivePainted: {{Name: "a", Value: 10, Color: RGB{1, 2, 3}}, {Name: "b", Value: 5}},
			BoardGlobalMixed: {{Name: "c", Value: 1 << 31}},
		}},
		&Pong{ClientTime: 42, Tick: 43},
		&GridCheck{Tick: 1200, CRC: 0xCBF43926},
		&Grid{Tick: 1300, GridW: 400, GridH: 300, Data: []byte{0x78, 0x9c, 9, 8, 7}},
		&Grid{Tick: 1},
	}
}

func TestServerRoundTrip(t *testing.T) {
	t.Parallel()
	for _, want := range serverSamples() {
		frame := want.Append(nil)
		got, err := DecodeServer(frame)
		if err != nil {
			t.Fatalf("%T: %v", want, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got  %#v\nwant %#v", got, want)
		}
		if again := got.Append(nil); !bytes.Equal(again, frame) {
			t.Errorf("%T: re-encoding differs", want)
		}
	}
}

func TestDecodeRejectsTruncatedAndTrailing(t *testing.T) {
	t.Parallel()
	for _, m := range serverSamples() {
		frame := m.Append(nil)
		for n := range frame {
			if _, err := DecodeServer(frame[:n]); !errors.Is(err, ErrMalformed) {
				t.Fatalf("%T truncated to %d bytes: err = %v", m, n, err)
			}
		}
		if _, err := DecodeServer(append(frame, 0)); !errors.Is(err, ErrMalformed) {
			t.Errorf("%T with trailing byte: err = %v", m, err)
		}
	}
	for _, frame := range [][]byte{nil, {0x7f}, {TypeInput, 1, 2}, Ping{}.Append([]byte(nil))[:4], append(Respawn{}.Append(nil), 1), {TypeResync, 0}} {
		if _, err := DecodeClient(frame); !errors.Is(err, ErrMalformed) {
			t.Errorf("DecodeClient(%v): err = %v", frame, err)
		}
	}
}

func TestWireLayout(t *testing.T) {
	t.Parallel()
	// Spot-check little-endian layout against the specification.
	got := Input{Seq: 0x04030201, DX: -1, DY: 2}.Append(nil)
	if want := []byte{0x02, 1, 2, 3, 4, 0xff, 2}; !bytes.Equal(got, want) {
		t.Errorf("Input = %v, want %v", got, want)
	}
	check := (&GridCheck{Tick: 0x04030201, CRC: 0x08070605}).Append(nil)
	if want := []byte{0x87, 1, 2, 3, 4, 5, 6, 7, 8}; !bytes.Equal(check, want) {
		t.Errorf("GridCheck = %v, want %v", check, want)
	}
	grid := (&Grid{Tick: 2, GridW: 0x0190, GridH: 0x012c, Data: []byte{0xaa, 0xbb}}).Append(nil)
	if want := []byte{0x88, 2, 0, 0, 0, 0x90, 1, 0x2c, 1, 2, 0, 0, 0, 0xaa, 0xbb}; !bytes.Equal(grid, want) {
		t.Errorf("Grid = %v, want %v", grid, want)
	}
	if got := (Resync{}).Append(nil); !bytes.Equal(got, []byte{0x06}) {
		t.Errorf("Resync = %v", got)
	}
	ev := (&Events{Tick: 1, Paint: []PaintOp{{X: 0x0102, Y: 0x0304, Radius2: 5, R: 6, G: 7, B: 8, Strength: 9}}}).Append(nil)
	if want := []byte{0x83, 1, 0, 0, 0, 1, 0, 2, 1, 4, 3, 5, 6, 7, 8, 9, 0}; !bytes.Equal(ev, want) {
		t.Errorf("Events = %v, want %v", ev, want)
	}
}

func TestPos(t *testing.T) {
	t.Parallel()
	tests := []struct {
		units float64
		want  Pos
	}{{0, 0}, {-5, 0}, {1, 16}, {100.03, 1600}, {4000, 64000}, {5000, 65535}}
	for _, tt := range tests {
		if got := ToPos(tt.units); got != tt.want {
			t.Errorf("ToPos(%v) = %d, want %d", tt.units, got, tt.want)
		}
	}
	if got := Pos(24).Units(); got != 1.5 {
		t.Errorf("Units = %v", got)
	}
}
