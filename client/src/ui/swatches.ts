import { COLOR_AUTO, type Rgb } from '../protocol';
import { el } from './dom';

/**
 * A row of paint swatches. The first one is the "auto" choice, which lets the
 * server decide (a random colour on join, the same colour on respawn).
 */
export class Swatches {
  readonly root = el('div', 'swatches');
  /** A palette index, or COLOR_AUTO. */
  selected: number = COLOR_AUTO;

  constructor(
    private readonly autoGlyph: string,
    private readonly autoTitle: string,
  ) {
    this.root.setAttribute('role', 'radiogroup');
    this.setPalette([]);
  }

  setPalette(palette: readonly Rgb[]): void {
    if (this.selected !== COLOR_AUTO && this.selected >= palette.length) this.selected = COLOR_AUTO;
    const auto = this.swatch(COLOR_AUTO, this.autoTitle);
    auto.classList.add('swatch-auto');
    auto.textContent = this.autoGlyph;
    this.root.replaceChildren(
      auto,
      ...palette.map((c, i) => {
        const button = this.swatch(i, `colour ${i + 1}`);
        button.style.setProperty('--paint', `rgb(${c.r}, ${c.g}, ${c.b})`);
        return button;
      }),
    );
  }

  private swatch(value: number, title: string): HTMLButtonElement {
    const button = el('button', 'swatch');
    button.type = 'button';
    button.title = title;
    button.setAttribute('role', 'radio');
    button.setAttribute('aria-label', title);
    button.setAttribute('aria-checked', String(value === this.selected));
    button.addEventListener('click', () => {
      this.selected = value;
      for (const other of this.root.children) other.setAttribute('aria-checked', 'false');
      button.setAttribute('aria-checked', 'true');
    });
    return button;
  }
}
