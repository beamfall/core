import { useCallback, useEffect, useRef, useState } from "react";
import type { PlaybackNext } from "../lib/playback";
import { shouldShowUpNext } from "../lib/transport";

// The Up Next countdown + auto-advance, ported from maybeShowUpNext /
// renderUpNextCountdown / dismissUpNext / advanceToIssuedNext (app.js). When the
// server pre-minted an `info.next` issuance and playback is within 15s of the
// end (or already ended), the card appears with a 10s countdown; at zero it
// auto-advances. Dismiss hides it for the rest of this Media (until the player
// loads a new one). The hook tracks live time off the <video> element and owns
// the countdown interval; the actual advance is delegated to onAdvance so the
// player can navigate to the next Media's route.
const COUNTDOWN_START = 10;

export function useUpNext({
  videoRef,
  next,
  onAdvance,
}: {
  videoRef: React.RefObject<HTMLVideoElement | null>;
  next: PlaybackNext | null | undefined;
  /** Advance to the issued next Media (navigation). */
  onAdvance: (next: PlaybackNext) => void;
}): {
  visible: boolean;
  countdown: number;
  title: string;
  advance: () => void;
  dismiss: () => void;
} {
  const [visible, setVisible] = useState(false);
  const [countdown, setCountdown] = useState(COUNTDOWN_START);
  const countdownRef = useRef(COUNTDOWN_START);
  const dismissedRef = useRef(false);
  const timerRef = useRef<number | null>(null);
  const nextRef = useRef(next);
  nextRef.current = next;
  const onAdvanceRef = useRef(onAdvance);
  onAdvanceRef.current = onAdvance;

  const clearTimer = useCallback(() => {
    if (timerRef.current !== null) {
      window.clearInterval(timerRef.current);
      timerRef.current = null;
    }
  }, []);

  const advance = useCallback(() => {
    const issued = nextRef.current;
    clearTimer();
    if (issued && issued.streams) {
      onAdvanceRef.current(issued);
    } else {
      dismissedRef.current = true;
      setVisible(false);
    }
  }, [clearTimer]);

  const dismiss = useCallback(() => {
    dismissedRef.current = true;
    clearTimer();
    setVisible(false);
  }, [clearTimer]);

  // A fresh `next` issuance (new Media loaded) re-arms the card.
  useEffect(() => {
    dismissedRef.current = false;
    clearTimer();
    setVisible(false);
    countdownRef.current = COUNTDOWN_START;
    setCountdown(COUNTDOWN_START);
  }, [next, clearTimer]);

  // Watch playback time + the ended edge; show the card once shouldShowUpNext
  // crosses, then run the countdown to auto-advance. A seek back out of the end
  // window cancels a running countdown (the card hides, undismissed, and re-arms
  // on the next crossing); unmount clears the interval.
  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;

    const inWindow = (ended: boolean) =>
      shouldShowUpNext({
        next: nextRef.current,
        duration: video.duration,
        currentTime: video.currentTime,
        ended,
      });

    const cancel = () => {
      clearTimer();
      setVisible(false);
    };

    const maybeShow = (ended: boolean) => {
      if (dismissedRef.current) return;
      const wanted = inWindow(ended);
      if (timerRef.current !== null) {
        if (!wanted) cancel();
        return;
      }
      if (!wanted) return;
      countdownRef.current = COUNTDOWN_START;
      setCountdown(COUNTDOWN_START);
      setVisible(true);
      timerRef.current = window.setInterval(() => {
        if (!inWindow(video.ended)) {
          cancel();
          return;
        }
        // Tick against a ref so the auto-advance side effect runs outside the
        // setState updater (StrictMode double-invokes updaters; navigation must
        // fire exactly once).
        const value = countdownRef.current - 1;
        countdownRef.current = value;
        setCountdown(value);
        if (value <= 0) {
          advance();
        }
      }, 1000);
    };

    const onTime = () => maybeShow(false);
    const onEnded = () => maybeShow(true);
    video.addEventListener("timeupdate", onTime);
    video.addEventListener("ended", onEnded);
    return () => {
      video.removeEventListener("timeupdate", onTime);
      video.removeEventListener("ended", onEnded);
      clearTimer();
    };
  }, [videoRef, advance, clearTimer]);

  return {
    visible,
    countdown,
    title: next?.title || "",
    advance,
    dismiss,
  };
}
