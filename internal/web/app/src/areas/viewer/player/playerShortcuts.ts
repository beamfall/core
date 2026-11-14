// The web player's keyboard shortcuts (CORE-1682). One table drives both the
// key handler (usePlayerShortcuts) and the `?` help sheet, so the sheet can
// never list a key the handler ignores. Resolution is pure: a key event in, an
// action (or null) out. Ctrl/Alt/Meta chords always stay with the browser.
// Shift is accepted only where it is part of the character: `?`, `<`, `>` and
// digits on layouts that shift them, plus Shift+N / Shift+P for the queue.

export type ShortcutAction =
  | "togglePlay"
  | "jumpBack"
  | "jumpForward"
  | "skipBack"
  | "skipForward"
  | "volumeUp"
  | "volumeDown"
  | "toggleMute"
  | "toggleFullscreen"
  | "frameBack"
  | "frameForward"
  | "slower"
  | "faster"
  | "toggleSubtitles"
  | "nextSubtitle"
  | "nextAudio"
  | "previousChapter"
  | "nextChapter"
  | "previousItem"
  | "nextItem"
  | "seekPercent"
  | "help";

export interface ResolvedShortcut {
  action: ShortcutAction;
  /** 0–90, for seekPercent only. */
  percent?: number;
}

/** Seconds J and L jump, whatever the skip-button interval is. */
export const JUMP_SECONDS = 10;
/** The frame step until one is measured: the wire carries no frame rate, and 1/30 s is common. */
export const FRAME_SECONDS = 1 / 30;

const UNSHIFTED: Record<string, ShortcutAction> = {
  " ": "togglePlay",
  k: "togglePlay",
  j: "jumpBack",
  l: "jumpForward",
  arrowleft: "skipBack",
  arrowright: "skipForward",
  arrowup: "volumeUp",
  arrowdown: "volumeDown",
  m: "toggleMute",
  f: "toggleFullscreen",
  ",": "frameBack",
  ".": "frameForward",
  c: "toggleSubtitles",
  s: "nextSubtitle",
  a: "nextAudio",
  pageup: "previousChapter",
  pagedown: "nextChapter",
};

// Keys whose character already carries the shift, so they answer either way.
const SHIFT_NEUTRAL: Record<string, ShortcutAction> = {
  "<": "slower",
  ">": "faster",
  "?": "help",
};

const SHIFTED: Record<string, ShortcutAction> = {
  p: "previousItem",
  n: "nextItem",
};

export function resolvePlayerShortcut(
  event: Pick<KeyboardEvent, "key" | "altKey" | "ctrlKey" | "metaKey" | "shiftKey">,
): ResolvedShortcut | null {
  if (event.altKey || event.ctrlKey || event.metaKey) return null;
  const key = event.key.toLowerCase();
  if (/^[0-9]$/.test(key)) return { action: "seekPercent", percent: Number(key) * 10 };
  const neutral = SHIFT_NEUTRAL[key];
  if (neutral) return { action: neutral };
  const action = event.shiftKey ? SHIFTED[key] : UNSHIFTED[key];
  return action ? { action } : null;
}

/** The help sheet's rows, in display order. `keys` are shown verbatim except "space". */
export type ShortcutLabelKey =
  | "shortcutPlayPause"
  | "shortcutJump"
  | "shortcutSkip"
  | "shortcutSeekPercent"
  | "shortcutFrameStep"
  | "shortcutSpeed"
  | "shortcutVolume"
  | "shortcutMute"
  | "shortcutFullscreen"
  | "shortcutSubtitlesToggle"
  | "shortcutNextSubtitle"
  | "shortcutNextAudio"
  | "shortcutChapter"
  | "shortcutQueueItem"
  | "shortcutHelp";

export const SHORTCUT_SHEET: ReadonlyArray<{ keys: string[]; label: ShortcutLabelKey }> = [
  { keys: ["space", "K"], label: "shortcutPlayPause" },
  { keys: ["J", "L"], label: "shortcutJump" },
  { keys: ["←", "→"], label: "shortcutSkip" },
  { keys: ["0", "9"], label: "shortcutSeekPercent" },
  { keys: [",", "."], label: "shortcutFrameStep" },
  { keys: ["<", ">"], label: "shortcutSpeed" },
  { keys: ["↓", "↑"], label: "shortcutVolume" },
  { keys: ["M"], label: "shortcutMute" },
  { keys: ["F"], label: "shortcutFullscreen" },
  { keys: ["C"], label: "shortcutSubtitlesToggle" },
  { keys: ["S"], label: "shortcutNextSubtitle" },
  { keys: ["A"], label: "shortcutNextAudio" },
  { keys: ["PgUp", "PgDn"], label: "shortcutChapter" },
  { keys: ["⇧P", "⇧N"], label: "shortcutQueueItem" },
  { keys: ["?"], label: "shortcutHelp" },
];

/** Frame timing measured from presented frames; null until observed. */
export interface FrameClock {
  /** Seconds between consecutive frames. */
  frame: number | null;
  /** Media time of the frame on screen. */
  mediaTime: number | null;
}

/**
 * Where a one-frame step lands: the middle of the neighbouring frame, so
 * rounding at a frame boundary cannot leave the picture unchanged. Without a
 * current frame timestamp it steps `currentTime` by the frame (or 1/30 s).
 */
export function frameStepTarget(clock: FrameClock, currentTime: number, direction: -1 | 1) {
  const frame = clock.frame ?? FRAME_SECONDS;
  const shown = clock.mediaTime;
  if (shown === null || Math.abs(shown - currentTime) > frame) {
    return currentTime + direction * frame;
  }
  return shown + direction * frame + frame / 2;
}

/** The next speed in `speeds` (ascending) from `current`, or null at the end. */
export function stepSpeed(speeds: readonly number[], current: number, direction: -1 | 1) {
  if (direction > 0) return speeds.find((speed) => speed > current + 1e-6) ?? null;
  for (let i = speeds.length - 1; i >= 0; i -= 1) {
    if (speeds[i] < current - 1e-6) return speeds[i];
  }
  return null;
}

interface TrackOption {
  id: string;
  selected: boolean;
  disabled: boolean;
}

/** The option after the selected one, wrapping and skipping disabled rows. */
export function nextTrackId(options: readonly TrackOption[]): string | null {
  if (options.length === 0) return null;
  const from = options.findIndex((option) => option.selected);
  for (let step = 1; step <= options.length; step += 1) {
    const option = options[(from + step + options.length) % options.length];
    if (!option.disabled && !option.selected) return option.id;
  }
  return null;
}

/**
 * Captions toggle: off when a track shows, else back on to `remembered` (the
 * last shown track) or the first selectable one. Subtitle ids are "off" plus
 * track indices, as useLiveTracks stamps them.
 */
export function subtitleToggleId(
  options: readonly TrackOption[],
  remembered: string | null,
): string | null {
  const showing = options.find((option) => option.selected && option.id !== "off");
  if (showing) return "off";
  const usable = options.filter((option) => option.id !== "off" && !option.disabled);
  return (usable.find((option) => option.id === remembered) ?? usable[0])?.id ?? null;
}
