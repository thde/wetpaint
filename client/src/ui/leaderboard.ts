import type { Leaderboard, LeaderboardEntry } from '../protocol';
import { el } from './dom';
import { formatDuration } from './format';

interface Tab {
  readonly label: string;
  readonly format: (value: number) => string;
}

// In wire order: each scope sends painted, longest, mixed.
const TABS: readonly Tab[] = [
  { label: 'Most painted', format: String },
  { label: 'Longest life', format: formatDuration },
  { label: 'Most colours mixed', format: String },
];

// In wire order. The global scope is empty unless the server has Valkey.
const SCOPES = ['dripping right now', 'best this hour', 'across every page'] as const;

function row(entry: LeaderboardEntry, rank: number, format: Tab['format']): HTMLLIElement {
  const li = el('li');
  const dot = el('span', 'dot');
  dot.style.background = `rgb(${entry.color.r}, ${entry.color.g}, ${entry.color.b})`;
  li.append(
    el('span', 'rank', `${rank}.`),
    dot,
    el('span', 'who', entry.name),
    el('span', 'score', format(entry.value)),
  );
  return li;
}

/** The pencil-scribbled list in the corner, with a tab per statistic. */
export class LeaderboardPanel {
  readonly root = el('aside', 'leaderboard sketch-box');

  private readonly tabButtons: HTMLButtonElement[];
  private readonly lists = el('div', 'boards');
  private boards: Leaderboard['boards'] = [];
  private tab = 0;

  constructor() {
    const tabs = el('div', 'tabs');
    tabs.setAttribute('role', 'tablist');
    this.tabButtons = TABS.map((tab, i) => {
      const button = el('button', 'tab', tab.label);
      button.type = 'button';
      button.setAttribute('role', 'tab');
      button.addEventListener('click', () => {
        this.tab = i;
        this.render();
      });
      return button;
    });
    tabs.append(...this.tabButtons);
    this.root.append(tabs, this.lists);
    this.render();
  }

  set(boards: Leaderboard['boards']): void {
    this.boards = boards;
    this.render();
  }

  private render(): void {
    this.tabButtons.forEach((button, i) =>
      button.setAttribute('aria-selected', String(i === this.tab)),
    );
    const { format } = TABS[this.tab];
    const sections: HTMLElement[] = [];
    SCOPES.forEach((scope, s) => {
      const entries = this.boards[s * TABS.length + this.tab] ?? [];
      // "Right now" always shows, so the panel is never an empty box.
      if (entries.length === 0 && s > 0) return;
      const list = el('ol');
      list.append(...entries.map((entry, i) => row(entry, i + 1, format)));
      if (entries.length === 0) list.append(el('li', 'empty', 'nobody yet…'));
      sections.push(el('h3', '', scope), list);
    });
    this.lists.replaceChildren(...sections);
  }
}
