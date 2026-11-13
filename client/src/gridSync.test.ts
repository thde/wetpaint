import { describe, expect, it } from 'vitest';
import { GridSync, RESYNC_RETRY_MS } from './gridSync';
import { PaintGrid } from './paintGrid';
import type { GridCheck } from './protocol';

function grid(): PaintGrid {
  const g = new PaintGrid(40, 30, 6000, 160);
  g.apply({ x: 1680, y: 1680, radius2: 60, r: 200, g: 100, b: 50, strength: 200 }, 100);
  return g;
}

const good = (g: PaintGrid, tick: number): GridCheck => ({
  type: 'gridCheck',
  tick,
  crc: g.checksum(tick),
});
const bad = (g: PaintGrid, tick: number): GridCheck => ({
  type: 'gridCheck',
  tick,
  crc: (g.checksum(tick) ^ 1) >>> 0,
});

describe('GridSync', () => {
  it('stays quiet while the grid matches', () => {
    const g = grid();
    const sync = new GridSync();
    expect(sync.check(g, good(g, 200), 0)).toBe(false);
    expect(sync.check(g, good(g, 300), 5000)).toBe(false);
  });

  it('asks exactly once on a mismatch, until the Grid is installed', () => {
    const g = grid();
    const sync = new GridSync();
    expect(sync.check(g, bad(g, 200), 0)).toBe(true);
    // Neither further mismatches nor matches count while waiting.
    expect(sync.check(g, bad(g, 300), 5000)).toBe(false);
    expect(sync.check(g, good(g, 400), 10_000)).toBe(false);

    sync.installed();
    expect(sync.check(g, good(g, 500), 11_000)).toBe(false);
    expect(sync.check(g, bad(g, 600), 16_000)).toBe(true);
  });

  it('asks again if the request went unanswered past the server limit', () => {
    const g = grid();
    const sync = new GridSync();
    expect(sync.check(g, bad(g, 200), 0)).toBe(true);
    expect(sync.check(g, bad(g, 300), RESYNC_RETRY_MS - 1)).toBe(false);
    expect(sync.check(g, bad(g, 400), RESYNC_RETRY_MS)).toBe(true);
    expect(sync.check(g, bad(g, 500), RESYNC_RETRY_MS + 5000)).toBe(false);
  });
});
