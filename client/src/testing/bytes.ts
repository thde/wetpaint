// Test helper: builds server frames byte by byte, independently of the decoder.

const utf8 = new TextEncoder();

export class Bytes {
  private readonly out: number[] = [];

  u8(...values: number[]): this {
    for (const v of values) this.out.push(v & 0xff);
    return this;
  }

  u16(v: number): this {
    return this.u8(v, v >>> 8);
  }

  u32(v: number): this {
    return this.u8(v, v >>> 8, v >>> 16, v >>> 24);
  }

  f32(v: number): this {
    const b = new Uint8Array(4);
    new DataView(b.buffer).setFloat32(0, v, true);
    return this.u8(...b);
  }

  /** A world coordinate as a wire `pos`. */
  pos(world: number): this {
    return this.u16(world * 16);
  }

  str(s: string): this {
    const b = utf8.encode(s);
    return this.u8(b.length, ...b);
  }

  raw(bytes: Uint8Array): this {
    return this.u8(...bytes);
  }

  buffer(): ArrayBuffer {
    return Uint8Array.from(this.out).buffer;
  }
}

export const TEST_CONSTANTS: readonly number[] = [
  4000, 3000, 20, 10, 300, 1.1, 0.3, 10, 0.25, 260, 120, 6, 100, 5, 14, 70, 60, 16, 4, 1.3,
];
