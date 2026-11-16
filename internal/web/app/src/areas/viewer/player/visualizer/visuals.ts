// The visuals the Web Viewer rotates through, with the default tuning Oscillux's preset tool
// derives from each one's manifest entry (tools/shader/preset.mjs at the pinned rev).
import manifest from "../../../../../vendor/oscillux/visuals.json";

export interface Visual {
  id: string;
  /** Uniform values a default preset writes. */
  params: [number, number, number];
  palette: [number, number, number, number];
  trailDecay: number;
}

export const VISUALS: readonly Visual[] = manifest.visuals.map((v) => {
  const params: [number, number, number] = [0, 0, 0];
  for (const p of v.params) params[Number(p.uniform.slice("param".length))] = p.default;
  return {
    id: v.id,
    params,
    palette: [v.palette[0] < 0 ? -1 : v.palette[0], 0, v.palette[2], 0],
    trailDecay: v.trailDecay,
  };
});

/** The visual for a track: stable per track, so returning to it shows the same visual. */
export function visualFor(key: string): Visual {
  let h = 2166136261;
  for (let i = 0; i < key.length; i++) h = Math.imul(h ^ key.charCodeAt(i), 16777619);
  return VISUALS[(h >>> 0) % VISUALS.length];
}
