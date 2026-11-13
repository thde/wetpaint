package game

import (
	"math"
	"math/rand/v2"

	"github.com/thde/wetpaint/server/internal/names"
	"github.com/thde/wetpaint/server/internal/paint"
	"github.com/thde/wetpaint/server/internal/protocol"
)

// Options adjust a World. The zero value is the real game.
type Options struct {
	// StartTick is the tick the world resumes from, so that a restored paint
	// grid keeps its age.
	StartTick uint32
	// NoWater disables the cloud, droplets and puddles.
	NoWater bool
	// NoHazards disables the brush, blotting paper and beetles.
	NoHazards bool
}

// World is the complete state of one page. It is not safe for concurrent use.
type World struct {
	rng     *rand.Rand
	opts    Options
	tick    uint32
	grid    *paint.Grid
	palette []protocol.RGB

	players []*Player // in join order
	nextID  uint16

	cloud          cloud
	droplets       []droplet
	nextDropletID  uint16
	puddles        []puddle
	nextPuddleID   uint8
	nextPuddleTick uint32

	blotters      []protocol.Rect
	beetles       []beetle
	brush         *protocol.Brush
	nextBrushTick uint32

	pairCooldown map[uint32]uint32 // pair key → tick the mix bonus is available again
	blooms       []protocol.Bloom

	out Output
}

// Output is everything one Step produced that clients must hear about.
type Output struct {
	Tick   uint32
	Paint  []paint.Op
	Events []protocol.Event
	Deaths []DeathReport
}

// DeathReport is the summary of a finished life.
type DeathReport struct {
	Player *Player
	Name   string
	Color  protocol.RGB
	Dead   protocol.Dead
}

// NewWorld creates a page. All randomness comes from rng, so a fixed seed
// replays identically.
func NewWorld(rng *rand.Rand, opts Options) *World {
	w := &World{
		rng:          rng,
		opts:         opts,
		tick:         opts.StartTick,
		grid:         paint.NewGrid(GridW, GridH, fadeTicks, freshTicks),
		palette:      Palette(),
		pairCooldown: make(map[uint32]uint32),
	}
	if !opts.NoWater {
		w.initWater()
	}
	if !opts.NoHazards {
		w.initHazards()
	}
	return w
}

// Tick is the number of the last completed step.
func (w *World) Tick() uint32 { return w.tick }

// Grid exposes the paint layer, e.g. for snapshots.
func (w *World) Grid() *paint.Grid { return w.grid }

// Players returns every seat on the page, alive or not. The slice aliases
// the world and is valid until the next AddPlayer or RemovePlayer.
func (w *World) Players() []*Player { return w.players }

// AddPlayer seats a new connection as a spectator. It returns nil if the
// page has run out of IDs, which the page cap prevents in practice.
func (w *World) AddPlayer() *Player {
	if len(w.players) >= math.MaxUint16 {
		return nil
	}
	for {
		w.nextID++
		if w.nextID != 0 && w.player(w.nextID) == nil {
			break
		}
	}
	p := &Player{ID: w.nextID, viewW: DefaultViewW, viewH: DefaultViewH}
	w.players = append(w.players, p)
	return p
}

func (w *World) player(id uint16) *Player {
	for _, p := range w.players {
		if p.ID == id {
			return p
		}
	}
	return nil
}

// RemovePlayer frees a seat. A living drop vanishes without a bloom.
func (w *World) RemovePlayer(p *Player) {
	if p.Alive {
		w.kill(p, protocol.CauseLeft, 0)
	}
	for i, q := range w.players {
		if q == p {
			w.players = append(w.players[:i], w.players[i+1:]...)
			break
		}
	}
}

// Join spawns p's drop. A player without a name gets the one its client's
// seed stands for, or that seed's next choice if someone on the page already
// has it, and keeps it for as long as it is connected. If the seed's choices
// are all taken (many clients sharing a seed), a random free name is used.
// Join is ignored while the drop is alive.
func (w *World) Join(p *Player, seed uint32, color uint8) {
	if p.Alive {
		return
	}
	if p.Name == "" {
		p.Name = names.FromSeed(seed, w.nameTaken)
		if w.nameTaken(p.Name) {
			p.Name = names.Generate(w.rng, w.nameTaken)
		}
	}
	p.joined = true
	w.spawn(p, color)
}

// nameTaken reports whether a player on the page is called name.
func (w *World) nameTaken(name string) bool {
	for _, q := range w.players {
		if q.Name == name {
			return true
		}
	}
	return false
}

// Respawn spawns p's drop again after death, keeping its name.
// [protocol.ColorRandom] keeps the previous palette colour.
func (w *World) Respawn(p *Player, color uint8) {
	if p.Alive || !p.joined {
		return
	}
	if color == protocol.ColorRandom {
		color = p.palette
	}
	w.spawn(p, color)
}

// SetInput queues one of p's Inputs. Each step applies one queued input in
// order (see [Player.nextInput]), so the server steps the drop exactly as
// often as the client predicted it, even when Inputs arrive unevenly.
func (w *World) SetInput(p *Player, seq uint32, dx, dy int8) {
	p.inputs.push(queuedInput{seq: seq, dx: dx, dy: dy})
}

// SetView records p's viewport size in world units, used for culling.
func (w *World) SetView(p *Player, width, height float64) {
	p.viewW = min(max(width, MinView), WorldW)
	p.viewH = min(max(height, MinView), WorldH)
}

func (w *World) spawn(p *Player, color uint8) {
	if int(color) >= len(w.palette) {
		color = uint8(w.rng.IntN(len(w.palette))) //nolint:gosec // the palette has fewer than 256 colours
	}
	p.palette = color
	p.ownColor = colorOf(w.palette[color])
	p.Color = p.ownColor
	p.X, p.Y = w.safeSpot()
	p.VX, p.VY, p.dx, p.dy = 0, 0, 0, 0
	p.inputs.clear() // steering meant for the previous life
	p.Wetness = MaxWetness
	p.CellsPainted, p.ColorsMixed = 0, 0
	p.bornTick, p.bumpUntil = w.tick, 0
	p.Alive = true
	w.emit(protocol.Spawn{ID: p.ID, Name: p.Name})
}

// safeSpot picks the candidate furthest from other drops and beetles, never
// on blotting paper or in the brush's path if that can be avoided.
func (w *World) safeSpot() (x, y float64) {
	best := math.Inf(-1)
	for range SpawnCandidates {
		cx := SpawnMargin + w.rng.Float64()*(WorldW-2*SpawnMargin)
		cy := SpawnMargin + w.rng.Float64()*(WorldH-2*SpawnMargin)
		score := WorldW
		for _, o := range w.players {
			if o.Alive {
				score = min(score, math.Hypot(o.X-cx, o.Y-cy)-o.Radius())
			}
		}
		for i := range w.beetles {
			score = min(score, math.Hypot(w.beetles[i].x-cx, w.beetles[i].y-cy))
		}
		if w.onBlotter(cx, cy) || w.inBrushPath(cx, cy, 2*BrushHalfWidth) {
			score -= WorldW + WorldH
		}
		if score > best {
			best, x, y = score, cx, cy
		}
	}
	return x, y
}

// Step advances the page by one tick and returns what happened, including
// events queued by Join, Respawn and RemovePlayer since the previous step.
// The returned slices are reused: consume them before calling the world
// again.
func (w *World) Step() Output {
	w.tick++

	for _, p := range w.players {
		if !p.Alive {
			continue
		}
		p.move()
		moving := p.Speed() > MovingSpeed
		p.Wetness = max(p.Wetness-dryRate(moving, w.onBlotter(p.X, p.Y))*dt, 0)
		if moving && p.Wetness > 0 {
			strength := TrailStrengthBase + p.Wetness*TrailStrengthPerWet
			p.CellsPainted += uint32(w.stamp(p.X, p.Y, p.Radius()*TrailRadiusFactor, p.Color.RGB(), strength)) //nolint:gosec // stamp returns a non-negative cell count
		}
	}

	if !w.opts.NoWater {
		w.stepCloud()
		w.stepDroplets()
		w.stepPuddles()
	}
	if !w.opts.NoHazards {
		w.stepBeetles()
		w.stepBrush()
	}
	w.stepInteractions()

	for _, p := range w.players {
		if p.Alive && p.Wetness <= 0 {
			w.kill(p, protocol.CauseDried, 0)
		}
	}
	w.expire()

	out := w.out
	out.Tick = w.tick
	w.out = Output{Paint: out.Paint[:0], Events: out.Events[:0], Deaths: out.Deaths[:0]}
	return out
}

// expire forgets bloom labels and pair cooldowns that no longer matter.
func (w *World) expire() {
	for len(w.blooms) > 0 && w.tick-w.blooms[0].Tick >= fadeTicks {
		w.blooms = w.blooms[1:]
	}
	if w.tick%Ticks(MixCooldownSecs) == 0 {
		for key, until := range w.pairCooldown {
			if w.tick >= until {
				delete(w.pairCooldown, key)
			}
		}
	}
}

func (w *World) emit(e protocol.Event) { w.out.Events = append(w.out.Events, e) }

// stamp paints one op and returns how many cells it newly covered.
func (w *World) stamp(x, y, radius float64, c protocol.RGB, strength float64) int {
	op := paint.Op{
		X:        uint16(protocol.ToPos(x)),
		Y:        uint16(protocol.ToPos(y)),
		Radius2:  uint8(math.Round(min(max(radius*2, 0), 255))),
		R:        c.R,
		G:        c.G,
		B:        c.B,
		Strength: uint8(math.Round(min(max(strength, 1), 255))),
	}
	w.out.Paint = append(w.out.Paint, op)
	return w.grid.Apply(op, w.tick)
}

// erase clears paint under a circle, if there is any.
func (w *World) erase(x, y, radius float64) {
	op := paint.Op{
		X:       uint16(protocol.ToPos(x)),
		Y:       uint16(protocol.ToPos(y)),
		Radius2: uint8(math.Round(min(max(radius*2, 0), 255))),
	}
	if w.grid.Painted(op, w.tick) {
		w.out.Paint = append(w.out.Paint, op)
		w.grid.Apply(op, w.tick)
	}
}

// visible reports whether a circle is inside viewer's viewport plus margin.
// Spectators and dead players see the whole page.
func visible(viewer *Player, x, y, radius float64) bool {
	if viewer == nil || !viewer.Alive {
		return true
	}
	return math.Abs(x-viewer.X) <= viewer.viewW/2+ViewMargin+radius &&
		math.Abs(y-viewer.Y) <= viewer.viewH/2+ViewMargin+radius
}

// FillState overwrites st with the world as viewer should see it, reusing
// st's slices.
func (w *World) FillState(st *protocol.State, viewer *Player) {
	st.Tick = w.tick
	st.AckSeq = viewer.AckSeq
	st.CloudX, st.CloudY = protocol.ToPos(w.cloud.x), protocol.ToPos(w.cloud.y)
	st.Alive = viewer.Alive
	if viewer.Alive {
		st.Self = protocol.SelfState{
			X: float32(viewer.X), Y: float32(viewer.Y),
			VX: float32(viewer.VX), VY: float32(viewer.VY),
			Wetness: float32(viewer.Wetness),
			Color:   viewer.Color.RGB(),
		}
	}

	st.Drops = st.Drops[:0]
	for _, p := range w.players {
		if p == viewer || !p.Alive || !visible(viewer, p.X, p.Y, p.Radius()) {
			continue
		}
		st.Drops = append(st.Drops, protocol.DropState{
			ID: p.ID,
			X:  protocol.ToPos(p.X), Y: protocol.ToPos(p.Y),
			Wetness2: uint8(math.Round(p.Wetness * 2)),
			Color:    p.Color.RGB(),
		})
	}

	st.Beetles = st.Beetles[:0]
	for i := range w.beetles {
		b := &w.beetles[i]
		if visible(viewer, b.x, b.y, BeetleRadius) {
			st.Beetles = append(st.Beetles, protocol.BeetleState{ID: b.id, X: protocol.ToPos(b.x), Y: protocol.ToPos(b.y)})
		}
	}

	st.Puddles = st.Puddles[:0]
	for i := range w.puddles {
		p := &w.puddles[i]
		st.Puddles = append(st.Puddles, protocol.PuddleState{
			ID: p.id,
			X:  protocol.ToPos(p.x), Y: protocol.ToPos(p.y),
			Remaining: uint8(math.Ceil(p.remaining - 1e-6)), // shrug off float dust
		})
	}
}

// FillWelcome sets the world-dependent fields of a Welcome message, except
// the paint grid, which the caller snapshots and compresses.
func (w *World) FillWelcome(m *protocol.Welcome) {
	m.Tick = w.tick
	m.Constants = WireConstants()
	m.Palette = w.palette
	m.Blotters = w.blotters
	for _, p := range w.players {
		if p.Alive {
			m.Roster = append(m.Roster, protocol.RosterEntry{ID: p.ID, Name: p.Name})
		}
	}
	for _, d := range w.droplets {
		m.Droplets = append(m.Droplets, protocol.Droplet{ID: d.id, X: protocol.ToPos(d.x), Y: protocol.ToPos(d.y)})
	}
	m.Blooms = append(m.Blooms, w.blooms...)
	if w.brush != nil {
		br := *w.brush
		m.Brush = &br
	}
	m.GridW, m.GridH = uint16(GridW), uint16(GridH)
}

// SecondsAlive is how long p's current life has lasted.
func (w *World) SecondsAlive(p *Player) float64 {
	return float64(w.tick-p.bornTick) / TickRate
}
