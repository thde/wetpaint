// Package protocol implements the Wet Paint wire protocol, version 2, as
// specified in protocol/PROTOCOL.md. Every message has an Append method that
// writes the full frame (including the type byte) and can be read back with
// [DecodeClient] or [DecodeServer].
package protocol

import (
	"math"

	"github.com/thde/wetpaint/server/internal/paint"
)

// Version is the protocol version announced in Welcome.
const Version = 3

// Message type bytes.
const (
	TypeJoin    = 0x01
	TypeInput   = 0x02
	TypeRespawn = 0x03
	TypePing    = 0x04
	TypeView    = 0x05
	TypeResync  = 0x06

	TypeWelcome     = 0x81
	TypeState       = 0x82
	TypeEvents      = 0x83
	TypeDead        = 0x84
	TypeLeaderboard = 0x85
	TypePong        = 0x86
	TypeGridCheck   = 0x87
	TypeGrid        = 0x88
)

// WebSocket close codes that tell the client what to do next.
const (
	// CloseFull: Join arrived on a page that already had its players. The
	// client should ask /join for another page at once.
	CloseFull = 4001
	// CloseIdle: the connection sat on the join or death card too long. The
	// client should stay away until its user comes back.
	CloseIdle = 4002
)

// ColorRandom asks the server to pick a palette colour on Join, and to keep
// the previous one on Respawn.
const ColorRandom = 255

// NumBoards is the number of boards in a Leaderboard message: live, page
// last hour and global, each for painted, longest and mixed.
const NumBoards = 9

// RGB is a 24-bit colour.
type RGB struct{ R, G, B uint8 }

// Pos is a world coordinate in 1/16 units.
type Pos uint16

// ToPos quantises a world coordinate, clamping it to the representable range.
func ToPos(units float64) Pos {
	return Pos(math.Round(min(max(units*paint.PosScale, 0), math.MaxUint16)))
}

// Units converts back to world units.
func (p Pos) Units() float64 { return float64(p) / paint.PosScale }

// PaintOp is a paint stamp; see [paint.Op].
type PaintOp = paint.Op

// Cause says how a drop died.
type Cause uint8

// Death causes.
const (
	CauseDried    Cause = 0
	CauseAbsorbed Cause = 1
	CauseBrush    Cause = 2
	CauseLeft     Cause = 3
)

// ---- Client → server ----

// ClientMessage is any message a client may send.
type ClientMessage interface {
	Append(b []byte) []byte
	clientMessage()
}

// Join spawns the connection's drop. The server derives its name from Seed.
type Join struct {
	Seed  uint32
	Color uint8
}

// Input is the desired movement vector, each component in [-127, 127].
type Input struct {
	Seq    uint32
	DX, DY int8
}

// Respawn is Join after death, keeping the name.
type Respawn struct{ Color uint8 }

// Ping is echoed as Pong.
type Ping struct{ ClientTime uint32 }

// View is the client's viewport size in world units.
type View struct{ W, H uint16 }

// Resync asks for a fresh Grid after a GridCheck mismatch.
type Resync struct{}

func (Join) clientMessage()    {}
func (Input) clientMessage()   {}
func (Respawn) clientMessage() {}
func (Ping) clientMessage()    {}
func (View) clientMessage()    {}
func (Resync) clientMessage()  {}

// Append implements ClientMessage.
func (m Join) Append(b []byte) []byte {
	return append(appendU32(append(b, TypeJoin), m.Seed), m.Color)
}

// Append implements ClientMessage.
func (m Input) Append(b []byte) []byte {
	return append(appendU32(append(b, TypeInput), m.Seq), uint8(m.DX), uint8(m.DY))
}

// Append implements ClientMessage.
func (m Respawn) Append(b []byte) []byte { return append(b, TypeRespawn, m.Color) }

// Append implements ClientMessage.
func (m Ping) Append(b []byte) []byte { return appendU32(append(b, TypePing), m.ClientTime) }

// Append implements ClientMessage.
func (m View) Append(b []byte) []byte {
	return appendU16(appendU16(append(b, TypeView), m.W), m.H)
}

// Append implements ClientMessage.
func (Resync) Append(b []byte) []byte { return append(b, TypeResync) }

// DecodeClient parses one client frame.
func DecodeClient(frame []byte) (ClientMessage, error) {
	r := reader{b: frame}
	var m ClientMessage
	switch r.u8() {
	case TypeJoin:
		m = Join{Seed: r.u32(), Color: r.u8()}
	case TypeInput:
		m = Input{Seq: r.u32(), DX: int8(r.u8()), DY: int8(r.u8())}
	case TypeRespawn:
		m = Respawn{Color: r.u8()}
	case TypePing:
		m = Ping{ClientTime: r.u32()}
	case TypeView:
		m = View{W: r.u16(), H: r.u16()}
	case TypeResync:
		m = Resync{}
	default:
		return nil, ErrMalformed
	}
	if err := r.done(); err != nil {
		return nil, err
	}
	return m, nil
}

// ---- Server → client ----

// ServerMessage is any message the server may send.
type ServerMessage interface {
	Append(b []byte) []byte
	serverMessage()
}

// Rect is an axis-aligned rectangle: top-left corner and size.
type Rect struct{ X, Y, W, H float32 }

// RosterEntry names a living drop.
type RosterEntry struct {
	ID   uint16
	Name string
}

// Droplet is a water pickup.
type Droplet struct {
	ID   uint16
	X, Y Pos
}

// Bloom is the pencilled name label beside a death bloom.
type Bloom struct {
	X, Y Pos
	Tick uint32
	Name string
}

// Brush describes one sweep of the brush hazard.
type Brush struct {
	X0, Y0, X1, Y1 float32
	HalfWidth      float32
	StartTick      uint32
	DurationTicks  uint16
	Color          RGB
}

// Welcome is the first message on every connection.
type Welcome struct {
	Version      uint16
	Page         uint32
	YourID       uint16
	Tick         uint32
	Constants    []float32
	Palette      []RGB
	Blotters     []Rect
	Roster       []RosterEntry
	Droplets     []Droplet
	Blooms       []Bloom
	Brush        *Brush
	GridW, GridH uint16
	Grid         []byte // zlib stream of the snapshot planes
}

func (*Welcome) serverMessage() {}

// Append implements ServerMessage.
func (m *Welcome) Append(b []byte) []byte {
	b = append(b, TypeWelcome)
	b = appendU16(b, m.Version)
	b = appendU32(b, m.Page)
	b = appendU16(b, m.YourID)
	b = appendU32(b, m.Tick)
	b = append(b, uint8(len(m.Constants)))
	for _, c := range m.Constants {
		b = appendF32(b, c)
	}
	b = append(b, uint8(len(m.Palette)))
	for _, c := range m.Palette {
		b = appendRGB(b, c)
	}
	b = append(b, uint8(len(m.Blotters)))
	for _, r := range m.Blotters {
		b = appendF32(appendF32(appendF32(appendF32(b, r.X), r.Y), r.W), r.H)
	}
	b = append(b, uint8(len(m.Roster)))
	for _, e := range m.Roster {
		b = appendStr(appendU16(b, e.ID), e.Name)
	}
	b = appendU16(b, uint16(len(m.Droplets)))
	for _, d := range m.Droplets {
		b = appendDroplet(b, d)
	}
	b = append(b, uint8(len(m.Blooms)))
	for _, l := range m.Blooms {
		b = appendStr(appendU32(appendU16(appendU16(b, uint16(l.X)), uint16(l.Y)), l.Tick), l.Name)
	}
	if m.Brush == nil {
		b = append(b, 0)
	} else {
		b = m.Brush.append(append(b, 1))
	}
	return appendGrid(b, m.GridW, m.GridH, m.Grid)
}

func (r *reader) welcome() *Welcome {
	m := &Welcome{Version: r.u16(), Page: r.u32(), YourID: r.u16(), Tick: r.u32()}
	for range r.count(int(r.u8())) {
		m.Constants = append(m.Constants, r.f32())
	}
	for range r.count(int(r.u8())) {
		m.Palette = append(m.Palette, r.rgb())
	}
	for range r.count(int(r.u8())) {
		m.Blotters = append(m.Blotters, Rect{r.f32(), r.f32(), r.f32(), r.f32()})
	}
	for range r.count(int(r.u8())) {
		m.Roster = append(m.Roster, RosterEntry{ID: r.u16(), Name: r.str()})
	}
	for range r.count(int(r.u16())) {
		m.Droplets = append(m.Droplets, r.droplet())
	}
	for range r.count(int(r.u8())) {
		m.Blooms = append(m.Blooms, Bloom{X: r.pos(), Y: r.pos(), Tick: r.u32(), Name: r.str()})
	}
	if r.u8() == 1 {
		br := r.brush()
		m.Brush = &br
	}
	m.GridW, m.GridH, m.Grid = r.grid()
	return m
}

// appendGrid writes grid dimensions and the length-prefixed zlib stream, as
// Welcome and Grid both end.
func appendGrid(b []byte, w, h uint16, z []byte) []byte {
	b = appendU16(appendU16(b, w), h)
	b = appendU32(b, uint32(len(z)))
	return append(b, z...)
}

func (r *reader) grid() (w, h uint16, z []byte) {
	w, h = r.u16(), r.u16()
	switch n := r.u32(); {
	case uint64(n) > uint64(len(r.b)):
		r.failed = true
	case n > 0:
		z = r.take(int(n))
	}
	return w, h, z
}

func appendDroplet(b []byte, d Droplet) []byte {
	return appendU16(appendU16(appendU16(b, d.ID), uint16(d.X)), uint16(d.Y))
}

func (r *reader) droplet() Droplet { return Droplet{ID: r.u16(), X: r.pos(), Y: r.pos()} }

func (br Brush) append(b []byte) []byte {
	b = appendF32(appendF32(appendF32(appendF32(b, br.X0), br.Y0), br.X1), br.Y1)
	b = appendF32(b, br.HalfWidth)
	b = appendU32(b, br.StartTick)
	b = appendU16(b, br.DurationTicks)
	return appendRGB(b, br.Color)
}

func (r *reader) brush() Brush {
	return Brush{
		X0: r.f32(), Y0: r.f32(), X1: r.f32(), Y1: r.f32(),
		HalfWidth: r.f32(), StartTick: r.u32(), DurationTicks: r.u16(), Color: r.rgb(),
	}
}

// SelfState is the receiving player's own drop, at full precision for
// client-side prediction.
type SelfState struct {
	X, Y, VX, VY float32
	Wetness      float32
	Color        RGB
}

// DropState is another player's drop.
type DropState struct {
	ID       uint16
	X, Y     Pos
	Wetness2 uint8 // wetness × 2
	Color    RGB
}

// BeetleState is an ink beetle.
type BeetleState struct {
	ID   uint8
	X, Y Pos
}

// PuddleState is a spilled-water puddle. Remaining is the wetness left in it,
// rounded up.
type PuddleState struct {
	ID        uint8
	X, Y      Pos
	Remaining uint8
}

// State is the per-client world snapshot sent every tick.
type State struct {
	Tick           uint32
	AckSeq         uint32
	CloudX, CloudY Pos
	Alive          bool
	Self           SelfState // meaningful only if Alive
	Drops          []DropState
	Beetles        []BeetleState
	Puddles        []PuddleState
}

func (*State) serverMessage() {}

// Append implements ServerMessage.
func (m *State) Append(b []byte) []byte {
	b = append(b, TypeState)
	b = appendU32(appendU32(b, m.Tick), m.AckSeq)
	b = appendU16(appendU16(b, uint16(m.CloudX)), uint16(m.CloudY))
	if m.Alive {
		s := m.Self
		b = append(b, 1)
		b = appendF32(appendF32(appendF32(appendF32(b, s.X), s.Y), s.VX), s.VY)
		b = appendRGB(appendF32(b, s.Wetness), s.Color)
	} else {
		b = append(b, 0)
	}
	b = append(b, uint8(len(m.Drops)))
	for _, d := range m.Drops {
		b = appendU16(appendU16(appendU16(b, d.ID), uint16(d.X)), uint16(d.Y))
		b = appendRGB(append(b, d.Wetness2), d.Color)
	}
	b = append(b, uint8(len(m.Beetles)))
	for _, bt := range m.Beetles {
		b = appendU16(appendU16(append(b, bt.ID), uint16(bt.X)), uint16(bt.Y))
	}
	b = append(b, uint8(len(m.Puddles)))
	for _, p := range m.Puddles {
		b = append(appendU16(appendU16(append(b, p.ID), uint16(p.X)), uint16(p.Y)), p.Remaining)
	}
	return b
}

func (r *reader) state() *State {
	m := &State{Tick: r.u32(), AckSeq: r.u32(), CloudX: r.pos(), CloudY: r.pos()}
	if m.Alive = r.u8()&1 != 0; m.Alive {
		m.Self = SelfState{X: r.f32(), Y: r.f32(), VX: r.f32(), VY: r.f32(), Wetness: r.f32(), Color: r.rgb()}
	}
	for range r.count(int(r.u8())) {
		m.Drops = append(m.Drops, DropState{ID: r.u16(), X: r.pos(), Y: r.pos(), Wetness2: r.u8(), Color: r.rgb()})
	}
	for range r.count(int(r.u8())) {
		m.Beetles = append(m.Beetles, BeetleState{ID: r.u8(), X: r.pos(), Y: r.pos()})
	}
	for range r.count(int(r.u8())) {
		m.Puddles = append(m.Puddles, PuddleState{ID: r.u8(), X: r.pos(), Y: r.pos(), Remaining: r.u8()})
	}
	return m
}

// Dead tells a player how their life went.
type Dead struct {
	Cause        Cause
	Killer       uint16
	SecondsAlive float32
	CellsPainted uint32
	ColorsMixed  uint16
}

func (*Dead) serverMessage() {}

// Append implements ServerMessage.
func (m *Dead) Append(b []byte) []byte {
	b = appendU16(append(b, TypeDead, uint8(m.Cause)), m.Killer)
	return appendU16(appendU32(appendF32(b, m.SecondsAlive), m.CellsPainted), m.ColorsMixed)
}

// Entry is one leaderboard line.
type Entry struct {
	Name  string
	Value uint32
	Color RGB
}

// Board indices within Leaderboard.Boards.
const (
	BoardLivePainted = iota
	BoardLiveLongest
	BoardLiveMixed
	BoardHourPainted
	BoardHourLongest
	BoardHourMixed
	BoardGlobalPainted
	BoardGlobalLongest
	BoardGlobalMixed
)

// Leaderboard carries all nine boards.
type Leaderboard struct {
	Boards [NumBoards][]Entry
}

func (*Leaderboard) serverMessage() {}

// Append implements ServerMessage.
func (m *Leaderboard) Append(b []byte) []byte {
	b = append(b, TypeLeaderboard, NumBoards)
	for _, board := range m.Boards {
		b = append(b, uint8(len(board)))
		for _, e := range board {
			b = appendRGB(appendU32(appendStr(b, e.Name), e.Value), e.Color)
		}
	}
	return b
}

func (r *reader) leaderboard() *Leaderboard {
	m := &Leaderboard{}
	if r.u8() != NumBoards {
		r.failed = true
	}
	for i := range m.Boards {
		for range r.count(int(r.u8())) {
			m.Boards[i] = append(m.Boards[i], Entry{Name: r.str(), Value: r.u32(), Color: r.rgb()})
		}
	}
	return m
}

// Pong echoes a Ping.
type Pong struct {
	ClientTime uint32
	Tick       uint32
}

func (*Pong) serverMessage() {}

// Append implements ServerMessage.
func (m *Pong) Append(b []byte) []byte {
	return appendU32(appendU32(append(b, TypePong), m.ClientTime), m.Tick)
}

// GridCheck carries the checksum of the page's paint grid after every paint
// op up to and including Tick; see [paint.Grid.Checksum].
type GridCheck struct {
	Tick uint32
	CRC  uint32
}

func (*GridCheck) serverMessage() {}

// Append implements ServerMessage.
func (m *GridCheck) Append(b []byte) []byte {
	return appendU32(appendU32(append(b, TypeGridCheck), m.Tick), m.CRC)
}

// Grid replaces a client's whole paint grid with the snapshot taken at Tick.
type Grid struct {
	Tick         uint32
	GridW, GridH uint16
	Data         []byte // zlib stream of the snapshot planes
}

func (*Grid) serverMessage() {}

// Append implements ServerMessage.
func (m *Grid) Append(b []byte) []byte {
	return appendGrid(appendU32(append(b, TypeGrid), m.Tick), m.GridW, m.GridH, m.Data)
}

// DecodeServer parses one server frame.
func DecodeServer(frame []byte) (ServerMessage, error) {
	r := reader{b: frame}
	var m ServerMessage
	switch r.u8() {
	case TypeWelcome:
		m = r.welcome()
	case TypeState:
		m = r.state()
	case TypeEvents:
		m = r.events()
	case TypeDead:
		m = &Dead{Cause: Cause(r.u8()), Killer: r.u16(), SecondsAlive: r.f32(), CellsPainted: r.u32(), ColorsMixed: r.u16()}
	case TypeLeaderboard:
		m = r.leaderboard()
	case TypePong:
		m = &Pong{ClientTime: r.u32(), Tick: r.u32()}
	case TypeGridCheck:
		m = &GridCheck{Tick: r.u32(), CRC: r.u32()}
	case TypeGrid:
		g := &Grid{Tick: r.u32()}
		g.GridW, g.GridH, g.Data = r.grid()
		m = g
	default:
		return nil, ErrMalformed
	}
	if err := r.done(); err != nil {
		return nil, err
	}
	return m, nil
}
