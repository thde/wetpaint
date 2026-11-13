import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { inflate } from './inflate';
import { PaintGrid, SNAPSHOT_BYTES_PER_CELL, crc32 } from './paintGrid';
import type { PaintOp } from './protocol';

const FADE = 6000;
const CELL = 160;

function op(cellX: number, cellY: number, over: Partial<PaintOp> = {}): PaintOp {
  // Centred on a cell, radius 30 units: wide enough for a flat middle.
  return {
    x: cellX * CELL + 80,
    y: cellY * CELL + 80,
    radius2: 60,
    r: 200,
    g: 100,
    b: 50,
    strength: 200,
    ...over,
  };
}

function cell(grid: PaintGrid, x: number, y: number): number[] {
  const i = (y * grid.width + x) * 4;
  return [...grid.rgba.subarray(i, i + 4)];
}

describe('PaintGrid.apply', () => {
  it('stamps full strength in the middle and a soft edge', () => {
    const grid = new PaintGrid(40, 30, FADE, CELL);
    grid.apply(op(10, 10), 100);

    expect(cell(grid, 10, 10)).toEqual([200, 100, 50, 200]);
    // Two cells out: d² = 320², R² = 480², w = 2·200·(R²−d²)/R² = 222 → capped.
    expect(cell(grid, 12, 10)).toEqual([200, 100, 50, 200]);
    // Diagonal (2, 2): d² = 2·320², w = floor(400·25600/230400) = 44.
    expect(cell(grid, 12, 12)).toEqual([200, 100, 50, 44]);
    // Three cells out is exactly on the radius and excluded.
    expect(cell(grid, 13, 10)).toEqual([0, 0, 0, 0]);
    expect(grid.t[10 * 40 + 10]).toBe(100);
    expect(grid.t[10 * 40 + 13]).toBe(0);
  });

  it('fades linearly to nothing over fadeTicks', () => {
    const grid = new PaintGrid(40, 30, FADE, CELL);
    grid.apply(op(5, 5), 1000);
    const i = 5 * 40 + 5;

    expect(grid.eff(i, 1000)).toBe(200);
    expect(grid.eff(i, 1000 + FADE / 2)).toBe(100);
    expect(grid.eff(i, 1000 + FADE - 1)).toBe(0); // floor(200 · 1 / 6000)
    expect(grid.eff(i, 1000 + FADE)).toBe(0);
    expect(grid.eff(i, 1000 + 10 * FADE)).toBe(0);
    expect(grid.rgba[i * 4 + 3]).toBe(200); // stored alpha is untouched by time
  });

  it('is idempotent when a colour is painted over itself', () => {
    const grid = new PaintGrid(40, 30, FADE, CELL);
    grid.apply(op(5, 5), 0);
    grid.apply(op(5, 5), 0);
    // a = e + w − e·w/255 = 200 + 200 − floor(40000/255) = 244
    expect(cell(grid, 5, 5)).toEqual([200, 100, 50, 244]);
  });

  it('mixes in squared space, biased towards the newer colour', () => {
    const grid = new PaintGrid(40, 30, FADE, CELL);
    grid.apply(op(5, 5, { r: 255, g: 0, b: 0 }), 0);
    grid.apply(op(5, 5, { r: 0, g: 0, b: 255 }), 0);
    // k = min(255, 200·384/400) = 192
    // r = isqrt(255²·63/255) = isqrt(16065) = 126; b = isqrt(255·192) = 221
    expect(cell(grid, 5, 5)).toEqual([126, 0, 221, 244]);
  });

  it('replaces the colour outright once the old paint has faded away', () => {
    const grid = new PaintGrid(40, 30, FADE, CELL);
    grid.apply(op(5, 5, { r: 255, g: 0, b: 0 }), 0);
    grid.apply(op(5, 5, { r: 0, g: 0, b: 255, strength: 90 }), FADE);
    expect(cell(grid, 5, 5)).toEqual([0, 0, 255, 90]);
    expect(grid.t[5 * 40 + 5]).toBe(FADE);
  });

  it('erases with strength 0, leaving colour and time alone', () => {
    const grid = new PaintGrid(40, 30, FADE, CELL);
    grid.apply(op(5, 5), 7);
    grid.apply(op(5, 5, { strength: 0, radius2: 20 }), 50);
    expect(cell(grid, 5, 5)).toEqual([200, 100, 50, 0]);
    expect(grid.t[5 * 40 + 5]).toBe(7);
    expect(cell(grid, 7, 5)[3]).toBe(200); // outside the eraser
  });

  it('clips at the grid edge and reports the dirty rectangle', () => {
    const dirty: number[][] = [];
    const grid = new PaintGrid(40, 30, FADE, CELL, (...rect) => dirty.push(rect));
    grid.apply(op(0, 29), 0);
    expect(dirty).toEqual([[0, 26, 3, 29]]);
    expect(cell(grid, 0, 29)[3]).toBe(200);

    grid.apply({ ...op(0, 0), x: 65535, y: 65535 }, 0); // far outside this small grid
    expect(dirty).toHaveLength(1);
  });
});

describe('PaintGrid.loadSnapshot', () => {
  it('reads the five planes and turns ages into paint ticks', async () => {
    const w = 3;
    const h = 2;
    const n = w * h;
    const planes = new Uint8Array(n * 6);
    for (let i = 0; i < n; i++) {
      planes[i] = 10 + i;
      planes[n + i] = 20 + i;
      planes[2 * n + i] = 30 + i;
      planes[3 * n + i] = 40 + i;
    }
    planes.set([0x34, 0x12], 4 * n); // cell 0: age 0x1234
    planes.set([0xff, 0xff], 4 * n + 10); // cell 5: age saturated

    // Round-trip through zlib, as the Welcome message carries it.
    const deflated = await new Response(
      new Blob([planes]).stream().pipeThrough(new CompressionStream('deflate')),
    ).arrayBuffer();
    const dirty: number[][] = [];
    const grid = new PaintGrid(w, h, FADE, CELL, (...rect) => dirty.push(rect));
    grid.loadSnapshot(await inflate(new Uint8Array(deflated)), 1000);

    expect(cell(grid, 0, 0)).toEqual([10, 20, 30, 40]);
    expect(cell(grid, 2, 1)).toEqual([15, 25, 35, 45]);
    expect(grid.t[0]).toBe(1000 - 0x1234);
    expect(grid.t[1]).toBe(1000);
    expect(grid.t[5]).toBe(1000 - 65535);
    expect(dirty).toEqual([[0, 0, 2, 1]]);
  });

  it('rejects a snapshot of the wrong size', () => {
    const grid = new PaintGrid(3, 2, FADE, CELL);
    expect(() => grid.loadSnapshot(new Uint8Array(35), 0)).toThrow(/36/);
  });
});

// The server generates these from its own implementation; replaying them here
// proves both sides compute bit-identical grids.
interface VectorOp {
  tick: number;
  x: number;
  y: number;
  radius2: number;
  color: [number, number, number];
  strength: number;
}

interface VectorCase {
  name: string;
  gridW: number;
  gridH: number;
  ops: VectorOp[];
  rgba: string;
  t: string;
  effTick: number;
  eff: string;
  /** Grid checksum at `effTick`. */
  crc: number;
}

interface Vectors {
  fadeTicks: number;
  cases: VectorCase[];
}

const vectors = JSON.parse(
  readFileSync(new URL('../../protocol/paint_vectors.json', import.meta.url), 'utf8'),
) as Vectors;

describe('shared paint vectors', () => {
  it.each(vectors.cases.map((c) => [c.name, c] as const))('%s', (_name, c) => {
    const grid = new PaintGrid(c.gridW, c.gridH, vectors.fadeTicks, CELL);
    for (const o of c.ops) {
      const [r, g, b] = o.color;
      grid.apply({ x: o.x, y: o.y, radius2: o.radius2, r, g, b, strength: o.strength }, o.tick);
    }
    const n = c.gridW * c.gridH;

    expect(Buffer.from(grid.rgba).toString('base64')).toBe(c.rgba);

    const t = Buffer.from(c.t, 'base64');
    expect(t).toHaveLength(n * 4);
    expect(Array.from(grid.t)).toEqual(Array.from({ length: n }, (_, i) => t.readUInt32LE(i * 4)));

    const eff = Uint8Array.from({ length: n }, (_, i) => grid.eff(i, c.effTick));
    expect(Buffer.from(eff).toString('base64')).toBe(c.eff);

    expect(c.crc).toBeTypeOf('number');
    expect(grid.checksum(c.effTick)).toBe(c.crc);
  });
});

/** The snapshot planes of `grid` at `tick`, built the slow, obvious way. */
function snapshotPlanes(grid: PaintGrid, tick: number): Uint8Array {
  const n = grid.width * grid.height;
  const planes = new Uint8Array(n * SNAPSHOT_BYTES_PER_CELL);
  for (let i = 0; i < n; i++) {
    if (grid.eff(i, tick) === 0) continue;
    for (let plane = 0; plane < 4; plane++) planes[plane * n + i] = grid.rgba[i * 4 + plane];
    const age = Math.min(65535, tick - grid.t[i]);
    planes[4 * n + 2 * i] = age & 0xff;
    planes[4 * n + 2 * i + 1] = age >> 8;
  }
  return planes;
}

describe('crc32', () => {
  it('matches the standard check value', () => {
    expect(crc32(new TextEncoder().encode('123456789'))).toBe(0xcbf43926);
    expect(crc32(new Uint8Array(0))).toBe(0);
  });
});

describe('PaintGrid.checksum', () => {
  function painted(): PaintGrid {
    const grid = new PaintGrid(40, 30, FADE, CELL);
    grid.apply(op(5, 5), 0); // fully faded by the time of the checksum
    grid.apply(op(10, 10), 3000);
    grid.apply(op(12, 11, { r: 20, g: 180, b: 240, strength: 90 }), 5000);
    grid.apply(op(30, 20, { radius2: 20, strength: 0 }), 5200);
    grid.apply(op(38, 28, { strength: 255 }), 6100);
    return grid;
  }

  it('is the CRC-32 of the snapshot planes', () => {
    const grid = painted();
    expect(grid.checksum(6500)).toBe(crc32(snapshotPlanes(grid, 6500)));
  });

  it('is zero-plane CRC for a blank or fully faded grid', () => {
    const blank = crc32(new Uint8Array(40 * 30 * SNAPSHOT_BYTES_PER_CELL));
    expect(new PaintGrid(40, 30, FADE, CELL).checksum(123)).toBe(blank);
    expect(painted().checksum(6100 + FADE)).toBe(blank);
  });

  it('survives a snapshot round trip, although the stored cells differ', () => {
    const grid = painted();
    const copy = new PaintGrid(40, 30, FADE, CELL);
    copy.loadSnapshot(snapshotPlanes(grid, 6500), 6500);

    expect(copy.t[5 * 40 + 5]).not.toBe(grid.t[5 * 40 + 5]);
    expect(copy.checksum(6500)).toBe(grid.checksum(6500));
    // …and keeps agreeing as both age and take the same paint.
    const next = op(11, 10, { r: 90, g: 90, b: 220 });
    grid.apply(next, 7000);
    copy.apply(next, 7000);
    expect(copy.checksum(9000)).toBe(grid.checksum(9000));
  });

  it('changes when one visible cell differs', () => {
    const grid = painted();
    const before = grid.checksum(6500);
    grid.rgba[(10 * 40 + 10) * 4] ^= 1;
    expect(grid.checksum(6500)).not.toBe(before);
  });
});
