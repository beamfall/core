import { type RefObject, useEffect, useState } from "react";

// A brief wait is not worth a spinner: only a stall longer than this shows one.
export const BUFFERING_SPINNER_DELAY_MS = 500;

/**
 * Whether the player should show its loading spinner (CORE-1363): from the
 * start of a load until the <video> has its first frame, and while playback
 * the viewer has not paused waits on data for longer than
 * {@link BUFFERING_SPINNER_DELAY_MS}. `active` is true while the stage (and
 * its <video>) is mounted for a load; each new load starts over.
 */
export function useMediaLoading(
  videoRef: RefObject<HTMLVideoElement | null>,
  active: boolean,
): boolean {
  const [firstFrame, setFirstFrame] = useState(false);
  const [buffering, setBuffering] = useState(false);

  useEffect(() => {
    setFirstFrame(false);
    setBuffering(false);
    const video = videoRef.current;
    if (!active || !video) return;
    let timer: number | null = null;
    let stalledAt = 0;
    const clearTimer = () => {
      if (timer !== null) {
        window.clearTimeout(timer);
        timer = null;
      }
    };
    const settle = () => {
      clearTimer();
      setBuffering(false);
    };
    const onFirstFrame = () => setFirstFrame(true);
    const onPlaying = () => {
      setFirstFrame(true);
      settle();
    };
    const onWaiting = () => {
      if (video.paused || timer !== null) return;
      stalledAt = video.currentTime;
      timer = window.setTimeout(() => {
        timer = null;
        if (!video.paused) setBuffering(true);
      }, BUFFERING_SPINNER_DELAY_MS);
    };
    // 'stalled' can fire while buffered data still plays: moving time ends it.
    const onTimeUpdate = () => {
      if (video.currentTime !== stalledAt) settle();
    };
    const listeners: Array<[string, () => void]> = [
      ["loadeddata", onFirstFrame],
      ["playing", onPlaying],
      ["waiting", onWaiting],
      ["stalled", onWaiting],
      ["timeupdate", onTimeUpdate],
      ["canplay", settle],
      ["pause", settle],
      ["ended", settle],
    ];
    for (const [type, listener] of listeners) video.addEventListener(type, listener);
    return () => {
      clearTimer();
      for (const [type, listener] of listeners) video.removeEventListener(type, listener);
    };
  }, [active, videoRef]);

  return active && (!firstFrame || buffering);
}
