import { type RefObject, useCallback, useEffect, useRef, useState } from "react";
import { getIdentityEpochSignal } from "@/lib/api";
import {
  nextPauseStart,
  parseSilenceMap,
  pauseSkipTarget,
  type SilentSpan,
  type SleepTimer,
  sleepFade,
  sleepRemaining,
} from "./listening";

type VideoRef = RefObject<HTMLVideoElement | null>;

/**
 * Applies the listener's speed with pitch preserved (CORE-2960) while
 * `enabled`. The default rate is set too, so a reload of the source keeps it.
 * When it stops applying, the element goes back to 1x, unless a Watch Party
 * (which sets its own rate) is why it stopped.
 */
export function usePlaybackSpeed(
  videoRef: VideoRef,
  speed: number,
  enabled: boolean,
  partyActive: boolean,
): void {
  const partyRef = useRef(partyActive);
  partyRef.current = partyActive;
  useEffect(() => {
    const video = videoRef.current;
    if (!enabled || !video) return;
    video.preservesPitch = true;
    video.defaultPlaybackRate = speed;
    video.playbackRate = speed;
    return () => {
      video.defaultPlaybackRate = 1;
      if (!partyRef.current) video.playbackRate = 1;
    };
  }, [videoRef, speed, enabled]);
}

const silenceMaps = new Map<string, Promise<SilentSpan[]>>();

function loadSilenceMap(url: string): Promise<SilentSpan[]> {
  const cached = silenceMaps.get(url);
  if (cached) return cached;
  const request = fetch(url, { credentials: "same-origin", signal: getIdentityEpochSignal() })
    .then((response) => (response.ok ? response.json() : null))
    .then(parseSilenceMap)
    .catch(() => {
      silenceMaps.delete(url);
      return [];
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
 * item has a map and the seconds saved this session.
 */
export function useLongPauseSkip(
  videoRef: VideoRef,
  url: string | undefined,
  enabled: boolean,
): { mapped: boolean; saved: number } {
  const [map, setMap] = useState<{ url: string; spans: SilentSpan[] } | null>(null);
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
    const check = () => {
      clearTimeout(timer);
      const time = video.currentTime;
      const target = pauseSkipTarget(spans, time);
      if (target !== undefined && !video.seeking) {
        video.currentTime = target;
        setSaved((total) => total + (target - time));
        return;
      }
      // timeupdate fires a few times a second; a timer lands on the next
      // pause's start so little of it plays first.
      const next = nextPauseStart(spans, time);
      if (next === undefined || video.paused) return;
      const wait = ((next - time) / (video.playbackRate || 1)) * 1000;
      timer = setTimeout(check, Math.max(0, wait));
    };
    const events = ["timeupdate", "seeked", "play", "pause", "ratechange"] as const;
    for (const event of events) video.addEventListener(event, check);
    check();
    return () => {
      clearTimeout(timer);
      for (const event of events) video.removeEventListener(event, check);
    };
  }, [videoRef, enabled, spans]);
  return { mapped: !!spans?.length, saved };
}

/**
 * The sleep timer (CORE-2960): after a number of minutes, at the end of this
 * item or at the end of this chapter, the volume fades out over 10 seconds,
 * then playback pauses and the volume comes back. An item or chapter timer
 * ends with its item.
 */
export function useSleepTimer(
  videoRef: VideoRef,
  mediaID: string,
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

  useEffect(() => {
    const video = videoRef.current;
    if (!timer || !video) {
      setRemaining(null);
      return;
    }
    // The volume before the fade, given back when the timer ends either way.
    // A volume the listener sets mid-fade is theirs: the fade restarts from it.
    let base: number | null = null;
    let faded = 0;
    let finish: ReturnType<typeof setTimeout> | undefined;
    const restore = () => {
      if (base !== null && video.volume === faded) video.volume = base;
      base = null;
    };
    const tick = () => {
      clearTimeout(finish);
      const left = sleepRemaining(timer, Date.now(), video);
      setRemaining(Number.isFinite(left) ? left : null);
      if (left <= 0) {
        video.pause();
        restore();
        setState(null);
        return;
      }
      // The last moments land on a timer of their own, so a fast speed cannot
      // carry the item past its end between ticks.
      if (left < 0.5 && !video.paused) finish = setTimeout(tick, left * 1000);
      if (base !== null && video.volume !== faded) base = null;
      const fade = sleepFade(left);
      if (fade < 1 && !video.paused) {
        base ??= video.volume;
        video.volume = base * fade;
        faded = video.volume;
      } else {
        restore();
      }
    };
    tick();
    const interval = setInterval(tick, 250);
    return () => {
      clearInterval(interval);
      clearTimeout(finish);
      restore();
    };
  }, [videoRef, timer]);

  return { timer, remaining: timer ? remaining : null, setTimer };
}
