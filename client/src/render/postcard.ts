import { COLORS, FONTS, POSTCARD } from '../constants';
import type { World } from '../world';
import { LABEL_FONT, drawLabel } from './entities';
import type { PaintLayer } from './paintLayer';

function isoDate(d: Date): string {
  const pad = (n: number): string => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

/**
 * Renders the whole sheet as it is right now, with its pencilled names and a
 * handwritten caption, and hands it to the browser as a PNG download.
 */
export function downloadPostcard(
  world: World,
  paint: PaintLayer,
  grain: HTMLCanvasElement,
  nowMs: number,
): void {
  const c = world.constants;
  const tick = world.clock.tickAt(nowMs);
  paint.flush(Math.floor(tick));

  const canvas = document.createElement('canvas');
  canvas.width = POSTCARD.width;
  canvas.height = Math.round((POSTCARD.width * c.worldH) / c.worldW);
  const ctx = canvas.getContext('2d');
  const pattern = ctx?.createPattern(grain, 'repeat');
  if (!ctx || !pattern) return;

  ctx.fillStyle = COLORS.paper;
  ctx.fillRect(0, 0, canvas.width, canvas.height);
  ctx.imageSmoothingQuality = 'high';
  ctx.drawImage(paint.canvas, 0, 0, canvas.width, canvas.height);
  ctx.globalCompositeOperation = 'multiply';
  ctx.fillStyle = pattern;
  ctx.fillRect(0, 0, canvas.width, canvas.height);
  ctx.globalCompositeOperation = 'source-over';

  const k = canvas.width / c.worldW;
  ctx.setTransform(k, 0, 0, k, 0, 0);
  ctx.font = LABEL_FONT;
  ctx.fillStyle = COLORS.pencil;
  for (const label of world.labels) {
    const fade = 1 - (tick - label.tick) / world.fadeTicks;
    if (fade > 0) drawLabel(ctx, label, Math.min(1, fade) * 0.8);
  }

  const today = new Date();
  ctx.setTransform(1, 0, 0, 1, 0, 0);
  ctx.font = `${POSTCARD.captionPx}px ${FONTS.pencil}`;
  ctx.textAlign = 'right';
  ctx.globalAlpha = 0.85;
  ctx.fillText(
    `Wet Paint, page ${world.page}, ${today.toLocaleDateString(undefined, { dateStyle: 'long' })}`,
    canvas.width - 36,
    canvas.height - 32,
  );

  canvas.toBlob((blob) => {
    if (!blob) return;
    const link = document.createElement('a');
    link.href = URL.createObjectURL(blob);
    link.download = `wet-paint-page-${world.page}-${isoDate(today)}.png`;
    link.click();
    // Give the download a moment to start before releasing the blob.
    window.setTimeout(() => URL.revokeObjectURL(link.href), 10_000);
  }, 'image/png');
}
