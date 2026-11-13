// Detects a paint grid that has drifted from the server's and decides when to
// ask for a fresh one. Pure: the caller sends the Resync and installs the Grid.

import type { PaintGrid } from './paintGrid';
import type { GridCheck } from './protocol';

/**
 * The server honours one Resync per 10 s and silently ignores the rest, so an
 * unanswered request is repeated once that window has safely passed.
 */
export const RESYNC_RETRY_MS = 12_000;

export class GridSync {
  /** When the outstanding Resync was sent, or null if none is. */
  private askedAtMs: number | null = null;

  /**
   * Compares the grid with a GridCheck. Must be called at the check's place in
   * the message stream, after every earlier paint op has been applied.
   * Returns true if the caller should send Resync now.
   */
  check(grid: PaintGrid, check: GridCheck, nowMs: number): boolean {
    if (this.askedAtMs !== null) {
      // Checks are meaningless until the replacement arrives.
      if (nowMs - this.askedAtMs < RESYNC_RETRY_MS) return false;
    } else if (grid.checksum(check.tick) === check.crc) {
      return false;
    }
    this.askedAtMs = nowMs;
    return true;
  }

  /** A Grid was installed; checks count again. */
  installed(): void {
    this.askedAtMs = null;
  }
}
