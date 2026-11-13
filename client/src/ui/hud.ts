import { el } from './dom';
import { Glass } from './glass';

export interface HudHandlers {
  /** Toggles sound and returns whether it is now muted. */
  toggleMute(): boolean;
  postcard(): void;
}

/** The always-on bits around the edge of the sheet. */
export class Hud {
  readonly root = el('div', 'hud');
  /** The mini-map's canvas; drawn by render/minimap. */
  readonly minimap = el('canvas', 'minimap sketch-box');

  private readonly glass = new Glass();
  private readonly page = el('div', 'page-number');
  private readonly status = el('div', 'status sketch-box');

  constructor(muted: boolean, handlers: HudHandlers) {
    const mute = el('button', 'sketch-button', muted ? 'sound off' : 'sound on');
    mute.type = 'button';
    mute.setAttribute('aria-pressed', String(muted));
    mute.addEventListener('click', () => {
      const nowMuted = handlers.toggleMute();
      mute.textContent = nowMuted ? 'sound off' : 'sound on';
      mute.setAttribute('aria-pressed', String(nowMuted));
    });

    const postcard = el('button', 'sketch-button', 'Postcard');
    postcard.type = 'button';
    postcard.title = 'download the whole sheet as a picture';
    postcard.addEventListener('click', handlers.postcard);

    const gauge = el('div', 'gauge');
    gauge.title = 'how wet you are';
    gauge.append(this.glass.canvas);

    const tools = el('div', 'tools');
    tools.append(gauge, postcard, mute);

    this.status.hidden = true;
    this.root.append(tools, this.minimap, this.page, this.status);
  }

  /** `fullness` ∈ [0, 1]. */
  setWetness(fullness: number): void {
    this.glass.set(fullness);
  }

  setPage(page: number): void {
    this.page.textContent = `p. ${page}`;
  }

  /** Shows a small note such as "reconnecting…", or hides it with null. */
  setStatus(text: string | null): void {
    this.status.hidden = text === null;
    this.status.textContent = text ?? '';
  }
}
