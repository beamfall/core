import { type RefObject, useCallback, useEffect, useRef, useState } from "react";
import { getIdentityEpochSignal } from "@/lib/api";
import { playbackDurationSeconds } from "../lib/transport";
import {
  longPauseAt,
  nextPauseStart,
  noteSleepStop,
  parseSilenceMap,
  type SilentSpan,
  type SleepTimer,
  sleepFade,
  sleepFadeSeconds,
  sleepRemaining,
} from "./listening";
import {
  cancelFadeAdvance,
  dropFadeAdvance,
  holdVolume,
  type VolumeHold,
  volumeHeld,
} from "./trackFade";

type VideoRef = RefObject<HTMLVideoElement | null>;

/**
 * Applies the listener's speed with pitch preserved (CORE-2960) while
 * `enabled`. The default rate is set too, so a reload of the source keeps it.
 * When it stops applying, the element goes back to 1x, so a Watch Party lead
 * does not hand the listener's own speed to the room; a rate something else
 * set meanwhile (a following party) is left alone.
 */
export function usePlaybackSpeed(videoRef: VideoRef, speed: number, enabled: boolean): void {
  useEffect(() => {
    const video = videoRef.current;
    if (!enabled || !video) return;
    video.preservesPitch = true;
    video.defaultPlaybackRate = speed;
    video.playbackRate = speed;
    return () => {
      video.defaultPlaybackRate = 1;
      if (video.playbackRate === speed) video.playbackRate = 1;
    };
  }, [videoRef, speed, enabled]);
}

// null: the map could not be read, so the item counts as not mapped.
const silenceMaps = new Map<string, Promise<SilentSpan[] | null>>();

function loadSilenceMap(url: string): Promise<SilentSpan[] | null> {
  const cached = silenceMaps.get(url);
  if (cached) return cached;
  const request = fetch(url, { credentials: "same-origin", signal: getIdentityEpochSignal() })
    .then((response) => {
      if (!response.ok) throw new Error(`silence map ${response.status}`);
      return response.json();
    })
    .then(parseSilenceMap)
    .catch(() => {
      silenceMaps.delete(url);
      return null;
    });
  silenceMaps.set(url, request);
  return request;
}

/** Test seam: forget every fetched silence map. */
export function resetSilenceMaps(): void {
  silenceMaps.clear();
}

/**
 * Removes long pauses (CORE-2960): with `enabled` and a silence map, playback
 * jumps over every silent span of 0.8 s or more. Offsets are file time, the
 * element's own clock, so resume positions are unchanged. Returns whether the
 * item's map was read and the seconds saved this session.
 */
export function useLongPauseSkip(
  videoRef: VideoRef,
  url: string | undefined,
  enabled: boolean,
): { mapped: boolean; saved: number } {
  const [map, setMap] = useState<{ url: string; spans: SilentSpan[] | null } | null>(null);
  const [saved, setSaved] = useState(0);
  useEffect(() => {
    if (!enabled || !url) return;
    let current = true;
    void loadSilenceMap(url).then((spans) => {
      if (current) setMap({ url, spans });
    });
    return () => {
      current = false;
    };
  }, [url, enabled]);
  const spans = map && map.url === url ? map.spans : null;

  useEffect(() => {
    const video = videoRef.current;
    if (!enabled || !video || !spans?.length) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    // The span last jumped over, so a seek the element clamps short (a
    // transcode not yet that far) plays the rest instead of seeking again, and
    // where that jump started, so only the time actually skipped is counted.
    let jumped: SilentSpan | null = null;
    let jumpFrom: number | null = null;
    const check = () => {
      clearTimeout(timer);
      const time = video.currentTime;
      const span = longPauseAt(spans, time);
      if (span && span !== jumped && !video.seeking) {
        jumped = span;
        jumpFrom = time;
        video.currentTime = span.end;
        return;
      }
      // timeupdate fires a few times a second; a timer lands on the next
      // pause's start so little of it plays first.
      const next = nextPauseStart(spans, time);
      if (next === undefined || video.paused) return;
      const wait = ((next - time) / (video.playbackRate || 1)) * 1000;
      timer = setTimeout(check, Math.max(0, wait));
    };
    const seeked = () => {
      if (jumpFrom === null) {
        jumped = null; // The listener's own seek: every pause applies again.
      } else {
        const gained = video.currentTime - jumpFrom;
        if (gained > 0) setSaved((total) => total + gained);
        jumpFrom = null;
      }
      check();
    };
    const events = ["timeupdate", "play", "pause", "ratechange"] as const;
    for (const event of events) video.addEventListener(event, check);
    video.addEventListener("seeked", seeked);
    check();
    return () => {
      clearTimeout(timer);
      for (const event of events) video.removeEventListener(event, check);
      video.removeEventListener("seeked", seeked);
    };
  }, [videoRef, enabled, spans]);
  return { mapped: spans !== null, saved };
}

/** How often the volume steps while the sleep timer fades it, so the fade sounds smooth. */
export const SLEEP_FADE_STEP_MS = 50;
/** How often the sleep timer checks the time left. */
const SLEEP_POLL_MS = 250;

/**
 * The sleep timer (CORE-2960): after a number of minutes, at the end of this
 * item or at the end of this chapter, the volume fades out over the last
 * stretch (up to 45 seconds, equal power), then playback pauses and the
 * listener's volume comes back, so the next play is not silent. A volume the
 * listener sets, a pause or a play, or a Play now skip ends the fade cleanly:
 * the level is theirs again, and what is left of the timer fades afresh from
 * it, so the stop never cuts. Cancelling or changing the timer gives the level
 * straight back. An item or chapter timer
 * ends with its item. It runs only while `enabled`; `durationSeconds` is the
 * server's duration, which a live transcode's element does not know yet.
 */
export function useSleepTimer(
  videoRef: VideoRef,
  mediaID: string,
  enabled: boolean,
  durationSeconds: number | undefined,
): {
  timer: SleepTimer | null;
  remaining: number | null;
  setTimer: (timer: SleepTimer | null) => void;
} {
  const [state, setState] = useState<{ timer: SleepTimer; mediaID: string } | null>(null);
  const [remaining, setRemaining] = useState<number | null>(null);
  const timer =
    state && (state.timer.kind === "minutes" || state.mediaID === mediaID) ? state.timer : null;
  const setTimer = useCallback(
    (next: SleepTimer | null) => setState(next ? { timer: next, mediaID } : null),
    [mediaID],
  );
  // Leaving the item ends its item or chapter timer, so coming back later does
  // not bring it back.
  useEffect(() => {
    setState((current) =>
      current && current.timer.kind !== "minutes" && current.mediaID !== mediaID ? null : current,
    );
  }, [mediaID]);
  const durationRef = useRef(durationSeconds);
  durationRef.current = durationSeconds;

  useEffect(() => {
    const first = videoRef.current;
    if (!timer || !first || !enabled) {
      setRemaining(null);
      return;
    }
    // The element the timer fades and pauses. Try again after an error mounts a
    // new one, and the timer moves to it (CORE-3365).
    let video = first;
    // The fade's hold on the volume, and the seconds left when it began: a
    // fade the listener interrupts starts again from their level over what is left.
    let hold: VolumeHold | null = null;
    let from = 0;
    // The fade's length, fixed by the time left when the timer is first measured.
    let fadeSeconds: number | null =
      timer.kind === "minutes" ? sleepFadeSeconds(timer.minutes * 60) : null;
    let finish: ReturnType<typeof setTimeout> | undefined;
    let stepping: ReturnType<typeof setInterval> | undefined;
    const letGo = () => {
      clearInterval(stepping);
      stepping = undefined;
      hold?.release(true);
      hold = null;
    };
    const measure = () =>
      sleepRemaining(timer, Date.now(), {
        currentTime: video.currentTime,
        playbackRate: video.playbackRate,
        duration: playbackDurationSeconds(durationRef.current, video.duration) || Number.NaN,
      });
    const fade = (left: number) => {
      // The listener's own volume, or a skip's fade, ends this fade, keeping their level.
      if (hold && (hold.theirs() || !hold.held())) {
        hold.release(false);
        hold = null;
      }
      if (fadeSeconds === null || video.paused) {
        letGo();
        return;
      }
      if (left >= fadeSeconds) {
        // The next poll could land past the fade's start, so a short fade steps from
        // here and still begins on time (CORE-3364).
        if (left - SLEEP_POLL_MS / 1000 > fadeSeconds) letGo();
        else stepping ??= setInterval(() => fade(measure()), SLEEP_FADE_STEP_MS);
        return;
      }
      if (!hold) {
        if (volumeHeld(video)) return; // A skip is fading; this fade starts once it is done.
        hold = holdVolume(video);
        from = left;
        stepping ??= setInterval(() => fade(measure()), SLEEP_FADE_STEP_MS);
      }
      hold.write(sleepFade(left, from));
    };
    // A pause gives the level back at once; a play starts what is left of the fade afresh.
    const playState = () => tick();
    const bind = (next: HTMLVideoElement) => {
      video.removeEventListener("pause", playState);
      video.removeEventListener("play", playState);
      letGo();
      video = next;
      video.addEventListener("pause", playState);
      video.addEventListener("play", playState);
    };
    function tick() {
      clearTimeout(finish);
      const mounted = videoRef.current;
      if (mounted && mounted !== video) bind(mounted);
      const left = measure();
      setRemaining(Number.isFinite(left) ? left : null);
      if (left <= 0) {
        // A skip still under way would start the next item, or leave it faded, after the stop.
        // Any element, and a load already under way: neither starts after the stop.
        cancelFadeAdvance();
        dropFadeAdvance(video);
        noteSleepStop();
        video.pause();
        letGo();
        setState(null);
        return;
      }
      if (fadeSeconds === null && Number.isFinite(left)) fadeSeconds = sleepFadeSeconds(left);
      // The last moments land on a timer of their own, so a fast speed cannot
      // carry the item past its end between ticks.
      if (left < 0.5 && !video.paused) finish = setTimeout(tick, left * 1000);
      fade(left);
    }
    tick();
    const interval = setInterval(tick, SLEEP_POLL_MS);
    video.addEventListener("pause", playState);
    video.addEventListener("play", playState);
    return () => {
      clearInterval(interval);
      clearTimeout(finish);
      video.removeEventListener("pause", playState);
      video.removeEventListener("play", playState);
      letGo();
    };
  }, [videoRef, timer, enabled]);

  return { timer, remaining: timer ? remaining : null, setTimer };
}
