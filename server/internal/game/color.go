package game

import (
	"math"

	"github.com/thde/wetpaint/server/internal/protocol"
)

// Color is an RGB colour with fractional channels in [0, 255], so slow
// bleeding accumulates instead of being lost to rounding.
type Color struct{ R, G, B float64 }

var (
	black = Color{}
	white = Color{255, 255, 255}
)

func colorOf(c protocol.RGB) Color {
	return Color{float64(c.R), float64(c.G), float64(c.B)}
}

// Toward returns c moved the fraction f of the way to o in plain RGB. It is
// for tints and shades of one colour; to blend two hues use [Color.Mix].
func (c Color) Toward(o Color, f float64) Color {
	return Color{
		R: c.R + (o.R-c.R)*f,
		G: c.G + (o.G-c.G)*f,
		B: c.B + (o.B-c.B)*f,
	}
}

// Mix returns c moved the fraction f of the way to o the way pigments
// would: lightness and chroma move in a straight line and hue travels the
// shorter way round the colour wheel (OKLCH), so two saturated colours meet
// in a third saturated colour rather than in grey.
func (c Color) Mix(o Color, f float64) Color {
	if f <= 0 || c == o {
		return c
	}
	if f >= 1 {
		return o
	}
	a, b := c.oklch(), o.oklch()
	// A grey has no hue of its own: it takes on the other colour's.
	switch {
	case a.c < achromatic:
		a.h = b.h
	case b.c < achromatic:
		b.h = a.h
	}
	dh := math.Remainder(b.h-a.h, 2*math.Pi) // in [-π, π]
	return oklch{
		l: a.l + (b.l-a.l)*f,
		c: a.c + (b.c-a.c)*f,
		h: a.h + dh*f,
	}.color()
}

// Dist is the largest difference between c and o in any channel.
func (c Color) Dist(o Color) float64 {
	return max(math.Abs(c.R-o.R), math.Abs(c.G-o.G), math.Abs(c.B-o.B))
}

// RGB rounds c to a wire colour.
func (c Color) RGB() protocol.RGB {
	ch := func(v float64) uint8 { return uint8(math.Round(min(max(v, 0), 255))) }
	return protocol.RGB{R: ch(c.R), G: ch(c.G), B: ch(c.B)}
}

// oklch is a colour in the cylindrical form of Björn Ottosson's OKLab:
// lightness, chroma and hue in radians.
type oklch struct{ l, c, h float64 }

const (
	// achromatic is the chroma below which a colour's hue is noise.
	achromatic = 0.02
	// gamutSlack is how far outside [0, 1] a linear channel may fall before
	// the colour counts as outside sRGB; it absorbs round-trip error.
	gamutSlack = 1e-6
	// gamutSteps is the number of bisections used to pull a colour into
	// gamut; chroma is found to within 2^-gamutSteps of its starting value.
	gamutSteps = 16
)

type mat3 [3][3]float64

func (m mat3) mul(x, y, z float64) (ox, oy, oz float64) {
	return m[0][0]*x + m[0][1]*y + m[0][2]*z,
		m[1][0]*x + m[1][1]*y + m[1][2]*z,
		m[2][0]*x + m[2][1]*y + m[2][2]*z
}

func (m mat3) inverse() mat3 {
	cofactor := func(i, j int) float64 {
		r0, r1 := (i+1)%3, (i+2)%3
		c0, c1 := (j+1)%3, (j+2)%3
		return m[r0][c0]*m[r1][c1] - m[r0][c1]*m[r1][c0]
	}
	det := m[0][0]*cofactor(0, 0) + m[0][1]*cofactor(0, 1) + m[0][2]*cofactor(0, 2)
	var inv mat3
	for i := range 3 {
		for j := range 3 {
			inv[j][i] = cofactor(i, j) / det
		}
	}
	return inv
}

// OKLab's two matrices: linear sRGB to cone responses, and cube-rooted cone
// responses to Lab. The way back uses their computed inverses rather than
// the published rounded ones, so a colour that is converted every tick for a
// whole life comes back unchanged.
var (
	rgbToLMS = mat3{
		{0.4122214708, 0.5363325363, 0.0514459929},
		{0.2119034982, 0.6806995451, 0.1073969566},
		{0.0883024619, 0.2817188376, 0.6299787005},
	}
	lmsToLab = mat3{
		{0.2104542553, 0.7936177850, -0.0040720468},
		{1.9779984951, -2.4285922050, 0.4505937099},
		{0.0259040371, 0.7827717662, -0.8086757660},
	}
	lmsToRGB = rgbToLMS.inverse()
	labToLMS = lmsToLab.inverse()
)

func (c Color) oklch() oklch {
	l, m, s := rgbToLMS.mul(srgbToLinear(c.R/255), srgbToLinear(c.G/255), srgbToLinear(c.B/255))
	lightness, ka, kb := lmsToLab.mul(math.Cbrt(l), math.Cbrt(m), math.Cbrt(s))
	return oklch{l: lightness, c: math.Hypot(ka, kb), h: math.Atan2(kb, ka)}
}

// linear returns the linear-light sRGB channels of k, which lie outside
// [0, 1] when k is not a displayable colour.
func (k oklch) linear() (r, g, b float64) {
	l, m, s := labToLMS.mul(k.l, k.c*math.Cos(k.h), k.c*math.Sin(k.h))
	return lmsToRGB.mul(l*l*l, m*m*m, s*s*s)
}

func (k oklch) inGamut() bool {
	r, g, b := k.linear()
	return min(r, g, b) >= -gamutSlack && max(r, g, b) <= 1+gamutSlack
}

// color converts k to sRGB. A colour too vivid to display keeps its
// lightness and hue and gives up just enough chroma to fit.
func (k oklch) color() Color {
	if !k.inGamut() {
		lo, hi := 0.0, k.c
		for range gamutSteps {
			k.c = (lo + hi) / 2
			if k.inGamut() {
				lo = k.c
			} else {
				hi = k.c
			}
		}
		k.c = lo
	}
	r, g, b := k.linear()
	return Color{255 * linearToSRGB(r), 255 * linearToSRGB(g), 255 * linearToSRGB(b)}
}

func srgbToLinear(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func linearToSRGB(v float64) float64 {
	v = min(max(v, 0), 1)
	if v <= 0.0031308 {
		return v * 12.92
	}
	return 1.055*math.Pow(v, 1/2.4) - 0.055
}
