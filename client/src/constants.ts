// Rendering-only values. Everything that affects gameplay arrives in the
// Welcome message (see movement.ts); nothing here may change what happens,
// only how it looks, sounds and feels.

/** Other entities are drawn this far in the past, interpolated. */
export const INTERP_DELAY_MS = 100;

/** Device pixel ratio is capped so phones do not render 3× canvases. */
export const MAX_DPR = 2;

export const VIEW = {
  /** The viewport never shows more of the world than this (world units). */
  maxW: 1600,
  maxH: 1000,
  /** How far past the sheet's edge the camera may look. */
  overscroll: 90,
  /** Camera follow rate, 1/s. */
  follow: 5,
} as const;

export const POINTER = {
  /** Distance (world units) at which the pointer asks for full throttle. */
  fullThrottle: 140,
  /** Inside this distance of the pointer the drop rests. */
  deadzone: 6,
} as const;

export const PAINT = {
  /** Offscreen paint canvas pixels per grid cell. */
  pxPerCell: 3,
  /** Paint is re-rendered in square tiles of this many cells. */
  tileCells: 8,
  /** Milliseconds per frame spent re-rendering dirty tiles. */
  frameBudgetMs: 4,
  /** Tiles visited per frame by the rolling refresh that shows fading. */
  refreshTilesPerFrame: 8,
  /**
   * Coverage is a pixel's alpha relative to the strongest paint next to it.
   * Below `edgeLo` it is clear paper, above `edgeHi` fully inside a stroke.
   */
  edgeLo: 0.22,
  edgeHi: 0.62,
  /** How far low-frequency noise pushes the stroke edge in and out. */
  edgeNoise: 0.22,
  /** Opacity of full-strength paint; weaker paint follows a square root. */
  bodyMax: 0.84,
  /** Edge pooling: gain on the alpha step to a neighbour, and its effect. */
  poolGain: 2.6,
  poolDarken: 0.24,
  poolAlpha: 0.22,
  /** Brightness variation within a stroke (±half of this). */
  variation: 0.26,
  /** Pigment granulation: alpha variation at pixel scale. */
  granulation: 0.16,
} as const;

export const COLORS = {
  desk: '#d9cfbb',
  paper: '#f8f3e7',
  pencil: '#55515c',
  pencilSoft: 'rgba(85, 81, 92, 0.55)',
  water: 'rgba(132, 190, 226, 0.5)',
  waterRim: 'rgba(76, 132, 180, 0.75)',
  blotter: 'rgba(205, 188, 152, 0.5)',
  beetle: 'rgba(72, 68, 80, 0.9)',
  shadow: 'rgba(60, 55, 70, 0.14)',
} as const;

export const FONTS = {
  hand: '"Patrick Hand", "Comic Sans MS", cursive',
  pencil: '"Caveat", "Patrick Hand", cursive',
} as const;

export const POSTCARD = {
  width: 2000,
  /** Caption text height in postcard pixels. */
  captionPx: 46,
} as const;

/** Mini-map size in CSS pixels (4:3, like the sheet). */
export const MINIMAP = { width: 148, height: 111, refreshMs: 400 } as const;

/** Name labels kept beside blooms. */
export const MAX_LABELS = 80;
