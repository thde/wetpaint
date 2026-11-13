import { COLORS, MAX_DPR, MINIMAP } from '../constants';
import type { Vec2 } from '../interpolation';
import type { World } from '../world';
import type { Camera } from './camera';
import { rgbCss } from './entities';
import type { PaintLayer } from './paintLayer';
import type { SelfSprite } from './renderer';

/** A thumbnail of the whole painted sheet, refreshed a few times a second. */
export class Minimap {
  private readonly ctx: CanvasRenderingContext2D;
  private readonly at: Vec2 = { x: 0, y: 0 };
  private nextDraw = 0;

  constructor(private readonly canvas: HTMLCanvasElement) {
    const dpr = Math.min(MAX_DPR, window.devicePixelRatio);
    canvas.width = MINIMAP.width * dpr;
    canvas.height = MINIMAP.height * dpr;
    const ctx = canvas.getContext('2d');
    if (!ctx) throw new Error('2D canvas is not available');
    this.ctx = ctx;
  }

  draw(
    world: World,
    paint: PaintLayer,
    camera: Camera,
    self: SelfSprite | null,
    nowMs: number,
  ): void {
    if (nowMs < this.nextDraw) return;
    this.nextDraw = nowMs + MINIMAP.refreshMs;

    const { ctx, canvas } = this;
    const c = world.constants;
    const k = canvas.width / c.worldW;
    ctx.setTransform(1, 0, 0, 1, 0, 0);
    ctx.fillStyle = COLORS.paper;
    ctx.fillRect(0, 0, canvas.width, canvas.height);
    ctx.drawImage(paint.canvas, 0, 0, canvas.width, canvas.height);

    ctx.setTransform(k, 0, 0, k, 0, 0);
    ctx.lineWidth = 1.2 / k;

    ctx.fillStyle = COLORS.waterRim;
    for (const puddle of world.puddles) {
      ctx.beginPath();
      ctx.arc(puddle.x, puddle.y, 3 / k, 0, Math.PI * 2);
      ctx.fill();
    }
    world.cloud.sample(world.clock.tickAt(nowMs), this.at);
    ctx.fillStyle = '#fff';
    ctx.strokeStyle = COLORS.pencil;
    ctx.beginPath();
    ctx.ellipse(this.at.x, this.at.y, 7 / k, 4 / k, 0, 0, Math.PI * 2);
    ctx.fill();
    ctx.stroke();

    ctx.strokeStyle = COLORS.pencilSoft;
    ctx.strokeRect(camera.left, camera.top, camera.viewW, camera.viewH);

    if (self) {
      ctx.fillStyle = rgbCss(self.color);
      ctx.strokeStyle = COLORS.pencil;
      ctx.beginPath();
      ctx.arc(self.x, self.y, 3.5 / k, 0, Math.PI * 2);
      ctx.fill();
      ctx.stroke();
    }
  }
}
