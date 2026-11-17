// The visuals the Web Viewer draws: every visual of the pinned Oscillux release, in its catalog
// order, with the default tuning Oscillux's preset tool derives from each one's manifest entry
// (tools/shader/preset.mjs at the pinned rev). Which one shows is host selection policy.
import manifest from "../../../../../vendor/oscillux/visuals.json";

export interface Visual {
  id: string;
  /** The visual's name in the Oscillux catalog. */
  name: string;
  /** Oscillux marks it calm: suitable when the viewer prefers reduced motion. */
  calm: boolean;
  /** Uniform values a default preset writes. */
  params: [number, number, number];
  palette: [number, number, number, number];
  trailDecay: number;
}

export const VISUALS: readonly Visual[] = manifest.visuals.map((v) => {
  const params: [number, number, number] = [0, 0, 0];
  for (const p of v.params ?? []) params[Number(p.uniform.slice("param".length))] = p.default;
  return {
    id: v.id,
    name: v.name,
    calm: v.calm,
    params,
    palette: [v.palette[0] < 0 ? -1 : v.palette[0], 0, v.palette[2], 0],
    trailDecay: v.trailDecay,
  };
});

const BY_ID = new Map(VISUALS.map((v) => [v.id, v]));

/** The visual with this id, if the release has it. */
export function visualById(id: string): Visual | undefined {
  return BY_ID.get(id);
}

/** The visual for a key: stable per key, so returning to it shows the same visual. */
export function visualFor(key: string): Visual {
  let h = 2166136261;
  for (let i = 0; i < key.length; i++) h = Math.imul(h ^ key.charCodeAt(i), 16777619);
  return VISUALS[(h >>> 0) % VISUALS.length];
}

/** Seconds of drawn time a rotating visual holds before the next one, at a downbeat. */
export const ROTATE_SECONDS = 40;
/** How long past ROTATE_SECONDS rotation waits for a downbeat before switching anyway. */
export const ROTATE_GRACE_SECONDS = 6;

/**
 * The next visual of a rotation: a random one other than `currentId`, drawn from the calm
 * visuals when the viewer prefers reduced motion.
 */
export function nextInRotation(
  currentId: string | null,
  reducedMotion: boolean,
  random: () => number = Math.random,
): Visual {
  const pool = VISUALS.filter((v) => v.id !== currentId && (!reducedMotion || v.calm));
  if (pool.length === 0) return VISUALS[0];
  return pool[Math.min(pool.length - 1, Math.floor(random() * pool.length))];
}
