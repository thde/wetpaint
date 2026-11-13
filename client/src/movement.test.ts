import { describe, expect, it } from 'vitest';
import { constantsFromList, quantizeInput, radiusOf, stepBody, type Body } from './movement';
import { TEST_CONSTANTS } from './testing/bytes';

const c = constantsFromList(TEST_CONSTANTS);
const at = (over: Partial<Body> = {}): Body => ({ x: 2000, y: 1500, vx: 0, vy: 0, wetness: 100, ...over });

describe('constantsFromList', () => {
  it('names the positional list and ignores unknown extras', () => {
    const named = constantsFromList([...TEST_CONSTANTS, 123]);
    expect(named.worldW).toBe(4000);
    expect(named.tickRate).toBe(20);
    expect(named.absorbRatio).toBe(1.3);
  });

  it('refuses a list that is too short', () => {
    expect(() => constantsFromList([1, 2, 3])).toThrow();
  });
});

describe('stepBody', () => {
  it('eases velocity towards the target speed', () => {
    const b = at();
    stepBody(b, 127, 0, c);
    // maxSpeed at full wetness = speedMin = 120; k = accel·dt = 0.3
    expect(b.vx).toBeCloseTo(36, 10);
    expect(b.x).toBeCloseTo(2000 + 36 / 20, 10);
    expect(b.vy).toBe(0);
    stepBody(b, 127, 0, c);
    expect(b.vx).toBeCloseTo(36 + (120 - 36) * 0.3, 10);
  });

  it('lets small drops move faster than big ones', () => {
    const big = at({ wetness: 100 });
    const small = at({ wetness: 0 });
    // Long enough to reach top speed, short enough to stay clear of the edge.
    for (let i = 0; i < 80; i++) {
      stepBody(big, 127, 0, c);
      stepBody(small, 127, 0, c);
    }
    expect(big.vx).toBeCloseTo(c.speedMin, 3);
    expect(small.vx).toBeCloseTo(c.speedMax, 3);
  });

  it('normalises diagonal input so it is no faster', () => {
    const b = at();
    for (let i = 0; i < 80; i++) stepBody(b, 127, 127, c);
    expect(Math.hypot(b.vx, b.vy)).toBeCloseTo(c.speedMin, 3);
  });

  it('treats a short vector as partial throttle', () => {
    const b = at();
    for (let i = 0; i < 80; i++) stepBody(b, 64, 0, c);
    expect(b.vx).toBeCloseTo((c.speedMin * 64) / 127, 3);
  });

  it('coasts to a stop with inertia when input is released', () => {
    const b = at({ vx: 100 });
    stepBody(b, 0, 0, c);
    expect(b.vx).toBeCloseTo(70, 10);
    expect(b.x).toBeGreaterThan(2000);
  });

  it('stops at the edge of the page, one radius in', () => {
    const r = radiusOf(100, c);
    expect(r).toBe(35);
    const b = at({ x: r + 1, y: c.worldH - r - 1, vx: -200, vy: 200 });
    stepBody(b, -127, 127, c);
    expect(b).toMatchObject({ x: r, y: c.worldH - r, vx: 0, vy: 0 });
  });
});

describe('quantizeInput', () => {
  it('maps the unit range onto the wire range', () => {
    expect(quantizeInput(1)).toBe(127);
    expect(quantizeInput(-1)).toBe(-127);
    expect(quantizeInput(0.5)).toBe(64);
    expect(quantizeInput(7)).toBe(127);
    expect(quantizeInput(0)).toBe(0);
  });
});
