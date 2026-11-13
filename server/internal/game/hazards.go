package game

import (
	"math"

	"github.com/thde/wetpaint/server/internal/protocol"
)

type beetle struct {
	id       uint8
	x, y     float64
	heading  float64
	tx, ty   float64 // where it is ambling to
	retarget uint32  // tick it picks somewhere new
}

func (w *World) initHazards() {
	w.placeBlotters()
	n := BeetlesMin + w.rng.IntN(BeetlesMax-BeetlesMin+1)
	for i := range n {
		b := beetle{
			id:      uint8(i + 1),
			x:       w.rng.Float64() * WorldW,
			y:       w.rng.Float64() * WorldH,
			heading: w.rng.Float64() * 2 * math.Pi,
		}
		w.beetles = append(w.beetles, b)
	}
	w.nextBrushTick = w.tick + w.brushInterval()
}

// placeBlotters scatters the static patches of blotting paper, avoiding
// overlaps where it can.
func (w *World) placeBlotters() {
	const margin, gap, tries = 100.0, 120.0, 30
	n := BlottersMin + w.rng.IntN(BlottersMax-BlottersMin+1)
	for range n {
		var r protocol.Rect
		for range tries {
			bw := 320 + w.rng.Float64()*240
			bh := 220 + w.rng.Float64()*160
			r = protocol.Rect{
				X: float32(margin + w.rng.Float64()*(WorldW-2*margin-bw)),
				Y: float32(margin + w.rng.Float64()*(WorldH-2*margin-bh)),
				W: float32(bw), H: float32(bh),
			}
			if !w.overlapsBlotter(r, gap) {
				break
			}
		}
		w.blotters = append(w.blotters, r)
	}
}

func (w *World) overlapsBlotter(r protocol.Rect, gap float32) bool {
	for _, o := range w.blotters {
		if r.X < o.X+o.W+gap && o.X < r.X+r.W+gap && r.Y < o.Y+o.H+gap && o.Y < r.Y+r.H+gap {
			return true
		}
	}
	return false
}

func (w *World) onBlotter(x, y float64) bool {
	for _, r := range w.blotters {
		if x >= float64(r.X) && x <= float64(r.X+r.W) && y >= float64(r.Y) && y <= float64(r.Y+r.H) {
			return true
		}
	}
	return false
}

// stepBeetles lets each beetle amble toward its target with a wiggle, nibble
// the paint under it and bump any drop it walks into.
func (w *World) stepBeetles() {
	const turnPerTick, arrive = 3.0 * dt, 30.0
	for i := range w.beetles {
		b := &w.beetles[i]
		if w.tick >= b.retarget || math.Hypot(b.tx-b.x, b.ty-b.y) < arrive {
			w.retargetBeetle(b)
		}
		want := math.Atan2(b.ty-b.y, b.tx-b.x) + 0.6*math.Sin(float64(w.tick)*0.35+float64(b.id))
		b.heading += min(max(angleDiff(want, b.heading), -turnPerTick), turnPerTick)
		b.x = min(max(b.x+math.Cos(b.heading)*BeetleSpeed*dt, BeetleRadius), WorldW-BeetleRadius)
		b.y = min(max(b.y+math.Sin(b.heading)*BeetleSpeed*dt, BeetleRadius), WorldH-BeetleRadius)

		if (w.tick+uint32(b.id))%2 == 0 {
			w.erase(b.x, b.y, BeetleBiteRadius)
		}

		for _, p := range w.players {
			if !p.Alive || w.tick < p.bumpUntil {
				continue
			}
			dx, dy := p.X-b.x, p.Y-b.y
			d := math.Hypot(dx, dy)
			if d >= BeetleRadius+p.Radius() {
				continue
			}
			if d < 1e-6 {
				angle := w.rng.Float64() * 2 * math.Pi
				dx, dy, d = math.Cos(angle), math.Sin(angle), 1
			}
			p.VX, p.VY = dx/d*BeetleKnockback, dy/d*BeetleKnockback
			p.addWetness(-BeetleDrain)
			p.bumpUntil = w.tick + Ticks(BeetleBumpCooldown)
			w.emit(protocol.BeetleBump{Player: p.ID, Beetle: b.id})
			w.retargetBeetle(b)
		}
	}
}

// retargetBeetle sends a beetle somewhere new: half the time toward a living
// drop, because that is where the fresh paint is.
func (w *World) retargetBeetle(b *beetle) {
	b.retarget = w.tick + Ticks(4+w.rng.Float64()*6)
	b.tx, b.ty = w.rng.Float64()*WorldW, w.rng.Float64()*WorldH
	if w.rng.IntN(2) == 0 {
		return
	}
	// Reservoir-sample one living drop.
	seen := 0
	for _, p := range w.players {
		if p.Alive {
			seen++
			if w.rng.IntN(seen) == 0 {
				b.tx, b.ty = p.X, p.Y
			}
		}
	}
}

// angleDiff returns a-b wrapped into (-π, π].
func angleDiff(a, b float64) float64 {
	d := math.Mod(a-b, 2*math.Pi)
	switch {
	case d > math.Pi:
		d -= 2 * math.Pi
	case d <= -math.Pi:
		d += 2 * math.Pi
	}
	return d
}

func (w *World) brushInterval() uint32 {
	return Ticks(BrushIntervalMin + w.rng.Float64()*(BrushIntervalMax-BrushIntervalMin))
}

// stepBrush schedules, telegraphs and sweeps the brush.
func (w *World) stepBrush() {
	if w.brush == nil {
		if w.tick >= w.nextBrushTick {
			w.brush = w.newBrush()
			w.emit(protocol.BrushWarn{Brush: *w.brush})
		}
		return
	}
	br := w.brush
	if w.tick < br.StartTick {
		return
	}
	end := br.StartTick + uint32(br.DurationTicks)
	if w.tick > end {
		w.brush = nil
		w.nextBrushTick = w.tick + w.brushInterval()
		return
	}

	hx, hy := brushHead(br, w.tick)
	px, py := brushHead(br, max(w.tick, br.StartTick+1)-1)
	for _, p := range w.players {
		if p.Alive && segmentDist(p.X, p.Y, px, py, hx, hy) < BrushHalfWidth+p.Radius()*BrushReach {
			w.kill(p, protocol.CauseBrush, 0)
		}
	}
	// Paint while the head is near the page; paint ops cannot be centred
	// off it, so the ends of the stroke are stamped from the edge.
	if hx > -BrushHalfWidth && hx < WorldW+BrushHalfWidth && hy > -BrushHalfWidth && hy < WorldH+BrushHalfWidth {
		w.stamp(min(max(hx, 0), WorldW), min(max(hy, 0), WorldH), BrushHalfWidth, br.Color, BrushStrength)
	}
}

// newBrush picks a straight band through the middle region of the page,
// starting and ending just off the paper.
func (w *World) newBrush() *protocol.Brush {
	cx := WorldW * (0.2 + 0.6*w.rng.Float64())
	cy := WorldH * (0.2 + 0.6*w.rng.Float64())
	angle := w.rng.Float64() * 2 * math.Pi
	dx, dy := math.Cos(angle), math.Sin(angle)

	// Distance from (cx, cy) to the page edge going forwards and backwards.
	fwd, back := math.Inf(1), math.Inf(1)
	edge := func(pos, dir, size float64) {
		if dir > 1e-9 {
			fwd, back = min(fwd, (size-pos)/dir), min(back, pos/dir)
		} else if dir < -1e-9 {
			fwd, back = min(fwd, -pos/dir), min(back, (pos-size)/dir)
		}
	}
	edge(cx, dx, WorldW)
	edge(cy, dy, WorldH)
	const overshoot = 1.5 * BrushHalfWidth
	fwd, back = fwd+overshoot, back+overshoot

	return &protocol.Brush{
		X0: float32(cx - dx*back), Y0: float32(cy - dy*back),
		X1: float32(cx + dx*fwd), Y1: float32(cy + dy*fwd),
		HalfWidth:     BrushHalfWidth,
		StartTick:     w.tick + Ticks(BrushWarnSeconds),
		DurationTicks: uint16(Ticks(BrushSeconds)), //nolint:gosec // constant, a few dozen ticks
		Color:         w.palette[w.rng.IntN(len(w.palette))],
	}
}

// brushHead is where the brush is at tick, clamped to its sweep.
func brushHead(br *protocol.Brush, tick uint32) (x, y float64) {
	f := min(max(float64(int64(tick)-int64(br.StartTick))/float64(br.DurationTicks), 0), 1)
	return float64(br.X0) + float64(br.X1-br.X0)*f, float64(br.Y0) + float64(br.Y1-br.Y0)*f
}

// inBrushPath reports whether a point lies within pad of the band a pending
// or active brush covers.
func (w *World) inBrushPath(x, y, pad float64) bool {
	br := w.brush
	return br != nil && segmentDist(x, y, float64(br.X0), float64(br.Y0), float64(br.X1), float64(br.Y1)) < pad
}

// segmentDist is the distance from point p to segment ab.
func segmentDist(px, py, ax, ay, bx, by float64) float64 {
	abx, aby := bx-ax, by-ay
	t := 0.0
	if l2 := abx*abx + aby*aby; l2 > 0 {
		t = min(max(((px-ax)*abx+(py-ay)*aby)/l2, 0), 1)
	}
	return math.Hypot(px-(ax+abx*t), py-(ay+aby*t))
}
