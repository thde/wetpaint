// Package game is the authoritative Wet Paint simulation. It is pure: no
// clocks, no goroutines, no I/O. A page goroutine owns one [World] and calls
// [World.Step] twenty times a second.
package game

import (
	"github.com/thde/wetpaint/server/internal/paint"
	"github.com/thde/wetpaint/server/internal/protocol"
)

// This file is the single source of truth for gameplay tuning. The values
// clients need for prediction and rendering reach them through
// [WireConstants] in the Welcome message, so nothing here is duplicated in
// the client.

// World and timing.
const (
	WorldW   = 4000.0 // page width, world units
	WorldH   = 3000.0 // page height
	TickRate = 20     // simulation steps per second
	PageCap  = 60     // players per page: connections that have joined, alive or dead

	SpectatorCap = 20    // connections per page that have not joined yet
	IdleSeconds  = 180.0 // a connection that has not been alive for this long is closed

	// CellSize is fixed by the wire format of paint ops (see package paint).
	CellSize = paint.CellSize
	GridW    = int(WorldW / CellSize)
	GridH    = int(WorldH / CellSize)

	FadeSeconds    = 300.0 // paint alpha decays to zero over this long
	RepaintSeconds = 2.0   // a cell repainted after this long counts as painted again
)

// The drop.
const (
	MaxWetness       = 100.0
	DryPerSec        = 1.1  // passive drying
	DryMovingPerSec  = 0.3  // extra drying while moving
	MovingSpeed      = 15.0 // speed above which a drop counts as moving
	RadiusBase       = 10.0 // radius at wetness 0
	RadiusPerWetness = 0.25
	SpeedMax         = 320.0 // top speed at wetness 0, units/s
	SpeedMin         = 150.0 // top speed at full wetness
	Accel            = 4.0   // velocity approach rate, 1/s; low values feel slippery

	TrailRadiusFactor   = 0.7 // stamp radius as a fraction of drop radius
	TrailStrengthBase   = 110 // stamp strength at wetness 0
	TrailStrengthPerWet = 1.0 // extra strength per wetness point

	SpawnMargin     = 150.0 // spawns keep this far from the page edge
	SpawnCandidates = 16    // random spots tried per spawn; the loneliest wins
)

// Water.
const (
	CloudSpeed      = 40.0  // units/s
	CloudHalfWidth  = 90.0  // droplets fall within this of the cloud centre
	CloudRainTop    = 30.0  // droplets land between here…
	CloudRainBottom = 170.0 // …and here below the cloud
	DropletsPerSec  = 3.0
	DropletWetness  = 15.0
	DropletRadius   = 8.0
	DropletSeconds  = 12.0 // a droplet evaporates after this long
	MaxDroplets     = 120

	PuddleIntervalMin   = 20.0 // seconds between spills
	PuddleIntervalMax   = 30.0
	PuddleRadius        = 70.0
	PuddleCapacity      = 60.0 // wetness in a fresh puddle
	PuddleRestorePerSec = 8.0
	PuddleSeconds       = 90.0 // an untouched puddle dries up after this long
	MaxPuddles          = 4
)

// Drop-to-drop interaction.
const (
	BleedPerSec     = 0.25 // colour shift toward a touching drop, per second
	UnbleedPerSec   = 0.1  // drift back to a drop's own colour while touching nobody, per second
	MixBonus        = 5.0  // wetness for each partner…
	MixCooldownSecs = 10.0 // …at most this often per pair
	AbsorbRatio     = 1.3  // radius ratio needed to absorb
	AbsorbWetness   = 0.5  // fraction of the absorbed drop's wetness gained
	AbsorbBlend     = 0.3  // colour shift toward the absorbed drop; wears off like bleed
)

// Hazards.
const (
	BrushIntervalMin = 25.0 // seconds between sweeps
	BrushIntervalMax = 40.0
	BrushWarnSeconds = 2.0
	BrushSeconds     = 3.0 // time to cross the page
	BrushHalfWidth   = 110.0
	BrushStrength    = 170
	BrushReach       = 0.5 // fraction of a drop's radius that must enter the band

	BlottersMin          = 3
	BlottersMax          = 5
	BlotterDryMultiplier = 4.0

	BeetlesMin         = 4
	BeetlesMax         = 6
	BeetleRadius       = 14.0
	BeetleSpeed        = 45.0
	BeetleBiteRadius   = 12.0  // paint within this of a beetle gets eaten
	BeetleKnockback    = 380.0 // speed given to a bumped drop, units/s
	BeetleDrain        = 10.0
	BeetleBumpCooldown = 1.0 // seconds a bumped drop is immune
)

// Death.
const (
	BloomRadius = 55.0
	MaxBlooms   = 64 // name labels remembered for late joiners
)

// Viewport culling.
const (
	DefaultViewW = 1600.0
	DefaultViewH = 1000.0
	MinView      = 200.0
	ViewMargin   = 200.0
)

const (
	dt         = 1.0 / TickRate
	fadeTicks  = uint32(FadeSeconds * TickRate)
	freshTicks = uint32(RepaintSeconds * TickRate)
)

// Ticks converts seconds to whole simulation ticks.
func Ticks(seconds float64) uint32 { return uint32(seconds*TickRate + 0.5) }

// WireConstants returns the positional constants list of the Welcome
// message. The order is part of the protocol: append, never reorder.
func WireConstants() []float32 {
	return []float32{
		0:  WorldW,
		1:  WorldH,
		2:  TickRate,
		3:  CellSize,
		4:  FadeSeconds,
		5:  DryPerSec,
		6:  DryMovingPerSec,
		7:  RadiusBase,
		8:  RadiusPerWetness,
		9:  SpeedMax,
		10: SpeedMin,
		11: Accel,
		12: MaxWetness,
		13: MovingSpeed,
		14: DropletRadius,
		15: PuddleRadius,
		16: PuddleCapacity,
		17: BeetleRadius,
		18: BlotterDryMultiplier,
		19: AbsorbRatio,
	}
}

// Palette returns the ten soft watercolour hues players choose from.
func Palette() []protocol.RGB {
	return []protocol.RGB{
		{R: 232, G: 106, B: 122}, // rose madder
		{R: 240, G: 146, B: 92},  // burnt orange
		{R: 244, G: 200, B: 96},  // gamboge
		{R: 156, G: 200, B: 112}, // sap green
		{R: 88, G: 178, B: 150},  // viridian
		{R: 96, G: 190, B: 214},  // cerulean
		{R: 98, G: 136, B: 216},  // ultramarine
		{R: 150, G: 120, B: 208}, // dioxazine violet
		{R: 214, G: 124, B: 190}, // opera pink
		{R: 168, G: 124, B: 96},  // burnt sienna
	}
}
