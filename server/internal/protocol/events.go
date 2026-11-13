package protocol

// Event kinds within an Events message.
const (
	kindSpawn        = 1
	kindDeath        = 2
	kindDropletSpawn = 3
	kindDropletGone  = 4
	kindBrushWarn    = 5
	kindMix          = 6
	kindBeetleBump   = 7
)

// Event is one discrete happening on the page.
type Event interface {
	kind() uint8
	append(b []byte) []byte
}

// Spawn announces a drop entering play, with its sanitised name.
type Spawn struct {
	ID   uint16
	Name string
}

// Death announces a drop leaving play. The bloom arrives as paint ops.
type Death struct {
	ID     uint16
	Cause  Cause
	Killer uint16 // 0 unless absorbed
	X, Y   Pos
	Color  RGB
}

// DropletSpawn adds a water droplet.
type DropletSpawn Droplet

// DropletGone removes a droplet; By is the drinker, or 0 if it evaporated.
type DropletGone struct {
	ID uint16
	By uint16
}

// BrushWarn telegraphs a brush sweep.
type BrushWarn struct{ Brush Brush }

// Mix fires when two drops earn their mixing bonus.
type Mix struct{ A, B uint16 }

// BeetleBump fires when a beetle knocks a player back.
type BeetleBump struct {
	Player uint16
	Beetle uint8
}

func (Spawn) kind() uint8        { return kindSpawn }
func (Death) kind() uint8        { return kindDeath }
func (DropletSpawn) kind() uint8 { return kindDropletSpawn }
func (DropletGone) kind() uint8  { return kindDropletGone }
func (BrushWarn) kind() uint8    { return kindBrushWarn }
func (Mix) kind() uint8          { return kindMix }
func (BeetleBump) kind() uint8   { return kindBeetleBump }

func (e Spawn) append(b []byte) []byte { return appendStr(appendU16(b, e.ID), e.Name) }

func (e Death) append(b []byte) []byte {
	b = appendU16(append(appendU16(b, e.ID), uint8(e.Cause)), e.Killer)
	return appendRGB(appendU16(appendU16(b, uint16(e.X)), uint16(e.Y)), e.Color)
}

func (e DropletSpawn) append(b []byte) []byte { return appendDroplet(b, Droplet(e)) }
func (e DropletGone) append(b []byte) []byte  { return appendU16(appendU16(b, e.ID), e.By) }
func (e BrushWarn) append(b []byte) []byte    { return e.Brush.append(b) }
func (e Mix) append(b []byte) []byte          { return appendU16(appendU16(b, e.A), e.B) }
func (e BeetleBump) append(b []byte) []byte   { return append(appendU16(b, e.Player), e.Beetle) }

// Events carries one tick's paint ops and events. It is reliable: clients
// replay the paint ops to keep their grid identical to the server's.
type Events struct {
	Tick   uint32
	Paint  []PaintOp
	Events []Event
}

func (*Events) serverMessage() {}

// Append implements ServerMessage.
func (m *Events) Append(b []byte) []byte {
	b = appendU32(append(b, TypeEvents), m.Tick)
	b = appendU16(b, uint16(len(m.Paint)))
	for _, op := range m.Paint {
		b = appendU16(appendU16(b, op.X), op.Y)
		b = append(b, op.Radius2, op.R, op.G, op.B, op.Strength)
	}
	b = append(b, uint8(len(m.Events)))
	for _, e := range m.Events {
		b = e.append(append(b, e.kind()))
	}
	return b
}

// Limits of one Events message, imposed by its u16 and u8 counts.
const (
	MaxPaintOps = 1<<16 - 1
	MaxEvents   = 1<<8 - 1
)

func (r *reader) events() *Events {
	m := &Events{Tick: r.u32()}
	for range r.count(int(r.u16())) {
		m.Paint = append(m.Paint, PaintOp{
			X: r.u16(), Y: r.u16(), Radius2: r.u8(),
			R: r.u8(), G: r.u8(), B: r.u8(), Strength: r.u8(),
		})
	}
	for range r.count(int(r.u8())) {
		var e Event
		switch r.u8() {
		case kindSpawn:
			e = Spawn{ID: r.u16(), Name: r.str()}
		case kindDeath:
			e = Death{ID: r.u16(), Cause: Cause(r.u8()), Killer: r.u16(), X: r.pos(), Y: r.pos(), Color: r.rgb()}
		case kindDropletSpawn:
			e = DropletSpawn(r.droplet())
		case kindDropletGone:
			e = DropletGone{ID: r.u16(), By: r.u16()}
		case kindBrushWarn:
			e = BrushWarn{Brush: r.brush()}
		case kindMix:
			e = Mix{A: r.u16(), B: r.u16()}
		case kindBeetleBump:
			e = BeetleBump{Player: r.u16(), Beetle: r.u8()}
		default:
			r.failed = true
			return m
		}
		m.Events = append(m.Events, e)
	}
	return m
}
