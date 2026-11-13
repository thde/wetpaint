// Deterministic noise for textures and hand-drawn wobble.

/** Edge length of every noise texture; a power of two so `& NOISE_MASK` wraps. */
export const NOISE_SIZE = 256;
export const NOISE_MASK = NOISE_SIZE - 1;

/** Small seeded PRNG (mulberry32) returning values in [0, 1). */
export function prng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** Stateless hash of an integer to [0, 1); stable wobble without storage. */
export function hash01(n: number): number {
  let h = Math.imul(n ^ 0x9e3779b9, 0x85ebca6b);
  h ^= h >>> 13;
  h = Math.imul(h, 0xc2b2ae35);
  h ^= h >>> 16;
  return (h >>> 0) / 4294967296;
}

/** Uniform per-pixel noise, 0–255. */
export function whiteNoise(seed: number): Uint8Array {
  const rand = prng(seed);
  const out = new Uint8Array(NOISE_SIZE * NOISE_SIZE);
  for (let i = 0; i < out.length; i++) out[i] = rand() * 256;
  return out;
}

/**
 * Smooth, tileable value noise, 0–255, with features about `cell` pixels wide
 * (`cell` must divide NOISE_SIZE).
 */
export function valueNoise(cell: number, seed: number): Uint8Array {
  const n = NOISE_SIZE / cell;
  const rand = prng(seed);
  const lattice = new Float32Array(n * n);
  for (let i = 0; i < lattice.length; i++) lattice[i] = rand();

  const out = new Uint8Array(NOISE_SIZE * NOISE_SIZE);
  for (let y = 0; y < NOISE_SIZE; y++) {
    const gy = Math.floor(y / cell);
    const gy1 = (gy + 1) % n;
    const fy = smooth(y / cell - gy);
    for (let x = 0; x < NOISE_SIZE; x++) {
      const gx = Math.floor(x / cell);
      const gx1 = (gx + 1) % n;
      const fx = smooth(x / cell - gx);
      const top = lattice[gy * n + gx] * (1 - fx) + lattice[gy * n + gx1] * fx;
      const bottom = lattice[gy1 * n + gx] * (1 - fx) + lattice[gy1 * n + gx1] * fx;
      out[y * NOISE_SIZE + x] = (top * (1 - fy) + bottom * fy) * 255;
    }
  }
  return out;
}

function smooth(t: number): number {
  return t * t * (3 - 2 * t);
}
