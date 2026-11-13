// Package paint implements the shared paint grid of a page.
//
// The algorithm is specified in protocol/PROTOCOL.md ("Paint grid") and is
// implemented identically, in integer maths, by the server and every client,
// so that grids stay in sync from paint ops alone.
package paint

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

// ErrBadSnapshot is returned when snapshot planes do not fit the grid.
var ErrBadSnapshot = errors.New("paint: bad snapshot")

const (
	// CellSize is the edge length of a paint cell in world units.
	CellSize = 10
	// PosScale is the number of wire position steps per world unit.
	PosScale = 16

	cell     = CellSize * PosScale // cell edge in wire position steps
	halfCell = cell / 2

	// SnapshotBytesPerCell is the size of one cell in an inflated snapshot:
	// R, G, B, A planes of one byte each plus a u16 age plane.
	SnapshotBytesPerCell = 6
)

// Op is one stamp of pigment, exactly as it travels on the wire.
type Op struct {
	X, Y     uint16 // centre, 1/16 world units
	Radius2  uint8  // radius × 2, world units
	R, G, B  uint8
	Strength uint8 // 0 erases
}

// Grid is the paint layer of one page. It is not safe for concurrent use; the
// owning page goroutine is its only user.
type Grid struct {
	w, h       int
	fadeTicks  uint32
	freshTicks uint32
	rgba       []uint8  // 4 bytes per cell, row-major
	t          []uint32 // tick each cell was last painted
	scratch    []byte   // snapshot planes reused by Checksum
}

// NewGrid returns an empty w×h grid whose paint fades to nothing over
// fadeTicks. A cell repainted after freshTicks counts as newly covered in
// [Grid.Apply]'s return value.
func NewGrid(w, h int, fadeTicks, freshTicks uint32) *Grid {
	return &Grid{
		w:          w,
		h:          h,
		fadeTicks:  fadeTicks,
		freshTicks: freshTicks,
		rgba:       make([]uint8, w*h*4),
		t:          make([]uint32, w*h),
	}
}

// Size returns the grid dimensions in cells.
func (g *Grid) Size() (w, h int) { return g.w, g.h }

// RGBA returns the raw cell colours (stored alpha, not faded). The slice
// aliases the grid.
func (g *Grid) RGBA() []uint8 { return g.rgba }

// Times returns the last-painted tick of every cell. The slice aliases the
// grid.
func (g *Grid) Times() []uint32 { return g.t }

// Eff returns the visible alpha of cell i at tick now.
func (g *Grid) Eff(i int, now uint32) uint8 { return uint8(g.eff(i, now)) } //nolint:gosec // eff is in [0, 255]

func (g *Grid) eff(i int, now uint32) int {
	a := int(g.rgba[i*4+3])
	if a == 0 {
		return 0
	}
	age := now - g.t[i]
	if age >= g.fadeTicks {
		return 0
	}
	return a * int(g.fadeTicks-age) / int(g.fadeTicks)
}

// footprint is the clamped range of cells an op can touch.
type footprint struct {
	x, y, r2 int
	cx0, cx1 int
	cy0, cy1 int
	empty    bool
}

func (g *Grid) footprint(op Op) footprint {
	x, y := int(op.X), int(op.Y)
	r := int(op.Radius2) * (PosScale / 2)
	f := footprint{
		x: x, y: y, r2: r * r,
		cx0: max(floorDiv(x-r, cell), 0), cx1: min(floorDiv(x+r, cell), g.w-1),
		cy0: max(floorDiv(y-r, cell), 0), cy1: min(floorDiv(y+r, cell), g.h-1),
	}
	f.empty = r == 0 || f.cx0 > f.cx1 || f.cy0 > f.cy1
	return f
}

// Apply stamps op onto the grid at tick now and reports how many cells it
// newly covered: cells that were blank, or last painted at least freshTicks
// ago. Erase ops report 0.
func (g *Grid) Apply(op Op, now uint32) (fresh int) {
	f := g.footprint(op)
	if f.empty {
		return 0
	}
	s := int(op.Strength)
	for cy := f.cy0; cy <= f.cy1; cy++ {
		dy := cy*cell + halfCell - f.y
		for cx := f.cx0; cx <= f.cx1; cx++ {
			dx := cx*cell + halfCell - f.x
			d2 := dx*dx + dy*dy
			if d2 >= f.r2 {
				continue
			}
			i := cy*g.w + cx
			px := g.rgba[i*4 : i*4+4 : i*4+4]
			if s == 0 {
				px[3] = 0
				continue
			}
			w := min(s, 2*s*(f.r2-d2)/f.r2)
			if w == 0 {
				continue
			}
			e := g.eff(i, now)
			if e == 0 {
				px[0], px[1], px[2], px[3] = op.R, op.G, op.B, uint8(w) //nolint:gosec // w is at most s, a uint8 strength
				fresh++
			} else {
				if now-g.t[i] >= g.freshTicks {
					fresh++
				}
				k := min(255, w*384/(w+e))
				px[0] = mix(px[0], op.R, k)
				px[1] = mix(px[1], op.G, k)
				px[2] = mix(px[2], op.B, k)
				px[3] = uint8(min(255, e+w-e*w/255)) //nolint:gosec // clamped to 255
			}
			g.t[i] = now
		}
	}
	return fresh
}

// Painted reports whether any cell under op's footprint is visibly painted at
// tick now.
func (g *Grid) Painted(op Op, now uint32) bool {
	f := g.footprint(op)
	if f.empty {
		return false
	}
	for cy := f.cy0; cy <= f.cy1; cy++ {
		dy := cy*cell + halfCell - f.y
		for cx := f.cx0; cx <= f.cx1; cx++ {
			dx := cx*cell + halfCell - f.x
			if dx*dx+dy*dy < f.r2 && g.eff(cy*g.w+cx, now) > 0 {
				return true
			}
		}
	}
	return false
}

// mix blends channel c toward o by k/255 in squared (pigment-like) space.
func mix(c, o uint8, k int) uint8 {
	cc, oo := int(c), int(o)
	return sqrtTable[(cc*cc*(255-k)+oo*oo*k)/255]
}

// sqrtTable[n] is floor(sqrt(n)) for every value mix can produce.
var sqrtTable = func() *[255*255 + 1]uint8 {
	var t [255*255 + 1]uint8
	r := 0
	for n := range t {
		if (r+1)*(r+1) <= n {
			r++
		}
		t[n] = uint8(r)
	}
	return &t
}()

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// AppendSnapshot appends the inflated snapshot planes (R, G, B, A, then u16
// little-endian age) as seen at tick now. Cells that are no longer visible
// are written as zeros: they behave identically to blank cells and compress
// away.
func (g *Grid) AppendSnapshot(dst []byte, now uint32) []byte {
	base := len(dst)
	dst = append(dst, make([]byte, g.w*g.h*SnapshotBytesPerCell)...)
	g.fillPlanes(dst[base:], now)
	return dst
}

// Checksum is the CRC-32 (IEEE) of the snapshot planes at tick now: the
// canonical fingerprint of the grid that clients compare their own against.
// Raw cell memory is not hashed because the stored tick of a cell differs
// between a grid and one rebuilt from its snapshot.
func (g *Grid) Checksum(now uint32) uint32 {
	if g.scratch == nil {
		g.scratch = make([]byte, g.w*g.h*SnapshotBytesPerCell)
	}
	g.fillPlanes(g.scratch, now)
	return crc32.ChecksumIEEE(g.scratch)
}

// fillPlanes overwrites out, which must hold exactly one snapshot, with the
// planes at tick now.
func (g *Grid) fillPlanes(out []byte, now uint32) {
	n := g.w * g.h
	red, green, blue, alpha, ages := out[:n], out[n:2*n], out[2*n:3*n], out[3*n:4*n], out[4*n:6*n]
	for i := range n {
		var px [4]uint8
		var age uint16
		if g.eff(i, now) != 0 {
			px = [4]uint8(g.rgba[i*4 : i*4+4])
			age = uint16(min(now-g.t[i], 0xFFFF))
		}
		red[i], green[i], blue[i], alpha[i] = px[0], px[1], px[2], px[3]
		binary.LittleEndian.PutUint16(ages[2*i:], age)
	}
}

// LoadSnapshot replaces the grid contents with inflated snapshot planes taken
// at tick now. now must be at least 65535 or at least every age in the
// snapshot, which holds for any snapshot written at tick now.
func (g *Grid) LoadSnapshot(raw []byte, now uint32) error {
	n := g.w * g.h
	if len(raw) != n*SnapshotBytesPerCell {
		return fmt.Errorf("%w: %d bytes, want %d", ErrBadSnapshot, len(raw), n*SnapshotBytesPerCell)
	}
	for i := range n {
		px := g.rgba[i*4 : i*4+4 : i*4+4]
		px[0], px[1], px[2], px[3] = raw[i], raw[n+i], raw[2*n+i], raw[3*n+i]
		age := uint32(binary.LittleEndian.Uint16(raw[4*n+2*i:]))
		if age > now {
			return fmt.Errorf("%w: cell %d is older (%d ticks) than tick %d", ErrBadSnapshot, i, age, now)
		}
		g.t[i] = now - age
	}
	return nil
}

// Compress deflates snapshot planes into a zlib stream.
func Compress(raw []byte) []byte {
	var buf bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&buf, zlib.BestSpeed) // level is valid; cannot fail
	_, _ = zw.Write(raw)                               // bytes.Buffer writes cannot fail
	_ = zw.Close()
	return buf.Bytes()
}

// Decompress inflates a zlib stream that must hold exactly size bytes.
func Decompress(z []byte, size int) ([]byte, error) {
	zr, err := zlib.NewReader(bytes.NewReader(z))
	if err != nil {
		return nil, fmt.Errorf("paint: open snapshot: %w", err)
	}
	defer zr.Close()
	raw := make([]byte, size)
	if _, err := io.ReadFull(zr, raw); err != nil {
		return nil, fmt.Errorf("paint: inflate snapshot: %w", err)
	}
	var extra [1]byte
	if n, _ := zr.Read(extra[:]); n != 0 {
		return nil, fmt.Errorf("%w: larger than %d bytes", ErrBadSnapshot, size)
	}
	return raw, nil
}
