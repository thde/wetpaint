import { DeathCause, type Dead, type Rgb } from '../protocol';
import { el } from './dom';
import { formatDuration } from './format';
import { Swatches } from './swatches';

function epitaph(cause: number, killer: string | undefined): string {
  switch (cause) {
    case DeathCause.Absorbed:
      return killer ? `You became part of ${killer}.` : 'You became part of a bigger drop.';
    case DeathCause.Brushed:
      return 'The big brush soaked you right up.';
    default:
      return 'You dried into a lovely bloom.';
  }
}

/** The gentle card shown after a life, with its stats and a way back in. */
export class DeathCard {
  readonly root = el('div', 'overlay clear');

  private readonly swatches = new Swatches('=', 'same colour');
  private readonly headline = el('p', 'epitaph');
  private readonly alive = el('dd');
  private readonly painted = el('dd');
  private readonly mixed = el('dd');

  constructor(onRespawn: (color: number) => void) {
    const card = el('div', 'card death-card');
    const stats = el('dl', 'stats');
    stats.append(
      el('dt', '', 'time alive'),
      this.alive,
      el('dt', '', 'area painted'),
      this.painted,
      el('dt', '', 'colours mixed'),
      this.mixed,
    );
    const again = el('button', 'sketch-button big', 'Drip again');
    again.type = 'button';
    again.addEventListener('click', () => onRespawn(this.swatches.selected));

    card.append(
      el('h2', 'title small', 'a lovely bloom!'),
      this.headline,
      stats,
      el('p', 'hint', 'change colour?'),
      this.swatches.root,
      again,
    );
    this.root.append(card);
    this.root.hidden = true;
  }

  setPalette(palette: readonly Rgb[]): void {
    this.swatches.setPalette(palette);
  }

  show(dead: Dead, killerName: string | undefined): void {
    this.headline.textContent = epitaph(dead.cause, killerName);
    this.alive.textContent = formatDuration(dead.secondsAlive);
    this.painted.textContent = `${dead.cellsPainted} cells`;
    this.mixed.textContent = String(dead.colorsMixed);
    this.root.hidden = false;
  }

  hide(): void {
    this.root.hidden = true;
  }
}
