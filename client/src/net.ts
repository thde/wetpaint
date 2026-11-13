// Transport: asks /join which page to use, holds the WebSocket open, keeps
// the server's idle timer fed with pings, and reconnects: with backoff after
// a lost socket, at once when the page was full, and not at all once parked.

import { CloseCode, encodePing } from './protocol';

export interface ConnectionHandlers {
  /** A socket opened; a Welcome follows. */
  onOpen(): void;
  onMessage(frame: ArrayBuffer): void;
  /**
   * The socket closed. 'lost' and 'full' are already being reconnected;
   * after 'idle' the connection stays parked until `resume`.
   */
  onClose(why: CloseReason): void;
}

export type CloseReason = 'lost' | 'full' | 'idle';

interface JoinReply {
  readonly page: number;
  readonly ws: string;
}

const PING_INTERVAL_MS = 1000;
const BACKOFF_MS = [500, 1000, 2000, 4000, 8000] as const;

function isJoinReply(v: unknown): v is JoinReply {
  return (
    typeof v === 'object' &&
    v !== null &&
    typeof (v as { page?: unknown }).page === 'number' &&
    typeof (v as { ws?: unknown }).ws === 'string'
  );
}

/** Turns the `ws` field of a /join reply into an absolute WebSocket URL. */
export function resolveSocketUrl(ws: string, base: string): string {
  const url = new URL(ws, base);
  if (url.protocol === 'http:') url.protocol = 'ws:';
  else if (url.protocol === 'https:') url.protocol = 'wss:';
  return url.href;
}

export class Connection {
  private socket: WebSocket | null = null;
  private pingTimer: number | undefined;
  private attempts = 0;
  /** While parked, nothing reconnects until `resume`. */
  private parked = false;

  constructor(private readonly handlers: ConnectionHandlers) {}

  /** Connects and keeps reconnecting for the life of the page. */
  start(): void {
    void this.open();
  }

  /** Sends one frame; silently dropped while disconnected. */
  send(frame: ArrayBuffer): void {
    if (this.socket?.readyState === WebSocket.OPEN) this.socket.send(frame);
  }

  /** Abandons a connection whose stream can no longer be trusted. */
  restart(): void {
    this.socket?.close();
  }

  /** Closes the connection and stays away until `resume`. */
  park(): void {
    this.parked = true;
    this.socket?.close();
  }

  /** Reconnects after `park` or an idle close. */
  resume(): void {
    if (!this.parked) return;
    this.parked = false;
    this.attempts = 0;
    void this.open();
  }

  private async open(): Promise<void> {
    let url: string;
    try {
      url = await this.findPage();
    } catch {
      this.retry();
      return;
    }

    const socket = new WebSocket(url);
    socket.binaryType = 'arraybuffer';
    this.socket = socket;

    socket.onopen = () => {
      this.attempts = 0;
      this.pingTimer = window.setInterval(
        () => this.send(encodePing(performance.now())),
        PING_INTERVAL_MS,
      );
      this.handlers.onOpen();
    };
    socket.onmessage = (ev: MessageEvent<unknown>) => {
      if (ev.data instanceof ArrayBuffer) this.handlers.onMessage(ev.data);
    };
    // An error is always followed by a close, which does the work.
    socket.onclose = (ev: CloseEvent) => {
      window.clearInterval(this.pingTimer);
      this.socket = null;
      if (this.parked) return;
      if (ev.code === CloseCode.Idle) {
        this.parked = true;
        this.handlers.onClose('idle');
      } else if (ev.code === CloseCode.PageFull) {
        // Another page has room right now; /join knows which.
        this.handlers.onClose('full');
        void this.open();
      } else {
        this.handlers.onClose('lost');
        this.retry();
      }
    };
  }

  private async findPage(): Promise<string> {
    const res = await fetch('/join', { cache: 'no-store' });
    if (!res.ok) throw new Error(`/join answered ${res.status}`);
    const reply: unknown = await res.json();
    if (!isJoinReply(reply)) throw new Error('/join sent an unexpected reply');
    return resolveSocketUrl(reply.ws, window.location.href);
  }

  private retry(): void {
    const delay = BACKOFF_MS[Math.min(this.attempts, BACKOFF_MS.length - 1)];
    this.attempts += 1;
    window.setTimeout(() => {
      if (!this.parked) void this.open();
    }, delay);
  }
}
