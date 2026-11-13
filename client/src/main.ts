import '@fontsource/patrick-hand/400.css';
import '@fontsource/caveat/400.css';
import '@fontsource/caveat/700.css';
import './style.css';

import { Game } from './game';

const canvas = document.getElementById('game');
const ui = document.getElementById('ui');
if (!(canvas instanceof HTMLCanvasElement) || !ui) {
  throw new Error('index.html is missing #game or #ui');
}
new Game(canvas, ui).start();
