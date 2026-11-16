import type { CapabilityProfile } from "./capabilityProfile";

// The quality step the viewer last picked in this browser (CORE-1971, Plex
// parity). It is a per-device convenience: a blocked or cleared store means
// Auto. The step is kept as the rendition id the menu marks and the bandwidth
// sent as capabilityProfile.maxStreamingBitrate, so the server caps every
// later playback at that step, never above an admin cap.
const QUALITY_CHOICE_KEY = "beamfall.quality";

export interface QualityChoice {
  id: string;
  maxBitrate: number;
}

function validChoice(value: unknown): QualityChoice | null {
  if (!value || typeof value !== "object") return null;
  const { id, maxBitrate } = value as Partial<QualityChoice>;
  if (typeof id !== "string" || !id) return null;
  if (typeof maxBitrate !== "number" || !Number.isSafeInteger(maxBitrate) || maxBitrate <= 0) {
    return null;
  }
  return { id, maxBitrate };
}

export function readQualityChoice(): QualityChoice | null {
  try {
    const raw = window.localStorage.getItem(QUALITY_CHOICE_KEY);
    return raw ? validChoice(JSON.parse(raw)) : null;
  } catch {
    return null;
  }
}

/** Remember a step, or forget it (Auto) when choice is null. */
export function writeQualityChoice(choice: QualityChoice | null): void {
  try {
    const valid = validChoice(choice);
    if (valid) window.localStorage.setItem(QUALITY_CHOICE_KEY, JSON.stringify(valid));
    else window.localStorage.removeItem(QUALITY_CHOICE_KEY);
  } catch {
    // Remembering the choice is optional.
  }
}

/** The profile with the remembered step's ceiling, or unchanged under Auto. */
export function withQualityCeiling(profile: CapabilityProfile): CapabilityProfile {
  const choice = readQualityChoice();
  return choice ? { ...profile, maxStreamingBitrate: choice.maxBitrate } : profile;
}
