import { listenerMuted } from "./listenerMute";

/** How long a listener's skip fades the playing track out, and the next one in. */
export const TRACK_FADE_MS = 350;
/** A skip that loads nothing within this long did not happen: the track fades back in. */
export const LOAD_WAIT_MS = 1_500;
/** The next track gets this long to start playing before it fades in anyway. */
export const PLAY_WAIT_MS = 5_000;

// A skip the listener asks for (Play now) fades the playing track out, moves
// on, and fades the next track in once it plays, instead of cutting. Like the
// sleep timer's fade (useSleepTimer) it rides the element's volume, which the
// gapless bridge follows, through one volume hold per element; only the
// listener's own volume changes are saved. A volume the listener sets mid-fade
// is theirs: the fade stops and keeps it, and the skip still happens.

/** Equal-power gain at progress `t` (0 to 1) of a fade out: a quarter cosine. */
export function fadeOutGain(t: number): number {
  return Math.cos((Math.min(1, Math.max(0, t)) * Math.PI) / 2);
}

/** Equal-power gain at progress `t` (0 to 1) of a fade in: a quarter sine. */
export function fadeInGain(t: number): number {
  return Math.sin((Math.min(1, Math.max(0, t)) * Math.PI) / 2);
}

/**
 * A fade's hold on an element's volume. It writes the listener's level times
 * a gain, and tells a level the listener set apart from its own writes.
 */
export type VolumeHold = {
  /** The listener's level when the hold began. */
  readonly base: number;
  /** Sets the element to `base * gain`; does nothing once the hold is gone. */
  write: (gain: number) => void;
  /** Whether the element's volume is no longer the hold's last write: the listener set it. */
  theirs: () => boolean;
  /** Whether this is still the element's hold: a newer fade takes it over. */
  held: () => boolean;
  /** Lets go; with `restore`, the listener's level comes back unless they set their own. */
  release: (restore: boolean) => void;
};

const holds = new WeakMap<HTMLMediaElement, VolumeHold & { level: () => number }>();

/**
 * Holds the element's volume for a fade. A hold already on the element ends
 * without restoring, and the new one starts from the listener's level it kept,
 * so two fades never both write and the level saved is always the listener's.
 */
export function holdVolume(video: HTMLMediaElement): VolumeHold {
  const prior = holds.get(video);
  const base = prior ? prior.level() : video.volume;
  if (prior) holds.delete(video);
  let written = video.volume;
  const theirs = () => video.volume !== written;
  const hold = {
    base,
    write: (gain: number) => {
      if (holds.get(video) !== hold) return;
      written = Math.max(0, Math.min(1, base * gain));
      video.volume = written;
    },
    theirs,
    held: () => holds.get(video) === hold,
    level: () => (theirs() ? video.volume : base),
    release: (restore: boolean) => {
      if (holds.get(video) !== hold) return;
      holds.delete(video);
      if (restore && !theirs()) video.volume = base;
    },
  };
  holds.set(video, hold);
  return hold;
}

/** Whether a fade holds the element's volume. */
export function volumeHeld(video: HTMLMediaElement): boolean {
  return holds.has(video);
}

/** The listener's volume while a fade holds the element lower, for saving and showing. */
export function fadeBaseVolume(video: HTMLMediaElement): number {
  return holds.get(video)?.level() ?? video.volume;
}

type Run = {
  supersede: () => void;
  cancel: () => void;
  finish: () => void;
};
const runs = new WeakMap<HTMLMediaElement, Run>();
// Skips still fading out, on any element: a retried track mounts a new one.
const fadingOut = new Set<Run>();

/** Drops every skip still fading out, without moving on: the listener chose something else first. */
export function cancelFadeAdvance() {
  for (const run of [...fadingOut]) run.cancel();
}

/**
 * Moves every skip still fading out on at once: the track ended, so nothing is
 * left to fade, and the listener's pick owns what plays next. Needs no
 * animation frame, so a hidden tab still advances. Returns whether one did.
 */
export function finishFadeAdvance(): boolean {
  const pending = [...fadingOut];
  for (const run of pending) run.finish();
  return pending.length > 0;
}

/** Runs `advance` after a short fade out, then fades the element back in once it plays again. */
export function fadeAdvance(video: HTMLMediaElement | null, advance: () => void) {
  if (!video) return advance();
  // A second skip mid-fade replaces the first: its level comes back, and only the newer skip happens.
  runs.get(video)?.supersede();
  // A hidden tab runs no animation frames (CORE-3356): there the skip is a cut.
  const cut =
    video.paused || listenerMuted(video) || fadeBaseVolume(video) === 0 || document.hidden;
  // A cut writes nothing, so it takes no hold on the volume.
  let hold: VolumeHold | null = cut ? null : holdVolume(video);

  let frame = 0;
  let wait: ReturnType<typeof setTimeout> | undefined;
  let advanced = false;
  let advancing = false;
  const done = (restore: boolean) => {
    cancelAnimationFrame(frame);
    clearTimeout(wait);
    video.removeEventListener("loadstart", loading);
    video.removeEventListener("playing", fadeIn);
    document.removeEventListener("visibilitychange", hide);
    hold?.release(restore);
    fadingOut.delete(run);
    if (runs.get(video) === run) runs.delete(video);
  };
  // The skip owns what plays next until it fades in, the item changes or the wait runs out:
  // the outgoing track's own end must not advance a second time.
  const go = () => {
    advanced = true;
    advancing = true;
    try {
      advance();
    } finally {
      advancing = false;
    }
  };
  const run: Run = {
    supersede: () => done(true),
    cancel: () => {
      // The skip's own advance opening the item is not a newer choice.
      if (advancing) return;
      if (!advanced) done(true);
      else fadingOut.delete(run);
    },
    // The track ended: the listener's level comes back at once, with no fade
    // in to wait on frames, and the skip happens.
    finish: () => {
      if (advanced) return;
      done(true);
      go();
    },
  };
  const stop = () => done(true);
  // Equal power, so the level sounds even.
  const ramp = (gain: (t: number) => number, then: () => void) => {
    const started = performance.now();
    const step = () => {
      // The listener's own volume, or a newer fade, ends this one early; the skip they asked for still happens.
      if (!hold || hold.theirs() || !hold.held()) return settle();
      const t = Math.min(1, (performance.now() - started) / TRACK_FADE_MS);
      hold.write(gain(t));
      if (t < 1) frame = requestAnimationFrame(step);
      else then();
    };
    frame = requestAnimationFrame(step);
  };
  function fadeIn() {
    video?.removeEventListener("loadstart", loading);
    video?.removeEventListener("playing", fadeIn);
    clearTimeout(wait);
    ramp(fadeInGain, stop);
  }
  function loading() {
    video?.removeEventListener("loadstart", loading);
    clearTimeout(wait);
    wait = setTimeout(fadeIn, PLAY_WAIT_MS);
  }
  // Ends the fade where it stands and skips now if it has not, still owning what
  // plays next until the item changes or the wait runs out.
  function settle() {
    cancelAnimationFrame(frame);
    clearTimeout(wait);
    video?.removeEventListener("loadstart", loading);
    video?.removeEventListener("playing", fadeIn);
    document.removeEventListener("visibilitychange", hide);
    // The volume is the listener's again: the fade writes and restores nothing more.
    hold?.release(false);
    hold = null;
    // Armed first, so a skip that throws still lets go.
    wait = setTimeout(stop, LOAD_WAIT_MS);
    if (!advanced) go();
  }
  // Hidden mid-fade, the frames stop: the skip happens now, at the listener's level.
  function hide() {
    if (!document.hidden) return;
    hold?.release(true);
    hold = null;
    settle();
  }
  runs.set(video, run);
  fadingOut.add(run);
  // A cut owns what plays next too: the outgoing track's end must not act as well.
  if (cut) return settle();
  document.addEventListener("visibilitychange", hide);
  ramp(fadeOutGain, () => {
    video.addEventListener("loadstart", loading);
    video.addEventListener("playing", fadeIn);
    wait = setTimeout(fadeIn, LOAD_WAIT_MS);
    go();
  });
}
