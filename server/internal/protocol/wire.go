package protocol

import (
	"encoding/binary"
	"errors"
	"math"
)

// ErrMalformed is returned when a message is truncated, has trailing bytes or
// carries an unknown type.
var ErrMalformed = errors.New("protocol: malformed message")

func appendU16(b []byte, v uint16) []byte { return binary.LittleEndian.AppendUint16(b, v) }
func appendU32(b []byte, v uint32) []byte { return binary.LittleEndian.AppendUint32(b, v) }
func appendF32(b []byte, v float32) []byte {
	return binary.LittleEndian.AppendUint32(b, math.Float32bits(v))
}
func appendRGB(b []byte, c RGB) []byte { return append(b, c.R, c.G, c.B) }

// appendStr writes a length-prefixed string, truncating at 255 bytes.
func appendStr(b []byte, s string) []byte {
	if len(s) > math.MaxUint8 {
		s = s[:math.MaxUint8]
	}
	return append(append(b, uint8(len(s))), s...)
}

// reader consumes a message front to back. After the first short read every
// accessor returns zero and ok reports false.
type reader struct {
	b      []byte
	failed bool
}

func (r *reader) take(n int) []byte {
	if r.failed || len(r.b) < n {
		r.failed = true
		return nil
	}
	out := r.b[:n]
	r.b = r.b[n:]
	return out
}

func (r *reader) u8() uint8 {
	if b := r.take(1); b != nil {
		return b[0]
	}
	return 0
}

func (r *reader) u16() uint16 {
	if b := r.take(2); b != nil {
		return binary.LittleEndian.Uint16(b)
	}
	return 0
}

func (r *reader) u32() uint32 {
	if b := r.take(4); b != nil {
		return binary.LittleEndian.Uint32(b)
	}
	return 0
}

func (r *reader) f32() float32 { return math.Float32frombits(r.u32()) }
func (r *reader) pos() Pos     { return Pos(r.u16()) }
func (r *reader) rgb() RGB     { return RGB{r.u8(), r.u8(), r.u8()} }
func (r *reader) str() string  { return string(r.take(int(r.u8()))) }

// count reads a list length and returns 0 once the reader has failed, so
// decode loops never run on garbage.
func (r *reader) count(n int) int {
	if r.failed {
		return 0
	}
	return n
}

// done reports ErrMalformed unless the message was consumed exactly.
func (r *reader) done() error {
	if r.failed || len(r.b) != 0 {
		return ErrMalformed
	}
	return nil
}
