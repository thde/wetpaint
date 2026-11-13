import { MAX_DPR, VIEW } from '../constants';

function clampCentre(target: number, view: number, world: number): number {
  const lo = view / 2 - VIEW.overscroll;
  const hi = world - view / 2 + VIEW.overscroll;
  return lo > hi ? world / 2 : Math.min(hi, Math.max(lo, target));
}

/** Maps between world units, CSS pixels and device pixels. */
export class Camera {
  /** Centre of the view, world units. */
  x = 0;
  y = 0;
  cssW = 1;
  cssH = 1;
  dpr = 1;
  /** CSS pixels per world unit. */
  zoom = 1;

  resize(cssW: number, cssH: number, devicePixelRatio: number): void {
    this.cssW = cssW;
    this.cssH = cssH;
    this.dpr = Math.min(MAX_DPR, devicePixelRatio);
    this.zoom = Math.max(cssW / VIEW.maxW, cssH / VIEW.maxH);
  }

  /** Device pixels per world unit. */
  get scale(): number {
    return this.zoom * this.dpr;
  }

  /** Visible world size. */
  get viewW(): number {
    return this.cssW / this.zoom;
  }

  get viewH(): number {
    return this.cssH / this.zoom;
  }

  get left(): number {
    return this.x - this.viewW / 2;
  }

  get top(): number {
    return this.y - this.viewH / 2;
  }

  worldX(cssX: number): number {
    return this.left + cssX / this.zoom;
  }

  worldY(cssY: number): number {
    return this.top + cssY / this.zoom;
  }

  /** Eases towards the target; `dt` in seconds. */
  follow(tx: number, ty: number, dt: number, worldW: number, worldH: number): void {
    const k = 1 - Math.exp(-VIEW.follow * dt);
    this.x += (clampCentre(tx, this.viewW, worldW) - this.x) * k;
    this.y += (clampCentre(ty, this.viewH, worldH) - this.y) * k;
  }

  jump(tx: number, ty: number, worldW: number, worldH: number): void {
    this.x = clampCentre(tx, this.viewW, worldW);
    this.y = clampCentre(ty, this.viewH, worldH);
  }
}
