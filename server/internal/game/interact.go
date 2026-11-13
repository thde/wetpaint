package game

import (
	"math"

	"github.com/thde/wetpaint/server/internal/protocol"
)

// stepInteractions resolves every overlapping pair of drops: the clearly
// larger absorbs the smaller, otherwise their colours bleed into each other.
// A drop left alone recovers its own colour.
func (w *World) stepInteractions() {
	for _, p := range w.players {
		p.touched = false
	}
	for i, a := range w.players {
		for _, b := range w.players[i+1:] {
			if !a.Alive {
				break
			}
			if !b.Alive {
				continue
			}
			d := math.Hypot(a.X-b.X, a.Y-b.Y)
			if d >= a.Radius()+b.Radius() {
				continue
			}
			switch {
			case canAbsorb(a, b, d):
				w.absorb(a, b)
			case canAbsorb(b, a, d):
				w.absorb(b, a)
			default:
				w.bleed(a, b)
			}
		}
	}
	for _, p := range w.players {
		if p.Alive && !p.touched {
			p.unbleed()
		}
	}
}

func (w *World) absorb(big, small *Player) {
	big.addWetness(small.Wetness * AbsorbWetness)
	big.Color = big.Color.Mix(small.Color, AbsorbBlend)
	big.touched = true
	w.kill(small, protocol.CauseAbsorbed, big.ID)
}

// bleed shifts two touching drops' colours toward each other and, once per
// cooldown for this pair, grants both the mixing bonus.
func (w *World) bleed(a, b *Player) {
	ca, cb := a.Color, b.Color
	a.Color = ca.Mix(cb, BleedPerSec*dt)
	b.Color = cb.Mix(ca, BleedPerSec*dt)
	a.touched, b.touched = true, true

	key := pairKey(a.ID, b.ID)
	if w.tick < w.pairCooldown[key] {
		return
	}
	w.pairCooldown[key] = w.tick + Ticks(MixCooldownSecs)
	for _, p := range [...]*Player{a, b} {
		p.addWetness(MixBonus)
		p.ColorsMixed++
	}
	w.emit(protocol.Mix{A: a.ID, B: b.ID})
}

func pairKey(a, b uint16) uint32 {
	if a > b {
		a, b = b, a
	}
	return uint32(a)<<16 | uint32(b)
}

// kill ends p's life. Unless the player simply left, it leaves a bloom with
// a name label and a report of the life.
func (w *World) kill(p *Player, cause protocol.Cause, killer uint16) {
	p.Alive = false
	color := p.Color.RGB()
	x, y := protocol.ToPos(p.X), protocol.ToPos(p.Y)
	w.emit(protocol.Death{ID: p.ID, Cause: cause, Killer: killer, X: x, Y: y, Color: color})
	if cause == protocol.CauseLeft {
		return
	}

	w.bloom(p.X, p.Y, p.Color)
	if len(w.blooms) == MaxBlooms {
		w.blooms = w.blooms[1:]
	}
	w.blooms = append(w.blooms, protocol.Bloom{X: x, Y: y, Tick: w.tick, Name: p.Name})

	w.out.Deaths = append(w.out.Deaths, DeathReport{
		Player: p,
		Name:   p.Name,
		Color:  color,
		Dead: protocol.Dead{
			Cause:        cause,
			Killer:       killer,
			SecondsAlive: float32(w.tick-p.bornTick) / TickRate,
			CellsPainted: p.CellsPainted,
			ColorsMixed:  p.ColorsMixed,
		},
	})
}

// bloom stamps a watercolour bloom: a ring of darker lobes for the
// cauliflower edge and rim, washed over with a lighter centre.
func (w *World) bloom(x, y float64, c Color) {
	radius := BloomRadius * (0.85 + 0.3*w.rng.Float64())
	rim := c.Toward(black, 0.28).RGB()
	lobes := 9 + w.rng.IntN(4)
	for i := range lobes {
		angle := 2 * math.Pi * (float64(i) + 0.6*w.rng.Float64()) / float64(lobes)
		reach := radius * (0.58 + 0.2*w.rng.Float64())
		size := radius * (0.34 + 0.2*w.rng.Float64())
		w.stamp(x+math.Cos(angle)*reach, y+math.Sin(angle)*reach, size, rim, 210)
	}
	w.stamp(x, y, radius*0.8, c.RGB(), 190)
	w.stamp(x, y, radius*0.5, c.Toward(white, 0.45).RGB(), 150)
}
