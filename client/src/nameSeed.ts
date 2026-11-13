import { prefs } from './prefs';

const SEED_KEY = 'wetpaint.nameSeed';

/**
 * The number the server turns into this player's name. It is kept so a
 * returning player gets the same name; without storage it lasts the session.
 */
export function nameSeed(): number {
  const stored = Number(prefs.get(SEED_KEY));
  if (Number.isInteger(stored) && stored > 0 && stored <= 0xffffffff) return stored;
  const seed = crypto.getRandomValues(new Uint32Array(1))[0] || 1;
  prefs.set(SEED_KEY, String(seed));
  return seed;
}
