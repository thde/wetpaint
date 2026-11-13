// Everything that is not the player's own drop is drawn slightly in the past,
// interpolating between the server States either side of the render time.

export interface Vec2 {
  x: number;
  y: number;
}

/** Maps local time to (fractional) server ticks, smoothing network jitter. */
export class ServerClock {
  private offsetMs = Number.NaN;

  constructor(private readonly tickMs: number) {}

  /** Records that the State for `tick` arrived at local time `nowMs`. */
  observe(tick: number, nowMs: number): void {
    const estimate = tick * this.tickMs - nowMs;
    if (Number.isNaN(this.offsetMs) || Math.abs(estimate - this.offsetMs) > RESYNC_MS) {
      this.offsetMs = estimate;
    } else {
      this.offsetMs += (estimate - this.offsetMs) * SMOOTHING;
    }
  }

  tickAt(nowMs: number): number {
    return Number.isNaN(this.offsetMs) ? 0 : (nowMs + this.offsetMs) / this.tickMs;
  }
}

const RESYNC_MS = 250;
const SMOOTHING = 0.1;
const SAMPLES = 4;

/** The last few timestamped positions of one entity. */
export class Track {
  private readonly ticks = new Float64Array(SAMPLES);
  private readonly xs = new Float64Array(SAMPLES);
  private readonly ys = new Float64Array(SAMPLES);
  private count = 0;

  push(tick: number, x: number, y: number): void {
    if (this.count === SAMPLES) {
      this.ticks.copyWithin(0, 1);
      this.xs.copyWithin(0, 1);
      this.ys.copyWithin(0, 1);
      this.count -= 1;
    }
    this.ticks[this.count] = tick;
    this.xs[this.count] = x;
    this.ys[this.count] = y;
    this.count += 1;
  }

  /**
   * Writes the position at `tick` into `out`. Clamps to the oldest and newest
   * samples rather than extrapolating.
   */
  sample(tick: number, out: Vec2): void {
    const last = this.count - 1;
    if (last < 0) return;
    let i = last;
    while (i > 0 && this.ticks[i] > tick) i--;
    if (i === last || this.ticks[i] > tick) {
      out.x = this.xs[i];
      out.y = this.ys[i];
      return;
    }
    const f = (tick - this.ticks[i]) / (this.ticks[i + 1] - this.ticks[i]);
    out.x = this.xs[i] + (this.xs[i + 1] - this.xs[i]) * f;
    out.y = this.ys[i] + (this.ys[i + 1] - this.ys[i]) * f;
  }
}
