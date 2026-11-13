// Turns the paint grid into watercolour on an offscreen canvas.
//
// The sheet is split into tiles. A paint op marks the tiles it touches dirty
// and only those are re-rendered, a few milliseconds' worth per frame. A slow
// rolling refresh revisits painted tiles so fading shows without ever
// redrawing the whole sheet.
//
// Each tile is shaded per pixel from blurred, bilinearly interpolated cells:
//   - a noisy threshold on coverage (alpha relative to the strongest paint
//     nearby) gives strokes a soft but definite edge at any strength,
//   - cells that are stronger than a neighbour darken ("edge pooling"),
//   - low-frequency noise varies the wash, pixel noise granulates it.

import { PAINT } from '../constants';
import type { PaintGrid } from '../paintGrid';
import { NOISE_MASK, NOISE_SIZE, valueNoise, whiteNoise } from './noise';

const S = PAINT.pxPerCell;
const T = PAINT.tileCells;
const TILE_PX = T * S;
/** Interpolation buffers cover the tile plus one cell on each side. */
const PAD = T + 2;
/** Effective alpha is gathered one cell further out, for pooling. */
const PAD2 = T + 4;
/** A changed cell can alter pixels this many cells away. */
const REACH = 2;

const LOW_NOISE = valueNoise(32, 0x5eed);
const HIGH_NOISE = whiteNoise(0xbead);

// For pixel p of a tile: the padded-buffer cell to its upper left and the
// weight of the next cell along. Identical for x and y.
const CELL0 = new Uint8Array(TILE_PX);
const FRAC = new Float32Array(TILE_PX);
for (let p = 0; p < TILE_PX; p++) {
  const g = (p + 0.5) / S - 0.5 + 1;
  CELL0[p] = Math.floor(g);
  FRAC[p] = g - CELL0[p];
}

/** The 3×3 tent-filtered value of a `PAD2`-wide buffer at index `o`. */
function tent(buf: Float32Array, o: number): number {
  return (
    (buf[o] * 4 +
      (buf[o - 1] + buf[o + 1] + buf[o - PAD2] + buf[o + PAD2]) * 2 +
      buf[o - PAD2 - 1] +
      buf[o - PAD2 + 1] +
      buf[o + PAD2 - 1] +
      buf[o + PAD2 + 1]) /
    16
  );
}

function smoothstep(lo: number, hi: number, v: number): number {
  const t = Math.min(1, Math.max(0, (v - lo) / (hi - lo)));
  return t * t * (3 - 2 * t);
}

export class PaintLayer {
  /** The whole sheet, `pxPerCell` pixels per grid cell. */
  readonly canvas: HTMLCanvasElement;

  private readonly ctx: CanvasRenderingContext2D;
  private readonly tilesX: number;
  private readonly tilesY: number;
  private readonly dirty: Uint8Array;
  private readonly painted: Uint8Array;
  private dirtyCount = 0;
  private dirtyCursor = 0;
  private refreshCursor = 0;

  private readonly image: ImageData;
  private readonly alpha = new Float32Array(PAD2 * PAD2);
  private readonly rawR = new Float32Array(PAD2 * PAD2);
  private readonly rawG = new Float32Array(PAD2 * PAD2);
  private readonly rawB = new Float32Array(PAD2 * PAD2);
  private readonly pr = new Float32Array(PAD * PAD);
  private readonly pg = new Float32Array(PAD * PAD);
  private readonly pb = new Float32Array(PAD * PAD);
  private readonly pa = new Float32Array(PAD * PAD);
  private readonly pool = new Float32Array(PAD * PAD);
  private readonly peak = new Float32Array(PAD * PAD);

  constructor(private readonly grid: PaintGrid) {
    this.canvas = document.createElement('canvas');
    this.canvas.width = grid.width * S;
    this.canvas.height = grid.height * S;
    const ctx = this.canvas.getContext('2d');
    if (!ctx) throw new Error('2D canvas is not available');
    this.ctx = ctx;
    this.image = ctx.createImageData(TILE_PX, TILE_PX);
    this.tilesX = Math.ceil(grid.width / T);
    this.tilesY = Math.ceil(grid.height / T);
    this.dirty = new Uint8Array(this.tilesX * this.tilesY);
    this.painted = new Uint8Array(this.tilesX * this.tilesY);
  }

  /** Marks the tiles affected by a change to the inclusive cell rectangle. */
  markCells = (x0: number, y0: number, x1: number, y1: number): void => {
    const tx0 = Math.max(0, Math.floor((x0 - REACH) / T));
    const ty0 = Math.max(0, Math.floor((y0 - REACH) / T));
    const tx1 = Math.min(this.tilesX - 1, Math.floor((x1 + REACH) / T));
    const ty1 = Math.min(this.tilesY - 1, Math.floor((y1 + REACH) / T));
    for (let ty = ty0; ty <= ty1; ty++) {
      for (let tx = tx0; tx <= tx1; tx++) {
        const i = ty * this.tilesX + tx;
        if (this.dirty[i] === 0) {
          this.dirty[i] = 1;
          this.dirtyCount++;
        }
      }
    }
  };

  /**
   * Re-renders dirty tiles for at most `budgetMs`, then a few painted tiles
   * so fading progresses. `tick` is the current server tick.
   */
  update(tick: number, budgetMs: number): void {
    const n = this.dirty.length;
    if (this.dirtyCount > 0) {
      const deadline = performance.now() + budgetMs;
      for (let visited = 0; visited < n && this.dirtyCount > 0; visited++) {
        const i = this.dirtyCursor;
        this.dirtyCursor = (i + 1) % n;
        if (this.dirty[i] === 0) continue;
        this.renderTile(i, tick);
        if (performance.now() >= deadline) break;
      }
    }
    for (let k = 0; k < PAINT.refreshTilesPerFrame; k++) {
      const i = this.refreshCursor;
      this.refreshCursor = (i + 1) % n;
      if (this.painted[i] === 1) this.renderTile(i, tick);
    }
  }

  /** Brings every tile up to date, however long it takes. */
  flush(tick: number): void {
    for (let i = 0; i < this.dirty.length; i++) {
      if (this.dirty[i] === 1 || this.painted[i] === 1) this.renderTile(i, tick);
    }
  }

  private renderTile(tile: number, tick: number): void {
    if (this.dirty[tile] === 1) {
      this.dirty[tile] = 0;
      this.dirtyCount--;
    }
    const grid = this.grid;
    const cx0 = (tile % this.tilesX) * T;
    const cy0 = Math.floor(tile / this.tilesX) * T;
    const pxW = Math.min(T, grid.width - cx0) * S;
    const pxH = Math.min(T, grid.height - cy0) * S;

    if (!this.gather(cx0, cy0, tick)) {
      if (this.painted[tile] === 1) {
        this.ctx.clearRect(cx0 * S, cy0 * S, pxW, pxH);
        this.painted[tile] = 0;
      }
      return;
    }
    this.painted[tile] = 1;
    this.shade(cx0 * S, cy0 * S, pxW, pxH);
    this.ctx.putImageData(this.image, cx0 * S, cy0 * S, 0, 0, pxW, pxH);
  }

  /**
   * Fills the padded cell buffers for the tile at cell `(cx0, cy0)`: faded
   * alpha, premultiplied colour and pooling. Returns false if nothing in
   * reach of the tile is visible.
   */
  private gather(cx0: number, cy0: number, tick: number): boolean {
    const grid = this.grid;
    const alpha = this.alpha;
    let any = false;
    for (let j = 0; j < PAD2; j++) {
      const cy = cy0 - 2 + j;
      const rowInside = cy >= 0 && cy < grid.height;
      for (let i = 0; i < PAD2; i++) {
        const cx = cx0 - 2 + i;
        let e = 0;
        if (rowInside && cx >= 0 && cx < grid.width) {
          e = Math.min(255, grid.eff(cy * grid.width + cx, tick));
          if (e > 0) any = true;
        }
        alpha[j * PAD2 + i] = e;
      }
    }
    if (!any) return false;

    // Premultiplied colour of every cell in reach, before smoothing.
    const rgba = grid.rgba;
    const { rawR, rawG, rawB } = this;
    for (let j = 0; j < PAD2; j++) {
      const cy = cy0 - 2 + j;
      for (let i = 0; i < PAD2; i++) {
        const o = j * PAD2 + i;
        const e = alpha[o];
        if (e === 0) {
          rawR[o] = rawG[o] = rawB[o] = 0;
          continue;
        }
        const p = (cy * grid.width + cx0 - 2 + i) * 4;
        rawR[o] = rgba[p] * e;
        rawG[o] = rgba[p + 1] * e;
        rawB[o] = rgba[p + 2] * e;
      }
    }

    for (let j = 0; j < PAD; j++) {
      for (let i = 0; i < PAD; i++) {
        const a2 = (j + 1) * PAD2 + i + 1;
        const e = alpha[a2];
        const o = j * PAD + i;
        // The strongest paint within a cell, for judging coverage at edges.
        this.peak[o] =
          Math.max(
            Math.max(alpha[a2 - PAD2 - 1], alpha[a2 - PAD2], alpha[a2 - PAD2 + 1]),
            Math.max(alpha[a2 - 1], e, alpha[a2 + 1]),
            Math.max(alpha[a2 + PAD2 - 1], alpha[a2 + PAD2], alpha[a2 + PAD2 + 1]),
          ) / 255;
        // A 1-2-1 blur across cells rounds the staircase a coarse grid would
        // otherwise leave along diagonal strokes.
        this.pa[o] = tent(alpha, a2) / 255;
        this.pr[o] = tent(rawR, a2) / 255;
        this.pg[o] = tent(rawG, a2) / 255;
        this.pb[o] = tent(rawB, a2) / 255;
        const weakest = Math.min(alpha[a2 - 1], alpha[a2 + 1], alpha[a2 - PAD2], alpha[a2 + PAD2]);
        this.pool[o] = Math.max(0, e - weakest) / 255;
      }
    }
    return true;
  }

  /** Shades the gathered tile into `image`; `(px0, py0)` anchors the noise. */
  private shade(px0: number, py0: number, pxW: number, pxH: number): void {
    const { pr, pg, pb, pa, pool, peak } = this;
    const out = this.image.data;
    for (let y = 0; y < pxH; y++) {
      const row = CELL0[y] * PAD;
      const fy = FRAC[y];
      const noiseRow = ((py0 + y) & NOISE_MASK) * NOISE_SIZE;
      for (let x = 0; x < pxW; x++) {
        const o00 = row + CELL0[x];
        const o01 = o00 + 1;
        const o10 = o00 + PAD;
        const o11 = o10 + 1;
        const fx = FRAC[x];
        const w00 = (1 - fx) * (1 - fy);
        const w01 = fx * (1 - fy);
        const w10 = (1 - fx) * fy;
        const w11 = fx * fy;

        const q = (y * TILE_PX + x) * 4;
        const a = pa[o00] * w00 + pa[o01] * w01 + pa[o10] * w10 + pa[o11] * w11;
        if (a < 0.004) {
          out[q + 3] = 0;
          continue;
        }

        const noiseAt = noiseRow + ((px0 + x) & NOISE_MASK);
        const low = LOW_NOISE[noiseAt] / 255 - 0.5;
        const high = HIGH_NOISE[noiseAt] / 255 - 0.5;

        const cover =
          a / (peak[o00] * w00 + peak[o01] * w01 + peak[o10] * w10 + peak[o11] * w11);
        const edge = smoothstep(PAINT.edgeLo, PAINT.edgeHi, cover + low * PAINT.edgeNoise);
        const pooled = Math.min(
          1,
          (pool[o00] * w00 + pool[o01] * w01 + pool[o10] * w10 + pool[o11] * w11) *
            PAINT.poolGain,
        );
        // Square-root response: thin washes stay visible, yet fade to nothing.
        const body = Math.sqrt(a) * PAINT.bodyMax;
        const opacity =
          edge * Math.min(1, body * (1 + high * PAINT.granulation) + pooled * a * PAINT.poolAlpha);
        const shade = ((1 - pooled * PAINT.poolDarken) * (1 + low * PAINT.variation)) / a;

        out[q] = (pr[o00] * w00 + pr[o01] * w01 + pr[o10] * w10 + pr[o11] * w11) * shade;
        out[q + 1] = (pg[o00] * w00 + pg[o01] * w01 + pg[o10] * w10 + pg[o11] * w11) * shade;
        out[q + 2] = (pb[o00] * w00 + pb[o01] * w01 + pb[o10] * w10 + pb[o11] * w11) * shade;
        out[q + 3] = opacity * 255;
      }
    }
  }
}
