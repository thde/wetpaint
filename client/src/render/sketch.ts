// Pencil primitives. Every shape takes a seed so its wobble is stable from
// frame to frame; add `boil(now)` to a seed for doodles that should wiggle.

import { hash01 } from './noise';

const TAU = Math.PI * 2;
const BLOB_POINTS = 9;

/** A seed offset that changes a few times a second, like hand-drawn animation. */
export function boil(nowMs: number): number {
  return Math.floor(nowMs / 160) * 7919;
}

function jitter(seed: number, amount: number): number {
  return (hash01(seed) - 0.5) * 2 * amount;
}

/**
 * Adds a closed, slightly lumpy ellipse to the current path. `wobble` is the
 * lump size as a fraction of the radius; `phase` slides the lumps around.
 */
export function blobPath(
  ctx: CanvasRenderingContext2D,
  x: number,
  y: number,
  rx: number,
  ry: number,
  seed: number,
  wobble: number,
  phase = 0,
): void {
  // A smooth closed curve: quadratic segments between edge midpoints, with
  // the blob's own points as control points.
  let prevX = 0;
  let prevY = 0;
  for (let i = 0; i <= BLOB_POINTS + 1; i++) {
    const k = i % BLOB_POINTS;
    const a = (k / BLOB_POINTS) * TAU;
    const lump = 1 + wobble * Math.sin(phase + hash01(seed + k) * TAU);
    const px = x + Math.cos(a) * rx * lump;
    const py = y + Math.sin(a) * ry * lump;
    if (i === 1) ctx.moveTo((prevX + px) / 2, (prevY + py) / 2);
    else if (i > 1) ctx.quadraticCurveTo(prevX, prevY, (prevX + px) / 2, (prevY + py) / 2);
    prevX = px;
    prevY = py;
  }
  ctx.closePath();
}

/** Adds a pencil line with a slight bow and imprecise ends. */
export function linePath(
  ctx: CanvasRenderingContext2D,
  x0: number,
  y0: number,
  x1: number,
  y1: number,
  seed: number,
  wobble: number,
): void {
  const mx = (x0 + x1) / 2 + jitter(seed, wobble * 1.5);
  const my = (y0 + y1) / 2 + jitter(seed + 1, wobble * 1.5);
  ctx.moveTo(x0 + jitter(seed + 2, wobble), y0 + jitter(seed + 3, wobble));
  ctx.quadraticCurveTo(mx, my, x1 + jitter(seed + 4, wobble), y1 + jitter(seed + 5, wobble));
}

/** Adds a four-sided pencil outline whose corners overshoot a little. */
export function rectPath(
  ctx: CanvasRenderingContext2D,
  x: number,
  y: number,
  w: number,
  h: number,
  seed: number,
  wobble: number,
): void {
  linePath(ctx, x, y, x + w, y, seed, wobble);
  linePath(ctx, x + w, y, x + w, y + h, seed + 10, wobble);
  linePath(ctx, x + w, y + h, x, y + h, seed + 20, wobble);
  linePath(ctx, x, y + h, x, y, seed + 30, wobble);
}
