// Client-side prediction for the player's own drop: a fixed-tick simulation
// that applies inputs immediately and, on every server State, rewinds to the
// authoritative body and replays the inputs the server has not yet applied.

import { stepBody, type Body, type GameConstants } from './movement';

interface PendingInput {
  readonly seq: number;
  readonly dx: number;
  readonly dy: number;
}

/** Unacknowledged inputs kept for replay; ~3 s at 20 Hz. */
const MAX_PENDING = 64;
/** A correction larger than this (world units) is shown as a jump, not eased. */
const SNAP_DISTANCE = 120;
/** Visual error half-life, seconds. */
const ERROR_HALF_LIFE = 0.08;

function copyBody(to: Body, from: Readonly<Body>): void {
  to.x = from.x;
  to.y = from.y;
  to.vx = from.vx;
  to.vy = from.vy;
  to.wetness = from.wetness;
}

export class Predictor {
  /** Predicted body after the latest local tick. */
  readonly body: Body = { x: 0, y: 0, vx: 0, vy: 0, wetness: 0 };

  private prevX = 0;
  private prevY = 0;
  // Offset between where the drop was being drawn and where prediction now
  // says it is; decays to zero so corrections glide instead of popping.
  private errX = 0;
  private errY = 0;
  private pending: PendingInput[] = [];
  private seq = 0;

  constructor(private readonly constants: GameConstants) {}

  /** Starts a life at the server's body. Input sequence numbers carry on. */
  reset(server: Readonly<Body>): void {
    copyBody(this.body, server);
    this.prevX = server.x;
    this.prevY = server.y;
    this.errX = 0;
    this.errY = 0;
    this.pending.length = 0;
  }

  /** Applies one tick of input locally and returns the `seq` to send with it. */
  advance(dx: number, dy: number): number {
    this.seq += 1;
    this.pending.push({ seq: this.seq, dx, dy });
    if (this.pending.length > MAX_PENDING) this.pending.shift();
    this.prevX = this.body.x;
    this.prevY = this.body.y;
    stepBody(this.body, dx, dy, this.constants);
    return this.seq;
  }

  /** Rewinds to the server's body as of `ackSeq` and replays newer inputs. */
  reconcile(server: Readonly<Body>, ackSeq: number): void {
    const oldX = this.body.x;
    const oldY = this.body.y;

    copyBody(this.body, server);
    this.pending = this.pending.filter((input) => input.seq > ackSeq);
    for (const input of this.pending) {
      stepBody(this.body, input.dx, input.dy, this.constants);
    }

    const shiftX = this.body.x - oldX;
    const shiftY = this.body.y - oldY;
    this.prevX += shiftX;
    this.prevY += shiftY;
    this.errX -= shiftX;
    this.errY -= shiftY;
    if (Math.hypot(this.errX, this.errY) > SNAP_DISTANCE) {
      this.errX = 0;
      this.errY = 0;
    }
  }

  /** Eases the visual correction; call once per rendered frame. */
  settle(dtSeconds: number): void {
    const keep = Math.pow(0.5, dtSeconds / ERROR_HALF_LIFE);
    this.errX *= keep;
    this.errY *= keep;
  }

  /** Drawn position, `alpha` ∈ [0, 1] of the way through the current tick. */
  renderX(alpha: number): number {
    return this.prevX + (this.body.x - this.prevX) * alpha + this.errX;
  }

  renderY(alpha: number): number {
    return this.prevY + (this.body.y - this.prevY) * alpha + this.errY;
  }
}
