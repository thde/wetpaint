// Gameplay constants as sent in Welcome, and the movement step the client
// predicts with. Both mirror protocol/PROTOCOL.md; the server is the source
// of truth for every value here.

const CONSTANT_NAMES = [
  'worldW',
  'worldH',
  'tickRate',
  'cellSize',
  'fadeSeconds',
  'dryPerSec',
  'dryMovingPerSec',
  'radiusBase',
  'radiusPerWetness',
  'speedMax',
  'speedMin',
  'accel',
  'maxWetness',
  'movingSpeed',
  'dropletRadius',
  'puddleRadius',
  'puddleCapacity',
  'beetleRadius',
  'blotterDryMultiplier',
  'absorbRatio',
] as const;

export type GameConstants = Readonly<Record<(typeof CONSTANT_NAMES)[number], number>>;

/** Names the positional list from Welcome. Unknown trailing entries are ignored. */
export function constantsFromList(list: readonly number[]): GameConstants {
  if (list.length < CONSTANT_NAMES.length) {
    throw new Error(`expected at least ${CONSTANT_NAMES.length} constants, got ${list.length}`);
  }
  const out = {} as Record<(typeof CONSTANT_NAMES)[number], number>;
  CONSTANT_NAMES.forEach((name, i) => {
    out[name] = list[i];
  });
  return out;
}

export interface Body {
  x: number;
  y: number;
  vx: number;
  vy: number;
  wetness: number;
}

export function radiusOf(wetness: number, c: GameConstants): number {
  return c.radiusBase + wetness * c.radiusPerWetness;
}

/** Largest magnitude of a wire input component. */
export const INPUT_MAX = 127;

/** Converts one component of a throttle vector (|v| ≤ 1) to its wire value. */
export function quantizeInput(v: number): number {
  return Math.round(Math.min(1, Math.max(-1, v)) * INPUT_MAX);
}

/** Advances `b` by one server tick under wire input `(dx, dy)`. */
export function stepBody(b: Body, dx: number, dy: number, c: GameConstants): void {
  let ix = dx / INPUT_MAX;
  let iy = dy / INPUT_MAX;
  const len = Math.hypot(ix, iy);
  if (len > 1) {
    ix /= len;
    iy /= len;
  }

  const dt = 1 / c.tickRate;
  const maxSpeed = c.speedMax - ((c.speedMax - c.speedMin) * b.wetness) / c.maxWetness;
  const k = Math.min(1, c.accel * dt);
  b.vx += (ix * maxSpeed - b.vx) * k;
  b.vy += (iy * maxSpeed - b.vy) * k;
  b.x += b.vx * dt;
  b.y += b.vy * dt;

  const r = radiusOf(b.wetness, c);
  if (b.x < r) {
    b.x = r;
    b.vx = 0;
  } else if (b.x > c.worldW - r) {
    b.x = c.worldW - r;
    b.vx = 0;
  }
  if (b.y < r) {
    b.y = r;
    b.vy = 0;
  } else if (b.y > c.worldH - r) {
    b.y = c.worldH - r;
    b.vy = 0;
  }
}
