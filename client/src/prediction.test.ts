import { describe, expect, it } from 'vitest';
import { constantsFromList, stepBody, type Body } from './movement';
import { Predictor } from './prediction';
import { TEST_CONSTANTS } from './testing/bytes';

const c = constantsFromList(TEST_CONSTANTS);
const START: Body = { x: 2000, y: 1500, vx: 0, vy: 0, wetness: 80 };

/** The authoritative simulation: applies the same inputs in order. */
function serverAfter(inputs: readonly (readonly [number, number])[]): Body {
  const body = { ...START };
  for (const [dx, dy] of inputs) stepBody(body, dx, dy, c);
  return body;
}

describe('Predictor', () => {
  it('numbers inputs from 1 and moves immediately', () => {
    const p = new Predictor(c);
    p.reset(START);
    expect(p.advance(127, 0)).toBe(1);
    expect(p.advance(127, 0)).toBe(2);
    expect(p.body.x).toBeGreaterThan(START.x);
  });

  it('is unchanged by a server state that agrees with it', () => {
    const p = new Predictor(c);
    p.reset(START);
    const inputs = [[127, 0], [127, 0], [0, 127], [-60, 90]] as const;
    for (const [dx, dy] of inputs) p.advance(dx, dy);
    const before = { ...p.body };

    // The server has applied the first two inputs; two are still in flight.
    p.reconcile(serverAfter(inputs.slice(0, 2)), 2);

    expect(p.body.x).toBeCloseTo(before.x, 9);
    expect(p.body.y).toBeCloseTo(before.y, 9);
    expect(p.body.vx).toBeCloseTo(before.vx, 9);
    expect(p.renderX(1)).toBeCloseTo(before.x, 9);
  });

  it('adopts a server correction and replays unacknowledged inputs on top', () => {
    const p = new Predictor(c);
    p.reset(START);
    p.advance(127, 0);
    p.advance(127, 0);
    p.advance(127, 0);

    // The server knocked us sideways after input 1 (say, a beetle).
    const bumped: Body = { ...serverAfter([[127, 0]]), y: 1400, vy: -50 };
    p.reconcile(bumped, 1);

    const expected = { ...bumped };
    stepBody(expected, 127, 0, c);
    stepBody(expected, 127, 0, c);
    expect(p.body).toEqual(expected);
  });

  it('eases the drawn position into a correction instead of popping', () => {
    const p = new Predictor(c);
    p.reset(START);
    p.advance(127, 0);
    const drawnX = p.renderX(1);
    const drawnY = p.renderY(1);

    p.reconcile({ ...p.body, y: p.body.y + 30 }, 1);

    expect(p.body.y).toBeCloseTo(drawnY + 30, 9);
    expect(p.renderX(1)).toBeCloseTo(drawnX, 9);
    expect(p.renderY(1)).toBeCloseTo(drawnY, 9);
    p.settle(0.08);
    expect(p.renderY(1)).toBeCloseTo(drawnY + 15, 6);
    p.settle(10);
    expect(p.renderY(1)).toBeCloseTo(drawnY + 30, 6);
  });

  it('jumps straight to a far-away correction', () => {
    const p = new Predictor(c);
    p.reset(START);
    p.advance(0, 0);
    p.reconcile({ ...START, x: 500 }, 1);
    expect(p.renderX(1)).toBe(500);
  });

  it('ignores stale acknowledgements and keeps numbering across lives', () => {
    const p = new Predictor(c);
    p.reset(START);
    p.advance(127, 0);
    p.advance(127, 0);
    p.reconcile(serverAfter([[127, 0], [127, 0]]), 2);
    p.reconcile(serverAfter([[127, 0], [127, 0]]), 2);
    expect(p.body).toEqual(serverAfter([[127, 0], [127, 0]]));

    p.reset({ ...START, x: 100 });
    expect(p.advance(0, 0)).toBe(3);
    // Nothing from the previous life is replayed.
    p.reconcile({ ...START, x: 100 }, 3);
    expect(p.body.x).toBe(100);
  });

  it('interpolates the drawn position within a tick', () => {
    const p = new Predictor(c);
    p.reset(START);
    p.advance(127, 0);
    expect(p.renderX(0)).toBe(START.x);
    expect(p.renderX(0.5)).toBeCloseTo((START.x + p.body.x) / 2, 9);
    expect(p.renderX(1)).toBe(p.body.x);
  });
});
