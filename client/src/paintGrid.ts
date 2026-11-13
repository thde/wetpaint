// The shared paint grid. The server sends a snapshot once and paint ops
// afterwards; this runs the integer algorithm from protocol/PROTOCOL.md so the
// grid stays identical to the server's. Every quantity below is an integer
// far inside the 2^53 range where float64 arithmetic is exact.

import type { PaintOp } from './protocol';

/** Called with the inclusive cell bounds an op or snapshot touched. */
export type DirtyListener = (x0: number, y0: number, x1: number, y1: number) => void;

/** Bytes per cell in an inflated snapshot: R, G, B, A planes plus a u16 age. */
export const SNAPSHOT_BYTES_PER_CELL = 6;

const CRC_TABLE = new Uint32Array(256);
for (let n = 0; n < 256; n++) {
  let c = n;
  for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
  CRC_TABLE[n] = c;
}

/** CRC-32 (IEEE 802.3, as in zlib and PNG) of `bytes`. */
export function crc32(bytes: Uint8Array): number {
  let c = 0xffffffff;
  for (let i = 0; i < bytes.length; i++) c = CRC_TABLE[(c ^ bytes[i]) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

function isqrt(n: number): number {
  // Exact for the inputs used here (n ≤ 255²).
  return Math.floor(Math.sqrt(n));
}

function mix(c: number, opC: number, k: number): number {
  return isqrt(Math.floor((c * c * (255 - k) + opC * opC * k) / 255));
}

export class PaintGrid {
  /** Row-major r, g, b, a per cell; `a` is the stored (unfaded) alpha. */
  readonly rgba: Uint8Array;
  /** Tick each cell was last painted. May be negative after a snapshot. */
  readonly t: Float64Array;
  /** Scratch for `checksum`: each visible cell's age + 1, or 0 if invisible. */
  private readonly agePlus1: Uint32Array;

  /**
   * @param fadeTicks `fadeSeconds · tickRate`
   * @param cellWire  cell size in wire `pos` units (1/16 world units)
   */
  constructor(
    readonly width: number,
    readonly height: number,
    readonly fadeTicks: number,
    readonly cellWire: number,
    private readonly onDirty?: DirtyListener,
  ) {
    this.rgba = new Uint8Array(width * height * 4);
    this.t = new Float64Array(width * height);
    this.agePlus1 = new Uint32Array(width * height);
  }

  /** Visible alpha of cell `i` at tick `now`. */
  eff(i: number, now: number): number {
    const left = this.fadeTicks - (now - this.t[i]);
    return left <= 0 ? 0 : Math.floor((this.rgba[i * 4 + 3] * left) / this.fadeTicks);
  }

  /** Replaces the grid with an inflated snapshot taken at `tick`. */
  loadSnapshot(planes: Uint8Array, tick: number): void {
    const n = this.width * this.height;
    if (planes.length !== n * SNAPSHOT_BYTES_PER_CELL) {
      throw new Error(`snapshot is ${planes.length} bytes, expected ${n * SNAPSHOT_BYTES_PER_CELL}`);
    }
    const ages = n * 4;
    for (let i = 0; i < n; i++) {
      this.rgba[i * 4] = planes[i];
      this.rgba[i * 4 + 1] = planes[n + i];
      this.rgba[i * 4 + 2] = planes[2 * n + i];
      this.rgba[i * 4 + 3] = planes[3 * n + i];
      this.t[i] = tick - (planes[ages + 2 * i] | (planes[ages + 2 * i + 1] << 8));
    }
    this.onDirty?.(0, 0, this.width - 1, this.height - 1);
  }

  /**
   * The "Grid checksum" of the spec: CRC-32 of the snapshot planes this grid
   * would have at tick `now`, computed without materialising them. Cells whose
   * visible alpha is 0 count as zero in every plane, so a grid that has been
   * through a snapshot hashes the same as the original.
   */
  checksum(now: number): number {
    const n = this.width * this.height;
    const rgba = this.rgba;
    const agePlus1 = this.agePlus1;
    for (let i = 0; i < n; i++) {
      agePlus1[i] = this.eff(i, now) === 0 ? 0 : Math.min(65535, now - this.t[i]) + 1;
    }
    const table = CRC_TABLE;
    let c = 0xffffffff;
    for (let plane = 0; plane < 4; plane++) {
      for (let i = 0; i < n; i++) {
        const byte = agePlus1[i] === 0 ? 0 : rgba[i * 4 + plane];
        c = table[(c ^ byte) & 0xff] ^ (c >>> 8);
      }
    }
    for (let i = 0; i < n; i++) {
      const age = agePlus1[i] === 0 ? 0 : agePlus1[i] - 1;
      c = table[(c ^ age) & 0xff] ^ (c >>> 8);
      c = table[(c ^ (age >>> 8)) & 0xff] ^ (c >>> 8);
    }
    return (c ^ 0xffffffff) >>> 0;
  }

  /** Applies one paint op at tick `now`. */
  apply(op: PaintOp, now: number): void {
    const cell = this.cellWire;
    const half = cell >> 1;
    const X = op.x;
    const Y = op.y;
    const R = op.radius2 * 8;
    const R2 = R * R;
    const S = op.strength;

    const cx0 = Math.max(0, Math.floor((X - R) / cell));
    const cx1 = Math.min(this.width - 1, Math.floor((X + R) / cell));
    const cy0 = Math.max(0, Math.floor((Y - R) / cell));
    const cy1 = Math.min(this.height - 1, Math.floor((Y + R) / cell));
    if (cx0 > cx1 || cy0 > cy1) return;

    const rgba = this.rgba;
    for (let cy = cy0; cy <= cy1; cy++) {
      const dy = cy * cell + half - Y;
      for (let cx = cx0; cx <= cx1; cx++) {
        const dx = cx * cell + half - X;
        const d2 = dx * dx + dy * dy;
        if (d2 >= R2) continue;

        const i = cy * this.width + cx;
        const p = i * 4;
        if (S === 0) {
          rgba[p + 3] = 0;
          continue;
        }
        const w = Math.min(S, Math.floor((2 * S * (R2 - d2)) / R2));
        if (w === 0) continue;

        const e = this.eff(i, now);
        if (e === 0) {
          rgba[p] = op.r;
          rgba[p + 1] = op.g;
          rgba[p + 2] = op.b;
          rgba[p + 3] = w;
        } else {
          const k = Math.min(255, Math.floor((w * 384) / (w + e)));
          rgba[p] = mix(rgba[p], op.r, k);
          rgba[p + 1] = mix(rgba[p + 1], op.g, k);
          rgba[p + 2] = mix(rgba[p + 2], op.b, k);
          rgba[p + 3] = Math.min(255, e + w - Math.floor((e * w) / 255));
        }
        this.t[i] = now;
      }
    }
    this.onDirty?.(cx0, cy0, cx1, cy1);
  }
}
