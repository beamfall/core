// The expanded desktop sidebar's width (CORE-2734): a per-browser
// convenience, so it lives in localStorage rather than roaming with the
// Profile. Storage may be unavailable (private window, blocked site data), so
// every access falls back silently to the default.
export const SIDEBAR_WIDTH_PREF = "beamfall.sidebarWidth";
export const SIDEBAR_WIDTH_DEFAULT = 200;
export const SIDEBAR_WIDTH_MIN = 168;
export const SIDEBAR_WIDTH_MAX = 360;
/** One arrow-key press. */
export const SIDEBAR_WIDTH_STEP = 16;

/** Round to whole pixels and hold within the limits; anything non-finite is the default. */
export function clampSidebarWidth(width: number): number {
  if (!Number.isFinite(width)) return SIDEBAR_WIDTH_DEFAULT;
  return Math.min(SIDEBAR_WIDTH_MAX, Math.max(SIDEBAR_WIDTH_MIN, Math.round(width)));
}

/**
 * The width a separator key asks for (window splitter pattern): arrows step,
 * Home and End jump to the limits, Enter restores the default. Other keys are
 * not the separator's and return null.
 */
export function sidebarWidthForKey(key: string, width: number): number | null {
  switch (key) {
    case "ArrowLeft":
      return clampSidebarWidth(width - SIDEBAR_WIDTH_STEP);
    case "ArrowRight":
      return clampSidebarWidth(width + SIDEBAR_WIDTH_STEP);
    case "Home":
      return SIDEBAR_WIDTH_MIN;
    case "End":
      return SIDEBAR_WIDTH_MAX;
    case "Enter":
      return SIDEBAR_WIDTH_DEFAULT;
    default:
      return null;
  }
}

export function readSidebarWidth(): number {
  try {
    const stored = window.localStorage.getItem(SIDEBAR_WIDTH_PREF);
    if (stored != null && stored.trim() !== "") return clampSidebarWidth(Number(stored));
  } catch {
    // fall through to the default
  }
  return SIDEBAR_WIDTH_DEFAULT;
}

/** Store a width; the default clears the entry so a later default change reaches it. */
export function writeSidebarWidth(width: number) {
  const clamped = clampSidebarWidth(width);
  try {
    if (clamped === SIDEBAR_WIDTH_DEFAULT) window.localStorage.removeItem(SIDEBAR_WIDTH_PREF);
    else window.localStorage.setItem(SIDEBAR_WIDTH_PREF, String(clamped));
  } catch {
    // a convenience only
  }
}
