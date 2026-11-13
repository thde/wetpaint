import { describe, expect, it } from 'vitest';
import { ServerClock, Track } from './interpolation';

describe('Track', () => {
  it('interpolates between the samples either side of the tick', () => {
    const track = new Track();
    track.push(10, 0, 100);
    track.push(11, 10, 100);
    track.push(13, 30, 60);
    const out = { x: -1, y: -1 };

    track.sample(10.5, out);
    expect(out).toEqual({ x: 5, y: 100 });
    track.sample(12, out);
    expect(out).toEqual({ x: 20, y: 80 });
  });

  it('clamps instead of extrapolating', () => {
    const track = new Track();
    track.push(10, 1, 2);
    track.push(11, 3, 4);
    const out = { x: 0, y: 0 };
    track.sample(5, out);
    expect(out).toEqual({ x: 1, y: 2 });
    track.sample(99, out);
    expect(out).toEqual({ x: 3, y: 4 });
  });

  it('keeps only the most recent samples and leaves `out` alone when empty', () => {
    const out = { x: 7, y: 7 };
    new Track().sample(1, out);
    expect(out).toEqual({ x: 7, y: 7 });

    const track = new Track();
    for (let t = 0; t < 10; t++) track.push(t, t * 10, 0);
    track.sample(0, out);
    expect(out.x).toBe(60); // tick 6 is the oldest of the last four
    track.sample(8.5, out);
    expect(out.x).toBe(85);
  });
});

describe('ServerClock', () => {
  it('reports zero before any State', () => {
    expect(new ServerClock(50).tickAt(1234)).toBe(0);
  });

  it('advances smoothly between States', () => {
    const clock = new ServerClock(50);
    clock.observe(100, 1000);
    expect(clock.tickAt(1000)).toBe(100);
    expect(clock.tickAt(1025)).toBe(100.5);
  });

  it('smooths jitter but resynchronises after a large jump', () => {
    const clock = new ServerClock(50);
    clock.observe(100, 1000);
    clock.observe(101, 1070); // arrived 20 ms late
    expect(clock.tickAt(1070)).toBeCloseTo(101.36, 6);
    clock.observe(500, 2000); // the tab was asleep
    expect(clock.tickAt(2000)).toBe(500);
  });
});
