import { COLORS, MAX_DPR } from '../constants';
import { linePath } from '../render/sketch';

const WIDTH = 54;
const HEIGHT = 72;
// The glass is a trapezoid: wider at the rim than at the base.
const RIM_Y = 8;
const BASE_Y = 64;
const RIM_HALF = 21;
const BASE_HALF = 14;

/** The wetness gauge: a little sketched glass of water that empties. */
export class Glass {
  readonly canvas = document.createElement('canvas');

  private readonly ctx: CanvasRenderingContext2D;
  private shown = -1;

  constructor() {
    const dpr = Math.min(MAX_DPR, window.devicePixelRatio);
    this.canvas.className = 'glass';
    this.canvas.width = WIDTH * dpr;
    this.canvas.height = HEIGHT * dpr;
    this.canvas.style.width = `${WIDTH}px`;
    this.canvas.style.height = `${HEIGHT}px`;
    const ctx = this.canvas.getContext('2d');
    if (!ctx) throw new Error('2D canvas is not available');
    ctx.scale(dpr, dpr);
    this.ctx = ctx;
    this.set(0);
  }

  /** `fullness` ∈ [0, 1]. Redraws only when the level visibly changes. */
  set(fullness: number): void {
    const level = Math.round(Math.min(1, Math.max(0, fullness)) * 50);
    if (level === this.shown) return;
    this.shown = level;

    const ctx = this.ctx;
    const cx = WIDTH / 2;
    const f = level / 50;
    const surfaceY = BASE_Y - (BASE_Y - RIM_Y - 4) * f;
    const surfaceHalf = BASE_HALF + (RIM_HALF - BASE_HALF) * ((BASE_Y - surfaceY) / (BASE_Y - RIM_Y));

    ctx.clearRect(0, 0, WIDTH, HEIGHT);
    ctx.fillStyle = 'rgba(255, 255, 255, 0.55)';
    ctx.beginPath();
    ctx.moveTo(cx - RIM_HALF, RIM_Y);
    ctx.lineTo(cx - BASE_HALF, BASE_Y);
    ctx.lineTo(cx + BASE_HALF, BASE_Y);
    ctx.lineTo(cx + RIM_HALF, RIM_Y);
    ctx.closePath();
    ctx.fill();

    if (level > 0) {
      // Running low turns the water a worried pink.
      ctx.fillStyle = f < 0.25 ? 'rgba(236, 140, 140, 0.7)' : 'rgba(120, 184, 226, 0.75)';
      ctx.beginPath();
      ctx.moveTo(cx - surfaceHalf, surfaceY);
      ctx.quadraticCurveTo(cx - surfaceHalf / 2, surfaceY - 3, cx, surfaceY);
      ctx.quadraticCurveTo(cx + surfaceHalf / 2, surfaceY + 3, cx + surfaceHalf, surfaceY);
      ctx.lineTo(cx + BASE_HALF, BASE_Y);
      ctx.lineTo(cx - BASE_HALF, BASE_Y);
      ctx.closePath();
      ctx.fill();
    }

    ctx.strokeStyle = COLORS.pencil;
    ctx.lineWidth = 1.8;
    ctx.lineCap = 'round';
    ctx.beginPath();
    linePath(ctx, cx - RIM_HALF, RIM_Y, cx - BASE_HALF, BASE_Y, 1, 1);
    linePath(ctx, cx - BASE_HALF, BASE_Y, cx + BASE_HALF, BASE_Y, 7, 1);
    linePath(ctx, cx + BASE_HALF, BASE_Y, cx + RIM_HALF, RIM_Y, 13, 1);
    ctx.ellipse(cx, RIM_Y, RIM_HALF, 3.5, 0, 0, Math.PI * 2);
    ctx.stroke();
  }
}
