// "Are you still watching?" streak (CORE-1689, docs/still-watching/README.md).
//
// The streak counts items in a row that the Up Next countdown started on its
// own. It lives in memory for this tab: not synced, gone on reload. Any input
// in the player resets it, and so does starting an item any other way (picking
// one, Play now, Continue on the prompt). The player route remounts per item,
// so the count is module state rather than component state.

type Listener = () => void;

let streak = 0;
// The item the last countdown expiry started; reaching any other item resets.
let autoStartedID: string | null = null;
// The item the player last reported, so a repeated report (a re-render or a
// development double effect) is not mistaken for a new start.
let currentID: string | null = null;
const listeners = new Set<Listener>();

function emit(): void {
  for (const listener of listeners) listener();
}

function set(value: number): void {
  if (value === streak) return;
  streak = value;
  emit();
}

export function subscribeStillWatching(listener: Listener): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function stillWatchingStreak(): number {
  return streak;
}

/** The Up Next countdown ran out and is starting `nextID` on its own. */
export function noteAutoAdvance(nextID: string): void {
  autoStartedID = nextID;
  set(streak + 1);
}

/** The player loaded `mediaID`; unless the countdown started it, the run ends. */
export function noteItemStarted(mediaID: string): void {
  if (mediaID === currentID) return;
  currentID = mediaID;
  const auto = autoStartedID !== null && autoStartedID === mediaID;
  autoStartedID = null;
  if (!auto) set(0);
}

/** Viewer input (a tap, click or key) or Continue on the prompt. */
export function resetStillWatching(): void {
  autoStartedID = null;
  set(0);
}

/** Test seam: forget everything, as a reload would. */
export function clearStillWatchingForTest(): void {
  autoStartedID = null;
  currentID = null;
  set(0);
}

/**
 * The prompt replaces the next autoplay when the setting is on, something is
 * queued, and the run of autoplayed items has reached the setting.
 */
export function shouldAskStillWatching(after: number, current: number, hasNext: boolean): boolean {
  return after > 0 && hasNext && current >= after;
}
