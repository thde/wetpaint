// The client's picture of one page, built from Welcome and kept current by
// State and Events messages. It owns no rendering and no input.

import { MAX_LABELS } from './constants';
import { ServerClock, Track } from './interpolation';
import { constantsFromList, type GameConstants } from './movement';
import { PaintGrid, type DirtyListener } from './paintGrid';
import {
  DeathCause,
  POS_SCALE,
  type BloomLabel,
  type Brush,
  type Events,
  type GameEvent,
  type Point,
  type PuddleState,
  type Rect,
  type Rgb,
  type SelfState,
  type State,
  type Welcome,
} from './protocol';

export interface RemoteDrop {
  readonly id: number;
  readonly track: Track;
  wetness: number;
  color: Rgb;
  /** Tick of the last State that included this drop. */
  seen: number;
}

export interface Beetle {
  readonly id: number;
  readonly track: Track;
  seen: number;
}

export class World {
  readonly constants: GameConstants;
  readonly page: number;
  readonly myId: number;
  readonly palette: readonly Rgb[];
  readonly blotters: readonly Rect[];
  readonly grid: PaintGrid;
  readonly clock: ServerClock;
  readonly fadeTicks: number;

  /** Names of living drops, including ones outside the viewport. */
  readonly names = new Map<number, string>();
  readonly droplets = new Map<number, Point>();
  readonly drops = new Map<number, RemoteDrop>();
  readonly beetles = new Map<number, Beetle>();
  readonly cloud = new Track();
  readonly labels: BloomLabel[];
  puddles: readonly PuddleState[] = [];
  brush: Brush | null;
  /** The player's own drop as of the latest State; null while not alive. */
  self: SelfState | null = null;

  constructor(welcome: Welcome, onDirty: DirtyListener) {
    this.constants = constantsFromList(welcome.constants);
    this.page = welcome.page;
    this.myId = welcome.yourId;
    this.palette = welcome.palette;
    this.blotters = welcome.blotters;
    this.fadeTicks = Math.round(this.constants.fadeSeconds * this.constants.tickRate);
    this.grid = new PaintGrid(
      welcome.gridW,
      welcome.gridH,
      this.fadeTicks,
      Math.round(this.constants.cellSize * POS_SCALE),
      onDirty,
    );
    this.clock = new ServerClock(1000 / this.constants.tickRate);
    for (const { id, name } of welcome.roster) this.names.set(id, name);
    for (const droplet of welcome.droplets) this.droplets.set(droplet.id, droplet);
    this.labels = welcome.blooms.slice(-MAX_LABELS);
    this.brush = welcome.brush;
  }

  applyState(state: State, nowMs: number): void {
    const { tick } = state;
    this.clock.observe(tick, nowMs);
    this.cloud.push(tick, state.cloudX, state.cloudY);
    this.self = state.self;
    this.puddles = state.puddles;

    for (const d of state.drops) {
      let drop = this.drops.get(d.id);
      if (!drop) {
        drop = { id: d.id, track: new Track(), wetness: d.wetness, color: d.color, seen: tick };
        this.drops.set(d.id, drop);
      }
      drop.track.push(tick, d.x, d.y);
      drop.wetness = d.wetness;
      drop.color = d.color;
      drop.seen = tick;
    }
    // A drop missing from a State is out of view or gone.
    for (const drop of this.drops.values()) {
      if (drop.seen !== tick) this.drops.delete(drop.id);
    }

    for (const b of state.beetles) {
      let beetle = this.beetles.get(b.id);
      if (!beetle) {
        beetle = { id: b.id, track: new Track(), seen: tick };
        this.beetles.set(b.id, beetle);
      }
      beetle.track.push(tick, b.x, b.y);
      beetle.seen = tick;
    }
    for (const beetle of this.beetles.values()) {
      if (beetle.seen !== tick) this.beetles.delete(beetle.id);
    }

    if (this.brush && tick > this.brush.startTick + this.brush.durationTicks) this.brush = null;
  }

  /**
   * Applies paint and events in order. `onEvent` runs before an event changes
   * the world, so a listener can still look up the names involved.
   */
  applyEvents(msg: Events, onEvent: (event: GameEvent) => void): void {
    for (const op of msg.paint) this.grid.apply(op, msg.tick);
    for (const event of msg.events) {
      onEvent(event);
      this.applyEvent(event, msg.tick);
    }
  }

  private applyEvent(event: GameEvent, tick: number): void {
    switch (event.kind) {
      case 'spawn':
        this.names.set(event.id, event.name);
        break;
      case 'death': {
        const name = this.names.get(event.id);
        if (name !== undefined && event.cause !== DeathCause.Left) {
          this.addLabel({ x: event.x, y: event.y, tick, name });
        }
        this.names.delete(event.id);
        this.drops.delete(event.id);
        break;
      }
      case 'dropletSpawn':
        this.droplets.set(event.id, event);
        break;
      case 'dropletGone':
        this.droplets.delete(event.id);
        break;
      case 'brushWarn':
        this.brush = event.brush;
        break;
      case 'mix':
      case 'beetleBump':
        break;
    }
  }

  private addLabel(label: BloomLabel): void {
    const oldest = label.tick - this.fadeTicks;
    let expired = 0;
    while (expired < this.labels.length && this.labels[expired].tick <= oldest) expired++;
    const overflow = this.labels.length - expired + 1 - MAX_LABELS;
    this.labels.splice(0, expired + Math.max(0, overflow));
    this.labels.push(label);
  }
}
