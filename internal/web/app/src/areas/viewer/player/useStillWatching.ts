import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import type { PlaybackNext } from "../lib/playback";
import {
  noteItemLeft,
  noteItemStarted,
  resetStillWatching,
  shouldAskStillWatching,
  stillWatchingStreak,
  subscribeStillWatching,
} from "../lib/stillWatching";

// "Are you still watching?" (CORE-1689). Counts the run of items the Up Next
// countdown started (the player reports each expiry with noteAutoAdvance), resets
// it on any tap, click or key, and, once the run reaches the Profile's
// stillwatching.after setting, replaces the next autoplay with a prompt: while
// `armed`, the player hides Up Next, and when the item ends the prompt opens.
export function useStillWatching({
  videoRef,
  mediaID,
  after,
  next,
  enabled,
  onContinue,
}: {
  videoRef: React.RefObject<HTMLVideoElement | null>;
  mediaID: string;
  /** Resolved stillwatching.after: 0 means never ask. */
  after: number;
  /** The issued next item; without a playable one there is no autoplay to stop. */
  next: PlaybackNext | null | undefined;
  /** False on paths with no countdown (audio auto-advance), which never count. */
  enabled: boolean;
  onContinue: (next: PlaybackNext) => void;
}): {
  /** The next autoplay will be replaced by the prompt; hide Up Next. */
  armed: boolean;
  prompting: boolean;
  title: string;
  continueWatching: () => void;
  stop: () => void;
} {
  const streak = useSyncExternalStore(subscribeStillWatching, stillWatchingStreak);
  const [prompting, setPrompting] = useState(false);
  const playable = next?.streams ? next : null;
  const armed = enabled && shouldAskStillWatching(after, streak, Boolean(playable));
  const armedRef = useRef(armed);
  armedRef.current = armed;
  const nextRef = useRef(playable);
  nextRef.current = playable;
  const onContinueRef = useRef(onContinue);
  onContinueRef.current = onContinue;

  useEffect(() => {
    setPrompting(false);
    if (!mediaID) return;
    noteItemStarted(mediaID);
    return () => noteItemLeft(mediaID);
  }, [mediaID]);

  // Any input in the player is a sign someone is there.
  useEffect(() => {
    const onInput = () => resetStillWatching();
    window.addEventListener("pointerdown", onInput, true);
    window.addEventListener("keydown", onInput, true);
    return () => {
      window.removeEventListener("pointerdown", onInput, true);
      window.removeEventListener("keydown", onInput, true);
    };
  }, []);

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    const onEnded = () => {
      if (armedRef.current) setPrompting(true);
    };
    video.addEventListener("ended", onEnded);
    return () => video.removeEventListener("ended", onEnded);
  }, [videoRef]);

  const continueWatching = useCallback(() => {
    const issued = nextRef.current;
    resetStillWatching();
    setPrompting(false);
    if (issued) onContinueRef.current(issued);
  }, []);

  const stop = useCallback(() => {
    resetStillWatching();
    setPrompting(false);
  }, []);

  return {
    armed,
    prompting,
    title: playable?.title || "",
    continueWatching,
    stop,
  };
}
