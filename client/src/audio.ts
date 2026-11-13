// Every sound is synthesised with the Web Audio API; there are no files.

import { prefs } from './prefs';

const MUTE_KEY = 'wetpaint.muted';

interface Rig {
  readonly ctx: AudioContext;
  readonly master: GainNode;
  readonly noise: AudioBuffer;
  /** Level of the looping paper rustle. */
  readonly rustle: GainNode;
}

function buildRig(muted: boolean): Rig {
  const ctx = new AudioContext();
  const master = ctx.createGain();
  master.gain.value = muted ? 0 : 1;
  master.connect(ctx.destination);

  const noise = ctx.createBuffer(1, ctx.sampleRate, ctx.sampleRate);
  const samples = noise.getChannelData(0);
  for (let i = 0; i < samples.length; i++) samples[i] = Math.random() * 2 - 1;

  // Paper rustle: quiet, bright noise whose level follows the drop's speed.
  const source = ctx.createBufferSource();
  source.buffer = noise;
  source.loop = true;
  const filter = ctx.createBiquadFilter();
  filter.type = 'bandpass';
  filter.frequency.value = 3400;
  filter.Q.value = 0.6;
  const rustle = ctx.createGain();
  rustle.gain.value = 0;
  source.connect(filter).connect(rustle).connect(master);
  source.start();

  return { ctx, master, noise, rustle };
}

export class Sfx {
  private rig: Rig | null = null;
  private mutedState = prefs.get(MUTE_KEY) === '1';

  get muted(): boolean {
    return this.mutedState;
  }

  /** Browsers only allow audio after a gesture; call this from one. */
  unlock(): void {
    try {
      this.rig ??= buildRig(this.mutedState);
      void this.rig.ctx.resume();
    } catch {
      // No audio device or API: the game is silent, which is fine.
    }
  }

  toggleMuted(): boolean {
    this.mutedState = !this.mutedState;
    prefs.set(MUTE_KEY, this.mutedState ? '1' : '0');
    if (this.rig) {
      this.rig.master.gain.setTargetAtTime(this.mutedState ? 0 : 1, this.rig.ctx.currentTime, 0.02);
    }
    return this.mutedState;
  }

  /** Sets the rustle from how fast the drop is sliding, 0–1. */
  rustle(level: number): void {
    if (!this.rig) return;
    const gain = Math.min(1, Math.max(0, level)) * 0.035;
    this.rig.rustle.gain.setTargetAtTime(gain, this.rig.ctx.currentTime, 0.12);
  }

  /** A tiny rising "plip" for picking up water. */
  plip(): void {
    this.tone(520 + Math.random() * 120, 1250, 0.11, 0.16);
  }

  /** A soft low thud for bumping a beetle. */
  bonk(): void {
    this.tone(210, 120, 0.14, 0.18);
  }

  /** A wet squish for mixing and merging. */
  squish(): void {
    this.tone(260, 90, 0.22, 0.14);
    this.noiseSweep('lowpass', 1400, 300, 0.24, 0.16, 1);
  }

  /** The brush coming through. */
  swoosh(): void {
    this.noiseSweep('bandpass', 350, 2600, 1.3, 0.2, 0.8);
  }

  private tone(from: number, to: number, seconds: number, level: number): void {
    const rig = this.rig;
    if (!rig || this.mutedState) return;
    const t = rig.ctx.currentTime;
    const osc = rig.ctx.createOscillator();
    osc.type = 'sine';
    osc.frequency.setValueAtTime(from, t);
    osc.frequency.exponentialRampToValueAtTime(to, t + seconds);
    const gain = rig.ctx.createGain();
    gain.gain.setValueAtTime(level, t);
    gain.gain.exponentialRampToValueAtTime(0.001, t + seconds);
    osc.connect(gain).connect(rig.master);
    osc.start(t);
    osc.stop(t + seconds);
  }

  private noiseSweep(
    type: BiquadFilterType,
    from: number,
    to: number,
    seconds: number,
    level: number,
    q: number,
  ): void {
    const rig = this.rig;
    if (!rig || this.mutedState) return;
    const t = rig.ctx.currentTime;
    const source = rig.ctx.createBufferSource();
    source.buffer = rig.noise;
    source.loop = true;
    const filter = rig.ctx.createBiquadFilter();
    filter.type = type;
    filter.Q.value = q;
    filter.frequency.setValueAtTime(from, t);
    filter.frequency.exponentialRampToValueAtTime(to, t + seconds);
    const gain = rig.ctx.createGain();
    gain.gain.setValueAtTime(0.001, t);
    gain.gain.linearRampToValueAtTime(level, t + seconds * 0.3);
    gain.gain.exponentialRampToValueAtTime(0.001, t + seconds);
    source.connect(filter).connect(gain).connect(rig.master);
    source.start(t);
    source.stop(t + seconds);
  }
}
