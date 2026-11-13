package game

import "math"

// Player is one connection's seat on the page. It exists from connect to
// disconnect; the drop itself only while Alive.
type Player struct {
	ID    uint16
	Name  string
	Alive bool

	X, Y, VX, VY float64
	Wetness      float64
	Color        Color // as it looks now, after bleeding

	AckSeq       uint32 // seq of the last input applied
	CellsPainted uint32 // this life
	ColorsMixed  uint16 // this life

	joined     bool // has sent Join, so Respawn is allowed
	palette    uint8
	ownColor   Color // the palette colour chosen at spawn; Color drifts back to it
	touched    bool  // overlapped another drop this tick
	dx, dy     int8
	pendingSeq uint32
	viewW      float64
	viewH      float64
	bornTick   uint32
	bumpUntil  uint32 // immune to beetles before this tick
}

// Radius is the drop's size, which grows with wetness.
func (p *Player) Radius() float64 { return RadiusBase + p.Wetness*RadiusPerWetness }

// Speed is the drop's current speed in units per second.
func (p *Player) Speed() float64 { return math.Hypot(p.VX, p.VY) }

// move advances the drop one tick. This is the movement step of the protocol
// document, which clients replicate for prediction; change both together.
func (p *Player) move() {
	dx, dy := float64(p.dx)/127, float64(p.dy)/127
	if l := math.Hypot(dx, dy); l > 1 {
		dx, dy = dx/l, dy/l
	}
	maxSpeed := SpeedMax - (SpeedMax-SpeedMin)*p.Wetness/MaxWetness
	k := math.Min(1, Accel*dt)
	p.VX += (dx*maxSpeed - p.VX) * k
	p.VY += (dy*maxSpeed - p.VY) * k
	p.X += p.VX * dt
	p.Y += p.VY * dt

	r := p.Radius()
	p.X, p.VX = clampAxis(p.X, p.VX, r, WorldW-r)
	p.Y, p.VY = clampAxis(p.Y, p.VY, r, WorldH-r)
	p.AckSeq = p.pendingSeq
}

func clampAxis(pos, vel, lo, hi float64) (newPos, newVel float64) {
	switch {
	case pos < lo:
		return lo, 0
	case pos > hi:
		return hi, 0
	}
	return pos, vel
}

// dryRate is how much wetness a drop loses per second.
func dryRate(moving, onBlotter bool) float64 {
	rate := DryPerSec
	if moving {
		rate += DryMovingPerSec
	}
	if onBlotter {
		rate *= BlotterDryMultiplier
	}
	return rate
}

// canAbsorb reports whether big swallows small when their centres are dist
// apart: it must be clearly larger and cover the smaller drop's centre.
func canAbsorb(big, small *Player, dist float64) bool {
	r := big.Radius()
	return r > AbsorbRatio*small.Radius() && dist < r
}

// unbleedSnap is the distance, in channel units, at which a recovering
// colour is set to the drop's own exactly. Below half a unit the two already
// round to the same wire colour.
const unbleedSnap = 0.5

// unbleed moves the drop's colour one tick back toward its own, so what it
// picked up from other drops wears off instead of accumulating.
func (p *Player) unbleed() {
	if p.Color == p.ownColor {
		return
	}
	p.Color = p.Color.Mix(p.ownColor, UnbleedPerSec*dt)
	if p.Color.Dist(p.ownColor) < unbleedSnap {
		p.Color = p.ownColor
	}
}

func (p *Player) addWetness(w float64) {
	p.Wetness = min(max(p.Wetness+w, 0), MaxWetness)
}
