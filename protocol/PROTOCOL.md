# Wet Paint wire protocol — version 2

This document is the contract between the Go server (`server/internal/protocol`)
and the TypeScript client (`client/src/protocol.ts`). Both sides implement it by
hand; change this file first, then both implementations, and bump `version`.

## Transport

- One WebSocket per player: `GET /ws[?page=N]`. Binary frames only, one message
  per frame.
- `GET /join` (HTTP, JSON) returns `{"page": 3, "ws": "/ws?page=3"}`. `ws` is
  either a path on the same origin or an absolute `ws(s)://` URL when the page
  lives on another instance. Clients should call `/join` first; connecting to
  `/ws` without `page` is also valid and lets the instance pick a page.
- The server sends `Welcome` immediately on connect. Until the client sends
  `Join` it is a **spectator**: it receives the whole page un-culled, so the
  join screen can show the live game.
- A connection that sends nothing for 3 s is closed and its drop removed.
  Clients send `Ping` once per second in addition to inputs.
- More than 60 client messages per second closes the connection.
- A page holds at most 60 **players** (connections that have sent `Join`,
  alive or dead) and, separately, at most 20 spectators. `/join` and `/ws`
  only place a connection on a page with room for both a spectator and a
  player.
- The server closes with an application close code when the client should
  act on it:

  | Code | Meaning | Client should |
  |------|---------|---------------|
  | 4001 | page full: `Join` arrived when the page already had 60 players | call `/join` again at once and re-send `Join` on the new page |
  | 4002 | idle: never joined, or dead, for 3 minutes | not reconnect until the user interacts |

## Encoding

- Little-endian everywhere.
- `u8 u16 u32 i8 f32` are the obvious fixed-width types.
- `pos` is a `u16`: world units × 16, clamped to `[0, 65535]` (the page is
  4000 × 3000 units, so 1/16-unit precision fits).
- `rgb` is three `u8`.
- `str` is `u8 length` + that many UTF-8 bytes.
- Every message starts with `u8 type`.
- Player IDs are `u16`, never 0 (0 means "nobody"). An ID belongs to a
  connection and is stable across respawns.

## Client → server

| Type | Name    | Payload |
|------|---------|---------|
| 0x01 | Join    | `str name`, `u8 color` |
| 0x02 | Input   | `u32 seq`, `i8 dx`, `i8 dy` |
| 0x03 | Respawn | `u8 color` |
| 0x04 | Ping    | `u32 clientTime` |
| 0x05 | View    | `u16 w`, `u16 h` |
| 0x06 | Resync  | *(none)* |

- **Join** spawns the drop. `name` is at most 16 characters after server-side
  sanitising (the server's version comes back in the `Spawn` event). `color` is
  a palette index, or 255 for random. Ignored while already alive.
- **Input** is the desired movement: `(dx, dy) / 127` is a vector whose length
  (clamped to 1) is the throttle. Pointer/touch control is converted by the
  client into this vector (direction to the pointer, throttle easing to 0 as the
  drop arrives). `seq` increases by 1 per message. The server applies the most
  recent input each tick.
- **Respawn** is Join after death, keeping the name. 255 keeps the old colour.
- **Ping** is echoed as `Pong`.
- **Resync** asks for a fresh `Grid` after a `GridCheck` mismatch. The server
  honours at most one per 10 s per connection and ignores the rest.
- **View** is the client's viewport size in world units; the server culls
  entities to it (plus a margin). Default 1600 × 1000, clamped to 4000 × 3000.

## Server → client

| Type | Name        | Delivery |
|------|-------------|----------|
| 0x81 | Welcome     | reliable, once |
| 0x82 | State       | droppable, every tick |
| 0x83 | Events      | reliable, every tick that has any |
| 0x84 | Dead        | reliable |
| 0x85 | Leaderboard | droppable, 1 Hz |
| 0x86 | Pong        | droppable |
| 0x87 | GridCheck   | droppable, every 5 s |
| 0x88 | Grid        | reliable, on `Resync` |

All messages to one client are delivered in the order the page produced them,
whether droppable or not.

"Droppable" means the server may discard the message for a slow client (a newer
one replaces it). Reliable messages are never dropped; a client that cannot
keep up with them is disconnected, because its paint grid would desync.

### 0x81 Welcome

```
u16  version            (= 2)
u32  page               sketchbook page number, from 1
u16  yourID
u32  tick               server tick of this snapshot
u8   nConstants ; f32 × n          see "Constants"
u8   nPalette   ; rgb × n
u8   nBlotters  ; { f32 x, f32 y, f32 w, f32 h } × n     top-left + size
u8   nRoster    ; { u16 id, str name } × n               living drops
u16  nDroplets  ; { u16 id, pos x, pos y } × n
u8   nBlooms    ; { pos x, pos y, u32 tick, str name } × n    name labels
u8   hasBrush   ; Brush if 1                             see event 5
u16  gridW ; u16 gridH
u32  zlen  ; u8 × zlen             zlib stream, see "Paint grid"
```

### 0x82 State

```
u32  tick
u32  ackSeq             seq of the last Input applied to your drop
pos  cloudX ; pos cloudY
u8   flags              bit 0: you are alive
if alive:  f32 x, f32 y, f32 vx, f32 vy, f32 wetness, rgb color
u8   nDrops   ; { u16 id, pos x, pos y, u8 wetness×2, rgb color } × n
u8   nBeetles ; { u8 id, pos x, pos y } × n
u8   nPuddles ; { u8 id, pos x, pos y, u8 remaining } × n
```

`nDrops` excludes your own drop. Drops and beetles are culled to your viewport
(spectators and dead players get everything). A drop missing from a State is
out of view or gone; clients drop it from interpolation.

### 0x83 Events

```
u32  tick
u16  nPaint  ; PaintOp × n
u8   nEvents ; { u8 kind, payload } × n
```

`PaintOp` (9 bytes): `pos x, pos y, u8 radius×2, rgb color, u8 strength`.
`strength == 0` erases. Ops are applied in order with `now = tick`; see
"Paint grid".

| Kind | Name         | Payload |
|------|--------------|---------|
| 1 | Spawn        | `u16 id`, `str name` |
| 2 | Death        | `u16 id`, `u8 cause`, `u16 killer`, `pos x`, `pos y`, `rgb color` |
| 3 | DropletSpawn | `u16 id`, `pos x`, `pos y` |
| 4 | DropletGone  | `u16 id`, `u16 by` (0 = evaporated) |
| 5 | BrushWarn    | Brush |
| 6 | Mix          | `u16 a`, `u16 b` |
| 7 | BeetleBump   | `u16 player`, `u8 beetle` |

- Death causes: 0 dried out, 1 absorbed (`killer` set), 2 soaked up by the
  brush, 3 left (no bloom, no label). The bloom itself arrives as paint ops;
  the client adds the pencilled name label at `(x, y)`.
- `Brush`: `f32 x0, y0, x1, y1` (centre line, may start/end off the page),
  `f32 halfWidth`, `u32 startTick`, `u16 durationTicks`, `rgb color`. The warn
  is sent 2 s before `startTick`. The brush head is at
  `lerp(p0, p1, (tick − startTick) / durationTicks)`.
- Mix fires once per pair when the +5 wetness bonus is granted.

### 0x84 Dead (only to the player who died)

`u8 cause`, `u16 killer`, `f32 secondsAlive`, `u32 cellsPainted`, `u16 colorsMixed`

### 0x85 Leaderboard

```
u8 nBoards (= 9) ; { u8 n ; { str name, u32 value, rgb color } × n } × nBoards
```

Board order: live painted, live longest, live mixed; page-last-hour painted,
longest, mixed; global painted, longest, mixed. "Longest" values are whole
seconds. Global boards are empty unless Valkey is enabled.

### 0x86 Pong

`u32 clientTime` (echo), `u32 tick`

### 0x87 GridCheck

`u32 tick`, `u32 crc`

`crc` is the checksum of the page's paint grid after every paint op up to and
including `tick` (see "Grid checksum"). Because delivery is ordered, a client
that receives it has applied exactly those ops. On a mismatch the client sends
`Resync` and ignores further `GridCheck`s until the `Grid` arrives.

### 0x88 Grid

```
u32  tick
u16  gridW ; u16 gridH
u32  zlen  ; u8 × zlen             zlib stream, as in Welcome
```

Replaces the client's whole grid with the snapshot taken at `tick`. Paint ops
in later messages apply on top of it. A client that inflates asynchronously
must hold back the messages that follow until the grid is installed.

## Constants

`Welcome.constants` is a positional `f32` list. New entries are appended;
clients ignore indices they do not know.

| # | Name | Meaning |
|---|------|---------|
| 0 | worldW | page width, world units |
| 1 | worldH | page height |
| 2 | tickRate | server ticks per second |
| 3 | cellSize | paint cell size, world units |
| 4 | fadeSeconds | paint fade time |
| 5 | dryPerSec | passive drying |
| 6 | dryMovingPerSec | extra drying while moving |
| 7 | radiusBase | radius at wetness 0 |
| 8 | radiusPerWetness | radius gained per wetness point |
| 9 | speedMax | top speed at wetness 0, units/s |
| 10 | speedMin | top speed at max wetness |
| 11 | accel | velocity approach rate, 1/s |
| 12 | maxWetness | |
| 13 | movingSpeed | speed above which a drop counts as moving |
| 14 | dropletRadius | pickup radius of a droplet |
| 15 | puddleRadius | |
| 16 | puddleCapacity | wetness in a fresh puddle |
| 17 | beetleRadius | |
| 18 | blotterDryMultiplier | |
| 19 | absorbRatio | radius ratio needed to absorb |

## Movement (shared by server simulation and client prediction)

One step, `dt = 1 / tickRate`:

```
dir      = (dx, dy) / 127 ; if |dir| > 1 then dir /= |dir|
maxSpeed = speedMax − (speedMax − speedMin) · wetness / maxWetness
v       += (dir · maxSpeed − v) · min(1, accel · dt)
p       += v · dt
radius   = radiusBase + wetness · radiusPerWetness
clamp p into [radius, world − radius] per axis; zero v on a clamped axis
```

The recommended client loop is a fixed `tickRate` simulation: each step sends
one `Input` and applies it locally. On every `State`, reset to the server's
`x, y, vx, vy, wetness` and replay the stored inputs with `seq > ackSeq`.
Other entities are rendered ~100 ms in the past, interpolating between States.

## Paint grid

The page is covered by `gridW × gridH` cells (400 × 300, 10 units each). Each
cell holds `r, g, b, a` (`u8`) and `t` (`u32`, the tick it was last painted).
The server never sends cells after the snapshot; it sends **paint ops**, and
both sides run the same integer algorithm below, so the grids stay identical.

Let `F = fadeSeconds · tickRate` (fade ticks). The visible alpha of a cell is

```
eff(cell, now) = floor(a · max(0, F − (now − t)) / F)
```

### Snapshot

The zlib stream inflates to `gridW · gridH · 6` bytes, five row-major planes:
`R`, `G`, `B`, `A` (one byte per cell each), then `age` (`u16` LE per cell),
where `age = min(65535, welcome.tick − t)`. The client stores
`t = welcome.tick − age` (as a signed number; it may be negative).

Cells with `eff(cell, welcome.tick) == 0` are written as zero in all five
planes; to the paint algorithm they are indistinguishable from blank cells.

### Grid checksum

`GridCheck.crc` is the CRC-32 (IEEE 802.3, as in zlib and PNG) of the
uncompressed snapshot bytes for `GridCheck.tick`: the five planes exactly as
above, including the zeroing of cells whose `eff` is 0. The stored `t` of a
cell differs between server and client once it has been through a snapshot, so
raw cell memory must not be hashed. `protocol/paint_vectors.json` carries a
`crc` per case, taken at `effTick`.

### Applying a paint op at tick `now`

All integer maths; `/` is floor division. Wire `pos` values are already in
1/16 units, and a cell is 160 of them wide.

```
X, Y = op.x, op.y                  (1/16 units)
R    = op.radius2 · 8              (wire radius×2 → 1/16 units)
R2   = R · R
S    = op.strength
for cy in floor((Y − R) / 160) ..= floor((Y + R) / 160), clamped to the grid
for cx in floor((X − R) / 160) ..= floor((X + R) / 160), clamped to the grid
    dx = cx · 160 + 80 − X ; dy = cy · 160 + 80 − Y
    d2 = dx · dx + dy · dy
    if d2 >= R2: continue
    if S == 0: a = 0 ; continue                        (erase)
    w = min(S, 2 · S · (R2 − d2) / R2)                 (soft edge)
    if w == 0: continue
    e = eff(cell, now)
    if e == 0:
        r, g, b = op.color ; a = w
    else:
        k = min(255, w · 384 / (w + e))                (bias to the newer colour)
        for each channel c:
            c = isqrt((c² · (255 − k) + op.c² · k) / 255)
        a = min(255, e + w − e · w / 255)
    t = now
```

`isqrt` is the floor of the square root. Mixing squared channel values keeps
overlapping colours bright instead of averaging them into mud, and is
idempotent when a colour is painted over itself.
