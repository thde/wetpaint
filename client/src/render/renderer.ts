// Composes a frame: desk, sheet, watercolour layer, paper grain, then the
// pencil doodles on top.

import { COLORS, INTERP_DELAY_MS } from '../constants';
import type { Vec2 } from '../interpolation';
import { radiusOf } from '../movement';
import type { Rgb } from '../protocol';
import type { World } from '../world';
import type { Camera } from './camera';
import {
  BeetleView,
  DropView,
  LABEL_FONT,
  NAME_FONT,
  NOTE_FONT,
  drawBeetle,
  drawBlotter,
  drawBrush,
  drawCloud,
  drawDrop,
  drawLabel,
  drawPuddle,
  dropletPath,
} from './entities';
import type { PaintLayer } from './paintLayer';
import { rectPath } from './sketch';

/** The player's own drop, already predicted and smoothed. */
export interface SelfSprite {
  readonly x: number;
  readonly y: number;
  readonly wetness: number;
  readonly color: Rgb;
  readonly name: string;
}

/** Seconds of warning the server gives before the brush comes down. */
const BRUSH_WARN_SECONDS = 2;
/** Slack for culling, so shadows and names do not pop at the screen edge. */
const CULL_MARGIN = 60;
/** Stale per-entity views tolerated before they are swept. */
const VIEW_SLACK = 32;

function sweep<V>(views: Map<number, V>, live: ReadonlyMap<number, unknown>): void {
  if (views.size <= live.size + VIEW_SLACK) return;
  for (const id of views.keys()) {
    if (!live.has(id)) views.delete(id);
  }
}

export class Renderer {
  private readonly ctx: CanvasRenderingContext2D;
  private readonly grain: CanvasPattern;
  private readonly grainSize: number;
  private readonly dropViews = new Map<number, DropView>();
  private readonly beetleViews = new Map<number, BeetleView>();
  private readonly selfView = new DropView();
  private readonly at: Vec2 = { x: 0, y: 0 };

  constructor(
    private readonly canvas: HTMLCanvasElement,
    private readonly camera: Camera,
    grain: HTMLCanvasElement,
  ) {
    const ctx = canvas.getContext('2d', { alpha: false });
    const pattern = ctx?.createPattern(grain, 'repeat');
    if (!ctx || !pattern) throw new Error('2D canvas is not available');
    this.ctx = ctx;
    this.grain = pattern;
    this.grainSize = grain.width;
  }

  /** Matches the canvas backing store to the camera's viewport. */
  resize(): void {
    this.canvas.width = Math.round(this.camera.cssW * this.camera.dpr);
    this.canvas.height = Math.round(this.camera.cssH * this.camera.dpr);
  }

  /** Paints the bare desk, for before a page has loaded. */
  drawEmpty(): void {
    const { ctx } = this;
    ctx.setTransform(1, 0, 0, 1, 0, 0);
    ctx.fillStyle = COLORS.paper;
    ctx.fillRect(0, 0, this.canvas.width, this.canvas.height);
    this.multiplyGrain(0, 0);
  }

  draw(world: World, paint: PaintLayer, self: SelfSprite | null, nowMs: number): void {
    const { ctx, camera } = this;
    const c = world.constants;
    const scale = camera.scale;
    const left = camera.left;
    const top = camera.top;
    const right = left + camera.viewW;
    const bottom = top + camera.viewH;
    const tick = world.clock.tickAt(nowMs);
    const visible = (x: number, y: number, r: number): boolean =>
      x + r > left - CULL_MARGIN &&
      x - r < right + CULL_MARGIN &&
      y + r > top - CULL_MARGIN &&
      y - r < bottom + CULL_MARGIN;

    ctx.setTransform(1, 0, 0, 1, 0, 0);
    ctx.fillStyle = COLORS.desk;
    ctx.fillRect(0, 0, this.canvas.width, this.canvas.height);

    ctx.setTransform(scale, 0, 0, scale, -left * scale, -top * scale);
    ctx.lineCap = 'round';
    ctx.fillStyle = COLORS.shadow;
    ctx.fillRect(8, 10, c.worldW, c.worldH);
    ctx.fillStyle = COLORS.paper;
    ctx.fillRect(0, 0, c.worldW, c.worldH);

    ctx.font = NOTE_FONT;
    ctx.textAlign = 'left';
    world.blotters.forEach((rect, i) => {
      if (visible(rect.x + rect.w / 2, rect.y + rect.h / 2, Math.max(rect.w, rect.h) / 2)) {
        drawBlotter(ctx, rect, 1000 + i * 977);
      }
    });

    // Only the visible part of the watercolour layer is drawn.
    const vx = Math.max(0, left);
    const vy = Math.max(0, top);
    const vw = Math.min(c.worldW, right) - vx;
    const vh = Math.min(c.worldH, bottom) - vy;
    if (vw > 0 && vh > 0) {
      const k = paint.canvas.width / c.worldW;
      ctx.drawImage(paint.canvas, vx * k, vy * k, vw * k, vh * k, vx, vy, vw, vh);
    }

    this.multiplyGrain(left * scale, top * scale);
    ctx.setTransform(scale, 0, 0, scale, -left * scale, -top * scale);

    ctx.strokeStyle = COLORS.pencilSoft;
    ctx.lineWidth = 2;
    ctx.beginPath();
    rectPath(ctx, 0, 0, c.worldW, c.worldH, 5, 2);
    ctx.stroke();

    ctx.font = LABEL_FONT;
    ctx.fillStyle = COLORS.pencil;
    for (const label of world.labels) {
      const fade = 1 - (tick - label.tick) / world.fadeTicks;
      if (fade > 0 && visible(label.x, label.y, 160)) drawLabel(ctx, label, Math.min(1, fade) * 0.8);
    }

    for (const puddle of world.puddles) {
      if (!visible(puddle.x, puddle.y, c.puddleRadius * 2)) continue;
      const fullness = Math.min(1, puddle.remaining / c.puddleCapacity);
      drawPuddle(ctx, puddle.x, puddle.y, c.puddleRadius, fullness, puddle.id * 131, nowMs);
    }

    ctx.beginPath();
    for (const droplet of world.droplets.values()) {
      if (!visible(droplet.x, droplet.y, c.dropletRadius * 2)) continue;
      const bob = Math.sin(nowMs / 420 + droplet.id) * 1.5;
      dropletPath(ctx, droplet.x, droplet.y + bob, c.dropletRadius);
    }
    ctx.fillStyle = COLORS.water;
    ctx.fill();
    ctx.strokeStyle = COLORS.waterRim;
    ctx.lineWidth = 1.6;
    ctx.stroke();

    const interpTick = tick - (INTERP_DELAY_MS * c.tickRate) / 1000;
    const at = this.at;

    for (const beetle of world.beetles.values()) {
      let view = this.beetleViews.get(beetle.id);
      if (!view) {
        view = new BeetleView();
        this.beetleViews.set(beetle.id, view);
      }
      beetle.track.sample(interpTick, at);
      view.moveTo(at.x, at.y);
      if (visible(at.x, at.y, c.beetleRadius * 2)) {
        drawBeetle(ctx, at.x, at.y, c.beetleRadius, view.heading, beetle.id * 17, nowMs);
      }
    }
    sweep(this.beetleViews, world.beetles);

    ctx.font = NAME_FONT;
    ctx.textAlign = 'center';
    for (const drop of world.drops.values()) {
      let view = this.dropViews.get(drop.id);
      if (!view) {
        view = new DropView();
        this.dropViews.set(drop.id, view);
      }
      drop.track.sample(interpTick, at);
      const wetFrac = Math.min(1, drop.wetness / c.maxWetness);
      view.setColor(drop.color, wetFrac);
      view.moveTo(at.x, at.y);
      const radius = radiusOf(drop.wetness, c);
      if (visible(at.x, at.y, radius)) {
        const name = world.names.get(drop.id) ?? '';
        drawDrop(ctx, at.x, at.y, radius, wetFrac, view, name, drop.id * 101, nowMs);
      }
    }
    sweep(this.dropViews, world.drops);

    if (self) {
      const wetFrac = Math.min(1, self.wetness / c.maxWetness);
      this.selfView.setColor(self.color, wetFrac);
      this.selfView.moveTo(self.x, self.y);
      const radius = radiusOf(self.wetness, c);
      drawDrop(ctx, self.x, self.y, radius, wetFrac, this.selfView, self.name, 7, nowMs);
    }

    world.cloud.sample(interpTick, at);
    if (visible(at.x, at.y, 160)) drawCloud(ctx, at.x, at.y, nowMs);

    if (world.brush) drawBrush(ctx, world.brush, tick, BRUSH_WARN_SECONDS * c.tickRate, nowMs);
  }

  /** Multiplies the paper grain over the whole canvas, anchored to the sheet. */
  private multiplyGrain(offsetX: number, offsetY: number): void {
    const { ctx, grainSize } = this;
    const ox = ((Math.round(offsetX) % grainSize) + grainSize) % grainSize;
    const oy = ((Math.round(offsetY) % grainSize) + grainSize) % grainSize;
    ctx.setTransform(1, 0, 0, 1, -ox, -oy);
    ctx.globalCompositeOperation = 'multiply';
    ctx.fillStyle = this.grain;
    ctx.fillRect(0, 0, this.canvas.width + grainSize, this.canvas.height + grainSize);
    ctx.globalCompositeOperation = 'source-over';
  }
}
