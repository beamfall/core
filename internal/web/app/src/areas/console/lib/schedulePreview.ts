// Next allow / next block preview for a managed-Profile access schedule
// (ADR-0087 §30). Windows are weekday (0=Sunday) + minute-of-day, interpreted in
// the SERVER's local time (the server stores no timezone offset, ADR-0032). This
// is a pure, weekly-wrap resolver: given a reference weekday+minute, it returns
// the next boundary where access flips. The caller frames the result as
// server-local time and does NOT render an authoritative "blocked right now"
// badge, since the Console user's timezone may differ from the server's.

export interface AccessWindow {
  weekday: number; // 0 = Sunday … 6 = Saturday
  startMinute: number; // inclusive minute-of-day
  endMinute: number; // exclusive minute-of-day
}

const WEEK_MINUTES = 7 * 24 * 60;
const DAY_MINUTES = 24 * 60;

/** The absolute minute-of-week for a weekday + minute-of-day. */
function weekMinute(weekday: number, minute: number): number {
  return ((weekday % 7) * DAY_MINUTES + minute + WEEK_MINUTES) % WEEK_MINUTES;
}

/**
 * isAllowedAt — true when the given week-minute falls inside any window. An
 * empty schedule means "always allowed" (matching the server/UI contract).
 */
export function isAllowedAt(windows: AccessWindow[], wm: number): boolean {
  if (windows.length === 0) return true;
  return windows.some((w) => {
    const start = weekMinute(w.weekday, w.startMinute);
    // A window's end can wrap past the week boundary or sit before its start
    // (e.g. 22:00–02:00); normalize the span length and test by offset.
    let span = w.endMinute - w.startMinute;
    if (span <= 0) span += DAY_MINUTES; // overnight window within a day
    const offset = (wm - start + WEEK_MINUTES) % WEEK_MINUTES;
    return offset < span;
  });
}

export interface NextTransition {
  /** Whether access is ALLOWED at/after the transition boundary. */
  allowed: boolean;
  /** The boundary weekday (0=Sunday) and minute-of-day, server-local. */
  weekday: number;
  minute: number;
  /** Minutes from the reference time until the boundary. */
  inMinutes: number;
}

/**
 * nextTransition scans minute-by-minute (capped to one week) from the reference
 * time for the next flip in allowed-state. Returns null when the schedule is
 * constant over the whole week (empty = always allowed, or fully-covering
 * windows = always allowed; a schedule that never allows flips at its first
 * window). One-minute granularity matches the schedule's minute resolution and
 * a full week is 10080 iterations — trivial.
 */
export function nextTransition(
  windows: AccessWindow[],
  nowWeekday: number,
  nowMinute: number,
): NextTransition | null {
  if (windows.length === 0) return null; // always allowed, never flips
  const start = weekMinute(nowWeekday, nowMinute);
  const current = isAllowedAt(windows, start);
  for (let i = 1; i <= WEEK_MINUTES; i++) {
    const wm = (start + i) % WEEK_MINUTES;
    if (isAllowedAt(windows, wm) !== current) {
      return {
        allowed: !current,
        weekday: Math.floor(wm / DAY_MINUTES),
        minute: wm % DAY_MINUTES,
        inMinutes: i,
      };
    }
  }
  return null; // constant over the whole week
}

/** Format a minute-of-day as HH:MM (24h, zero-padded). */
export function formatMinute(minute: number): string {
  const h = Math.floor(minute / 60) % 24;
  const m = minute % 60;
  return `${String(h).padStart(2, "0")}:${String(m).padStart(2, "0")}`;
}
