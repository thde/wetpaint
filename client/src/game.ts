// The conductor: owns the connection, the world, prediction, and the frame
// loop, and wires them to the renderer, the UI and the sound effects.

import { Sfx } from './audio';
import { PAINT } from './constants';
import { inflate } from './inflate';
import { GridSync } from './gridSync';
import { Input } from './input';
import type { Vec2 } from './interpolation';
import { nameSeed } from './nameSeed';
import { Connection, type CloseReason } from './net';
import { Predictor } from './prediction';
import {
  COLOR_AUTO,
  DeathCause,
  VersionError,
  decodeServerMessage,
  encodeInput,
  encodeJoin,
  encodeRespawn,
  encodeResync,
  encodeView,
  type Brush,
  type Dead,
  type GameEvent,
  type Grid,
  type Rgb,
  type ServerMessage,
  type Welcome,
} from './protocol';
import { Camera } from './render/camera';
import { LABEL_FONT, NAME_FONT } from './render/entities';
import { Minimap } from './render/minimap';
import { PaintLayer } from './render/paintLayer';
import { createGrain } from './render/paper';
import { downloadPostcard } from './render/postcard';
import { Renderer } from './render/renderer';
import { DeathCard } from './ui/deathCard';
import { Hud } from './ui/hud';
import { JoinCard } from './ui/joinCard';
import { LeaderboardPanel } from './ui/leaderboard';
import { World } from './world';

/** Everything that exists only while we are on a page. */
interface Session {
  readonly world: World;
  readonly paint: PaintLayer;
  readonly predictor: Predictor;
  readonly sync: GridSync;
  /** Whether Join has been sent on this connection (Respawn needs it). */
  joined: boolean;
  /** Whether the predictor is running a life. */
  alive: boolean;
  /** The brush whose swoosh has already been played. */
  heardBrush: Brush | null;
}

/** Longest frame gap that is simulated; beyond it the game just skips ahead. */
const MAX_FRAME_SECONDS = 0.1;
const VIEW_DEBOUNCE_MS = 150;

export class Game {
  private readonly camera = new Camera();
  private readonly grain = createGrain();
  private readonly renderer: Renderer;
  private readonly input: Input;
  private readonly sfx = new Sfx();
  private readonly conn: Connection;
  private readonly joinCard: JoinCard;
  private readonly deathCard: DeathCard;
  private readonly board = new LeaderboardPanel();
  private readonly hud: Hud;
  private readonly minimap: Minimap;

  private session: Session | null = null;
  /** Frames held back while a paint snapshot inflates; see `holdWhile`. */
  private backlog: ArrayBuffer[] | null = null;
  /** Bumped per socket so a stale Welcome cannot install itself. */
  private epoch = 0;

  /** Set once the player has dropped in; survives reconnects. */
  private player: { seed: number; color: number } | null = null;
  private onDeathCard = false;

  private lastFrameMs = 0;
  private tickDebtMs = 0;
  private viewTimer: number | undefined;
  private readonly sprite = { x: 0, y: 0, wetness: 0, color: { r: 0, g: 0, b: 0 } as Rgb, name: '' };
  private readonly cloudAt: Vec2 = { x: 0, y: 0 };

  constructor(canvas: HTMLCanvasElement, ui: HTMLElement) {
    this.renderer = new Renderer(canvas, this.camera, this.grain);
    this.input = new Input(canvas, this.camera);
    this.joinCard = new JoinCard((color) => this.dropIn(color));
    this.deathCard = new DeathCard((color) => this.dripAgain(color));
    this.hud = new Hud(this.sfx.muted, {
      toggleMute: () => this.sfx.toggleMuted(),
      postcard: () => this.postcard(),
    });
    this.minimap = new Minimap(this.hud.minimap);
    ui.append(this.hud.root, this.board.root, this.deathCard.root, this.joinCard.root);
    this.setSpectating(true);
    this.joinCard.setReady(false);

    this.conn = new Connection({
      onOpen: () => {
        this.epoch += 1;
        this.backlog = null;
      },
      onMessage: (frame) => this.receive(frame),
      onClose: (why) => this.disconnected(why),
    });

    window.addEventListener('resize', () => this.resize());
    this.resize();
    // Canvas text falls back until the handwriting fonts arrive; ask early.
    void document.fonts.load(NAME_FONT);
    void document.fonts.load(LABEL_FONT);
  }

  start(): void {
    this.conn.start();
    requestAnimationFrame(this.frame);
  }

  // --- player actions -----------------------------------------------------

  private dropIn(color: number): void {
    if (!this.session) return;
    this.sfx.unlock();
    this.player = { seed: nameSeed(), color };
    this.setSpectating(false);
    this.join(this.session);
  }

  private dripAgain(color: number): void {
    const session = this.session;
    if (!session || !this.player) return;
    this.deathCard.hide();
    this.onDeathCard = false;
    if (color !== COLOR_AUTO) this.player.color = color;
    if (session.joined) this.conn.send(encodeRespawn(color));
    else this.join(session);
  }

  private join(session: Session): void {
    if (!this.player) return;
    this.conn.send(encodeJoin(this.player.seed, this.player.color));
    session.joined = true;
  }

  private postcard(): void {
    if (this.session) {
      downloadPostcard(this.session.world, this.session.paint, this.grain, performance.now());
    }
  }

  private setSpectating(spectating: boolean): void {
    this.joinCard.setVisible(spectating);
    document.body.classList.toggle('spectating', spectating);
  }

  // --- network ------------------------------------------------------------

  private receive(frame: ArrayBuffer): void {
    if (this.backlog) {
      this.backlog.push(frame);
      return;
    }
    let msg: ServerMessage;
    try {
      msg = decodeServerMessage(frame);
    } catch (err) {
      if (err instanceof VersionError) {
        // Reconnecting cannot help: this build is older than the server.
        this.conn.park();
        this.joinCard.setReady(false, 'please refresh');
        this.hud.setStatus('the sketchbook has changed — please refresh the page');
        return;
      }
      // Paint ops may have been lost with this frame, so start over.
      console.error('dropping connection:', err);
      this.conn.restart();
      return;
    }
    if (msg.type === 'welcome') {
      this.welcome(msg);
      return;
    }
    const session = this.session;
    if (!session) return;
    switch (msg.type) {
      case 'state': {
        session.world.applyState(msg, performance.now());
        if (!msg.self) {
          session.alive = false;
        } else if (session.alive) {
          session.predictor.reconcile(msg.self, msg.ackSeq);
        } else {
          session.predictor.reset(msg.self);
          session.alive = true;
          this.tickDebtMs = 0;
          this.input.rest();
        }
        break;
      }
      case 'events':
        session.world.applyEvents(msg, (event) => this.hear(event, session.world));
        break;
      case 'dead':
        this.died(msg, session.world);
        break;
      case 'leaderboard':
        this.board.set(msg.boards);
        break;
      case 'gridCheck':
        // Reached in stream order, so every earlier paint op is applied.
        if (session.sync.check(session.world.grid, msg, performance.now())) {
          console.warn(`paint grid out of step at tick ${msg.tick}; asking for a fresh one`);
          this.conn.send(encodeResync());
        }
        break;
      case 'grid':
        this.regrid(session, msg);
        break;
      case 'pong':
        break;
    }
  }

  private disconnected(why: CloseReason): void {
    this.joinCard.setReady(false);
    // Without States there is nothing to reconcile against: hold still.
    if (this.session) this.session.alive = false;
    switch (why) {
      case 'lost':
        if (this.session) this.hud.setStatus('lost the page… finding it again');
        break;
      case 'full':
        this.hud.setStatus('that page just filled up… turning to the next');
        break;
      case 'idle': {
        this.joinCard.setReady(false, 'tap to come back');
        this.hud.setStatus('still there? tap to come back');
        const woken = new AbortController();
        const wake = (): void => {
          woken.abort();
          this.joinCard.setReady(false);
          this.hud.setStatus('finding the page again…');
          this.conn.resume();
        };
        window.addEventListener('pointerdown', wake, { signal: woken.signal });
        window.addEventListener('keydown', wake, { signal: woken.signal });
        break;
      }
    }
  }

  /**
   * Runs `prepare` (which may await) and then the commit it returns, holding
   * back every frame that arrives in between and replaying them afterwards, so
   * the stream is still applied in order. Abandoned if the socket changes.
   */
  private async holdWhile(prepare: () => Promise<() => void>): Promise<void> {
    const epoch = this.epoch;
    this.backlog = [];
    let commit: () => void;
    try {
      commit = await prepare();
    } catch (err) {
      console.error('could not load the paint:', err);
      if (epoch === this.epoch) this.conn.restart();
      return;
    }
    if (epoch !== this.epoch) return;
    commit();
    // A held frame may start another hold; `receive` then re-queues the rest.
    const held = this.backlog;
    this.backlog = null;
    for (const frame of held) this.receive(frame);
  }

  /** Replaces a drifted paint grid with the server's. */
  private regrid(session: Session, msg: Grid): void {
    const { grid } = session.world;
    if (msg.gridW !== grid.width || msg.gridH !== grid.height) {
      console.error(`grid is ${msg.gridW}×${msg.gridH}, expected ${grid.width}×${grid.height}`);
      this.conn.restart();
      return;
    }
    void this.holdWhile(async () => {
      const planes = await inflate(msg.gridZlib);
      return () => {
        // Marks the whole paint layer dirty through the grid's listener.
        grid.loadSnapshot(planes, msg.tick);
        session.sync.installed();
      };
    });
  }

  private welcome(welcome: Welcome): void {
    void this.holdWhile(async () => {
      let paint: PaintLayer | undefined;
      const world = new World(welcome, (x0, y0, x1, y1) => paint?.markCells(x0, y0, x1, y1));
      paint = new PaintLayer(world.grid);
      world.grid.loadSnapshot(await inflate(welcome.gridZlib), welcome.tick);
      const session: Session = {
        world,
        paint,
        predictor: new Predictor(world.constants),
        sync: new GridSync(),
        joined: false,
        alive: false,
        heardBrush: null,
      };
      return () => this.enter(session);
    });
  }

  /** Makes a freshly welcomed page the current one. */
  private enter(session: Session): void {
    const { world } = session;
    if (!this.session) {
      const { worldW, worldH } = world.constants;
      this.camera.jump(worldW / 2, worldH / 2, worldW, worldH);
    }
    this.session = session;
    this.joinCard.setPalette(world.palette);
    this.deathCard.setPalette(world.palette);
    this.joinCard.setReady(true);
    this.hud.setPage(world.page);
    this.hud.setStatus(null);
    this.sendView();
    // Coming back from a dropped connection mid-life: pick up where we were.
    if (this.player && !this.onDeathCard) this.join(session);
  }

  private hear(event: GameEvent, world: World): void {
    const me = world.myId;
    switch (event.kind) {
      case 'dropletGone':
        if (event.by === me) this.sfx.plip();
        break;
      case 'mix':
        if (event.a === me || event.b === me) this.sfx.squish();
        break;
      case 'death':
        if (event.cause === DeathCause.Absorbed && event.killer === me) this.sfx.squish();
        break;
      case 'beetleBump':
        if (event.player === me) this.sfx.bonk();
        break;
      default:
        break;
    }
  }

  private died(dead: Dead, world: World): void {
    this.onDeathCard = true;
    this.deathCard.show(dead, world.names.get(dead.killer));
  }

  // --- frame loop ---------------------------------------------------------

  private resize(): void {
    this.camera.resize(window.innerWidth, window.innerHeight, window.devicePixelRatio);
    this.renderer.resize();
    window.clearTimeout(this.viewTimer);
    this.viewTimer = window.setTimeout(() => this.sendView(), VIEW_DEBOUNCE_MS);
  }

  private sendView(): void {
    this.conn.send(encodeView(this.camera.viewW, this.camera.viewH));
  }

  private readonly frame = (nowMs: number): void => {
    requestAnimationFrame(this.frame);
    const dt = Math.min(MAX_FRAME_SECONDS, (nowMs - this.lastFrameMs) / 1000);
    this.lastFrameMs = nowMs;

    const session = this.session;
    if (!session) {
      this.renderer.drawEmpty();
      return;
    }
    const { world, paint, predictor } = session;
    const c = world.constants;
    const tick = world.clock.tickAt(nowMs);

    const me = session.alive ? world.self : null;
    if (me) {
      // Fixed-rate simulation: one input sent and predicted per server tick.
      const tickMs = 1000 / c.tickRate;
      this.tickDebtMs += dt * 1000;
      while (this.tickDebtMs >= tickMs) {
        this.tickDebtMs -= tickMs;
        this.input.sample(predictor.body.x, predictor.body.y);
        const seq = predictor.advance(this.input.dx, this.input.dy);
        this.conn.send(encodeInput(seq, this.input.dx, this.input.dy));
      }
      predictor.settle(dt);

      const sprite = this.sprite;
      sprite.x = predictor.renderX(this.tickDebtMs / tickMs);
      sprite.y = predictor.renderY(this.tickDebtMs / tickMs);
      sprite.wetness = me.wetness;
      sprite.color = me.color;
      sprite.name = world.names.get(world.myId) ?? '';
      this.camera.follow(sprite.x, sprite.y, dt, c.worldW, c.worldH);
      this.sfx.rustle(Math.hypot(predictor.body.vx, predictor.body.vy) / c.speedMax);
      this.hud.setWetness(me.wetness / c.maxWetness);
    } else {
      this.sfx.rustle(0);
      this.hud.setWetness(0);
      if (!this.player) {
        // Spectating from the join screen: drift along with the rain cloud.
        world.cloud.sample(tick, this.cloudAt);
        if (this.cloudAt.x !== 0 || this.cloudAt.y !== 0) {
          this.camera.follow(this.cloudAt.x, this.cloudAt.y, dt, c.worldW, c.worldH);
        }
      }
    }

    if (world.brush && world.brush !== session.heardBrush && tick >= world.brush.startTick) {
      session.heardBrush = world.brush;
      this.sfx.swoosh();
    }

    paint.update(Math.floor(tick), PAINT.frameBudgetMs);
    const self = me ? this.sprite : null;
    this.renderer.draw(world, paint, self, nowMs);
    this.minimap.draw(world, paint, this.camera, self, nowMs);
  };
}
