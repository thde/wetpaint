// Wire protocol, version 3. The contract is protocol/PROTOCOL.md; this file
// only translates between bytes and plain objects.

export const PROTOCOL_VERSION = 3;

/** Colour index meaning "random" in Join and "keep" in Respawn. */
export const COLOR_AUTO = 255;
/** Wire `pos` units per world unit. */
export const POS_SCALE = 16;

export const ClientMsg = {
  Join: 0x01,
  Input: 0x02,
  Respawn: 0x03,
  Ping: 0x04,
  View: 0x05,
  Resync: 0x06,
} as const;

export const ServerMsg = {
  Welcome: 0x81,
  State: 0x82,
  Events: 0x83,
  Dead: 0x84,
  Leaderboard: 0x85,
  Pong: 0x86,
  GridCheck: 0x87,
  Grid: 0x88,
} as const;

/** Application WebSocket close codes the client acts on. */
export const CloseCode = { PageFull: 4001, Idle: 4002 } as const;

export const EventKind = {
  Spawn: 1,
  Death: 2,
  DropletSpawn: 3,
  DropletGone: 4,
  BrushWarn: 5,
  Mix: 6,
  BeetleBump: 7,
} as const;

export const DeathCause = { Dried: 0, Absorbed: 1, Brushed: 2, Left: 3 } as const;

export class ProtocolError extends Error {
  override readonly name: string = 'ProtocolError';
}

/** The server speaks a protocol version this build does not. */
export class VersionError extends ProtocolError {
  override readonly name = 'VersionError';

  constructor(readonly serverVersion: number) {
    super(`server speaks protocol ${serverVersion}, client speaks ${PROTOCOL_VERSION}`);
  }
}

export interface Rgb {
  readonly r: number;
  readonly g: number;
  readonly b: number;
}

export interface Rect {
  readonly x: number;
  readonly y: number;
  readonly w: number;
  readonly h: number;
}

export interface Point {
  readonly id: number;
  readonly x: number;
  readonly y: number;
}

export interface Brush {
  readonly x0: number;
  readonly y0: number;
  readonly x1: number;
  readonly y1: number;
  readonly halfWidth: number;
  readonly startTick: number;
  readonly durationTicks: number;
  readonly color: Rgb;
}

/** A paint stamp, kept in wire units because the grid algorithm is integer. */
export interface PaintOp {
  /** Centre, 1/16 world units. */
  readonly x: number;
  readonly y: number;
  /** Radius × 2, world units. */
  readonly radius2: number;
  readonly r: number;
  readonly g: number;
  readonly b: number;
  /** 0 erases. */
  readonly strength: number;
}

export interface RosterEntry {
  readonly id: number;
  readonly name: string;
}

export interface BloomLabel {
  readonly x: number;
  readonly y: number;
  readonly tick: number;
  readonly name: string;
}

export interface Welcome {
  readonly type: 'welcome';
  readonly page: number;
  readonly yourId: number;
  readonly tick: number;
  readonly constants: readonly number[];
  readonly palette: readonly Rgb[];
  readonly blotters: readonly Rect[];
  readonly roster: readonly RosterEntry[];
  readonly droplets: readonly Point[];
  readonly blooms: readonly BloomLabel[];
  readonly brush: Brush | null;
  readonly gridW: number;
  readonly gridH: number;
  /** zlib stream of the paint grid planes. */
  readonly gridZlib: Uint8Array<ArrayBuffer>;
}

export interface SelfState {
  readonly x: number;
  readonly y: number;
  readonly vx: number;
  readonly vy: number;
  readonly wetness: number;
  readonly color: Rgb;
}

export interface DropState {
  readonly id: number;
  readonly x: number;
  readonly y: number;
  readonly wetness: number;
  readonly color: Rgb;
}

export interface PuddleState extends Point {
  /** Wetness left in the puddle, rounded up. */
  readonly remaining: number;
}

export interface State {
  readonly type: 'state';
  readonly tick: number;
  readonly ackSeq: number;
  readonly cloudX: number;
  readonly cloudY: number;
  /** Present while you are alive. */
  readonly self: SelfState | null;
  readonly drops: readonly DropState[];
  readonly beetles: readonly Point[];
  readonly puddles: readonly PuddleState[];
}

export type GameEvent =
  | { readonly kind: 'spawn'; readonly id: number; readonly name: string }
  | {
      readonly kind: 'death';
      readonly id: number;
      readonly cause: number;
      readonly killer: number;
      readonly x: number;
      readonly y: number;
      readonly color: Rgb;
    }
  | { readonly kind: 'dropletSpawn'; readonly id: number; readonly x: number; readonly y: number }
  | { readonly kind: 'dropletGone'; readonly id: number; readonly by: number }
  | { readonly kind: 'brushWarn'; readonly brush: Brush }
  | { readonly kind: 'mix'; readonly a: number; readonly b: number }
  | { readonly kind: 'beetleBump'; readonly player: number; readonly beetle: number };

export interface Events {
  readonly type: 'events';
  readonly tick: number;
  readonly paint: readonly PaintOp[];
  readonly events: readonly GameEvent[];
}

export interface Dead {
  readonly type: 'dead';
  readonly cause: number;
  readonly killer: number;
  readonly secondsAlive: number;
  readonly cellsPainted: number;
  readonly colorsMixed: number;
}

export interface LeaderboardEntry {
  readonly name: string;
  readonly value: number;
  readonly color: Rgb;
}

export interface Leaderboard {
  readonly type: 'leaderboard';
  /** Live, page-last-hour, global; each × painted, longest, mixed. */
  readonly boards: readonly (readonly LeaderboardEntry[])[];
}

export interface Pong {
  readonly type: 'pong';
  readonly clientTime: number;
  readonly tick: number;
}

export interface GridCheck {
  readonly type: 'gridCheck';
  /** The checksum covers every paint op up to and including this tick. */
  readonly tick: number;
  readonly crc: number;
}

/** A replacement paint grid, sent in answer to Resync. */
export interface Grid {
  readonly type: 'grid';
  readonly tick: number;
  readonly gridW: number;
  readonly gridH: number;
  readonly gridZlib: Uint8Array<ArrayBuffer>;
}

export type ServerMessage =
  | Welcome
  | State
  | Events
  | Dead
  | Leaderboard
  | Pong
  | GridCheck
  | Grid;

const utf8Decoder = new TextDecoder();

class Reader {
  private readonly view: DataView;
  private off = 0;

  constructor(private readonly buf: ArrayBuffer) {
    this.view = new DataView(buf);
  }

  u8(): number {
    const v = this.view.getUint8(this.off);
    this.off += 1;
    return v;
  }

  u16(): number {
    const v = this.view.getUint16(this.off, true);
    this.off += 2;
    return v;
  }

  u32(): number {
    const v = this.view.getUint32(this.off, true);
    this.off += 4;
    return v;
  }

  f32(): number {
    const v = this.view.getFloat32(this.off, true);
    this.off += 4;
    return v;
  }

  pos(): number {
    return this.u16() / POS_SCALE;
  }

  rgb(): Rgb {
    return { r: this.u8(), g: this.u8(), b: this.u8() };
  }

  bytes(n: number): Uint8Array<ArrayBuffer> {
    const v = new Uint8Array(this.buf, this.off, n);
    this.off += n;
    return v;
  }

  str(): string {
    return utf8Decoder.decode(this.bytes(this.u8()));
  }

  list<T>(n: number, item: () => T): T[] {
    const out = new Array<T>(n);
    for (let i = 0; i < n; i++) out[i] = item();
    return out;
  }

  end(): void {
    if (this.off !== this.view.byteLength) {
      throw new ProtocolError(`${this.view.byteLength - this.off} trailing bytes`);
    }
  }
}

function readBrush(r: Reader): Brush {
  return {
    x0: r.f32(),
    y0: r.f32(),
    x1: r.f32(),
    y1: r.f32(),
    halfWidth: r.f32(),
    startTick: r.u32(),
    durationTicks: r.u16(),
    color: r.rgb(),
  };
}

function readPoint(r: Reader, idBytes: 1 | 2): Point {
  return { id: idBytes === 1 ? r.u8() : r.u16(), x: r.pos(), y: r.pos() };
}

function readWelcome(r: Reader): Welcome {
  const version = r.u16();
  if (version !== PROTOCOL_VERSION) throw new VersionError(version);
  return {
    type: 'welcome',
    page: r.u32(),
    yourId: r.u16(),
    tick: r.u32(),
    constants: r.list(r.u8(), () => r.f32()),
    palette: r.list(r.u8(), () => r.rgb()),
    blotters: r.list(r.u8(), () => ({ x: r.f32(), y: r.f32(), w: r.f32(), h: r.f32() })),
    roster: r.list(r.u8(), () => ({ id: r.u16(), name: r.str() })),
    droplets: r.list(r.u16(), () => readPoint(r, 2)),
    blooms: r.list(r.u8(), () => ({ x: r.pos(), y: r.pos(), tick: r.u32(), name: r.str() })),
    brush: r.u8() === 1 ? readBrush(r) : null,
    gridW: r.u16(),
    gridH: r.u16(),
    gridZlib: r.bytes(r.u32()),
  };
}

function readState(r: Reader): State {
  const tick = r.u32();
  const ackSeq = r.u32();
  const cloudX = r.pos();
  const cloudY = r.pos();
  const alive = (r.u8() & 1) !== 0;
  const self = alive
    ? { x: r.f32(), y: r.f32(), vx: r.f32(), vy: r.f32(), wetness: r.f32(), color: r.rgb() }
    : null;
  return {
    type: 'state',
    tick,
    ackSeq,
    cloudX,
    cloudY,
    self,
    drops: r.list(r.u8(), () => ({
      id: r.u16(),
      x: r.pos(),
      y: r.pos(),
      wetness: r.u8() / 2,
      color: r.rgb(),
    })),
    beetles: r.list(r.u8(), () => readPoint(r, 1)),
    puddles: r.list(r.u8(), () => ({ ...readPoint(r, 1), remaining: r.u8() })),
  };
}

function readPaintOp(r: Reader): PaintOp {
  return {
    x: r.u16(),
    y: r.u16(),
    radius2: r.u8(),
    r: r.u8(),
    g: r.u8(),
    b: r.u8(),
    strength: r.u8(),
  };
}

function readEvent(r: Reader): GameEvent {
  const kind = r.u8();
  switch (kind) {
    case EventKind.Spawn:
      return { kind: 'spawn', id: r.u16(), name: r.str() };
    case EventKind.Death:
      return {
        kind: 'death',
        id: r.u16(),
        cause: r.u8(),
        killer: r.u16(),
        x: r.pos(),
        y: r.pos(),
        color: r.rgb(),
      };
    case EventKind.DropletSpawn:
      return { kind: 'dropletSpawn', ...readPoint(r, 2) };
    case EventKind.DropletGone:
      return { kind: 'dropletGone', id: r.u16(), by: r.u16() };
    case EventKind.BrushWarn:
      return { kind: 'brushWarn', brush: readBrush(r) };
    case EventKind.Mix:
      return { kind: 'mix', a: r.u16(), b: r.u16() };
    case EventKind.BeetleBump:
      return { kind: 'beetleBump', player: r.u16(), beetle: r.u8() };
    default:
      // Events carry no length, so an unknown kind cannot be skipped.
      throw new ProtocolError(`unknown event kind ${kind}`);
  }
}

function readEvents(r: Reader): Events {
  return {
    type: 'events',
    tick: r.u32(),
    paint: r.list(r.u16(), () => readPaintOp(r)),
    events: r.list(r.u8(), () => readEvent(r)),
  };
}

function readDead(r: Reader): Dead {
  return {
    type: 'dead',
    cause: r.u8(),
    killer: r.u16(),
    secondsAlive: r.f32(),
    cellsPainted: r.u32(),
    colorsMixed: r.u16(),
  };
}

function readLeaderboard(r: Reader): Leaderboard {
  return {
    type: 'leaderboard',
    boards: r.list(r.u8(), () =>
      r.list(r.u8(), () => ({ name: r.str(), value: r.u32(), color: r.rgb() })),
    ),
  };
}

function readMessage(r: Reader): ServerMessage {
  const type = r.u8();
  switch (type) {
    case ServerMsg.Welcome:
      return readWelcome(r);
    case ServerMsg.State:
      return readState(r);
    case ServerMsg.Events:
      return readEvents(r);
    case ServerMsg.Dead:
      return readDead(r);
    case ServerMsg.Leaderboard:
      return readLeaderboard(r);
    case ServerMsg.Pong:
      return { type: 'pong', clientTime: r.u32(), tick: r.u32() };
    case ServerMsg.GridCheck:
      return { type: 'gridCheck', tick: r.u32(), crc: r.u32() };
    case ServerMsg.Grid:
      return {
        type: 'grid',
        tick: r.u32(),
        gridW: r.u16(),
        gridH: r.u16(),
        gridZlib: r.bytes(r.u32()),
      };
    default:
      throw new ProtocolError(`unknown message type 0x${type.toString(16)}`);
  }
}

/** Decodes one binary frame. Throws ProtocolError on anything malformed. */
export function decodeServerMessage(buf: ArrayBuffer): ServerMessage {
  const r = new Reader(buf);
  try {
    const msg = readMessage(r);
    r.end();
    return msg;
  } catch (err) {
    if (err instanceof RangeError) throw new ProtocolError('truncated message', { cause: err });
    throw err;
  }
}

/** The server derives the name from `seed`. */
export function encodeJoin(seed: number, color: number): ArrayBuffer {
  const v = new DataView(new ArrayBuffer(6));
  v.setUint8(0, ClientMsg.Join);
  v.setUint32(1, seed >>> 0, true);
  v.setUint8(5, color);
  return v.buffer;
}

/** `dx`, `dy` are already quantised to [-127, 127]. */
export function encodeInput(seq: number, dx: number, dy: number): ArrayBuffer {
  const v = new DataView(new ArrayBuffer(7));
  v.setUint8(0, ClientMsg.Input);
  v.setUint32(1, seq, true);
  v.setInt8(5, dx);
  v.setInt8(6, dy);
  return v.buffer;
}

export function encodeRespawn(color: number): ArrayBuffer {
  return Uint8Array.of(ClientMsg.Respawn, color).buffer;
}

export function encodePing(clientTime: number): ArrayBuffer {
  const v = new DataView(new ArrayBuffer(5));
  v.setUint8(0, ClientMsg.Ping);
  v.setUint32(1, clientTime >>> 0, true);
  return v.buffer;
}

/** Asks for a fresh Grid after a GridCheck mismatch. */
export function encodeResync(): ArrayBuffer {
  return Uint8Array.of(ClientMsg.Resync).buffer;
}

/** Viewport size in world units. */
export function encodeView(w: number, h: number): ArrayBuffer {
  const v = new DataView(new ArrayBuffer(5));
  v.setUint8(0, ClientMsg.View);
  v.setUint16(1, Math.min(65535, Math.max(0, Math.round(w))), true);
  v.setUint16(3, Math.min(65535, Math.max(0, Math.round(h))), true);
  return v.buffer;
}
