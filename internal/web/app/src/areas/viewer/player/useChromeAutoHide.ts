import { useCallback, useEffect, useRef, useState } from "react";

// The transport chrome auto-hide, ported from showChrome / hideChrome (app.js):
// the controls fade out after a short idle while playback runs, and reveal on
// any pointer move, key press, or focus entering the stage. Chrome never hides
// while a menu/dialog is open (so an open audio menu can't vanish under the
// pointer) and never while the video is paused (a paused player keeps its
// controls). The hook is keyboard- and AT-safe: a focusin reveals chrome, so a
// Tab into a hidden control brings the whole transport back before focus lands.
const IDLE_MS = 3000;

export function useChromeAutoHide({
  stageRef,
  videoRef,
  menuOpen,
}: {
  stageRef: React.RefObject<HTMLElement | null>;
  videoRef: React.RefObject<HTMLVideoElement | null>;
  /** True while any track menu / overlay is open — suppresses hiding. */
  menuOpen: boolean;
}): { chromeHidden: boolean; reveal: () => void } {
  const [chromeHidden, setChromeHidden] = useState(false);
  const timerRef = useRef<number | null>(null);
  const menuOpenRef = useRef(menuOpen);
  menuOpenRef.current = menuOpen;

  const clearTimer = useCallback(() => {
    if (timerRef.current !== null) {
      window.clearTimeout(timerRef.current);
      timerRef.current = null;
    }
  }, []);

  const reveal = useCallback(() => {
    setChromeHidden(false);
    clearTimer();
    const video = videoRef.current;
    // Only schedule a hide while actively playing; a paused player keeps chrome.
    if (video && !video.paused) {
      timerRef.current = window.setTimeout(() => {
        if (!menuOpenRef.current) {
          setChromeHidden(true);
        }
      }, IDLE_MS);
    }
  }, [clearTimer, videoRef]);

  // While a menu is open the chrome must stay up; reschedule the idle timer when
  // it closes so the controls fade again after the user is done.
  useEffect(() => {
    if (menuOpen) {
      setChromeHidden(false);
      clearTimer();
    } else {
      reveal();
    }
  }, [menuOpen, reveal, clearTimer]);

  // Reveal on stage interaction; reveal (and keep up) whenever playback pauses.
  // The key listener binds on window, where PlayerTransport binds its shortcut
  // handler: a key pressed with body focus must bring chrome back, not only
  // act on playback.
  useEffect(() => {
    const stage = stageRef.current;
    const video = videoRef.current;
    if (!stage || !video) return;
    const onActivity = () => reveal();
    const onPause = () => {
      setChromeHidden(false);
      clearTimer();
    };
    stage.addEventListener("pointermove", onActivity);
    stage.addEventListener("focusin", onActivity);
    window.addEventListener("keydown", onActivity);
    video.addEventListener("play", onActivity);
    video.addEventListener("pause", onPause);
    return () => {
      stage.removeEventListener("pointermove", onActivity);
      stage.removeEventListener("focusin", onActivity);
      window.removeEventListener("keydown", onActivity);
      video.removeEventListener("play", onActivity);
      video.removeEventListener("pause", onPause);
      clearTimer();
    };
  }, [stageRef, videoRef, reveal, clearTimer]);

  return { chromeHidden, reveal };
}
