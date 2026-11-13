import { NOISE_SIZE, valueNoise, whiteNoise } from './noise';

/**
 * A tileable greyscale paper grain, meant to be multiplied over the sheet:
 * fine tooth plus broader, softer mottling. White leaves a pixel untouched.
 */
export function createGrain(): HTMLCanvasElement {
  const canvas = document.createElement('canvas');
  canvas.width = NOISE_SIZE;
  canvas.height = NOISE_SIZE;
  const ctx = canvas.getContext('2d');
  if (!ctx) throw new Error('2D canvas is not available');

  const tooth = whiteNoise(0x70071);
  const fibre = valueNoise(4, 0xf1b2e);
  const mottle = valueNoise(64, 0xc10d5);
  const image = ctx.createImageData(NOISE_SIZE, NOISE_SIZE);
  for (let i = 0; i < tooth.length; i++) {
    const v = 255 - (tooth[i] * 0.045 + fibre[i] * 0.07 + mottle[i] * 0.05);
    image.data[i * 4] = v;
    image.data[i * 4 + 1] = v;
    image.data[i * 4 + 2] = v - 2;
    image.data[i * 4 + 3] = 255;
  }
  ctx.putImageData(image, 0, 0);
  return canvas;
}
