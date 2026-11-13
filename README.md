# wetpaint

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/banner/banner-1-wash-dark.svg">
    <img src="docs/banner/banner-1-wash.svg" alt="Wet Paint" width="100%">
  </picture>
</p>

A drop-in multiplayer watercolour game. Every player is one drop of paint on a
huge shared sheet of paper. You dry out in about a minute, leave a trail while
you live and a bloom with your name when you go.

- `server/` — authoritative Go server (simulation, WebSockets, bots)
- `client/` — TypeScript + Canvas 2D client, bundled with Vite
- `protocol/` — the wire spec ([PROTOCOL.md](protocol/PROTOCOL.md)) and the
  shared paint test vectors both sides are tested against

## Running it

### Simplest: no Docker, no Valkey

Needs Go 1.27+ and Bun 1.4+.

```sh
cd client && bun install && bun run build
cd ../server && go run ./cmd/server -static ../client/dist
# play at http://localhost:8080
```

For client work with hot reload, run the server without `-static` and the Vite
dev server next to it (it proxies `/join` and `/ws` to `VITE_SERVER`, default
`http://localhost:8080`):

```sh
cd server && go run ./cmd/server
cd client && bun run dev        # http://localhost:5173
```

### Docker Compose (server + Vite + Valkey)

```sh
docker compose up --build
# http://localhost:5173 (hot reload) or http://localhost:8080 (built client)
```

Valkey is switched on by the `-valkey valkey:6379` argument in
`docker-compose.yml`; remove it to run the same stack on the in-memory store.

### Bots

```sh
cd server && go run ./cmd/bots -n 30 -url ws://localhost:8080/ws
```

Bots wander, seek water, sometimes chase each other, and respawn. Above 60 they
spill onto further pages. Without `-url` they connect to `$WETPAINT_URL`, or
`ws://localhost:8080/ws` if it is unset. Flags: `-ramp` (connect spread),
`-report` (stats interval), `-lazy` (never seek water, for measuring an
unrefilled life).

### Tests

```sh
cd server && go test -race ./...
cd client && bun run test && bun run typecheck
```

`go test ./internal/paint -update` regenerates `protocol/paint_vectors.json`;
the client's tests replay the same file, which is what keeps the two paint
implementations bit-identical.

## Server flags and endpoints

| Flag          | Default                | Meaning                                                                                                            |
| ------------- | ---------------------- | ------------------------------------------------------------------------------------------------------------------ |
| `-addr`       | `:$PORT`, else `:8080` | HTTP listen address                                                                                                |
| `-static`     | _(off)_                | directory with the built client, served at `/`                                                                     |
| `-valkey`     | _(off)_                | Valkey `host:port`; empty means a Deploio Key-Value Store if one is attached (see below), else the in-memory store |
| `-public-url` | _(none)_               | this instance's public WebSocket URL, e.g. `wss://eu1.example.com/ws` (multi-instance routing)                     |
| `-origins`    | localhost, 127.0.0.1   | extra host patterns allowed as WebSocket `Origin`, besides same-origin                                             |

| Endpoint           |                                                                                                                                                                                                           |
| ------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET /join`        | JSON: which page to join and the WebSocket URL for it                                                                                                                                                     |
| `GET /ws[?page=N]` | the game socket                                                                                                                                                                                           |
| `GET /healthz`     | liveness                                                                                                                                                                                                  |
| `GET /metrics`     | Prometheus: `wetpaint_players{page}`, `wetpaint_spectators{page}`, `wetpaint_tick_duration_seconds`, `wetpaint_bytes_sent_total`, `wetpaint_grid_resyncs_total`, dropped-message and persistence counters |

## Architecture

```
browser ── GET /join ──▶ hub ──▶ store (page directory)
        ── WebSocket ──▶ client reader ─┐ inputs (channel)
                                         ▼
                              page goroutine: game.World.Step @ 20 Hz
                                         │ encoded frames
        ◀── client writer ◀── bounded outbound queue (stale State dropped)
                                         │ jobs (buffered channel, dropped on overflow)
                                         ▼
                              persister goroutine ──▶ store (Valkey or memory)
```

**Server** (`server/internal`)

- `game` — the simulation. Pure: no clocks, goroutines or I/O; random source
  injected. `constants.go` is the single source of truth for tuning.
- `paint` — the integer paint grid (flat `[]uint8` RGBA + `[]uint32` ticks),
  snapshot planes and zlib.
- `protocol` — encode/decode of every message.
- `server` — hub (pages, `/join`), one goroutine per page that exclusively owns
  its `World` (no mutexes on game state), a reader and a writer goroutine per
  connection, metrics, and the background persister.
- `store` — `Store` interface with `Memory` and `Valkey` implementations: page
  directory (heartbeat + TTL), leaderboards (sorted sets: page-last-hour and
  global), grid snapshots (every 30 s, restored on start).
- `names` — name sanitising.

The tick loop never touches the store. Slow clients lose droppable messages
(State, Leaderboard, Pong) rather than stalling the page; a client that cannot
keep up with reliable messages (paint ops) is disconnected, because its
painting would otherwise desync.

**Client** (`client/src`)

- `net`, `protocol` — transport with reconnect, and the codec.
- `paintGrid` — the same integer paint algorithm as the server.
- `prediction`, `interpolation`, `movement` — fixed-tick prediction of your own
  drop with input replay on every State; other entities drawn ~100 ms in the
  past.
- `render/` — `paintLayer` shades the grid into watercolour on an offscreen
  canvas in 8×8-cell tiles, re-rendering only dirty tiles within a per-frame
  budget plus a slow rolling refresh that shows fading. The rest draws the
  pencil doodles, mini-map and postcard.
- `ui/`, `audio` — hand-drawn DOM cards and HUD; Web Audio synthesised sounds.

## Protocol

[protocol/PROTOCOL.md](protocol/PROTOCOL.md) specifies every message and its
little-endian layout. The points worth knowing before reading it:

- The server sends the paint grid once (zlib snapshot in `Welcome`). After that
  it sends **paint ops** — stamps and erases — not cells. Both sides apply them
  with the same integer maths, so the grids stay identical at a fraction of the
  bandwidth of cell deltas. The price is that the algorithm is part of the
  wire contract: change it in the spec, both implementations and the vectors
  together.
- Every 5 s the server sends a checksum of its grid (`GridCheck`). A client
  whose grid disagrees asks for a fresh one (`Resync` → `Grid`), so a
  divergence heals itself instead of lasting until reload. A rising
  `wetpaint_grid_resyncs_total` means the two paint implementations disagree.
- A page holds 60 players and, separately, 20 spectators. Joining a page that
  filled up in the meantime closes with code 4001 and the client moves on;
  connections that never join, or stay dead for 3 minutes, close with 4002 and
  come back when the user interacts.
- `Welcome` carries the gameplay constants and the palette, so the client has
  no copy of them to drift.
- Until a connection sends `Join` it is a spectator and receives the page
  un-culled; that is the live game behind the join card.

## Tuning

Gameplay: `server/internal/game/constants.go` — drying rates, radius and speed
curves, water, bleed and absorb rules, hazard timings, bloom size, culling
margins. Restart the server; clients pick the values up from `Welcome`.

Look and feel only: `client/src/constants.ts` — interpolation delay, camera,
pointer throttle, and the `PAINT` block that shapes the watercolour (edge
softness, pooling, granulation, in-stroke variation).

## Deploying

### Deploio

The app is built from the `Dockerfile`. The image contains the server, which
also serves the built client, and the `wetpaint-bots` binary. Replace the Git
URL with your fork:

```sh
nctl create project wetpaint --wait
nctl create kvs wetpaint --project wetpaint --wait
nctl create app wetpaint --project wetpaint \
  --git-url https://github.com/<you>/wetpaint.git \
  --build-type dockerfile \
  --port 8080 \
  --replicas 1 \
  --size standard-1 \
  --health-probe-path /healthz \
  --service cache=keyvaluestore/wetpaint \
  --wait

nctl get app wetpaint --project wetpaint   # shows the generated *.deploio.app host
```

Bots run as a worker job from the same image. Worker jobs share the app's
environment, and the bots connect to `$WETPAINT_URL`. Deploio doesn't inject
the app's URL, so set it to the public WebSocket URL:

```sh
nctl update app wetpaint --project wetpaint \
  --env WETPAINT_URL=wss://<host>/ws \
  --worker-job-name bots \
  --worker-job-command "/usr/local/bin/wetpaint-bots -n 30" \
  --worker-job-size mini
```

To remove them: `nctl update app wetpaint --project wetpaint --delete-worker-job bots`.

Notes:

- **Keep `--replicas 1`.** A page lives on one instance, and `/join` and `/ws`
  go through Deploio's load balancer separately. With two replicas, a `/ws`
  that lands on the replica that doesn't own the page opens a new page there,
  so players get split across half-empty pages.
- **Sizing.** CPU is the limit (memory stays under 100 MB). A full 60-player
  page needs roughly 0.3–0.4 CPU, so `standard-1` (0.5 CPU) holds about one
  page; scale up to `standard-2` or `standard-4` for more concurrent players,
  since only one replica is possible. The bots need about 0.15–0.2 CPU at
  `-n 30`, more than `micro` (0.125) allows. Watch
  `wetpaint_tick_duration_seconds`: a p99 near 50 ms means the app is too small.
- The server listens on `$PORT`, and without `-valkey` it uses the attached Key-Value
  Store: it reads the `NINE_KVS_<NAME>_FQDN`, `_PORT`, `_USER`, `_PASSWORD`
  and (if present) `_CA_CERT` variables Deploio injects and connects over TLS.
  Attach exactly one store. With several attached, the server refuses to start
  rather than guess.
- Same-origin WebSockets are always allowed, so the generated host and any
  `--hosts` work without `-origins`.

### Elsewhere

Build the image (`docker build -t wetpaint .`) and run it behind any reverse
proxy that passes WebSocket upgrades and keeps idle connections open for a
long time:

```
wetpaint -addr :8080 -static /static -origins play.example.com
```

Without Valkey the painting and the hourly boards live in memory and are lost on
restart. Add `-valkey host:6379` to keep them across deploys.

**Several instances.** Give every instance the same Valkey and its own
`-public-url` (each must be reachable directly by browsers, e.g.
`wss://eu1.example.com/ws`). Each instance heartbeats its pages into the
directory; `/join` on any instance returns the WebSocket URL of a page with
room, wherever it lives, so `/join` and the static files can sit behind an
ordinary load balancer. A page lives on exactly one instance. Directory entries expire 10 s after
an instance stops heartbeating, and a page number that is claimed again starts
from its saved snapshot (taken every 30 s, kept for an hour). Routing between
instances is covered by unit tests only; it has not been run with two live
instances.

## Not included

- Postgres (all-time hall of fame, postcard archive). The store interface is
  where it would go; writes would ride the existing persister goroutine.
