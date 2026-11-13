// Pencil-and-wash doodles for everything that lives on the sheet. All
// functions draw in world units; the caller has set the camera transform.

import { COLORS, FONTS } from '../constants';
import type { BloomLabel, Brush, Rect, Rgb } from '../protocol';
import { hash01 } from './noise';
import { blobPath, boil, linePath, rectPath } from './sketch';

const TAU = Math.PI * 2;
const PENCIL_WIDTH = 1.7;
const DASHED: number[] = [16, 13];
const SOLID: number[] = [];

export const NAME_FONT = `16px ${FONTS.hand}`;
export const LABEL_FONT = `28px ${FONTS.pencil}`;
export const NOTE_FONT = `24px ${FONTS.pencil}`;

function css(r: number, g: number, b: number): string {
  return `rgb(${Math.round(r)}, ${Math.round(g)}, ${Math.round(b)})`;
}

export function rgbCss(c: Rgb): string {
  return css(c.r, c.g, c.b);
}

/** Per-drop drawing state that would be wasteful to rebuild every frame. */
export class DropView {
  fill = '';
  rim = '';
  /** Where the eyes are looking, each component in [-1, 1]. */
  lookX = 0;
  lookY = 0;

  private key = -1;
  private lastX = Number.NaN;
  private lastY = Number.NaN;

  /** Drier drops are paler; the strings change only when the look does. */
  setColor(color: Rgb, wetFrac: number): void {
    const pale = Math.round((1 - wetFrac) * 16);
    const key = (((color.r << 16) | (color.g << 8) | color.b) << 5) | pale;
    if (key === this.key) return;
    this.key = key;
    const m = (pale / 16) * 0.5;
    this.fill = css(
      color.r + (255 - color.r) * m,
      color.g + (255 - color.g) * m,
      color.b + (255 - color.b) * m,
    );
    this.rim = css(color.r * 0.7, color.g * 0.7, color.b * 0.72);
  }

  moveTo(x: number, y: number): void {
    if (!Number.isNaN(this.lastX)) {
      const tx = Math.max(-1, Math.min(1, (x - this.lastX) * 0.6));
      const ty = Math.max(-1, Math.min(1, (y - this.lastY) * 0.6));
      this.lookX += (tx - this.lookX) * 0.15;
      this.lookY += (ty - this.lookY) * 0.15;
    }
    this.lastX = x;
    this.lastY = y;
  }
}

/** A drop of paint with a face. `ctx.font` must be NAME_FONT. */
export function drawDrop(
  ctx: CanvasRenderingContext2D,
  x: number,
  y: number,
  radius: number,
  wetFrac: number,
  view: DropView,
  name: string,
  seed: number,
  nowMs: number,
): void {
  const alpha = 0.55 + 0.4 * wetFrac;

  ctx.globalAlpha = alpha;
  ctx.beginPath();
  blobPath(ctx, x, y, radius, radius, seed, 0.07, nowMs / 260 + seed);
  ctx.fillStyle = view.fill;
  ctx.fill();
  // The darker rim is the same edge pooling the paint has.
  ctx.strokeStyle = view.rim;
  ctx.lineWidth = Math.max(1.4, radius * 0.1);
  ctx.stroke();

  ctx.globalAlpha = alpha * 0.55;
  ctx.fillStyle = '#fff';
  ctx.beginPath();
  ctx.ellipse(x - radius * 0.36, y - radius * 0.42, radius * 0.22, radius * 0.13, -0.6, 0, TAU);
  ctx.fill();

  ctx.globalAlpha = 0.85;
  ctx.fillStyle = COLORS.pencil;
  const ex = x + view.lookX * radius * 0.16;
  const ey = y - radius * 0.08 + view.lookY * radius * 0.16;
  const eye = Math.max(1.3, radius * 0.085);
  ctx.beginPath();
  ctx.arc(ex - radius * 0.27, ey, eye, 0, TAU);
  ctx.arc(ex + radius * 0.27, ey, eye, 0, TAU);
  ctx.fill();
  ctx.strokeStyle = COLORS.pencil;
  ctx.lineWidth = Math.max(1, radius * 0.05);
  ctx.beginPath();
  ctx.arc(ex, ey + radius * 0.12, radius * 0.17, 0.2 * Math.PI, 0.8 * Math.PI);
  ctx.stroke();

  if (name !== '') {
    ctx.globalAlpha = 0.8;
    ctx.fillText(name, x, y + radius + 15);
  }
  ctx.globalAlpha = 1;
}

/** Remembers which way a beetle is walking. */
export class BeetleView {
  heading = 0;
  private lastX = Number.NaN;
  private lastY = Number.NaN;

  moveTo(x: number, y: number): void {
    const dx = x - this.lastX;
    const dy = y - this.lastY;
    if (dx * dx + dy * dy > 0.01) {
      // Turn the short way round.
      const turn = Math.atan2(dy, dx) - this.heading;
      this.heading += Math.atan2(Math.sin(turn), Math.cos(turn)) * 0.2;
    }
    this.lastX = x;
    this.lastY = y;
  }
}

export function drawBeetle(
  ctx: CanvasRenderingContext2D,
  x: number,
  y: number,
  r: number,
  heading: number,
  seed: number,
  nowMs: number,
): void {
  ctx.save();
  ctx.translate(x, y);
  ctx.rotate(heading + Math.sin(nowMs / 130 + seed) * 0.12);

  ctx.strokeStyle = COLORS.pencil;
  ctx.lineWidth = PENCIL_WIDTH;
  ctx.lineCap = 'round';
  ctx.beginPath();
  for (let i = -1; i <= 1; i++) {
    for (let side = -1; side <= 1; side += 2) {
      const swing = Math.sin(nowMs / 70 + i * 2 + side) * 0.22 * r;
      ctx.moveTo(i * 0.45 * r, side * 0.55 * r);
      ctx.lineTo(i * 0.6 * r + swing, side * 1.3 * r);
    }
  }
  const twitch = Math.sin(nowMs / 190 + seed) * 0.15 * r;
  ctx.moveTo(1.1 * r, -0.15 * r);
  ctx.quadraticCurveTo(1.5 * r, -0.3 * r, 1.75 * r, -0.55 * r + twitch);
  ctx.moveTo(1.1 * r, 0.15 * r);
  ctx.quadraticCurveTo(1.5 * r, 0.3 * r, 1.75 * r, 0.55 * r - twitch);
  ctx.stroke();

  ctx.fillStyle = COLORS.beetle;
  ctx.beginPath();
  blobPath(ctx, 0, 0, r, r * 0.76, seed, 0.05);
  ctx.arc(r * 0.95, 0, r * 0.42, 0, TAU);
  ctx.fill();

  ctx.strokeStyle = 'rgba(248, 243, 231, 0.6)';
  ctx.beginPath();
  ctx.moveTo(-0.85 * r, 0);
  ctx.lineTo(0.5 * r, 0);
  ctx.stroke();

  ctx.fillStyle = '#fff';
  ctx.beginPath();
  ctx.arc(1.08 * r, -0.2 * r, 0.13 * r, 0, TAU);
  ctx.arc(1.08 * r, 0.2 * r, 0.13 * r, 0, TAU);
  ctx.fill();
  ctx.restore();
}

const CLOUD_PUFFS: readonly (readonly [number, number, number])[] = [
  [-62, 8, 32],
  [-26, -18, 42],
  [22, -24, 46],
  [64, 6, 34],
  [0, 12, 30],
];

/** The rain cloud, centred on `(x, y)`, with a soft shadow on the paper. */
export function drawCloud(ctx: CanvasRenderingContext2D, x: number, y: number, nowMs: number): void {
  ctx.fillStyle = COLORS.shadow;
  ctx.beginPath();
  ctx.ellipse(x + 14, y + 78, 92, 26, 0, 0, TAU);
  ctx.fill();

  const cy = y + Math.sin(nowMs / 900) * 4;
  const wiggle = boil(nowMs);

  // Stroke every puff, then fill them all: only the outer outline survives.
  ctx.strokeStyle = COLORS.pencil;
  ctx.lineWidth = PENCIL_WIDTH * 2.4;
  ctx.beginPath();
  for (let i = 0; i < CLOUD_PUFFS.length; i++) {
    const [px, py, pr] = CLOUD_PUFFS[i];
    blobPath(ctx, x + px, cy + py, pr, pr, wiggle + i * 31, 0.03);
  }
  ctx.stroke();
  ctx.fillStyle = '#fdfcf9';
  ctx.fill();

  ctx.fillStyle = 'rgba(240, 150, 150, 0.35)';
  ctx.beginPath();
  ctx.ellipse(x - 30, cy + 8, 9, 5, 0, 0, TAU);
  ctx.ellipse(x + 30, cy + 8, 9, 5, 0, 0, TAU);
  ctx.fill();
  ctx.fillStyle = COLORS.pencil;
  ctx.beginPath();
  ctx.arc(x - 15, cy - 2, 3.2, 0, TAU);
  ctx.arc(x + 15, cy - 2, 3.2, 0, TAU);
  ctx.fill();
  ctx.lineWidth = PENCIL_WIDTH;
  ctx.beginPath();
  ctx.arc(x, cy + 2, 9, 0.15 * Math.PI, 0.85 * Math.PI);
  ctx.stroke();

  ctx.strokeStyle = COLORS.waterRim;
  ctx.lineCap = 'round';
  for (let i = 0; i < 5; i++) {
    const fall = (nowMs / 700 + hash01(i + 3)) % 1;
    const rx = x - 52 + i * 26;
    const ry = cy + 50 + fall * 46;
    ctx.globalAlpha = 1 - fall;
    ctx.beginPath();
    ctx.moveTo(rx, ry);
    ctx.lineTo(rx - 2, ry + 11);
    ctx.stroke();
  }
  ctx.globalAlpha = 1;
}

/** Adds one teardrop to the current path so droplets can share a fill. */
export function dropletPath(
  ctx: CanvasRenderingContext2D,
  x: number,
  y: number,
  r: number,
): void {
  const cy = y + r * 0.3;
  ctx.moveTo(x, cy - r * 1.75);
  ctx.bezierCurveTo(x + r * 0.3, cy - r * 0.95, x + r, cy - r * 0.45, x + r, cy);
  ctx.arc(x, cy, r, 0, Math.PI);
  ctx.bezierCurveTo(x - r, cy - r * 0.45, x - r * 0.3, cy - r * 0.95, x, cy - r * 1.75);
}

/** A spilled glass of water; `fullness` ∈ [0, 1] shrinks the puddle. */
export function drawPuddle(
  ctx: CanvasRenderingContext2D,
  x: number,
  y: number,
  radius: number,
  fullness: number,
  seed: number,
  nowMs: number,
): void {
  const r = radius * (0.3 + 0.7 * Math.sqrt(fullness));
  ctx.lineWidth = PENCIL_WIDTH;
  ctx.fillStyle = COLORS.water;
  ctx.strokeStyle = COLORS.waterRim;
  ctx.beginPath();
  blobPath(ctx, x, y, r * 1.12, r * 0.86, seed, 0.09, nowMs / 700);
  ctx.fill();
  ctx.stroke();
  ctx.strokeStyle = 'rgba(255, 255, 255, 0.7)';
  ctx.beginPath();
  ctx.arc(x - r * 0.2, y - r * 0.1, r * 0.5, 1.1 * Math.PI, 1.5 * Math.PI);
  ctx.stroke();

  // The tipped-over glass it came from.
  ctx.save();
  ctx.translate(x + radius * 0.95, y - radius * 0.75);
  ctx.rotate(1.05);
  ctx.strokeStyle = COLORS.pencil;
  ctx.fillStyle = 'rgba(255, 255, 255, 0.45)';
  ctx.beginPath();
  ctx.moveTo(-13, -20);
  ctx.lineTo(-9, 20);
  ctx.lineTo(9, 20);
  ctx.lineTo(13, -20);
  ctx.closePath();
  ctx.fill();
  ctx.beginPath();
  linePath(ctx, -13, -20, -9, 20, seed + 1, 1);
  linePath(ctx, -9, 20, 9, 20, seed + 2, 1);
  linePath(ctx, 9, 20, 13, -20, seed + 3, 1);
  ctx.ellipse(0, -20, 13, 4, 0, 0, TAU);
  ctx.stroke();
  ctx.restore();
}

/** A patch of rough blotting paper. `ctx.font` must be NOTE_FONT. */
export function drawBlotter(ctx: CanvasRenderingContext2D, rect: Rect, seed: number): void {
  const { x, y, w, h } = rect;
  ctx.fillStyle = COLORS.blotter;
  ctx.fillRect(x, y, w, h);

  ctx.strokeStyle = COLORS.pencilSoft;
  ctx.lineWidth = PENCIL_WIDTH;
  ctx.beginPath();
  rectPath(ctx, x, y, w, h, seed, 3);
  rectPath(ctx, x, y, w, h, seed + 100, 4);
  // Short hatching strokes for the rough tooth.
  const strokes = Math.min(70, Math.floor((w * h) / 2600));
  for (let i = 0; i < strokes; i++) {
    const hx = x + 10 + hash01(seed + i * 2 + 200) * (w - 30);
    const hy = y + 14 + hash01(seed + i * 2 + 201) * (h - 24);
    ctx.moveTo(hx, hy);
    ctx.lineTo(hx + 12, hy - 9);
  }
  ctx.stroke();

  ctx.fillStyle = COLORS.pencilSoft;
  ctx.fillText('blotting paper', x + 12, y + h - 12);
}

/** A name pencilled beside a bloom. `ctx.font` must be LABEL_FONT. */
export function drawLabel(ctx: CanvasRenderingContext2D, label: BloomLabel, alpha: number): void {
  const seed = Math.floor(label.x * 7 + label.y * 13);
  ctx.save();
  ctx.translate(label.x + 50, label.y + 34);
  ctx.rotate((hash01(seed) - 0.5) * 0.3);
  ctx.globalAlpha = alpha;
  ctx.fillText(label.name, 0, 0);
  ctx.restore();
}

/**
 * The Brush hazard. Before `startTick` only its pencilled guideline and
 * shadow show; then a sketched hand drags the brush along the band.
 */
export function drawBrush(
  ctx: CanvasRenderingContext2D,
  brush: Brush,
  tick: number,
  warnTicks: number,
  nowMs: number,
): void {
  const dx = brush.x1 - brush.x0;
  const dy = brush.y1 - brush.y0;
  const len = Math.hypot(dx, dy);
  if (len === 0) return;
  const hw = brush.halfWidth;
  const nx = (-dy / len) * hw;
  const ny = (dx / len) * hw;
  const progress = (tick - brush.startTick) / brush.durationTicks;
  const sweeping = progress >= 0;
  // The shadow deepens as the brush gets closer to coming down.
  const looming = sweeping ? 1 : Math.max(0, 1 + (tick - brush.startTick) / warnTicks);

  ctx.globalAlpha = sweeping ? 0.5 : 0.35 + 0.65 * looming;
  ctx.fillStyle = COLORS.shadow;
  ctx.beginPath();
  ctx.moveTo(brush.x0 + nx, brush.y0 + ny);
  ctx.lineTo(brush.x1 + nx, brush.y1 + ny);
  ctx.lineTo(brush.x1 - nx, brush.y1 - ny);
  ctx.lineTo(brush.x0 - nx, brush.y0 - ny);
  ctx.closePath();
  ctx.fill();

  ctx.strokeStyle = COLORS.pencilSoft;
  ctx.lineWidth = PENCIL_WIDTH;
  ctx.setLineDash(DASHED);
  ctx.beginPath();
  ctx.moveTo(brush.x0, brush.y0);
  ctx.lineTo(brush.x1, brush.y1);
  ctx.moveTo(brush.x0 + nx, brush.y0 + ny);
  ctx.lineTo(brush.x1 + nx, brush.y1 + ny);
  ctx.moveTo(brush.x0 - nx, brush.y0 - ny);
  ctx.lineTo(brush.x1 - nx, brush.y1 - ny);
  ctx.stroke();
  ctx.setLineDash(SOLID);
  ctx.globalAlpha = 1;

  if (!sweeping || progress > 1) return;

  ctx.save();
  ctx.translate(brush.x0 + dx * progress, brush.y0 + dy * progress);
  ctx.rotate(Math.atan2(dy, dx));
  // From here +x is the direction of travel: the hand leads, bristles trail.

  ctx.fillStyle = COLORS.shadow;
  ctx.beginPath();
  ctx.ellipse(hw * 0.5, hw * 0.22, hw * 1.5, hw * 1.05, 0, 0, TAU);
  ctx.fill();

  ctx.strokeStyle = COLORS.pencil;
  ctx.lineWidth = PENCIL_WIDTH * 1.4;
  ctx.lineJoin = 'round';

  const wiggle = boil(nowMs);
  ctx.fillStyle = rgbCss(brush.color);
  ctx.beginPath();
  ctx.moveTo(0, -hw);
  const tufts = 9;
  for (let i = 0; i <= tufts; i++) {
    const ragged = (0.4 + hash01(wiggle + i) * 0.14) * hw;
    ctx.lineTo(-ragged, -hw + (i / tufts) * 2 * hw);
  }
  ctx.lineTo(0, hw);
  ctx.closePath();
  ctx.fill();
  ctx.stroke();

  ctx.fillStyle = '#cfcfd6';
  ctx.beginPath();
  ctx.rect(0, -hw, hw * 0.24, 2 * hw);
  ctx.fill();
  ctx.stroke();

  ctx.fillStyle = '#dcbf92';
  ctx.beginPath();
  ctx.moveTo(hw * 0.24, -hw * 0.36);
  ctx.lineTo(hw * 2.1, -hw * 0.17);
  ctx.quadraticCurveTo(hw * 2.25, 0, hw * 2.1, hw * 0.17);
  ctx.lineTo(hw * 0.24, hw * 0.36);
  ctx.closePath();
  ctx.fill();
  ctx.stroke();

  // Sleeve, palm, then four fingers curled over the handle.
  ctx.fillStyle = '#fbf7ee';
  ctx.beginPath();
  ctx.moveTo(hw * 1.5, -hw * 0.3);
  ctx.lineTo(hw * 3.4, -hw * 0.75);
  ctx.lineTo(hw * 3.4, hw * 1.15);
  ctx.lineTo(hw * 1.5, hw * 0.55);
  ctx.closePath();
  ctx.fill();
  ctx.stroke();
  ctx.beginPath();
  blobPath(ctx, hw * 1.2, hw * 0.16, hw * 0.52, hw * 0.46, 11, 0.05);
  ctx.fill();
  ctx.stroke();
  for (let i = 0; i < 4; i++) {
    ctx.beginPath();
    blobPath(ctx, hw * (0.86 + i * 0.21), -hw * 0.14, hw * 0.115, hw * 0.3, 20 + i, 0.05);
    ctx.fill();
    ctx.stroke();
  }
  ctx.restore();
}
