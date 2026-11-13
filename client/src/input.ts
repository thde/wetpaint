// Turns mouse, touch and keyboard into the protocol's Input vector: a
// direction whose length is the throttle, quantised to [-127, 127].

import { POINTER } from './constants';
import { quantizeInput } from './movement';
import type { Camera } from './render/camera';

const KEY_DIRECTIONS: Readonly<Record<string, readonly [number, number]>> = {
  KeyW: [0, -1],
  ArrowUp: [0, -1],
  KeyS: [0, 1],
  ArrowDown: [0, 1],
  KeyA: [-1, 0],
  ArrowLeft: [-1, 0],
  KeyD: [1, 0],
  ArrowRight: [1, 0],
};

/**
 * Where the drop is heading. A mouse pointer stays put on screen while the
 * camera moves, so its target is kept in screen space; a lifted finger leaves
 * a fixed spot on the paper to glide to.
 */
type Target =
  | { readonly kind: 'none' }
  | { readonly kind: 'screen'; readonly x: number; readonly y: number }
  | { readonly kind: 'world'; readonly x: number; readonly y: number };

const NO_TARGET: Target = { kind: 'none' };

export class Input {
  /** Wire input for the current tick; valid after `sample`. */
  dx = 0;
  dy = 0;

  private readonly keys = new Set<string>();
  private target: Target = NO_TARGET;

  constructor(
    surface: HTMLElement,
    private readonly camera: Camera,
  ) {
    const point = (ev: PointerEvent): void => {
      this.target = { kind: 'screen', x: ev.clientX, y: ev.clientY };
    };
    surface.addEventListener('pointerdown', point);
    surface.addEventListener('pointermove', (ev) => {
      // A finger steers only while it is down; a mouse always does.
      if (ev.pointerType === 'mouse' || ev.buttons !== 0) point(ev);
    });
    const lift = (ev: PointerEvent): void => {
      if (ev.pointerType === 'mouse') return;
      this.target = {
        kind: 'world',
        x: camera.worldX(ev.clientX),
        y: camera.worldY(ev.clientY),
      };
    };
    surface.addEventListener('pointerup', lift);
    surface.addEventListener('pointercancel', lift);

    window.addEventListener('keydown', (ev) => {
      if (!(ev.code in KEY_DIRECTIONS) || ev.target instanceof HTMLInputElement) return;
      ev.preventDefault();
      this.keys.add(ev.code);
      // The keyboard takes over until the pointer moves again.
      this.target = NO_TARGET;
    });
    window.addEventListener('keyup', (ev) => this.keys.delete(ev.code));
    window.addEventListener('blur', () => this.keys.clear());
  }

  /** Forgets any pointer target, e.g. when a new life starts. */
  rest(): void {
    this.target = NO_TARGET;
  }

  /** Computes `dx`, `dy` for a drop at `(selfX, selfY)`. */
  sample(selfX: number, selfY: number): void {
    let vx = 0;
    let vy = 0;
    if (this.keys.size > 0) {
      for (const code of this.keys) {
        const [kx, ky] = KEY_DIRECTIONS[code];
        vx += kx;
        vy += ky;
      }
      const len = Math.hypot(vx, vy);
      if (len > 0) {
        vx /= len;
        vy /= len;
      }
    } else if (this.target.kind !== 'none') {
      const screen = this.target.kind === 'screen';
      const tx = (screen ? this.camera.worldX(this.target.x) : this.target.x) - selfX;
      const ty = (screen ? this.camera.worldY(this.target.y) : this.target.y) - selfY;
      const dist = Math.hypot(tx, ty);
      if (dist > POINTER.deadzone) {
        // Ease off as the drop arrives so it settles instead of orbiting.
        const throttle = Math.min(1, dist / POINTER.fullThrottle) / dist;
        vx = tx * throttle;
        vy = ty * throttle;
      }
    }
    this.dx = quantizeInput(vx);
    this.dy = quantizeInput(vy);
  }
}
