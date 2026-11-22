import { useEffect } from "react";
import { useLocation, useNavigate } from "react-router";
import {
  idleRoute,
  lastSlideshowSource,
  pageBusy,
  SCREENSAVER_IDLE_MS,
  screensaverEnabled,
  screensaverPath,
  trackTyping,
} from "./lib/screensaver";

const INPUT_EVENTS = ["keydown", "pointerdown", "pointermove", "wheel", "touchstart", "scroll"];
const CHECK_MS = 5_000;

/**
 * The screensaver's idle trigger (CORE-2981), mounted once in the viewer shell.
 * With the per-device option on, five minutes without input on a browsing
 * route, in a visible tab with no dialog, playing media or text field active, opens the
 * slideshow in screensaver mode. Settings are reread on every check, since the
 * slideshow's setup sheet changes them.
 */
export function useScreensaver() {
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const eligibleRoute = idleRoute(pathname);

  useEffect(() => {
    if (!eligibleRoute) return;
    let lastInput = performance.now();
    const noteInput = () => {
      lastInput = performance.now();
    };
    const visible = () => {
      if (document.visibilityState === "visible") noteInput();
    };
    for (const name of INPUT_EVENTS) {
      window.addEventListener(name, noteInput, { capture: true, passive: true });
    }
    document.addEventListener("visibilitychange", visible);
    const untrackTyping = trackTyping(document);
    const timer = window.setInterval(() => {
      if (document.visibilityState !== "visible" || pageBusy(document)) {
        noteInput();
        return;
      }
      if (performance.now() - lastInput < SCREENSAVER_IDLE_MS || !screensaverEnabled()) return;
      noteInput();
      const source = lastSlideshowSource();
      if (source) navigate(screensaverPath(source));
    }, CHECK_MS);
    return () => {
      for (const name of INPUT_EVENTS) window.removeEventListener(name, noteInput, true);
      document.removeEventListener("visibilitychange", visible);
      untrackTyping();
      window.clearInterval(timer);
    };
  }, [eligibleRoute, navigate]);
}
