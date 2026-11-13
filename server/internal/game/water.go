package game

import (
	"math"

	"github.com/thde/wetpaint/server/internal/protocol"
)

type cloud struct{ x, y, vx, vy float64 }

type droplet struct {
	id      uint16
	x, y    float64
	expires uint32
}

type puddle struct {
	id        uint8
	x, y      float64
	remaining float64
	expires   uint32
}

// The cloud keeps to this box so that its rain always lands on the page.
const (
	cloudMinX = 200.0
	cloudMaxX = WorldW - 200.0
	cloudMinY = 100.0
	cloudMaxY = WorldH - CloudRainBottom - 50.0
)

func (w *World) initWater() {
	angle := w.rng.Float64() * 2 * math.Pi
	w.cloud = cloud{
		x:  cloudMinX + w.rng.Float64()*(cloudMaxX-cloudMinX),
		y:  cloudMinY + w.rng.Float64()*(cloudMaxY-cloudMinY),
		vx: math.Cos(angle) * CloudSpeed,
		vy: math.Sin(angle) * CloudSpeed,
	}
	w.nextPuddleTick = w.tick + w.puddleInterval()
}

func (w *World) puddleInterval() uint32 {
	return Ticks(PuddleIntervalMin + w.rng.Float64()*(PuddleIntervalMax-PuddleIntervalMin))
}

// stepCloud drifts the cloud on a gently meandering course and lets it rain.
func (w *World) stepCloud() {
	c := &w.cloud
	turn := (w.rng.Float64() - 0.5) * 0.08
	sin, cos := math.Sincos(turn)
	c.vx, c.vy = c.vx*cos-c.vy*sin, c.vx*sin+c.vy*cos
	c.x += c.vx * dt
	c.y += c.vy * dt
	if c.x < cloudMinX || c.x > cloudMaxX {
		c.vx = -c.vx
		c.x = min(max(c.x, cloudMinX), cloudMaxX)
	}
	if c.y < cloudMinY || c.y > cloudMaxY {
		c.vy = -c.vy
		c.y = min(max(c.y, cloudMinY), cloudMaxY)
	}

	if len(w.droplets) < MaxDroplets && w.rng.Float64() < DropletsPerSec*dt {
		// Quantise now so server and clients agree on where it is.
		pos := protocol.Droplet{
			X: protocol.ToPos(c.x + (w.rng.Float64()*2-1)*CloudHalfWidth),
			Y: protocol.ToPos(c.y + CloudRainTop + w.rng.Float64()*(CloudRainBottom-CloudRainTop)),
		}
		w.nextDropletID++
		pos.ID = w.nextDropletID
		w.droplets = append(w.droplets, droplet{
			id: pos.ID, x: pos.X.Units(), y: pos.Y.Units(),
			expires: w.tick + Ticks(DropletSeconds),
		})
		w.emit(protocol.DropletSpawn(pos))
	}
}

// stepDroplets lets drops drink droplets and evaporates old ones.
func (w *World) stepDroplets() {
	kept := w.droplets[:0]
	for _, d := range w.droplets {
		by, gone := uint16(0), w.tick >= d.expires
		for _, p := range w.players {
			if p.Alive && math.Hypot(p.X-d.x, p.Y-d.y) < p.Radius()+DropletRadius {
				p.addWetness(DropletWetness)
				by, gone = p.ID, true
				break
			}
		}
		if gone {
			w.emit(protocol.DropletGone{ID: d.id, By: by})
			continue
		}
		kept = append(kept, d)
	}
	w.droplets = kept
}

// stepPuddles spills new puddles and lets drops standing in one soak it up.
func (w *World) stepPuddles() {
	if w.tick >= w.nextPuddleTick {
		w.nextPuddleTick = w.tick + w.puddleInterval()
		if len(w.puddles) < MaxPuddles {
			w.nextPuddleID++
			w.puddles = append(w.puddles, puddle{
				id:        w.nextPuddleID,
				x:         PuddleRadius + w.rng.Float64()*(WorldW-2*PuddleRadius),
				y:         PuddleRadius + w.rng.Float64()*(WorldH-2*PuddleRadius),
				remaining: PuddleCapacity,
				expires:   w.tick + Ticks(PuddleSeconds),
			})
		}
	}

	kept := w.puddles[:0]
	for _, pd := range w.puddles {
		for _, p := range w.players {
			if !p.Alive || math.Hypot(p.X-pd.x, p.Y-pd.y) >= PuddleRadius {
				continue
			}
			sip := min(PuddleRestorePerSec*dt, pd.remaining, MaxWetness-p.Wetness)
			p.addWetness(sip)
			pd.remaining -= sip
		}
		if pd.remaining > 1e-9 && w.tick < pd.expires {
			kept = append(kept, pd)
		}
	}
	w.puddles = kept
}
