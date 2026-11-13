import { describe, expect, it } from 'vitest';
import {
  COLOR_AUTO,
  ProtocolError,
  decodeServerMessage,
  encodeInput,
  encodeJoin,
  encodePing,
  encodeRespawn,
  encodeResync,
  encodeView,
  VersionError,
} from './protocol';
import { Bytes, TEST_CONSTANTS } from './testing/bytes';

const bytes = (buf: ArrayBuffer): number[] => [...new Uint8Array(buf)];

function brush(b: Bytes): Bytes {
  return b.f32(-200).f32(1500).f32(4200).f32(1700).f32(120).u32(900).u16(60).u8(40, 50, 60);
}

const BRUSH = {
  x0: -200,
  y0: 1500,
  x1: 4200,
  y1: 1700,
  halfWidth: 120,
  startTick: 900,
  durationTicks: 60,
  color: { r: 40, g: 50, b: 60 },
};

describe('client messages', () => {
  it('encodes Join', () => {
    expect(bytes(encodeJoin('Zoë', 3))).toEqual([0x01, 4, 0x5a, 0x6f, 0xc3, 0xab, 3]);
    expect(bytes(encodeJoin('', COLOR_AUTO))).toEqual([0x01, 0, 255]);
  });

  it('never splits a code point when a name overflows the length byte', () => {
    const out = bytes(encodeJoin('é'.repeat(200), 0));
    expect(out[1]).toBe(254);
    expect(out).toHaveLength(2 + 254 + 1);
  });

  it('encodes Input with signed components', () => {
    expect(bytes(encodeInput(0x01020304, 127, -127))).toEqual([0x02, 4, 3, 2, 1, 0x7f, 0x81]);
  });

  it('encodes Resync', () => {
    expect(bytes(encodeResync())).toEqual([0x06]);
  });

  it('encodes Respawn, Ping and View', () => {
    expect(bytes(encodeRespawn(255))).toEqual([0x03, 255]);
    expect(bytes(encodePing(0x0a0b0c0d))).toEqual([0x04, 0x0d, 0x0c, 0x0b, 0x0a]);
    expect(bytes(encodeView(1600.4, 1000))).toEqual([0x05, 0x40, 0x06, 0xe8, 0x03]);
    expect(bytes(encodeView(1e9, -5))).toEqual([0x05, 0xff, 0xff, 0, 0]);
  });
});

describe('server messages', () => {
  it('decodes Welcome', () => {
    const zlib = Uint8Array.of(0x78, 0x9c, 1, 2, 3);
    const frame = new Bytes().u8(0x81).u16(2).u32(3).u16(77).u32(123456);
    frame.u8(TEST_CONSTANTS.length);
    for (const c of TEST_CONSTANTS) frame.f32(c);
    frame.u8(2).u8(255, 0, 10).u8(1, 2, 3);
    frame.u8(1).f32(100).f32(200).f32(300).f32(150);
    frame.u8(2).u16(5).str('ann').u16(9).str('');
    frame.u16(1).u16(400).pos(12.5).pos(2999.9375);
    frame.u8(1).pos(50).pos(60).u32(123000).str('bo');
    brush(frame.u8(1));
    frame.u16(400).u16(300).u32(zlib.length).raw(zlib);

    const msg = decodeServerMessage(frame.buffer());
    expect(msg).toEqual({
      type: 'welcome',
      page: 3,
      yourId: 77,
      tick: 123456,
      constants: TEST_CONSTANTS.map(Math.fround),
      palette: [
        { r: 255, g: 0, b: 10 },
        { r: 1, g: 2, b: 3 },
      ],
      blotters: [{ x: 100, y: 200, w: 300, h: 150 }],
      roster: [
        { id: 5, name: 'ann' },
        { id: 9, name: '' },
      ],
      droplets: [{ id: 400, x: 12.5, y: 2999.9375 }],
      blooms: [{ x: 50, y: 60, tick: 123000, name: 'bo' }],
      brush: BRUSH,
      gridW: 400,
      gridH: 300,
      gridZlib: zlib,
    });
  });

  it('rejects another protocol version', () => {
    const frame = new Bytes().u8(0x81).u16(1).buffer();
    expect(() => decodeServerMessage(frame)).toThrow(VersionError);
    expect(() => decodeServerMessage(frame)).toThrow(/protocol 1/);
  });

  it('decodes State for a living player', () => {
    const frame = new Bytes().u8(0x82).u32(1000).u32(42).pos(2000).pos(100).u8(1);
    frame.f32(10.5).f32(20.25).f32(-3).f32(4).f32(87.5).u8(9, 8, 7);
    frame.u8(1).u16(12).pos(300).pos(400).u8(151).u8(1, 2, 3);
    frame.u8(1).u8(2).pos(1).pos(2);
    frame.u8(1).u8(4).pos(700).pos(800).u8(33);

    expect(decodeServerMessage(frame.buffer())).toEqual({
      type: 'state',
      tick: 1000,
      ackSeq: 42,
      cloudX: 2000,
      cloudY: 100,
      self: { x: 10.5, y: 20.25, vx: -3, vy: 4, wetness: 87.5, color: { r: 9, g: 8, b: 7 } },
      drops: [{ id: 12, x: 300, y: 400, wetness: 75.5, color: { r: 1, g: 2, b: 3 } }],
      beetles: [{ id: 2, x: 1, y: 2 }],
      puddles: [{ id: 4, x: 700, y: 800, remaining: 33 }],
    });
  });

  it('decodes State for a spectator from literal bytes', () => {
    const frame = Uint8Array.of(
      0x82, // type
      0x10, 0x27, 0, 0, // tick 10000
      0, 0, 0, 0, // ackSeq
      0x00, 0x7d, 0x40, 0x06, // cloud (2000, 100)
      0, // not alive
      0, 0, 0, // no drops, beetles, puddles
    );
    expect(decodeServerMessage(frame.buffer)).toEqual({
      type: 'state',
      tick: 10000,
      ackSeq: 0,
      cloudX: 2000,
      cloudY: 100,
      self: null,
      drops: [],
      beetles: [],
      puddles: [],
    });
  });

  it('decodes Events with every kind', () => {
    const frame = new Bytes().u8(0x83).u32(555);
    frame.u16(2).u16(3200).u16(2400).u8(60).u8(226, 88, 96).u8(200);
    frame.u16(16).u16(32).u8(10).u8(0, 0, 0).u8(0);
    frame.u8(7);
    frame.u8(1).u16(5).str('ann');
    frame.u8(2).u16(5).u8(1).u16(9).pos(100).pos(200).u8(1, 2, 3);
    frame.u8(3).u16(40).pos(10).pos(20);
    frame.u8(4).u16(40).u16(0);
    brush(frame.u8(5));
    frame.u8(6).u16(5).u16(9);
    frame.u8(7).u16(9).u8(3);

    expect(decodeServerMessage(frame.buffer())).toEqual({
      type: 'events',
      tick: 555,
      paint: [
        { x: 3200, y: 2400, radius2: 60, r: 226, g: 88, b: 96, strength: 200 },
        { x: 16, y: 32, radius2: 10, r: 0, g: 0, b: 0, strength: 0 },
      ],
      events: [
        { kind: 'spawn', id: 5, name: 'ann' },
        { kind: 'death', id: 5, cause: 1, killer: 9, x: 100, y: 200, color: { r: 1, g: 2, b: 3 } },
        { kind: 'dropletSpawn', id: 40, x: 10, y: 20 },
        { kind: 'dropletGone', id: 40, by: 0 },
        { kind: 'brushWarn', brush: BRUSH },
        { kind: 'mix', a: 5, b: 9 },
        { kind: 'beetleBump', player: 9, beetle: 3 },
      ],
    });
  });

  it('decodes GridCheck and Grid', () => {
    const check = new Bytes().u8(0x87).u32(123456).u32(0xcbf43926).buffer();
    expect(decodeServerMessage(check)).toEqual({
      type: 'gridCheck',
      tick: 123456,
      crc: 0xcbf43926,
    });

    const grid = new Bytes().u8(0x88).u32(9000).u16(400).u16(300).u32(3).u8(7, 8, 9).buffer();
    expect(decodeServerMessage(grid)).toEqual({
      type: 'grid',
      tick: 9000,
      gridW: 400,
      gridH: 300,
      gridZlib: Uint8Array.of(7, 8, 9),
    });
    // A zlib length that overruns the frame is malformed, not a short read.
    const short = new Bytes().u8(0x88).u32(9000).u16(400).u16(300).u32(4).u8(7, 8, 9).buffer();
    expect(() => decodeServerMessage(short)).toThrow(ProtocolError);
  });

  it('decodes Dead, Leaderboard and Pong', () => {
    const dead = new Bytes().u8(0x84).u8(2).u16(0).f32(61.5).u32(1234).u16(7);
    expect(decodeServerMessage(dead.buffer())).toEqual({
      type: 'dead',
      cause: 2,
      killer: 0,
      secondsAlive: 61.5,
      cellsPainted: 1234,
      colorsMixed: 7,
    });

    const board = new Bytes().u8(0x85).u8(2).u8(1).str('ann').u32(99).u8(1, 2, 3).u8(0);
    expect(decodeServerMessage(board.buffer())).toEqual({
      type: 'leaderboard',
      boards: [[{ name: 'ann', value: 99, color: { r: 1, g: 2, b: 3 } }], []],
    });

    const pong = Uint8Array.of(0x86, 1, 0, 0, 0, 2, 0, 0, 0);
    expect(decodeServerMessage(pong.buffer)).toEqual({ type: 'pong', clientTime: 1, tick: 2 });
  });

  it('rejects truncated, over-long and unknown frames', () => {
    const pong = [0x86, 1, 0, 0, 0, 2, 0, 0, 0];
    expect(() => decodeServerMessage(Uint8Array.from(pong.slice(0, 6)).buffer)).toThrow(ProtocolError);
    expect(() => decodeServerMessage(Uint8Array.from([...pong, 0]).buffer)).toThrow(ProtocolError);
    expect(() => decodeServerMessage(Uint8Array.of(0x7f).buffer)).toThrow(ProtocolError);
    const badEvent = new Bytes().u8(0x83).u32(1).u16(0).u8(1).u8(99);
    expect(() => decodeServerMessage(badEvent.buffer())).toThrow(ProtocolError);
  });
});
