import { listenerMuted } from "./listenerMute";

/** How long a listener's skip fades the playing track out, and the next one in. */
export const TRACK_FADE_MS = 350;
/** A skip that loads nothing within this long did not happen: the track fades back in. */
export const LOAD_WAIT_MS = 1_500;
/** The next track gets this long to start playing before it fades in anyway. */
export const PLAY_WAIT_MS = 5_000;

// A skip the listener asks for (Play now) fades the playing track out, moves
// on, and fades the next track in once it plays, instead of cutting. Like the
// sleep timer's fade (CORE-2960) it rides the element's volume, which the
// gapless bridge follows; only the listener's own volume changes are saved.
// A volume the listener sets mid-fade is theirs: the fade stops and keeps it,
// and the skip still happens.

type Run = {
  level: () => number;
  supersede: () => void;
  cancel: () => void;
  finish: () => void;
};
const runs = new WeakMap<HTMLMediaElement, Run>();
// Skips still fading out, on any element: a retried track mounts a new one.
const fadingOut = new Set<Run>();

/** The listener's volume while a skip fade holds the element lower, for saving. */
export function fadeBaseVolume(video: HTMLMediaElement): number {
  return runs.get(video)?.level() ?? video.volume;
}

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
  const base = video.volume;
  if (video.paused || listenerMuted(video) || base === 0) return advance();

  let written = base;
  let frame = 0;
  let wait: ReturnType<typeof setTimeout> | undefined;
  let advanced = false;
  const write = (level: number) => {
    written = Math.max(0, Math.min(1, base * level));
    video.volume = written;
  };
  const theirs = () => video.volume !== written;
  const done = (restore: boolean) => {
    cancelAnimationFrame(frame);
    clearTimeout(wait);
    video.removeEventListener("loadstart", loading);
    video.removeEventListener("playing", fadeIn);
    if (restore && !theirs()) video.volume = base;
    fadingOut.delete(run);
    if (runs.get(video) === run) runs.delete(video);
  };
  const go = () => {
    advanced = true;
    fadingOut.delete(run);
    advance();
  };
  const run: Run = {
    // A level the listener set while the next track loads is theirs.
    level: () => (theirs() ? video.volume : base),
    supersede: () => done(true),
    cancel: () => {
      if (!advanced) done(true);
    },
    finish: () => {
      if (advanced) return;
      cancelAnimationFrame(frame);
      faded();
    },
  };
  const stop = () => done(true);
  // Equal power: out follows a quarter cosine, in a quarter sine, so the level sounds even.
  const ramp = (shape: (t: number) => number, then: () => void) => {
    const started = performance.now();
    const step = () => {
      // The listener's own volume ends the fade early; the skip they asked for still happens.
      if (theirs()) {
        done(false);
        if (!advanced) go();
        return;
      }
      const t = Math.min(1, (performance.now() - started) / TRACK_FADE_MS);
      write(shape((t * Math.PI) / 2));
      if (t < 1) frame = requestAnimationFrame(step);
      else then();
    };
    frame = requestAnimationFrame(step);
  };
  function fadeIn() {
    video?.removeEventListener("loadstart", loading);
    video?.removeEventListener("playing", fadeIn);
    clearTimeout(wait);
    ramp(Math.sin, stop);
  }
  function loading() {
    video?.removeEventListener("loadstart", loading);
    clearTimeout(wait);
    wait = setTimeout(fadeIn, PLAY_WAIT_MS);
  }
  runs.set(video, run);
  fadingOut.add(run);
  function faded() {
    video?.addEventListener("loadstart", loading);
    video?.addEventListener("playing", fadeIn);
    wait = setTimeout(fadeIn, LOAD_WAIT_MS);
    go();
  }
  ramp(Math.cos, faded);
}
