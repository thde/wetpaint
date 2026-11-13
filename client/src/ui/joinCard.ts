import type { Rgb } from '../protocol';
import { el } from './dom';
import { Swatches } from './swatches';

/** The paper card you fill in before dropping onto the page. */
export class JoinCard {
  readonly root = el('div', 'overlay');

  private readonly swatches = new Swatches('?', 'surprise me');
  private readonly button = el('button', 'sketch-button big', 'Drop in');

  constructor(onJoin: (color: number) => void) {
    const card = el('form', 'card join-card');

    this.button.type = 'submit';
    this.button.disabled = true;

    card.append(
      el('h1', 'title', 'Wet Paint'),
      el('p', 'tagline', 'you are one drop of watercolour. stay wet, leave a mark.'),
      el('p', 'hint', 'pick a colour'),
      this.swatches.root,
      this.button,
      el('p', 'hint small', 'follow your pointer or finger · WASD works too'),
    );
    card.addEventListener('submit', (ev) => {
      ev.preventDefault();
      onJoin(this.swatches.selected);
    });
    this.root.append(card);
  }

  setPalette(palette: readonly Rgb[]): void {
    this.swatches.setPalette(palette);
  }

  /**
   * The button only works once a page has welcomed us. Until then it says
   * why: the HUD's status note is hidden behind this card.
   */
  setReady(ready: boolean, waiting = 'finding a page…'): void {
    this.button.disabled = !ready;
    this.button.textContent = ready ? 'Drop in' : waiting;
  }

  setVisible(visible: boolean): void {
    this.root.hidden = !visible;
  }
}
