// Subtitle appearance, stored per Profile under the subtitle.* client preference
// keys (CORE-1681, openspec/changes/core-1681-subtitle-appearance). The keys and
// value lists below are the cross-client contract: native clients read the same
// keys from clientPrefs and map each value to their own renderer. The server
// treats them as opaque strings.
//
//   subtitle.size        "0.85" | "1" | "1.25" | "1.5"   scale of the base cue size
//   subtitle.background  "none" | "dark" | "solid"      none, ~55% black, ~92% black
//   subtitle.font        "sans" | "serif" | "mono" | "casual"
//   subtitle.color       lowercase #rrggbb from SUBTITLE_COLORS
//   subtitle.opacity     "1" | "0.75" | "0.5" | "0.25"  text (not background) opacity
//   subtitle.edge        "none" | "outline" | "drop-shadow" | "raised" | "depressed"
//   subtitle.position    "auto" (as authored) | "10" | "25" | "50" | "80": the
//                        cue's bottom edge, in percent of picture height above
//                        the bottom
//
// A value equal to its default is written as "" (clearing the key), and an
// unknown value reads as the default, so older and newer clients stay
// compatible.

import type { ClientPref } from "@/lib/graphql/types";
import { PREF_KEYS } from "./preferences";

export const SUBTITLE_SIZES = ["0.85", "1", "1.25", "1.5"] as const;
export const SUBTITLE_BACKGROUNDS = ["none", "dark", "solid"] as const;
export const SUBTITLE_FONTS = ["sans", "serif", "mono", "casual"] as const;
export const SUBTITLE_COLORS = [
  "#ffffff",
  "#ffff00",
  "#00ff00",
  "#00ffff",
  "#0000ff",
  "#ff00ff",
  "#ff0000",
  "#000000",
] as const;
export const SUBTITLE_OPACITIES = ["1", "0.75", "0.5", "0.25"] as const;
export const SUBTITLE_EDGES = ["none", "outline", "drop-shadow", "raised", "depressed"] as const;
export const SUBTITLE_POSITIONS = ["auto", "10", "25", "50", "80"] as const;

export interface SubtitleAppearance {
  size: (typeof SUBTITLE_SIZES)[number];
  background: (typeof SUBTITLE_BACKGROUNDS)[number];
  font: (typeof SUBTITLE_FONTS)[number];
  color: (typeof SUBTITLE_COLORS)[number];
  opacity: (typeof SUBTITLE_OPACITIES)[number];
  edge: (typeof SUBTITLE_EDGES)[number];
  position: (typeof SUBTITLE_POSITIONS)[number];
}

export type SubtitleAppearanceField = keyof SubtitleAppearance;

/** Options per field, in panel order. */
export const SUBTITLE_APPEARANCE_OPTIONS: {
  readonly [F in SubtitleAppearanceField]: readonly SubtitleAppearance[F][];
} = {
  size: SUBTITLE_SIZES,
  background: SUBTITLE_BACKGROUNDS,
  font: SUBTITLE_FONTS,
  color: SUBTITLE_COLORS,
  opacity: SUBTITLE_OPACITIES,
  edge: SUBTITLE_EDGES,
  position: SUBTITLE_POSITIONS,
};

export const SUBTITLE_APPEARANCE_DEFAULTS: SubtitleAppearance = {
  size: "1",
  background: "dark",
  font: "sans",
  color: "#ffffff",
  opacity: "1",
  edge: "none",
  position: "auto",
};

export const SUBTITLE_APPEARANCE_KEYS: { readonly [F in SubtitleAppearanceField]: string } = {
  size: PREF_KEYS.subtitleSize,
  background: PREF_KEYS.subtitleBackground,
  font: PREF_KEYS.subtitleFont,
  color: PREF_KEYS.subtitleColor,
  opacity: PREF_KEYS.subtitleOpacity,
  edge: PREF_KEYS.subtitleEdge,
  position: PREF_KEYS.subtitlePosition,
};

export const SUBTITLE_APPEARANCE_FIELDS = Object.keys(
  SUBTITLE_APPEARANCE_KEYS,
) as SubtitleAppearanceField[];

function pick<F extends SubtitleAppearanceField>(
  field: F,
  raw: string | undefined,
): SubtitleAppearance[F] {
  const options = SUBTITLE_APPEARANCE_OPTIONS[field] as readonly string[];
  return options.includes(raw ?? "")
    ? (raw as SubtitleAppearance[F])
    : SUBTITLE_APPEARANCE_DEFAULTS[field];
}

/** Fold a raw clientPrefs list into the typed appearance; unknown values read as defaults. */
export function resolveSubtitleAppearance(prefs: ClientPref[]): SubtitleAppearance {
  const map = new Map(prefs.map((p) => [p.key, p.value]));
  return {
    size: pick("size", map.get(SUBTITLE_APPEARANCE_KEYS.size)),
    background: pick("background", map.get(SUBTITLE_APPEARANCE_KEYS.background)),
    font: pick("font", map.get(SUBTITLE_APPEARANCE_KEYS.font)),
    color: pick("color", map.get(SUBTITLE_APPEARANCE_KEYS.color)),
    opacity: pick("opacity", map.get(SUBTITLE_APPEARANCE_KEYS.opacity)),
    edge: pick("edge", map.get(SUBTITLE_APPEARANCE_KEYS.edge)),
    position: pick("position", map.get(SUBTITLE_APPEARANCE_KEYS.position)),
  };
}

/** The stored value for one field: "" (clear the key) for the default or an unknown value. */
export function encodeSubtitleAppearance(field: SubtitleAppearanceField, value: string): string {
  const options = SUBTITLE_APPEARANCE_OPTIONS[field] as readonly string[];
  return options.includes(value) && value !== SUBTITLE_APPEARANCE_DEFAULTS[field] ? value : "";
}
