import { type RefObject, useEffect, useState } from "react";

/** The audio Playing next card shows over an item's final seconds (CORE-3216). */
export const PLAYING_NEXT_SECONDS = 12;

// The audio Playing next card: shown over the final PLAYING_NEXT_SECONDS of an
// item that has a next one. It only reports; when and whether playback moves
// on stays with the player's own `ended` handling. A seek out of the window
// hides it, and dismiss hides it for the rest of the item (`itemKey`).
export function usePlayingNext({
  videoRef,
  itemKey,
  enabled,
}: {
  videoRef: RefObject<HTMLVideoElement | null>;
  /** Stable per item: a new item re-arms a dismissed card. */
  itemKey: string;
  /** Whether a next item exists and the card may show. */
  enabled: boolean;
}): { visible: boolean; remaining: number; dismiss: () => void } {
  const [remaining, setRemaining] = useState<number | null>(null);
  const [dismissedKey, setDismissedKey] = useState<string | null>(null);

  useEffect(() => {
    const video = videoRef.current;
    if (!video || !enabled) {
      setRemaining(null);
      return;
    }
    const update = () => {
      const left = video.duration - video.currentTime;
      const inWindow =
        Number.isFinite(video.duration) &&
        video.duration > PLAYING_NEXT_SECONDS &&
        !video.ended &&
        left > 0 &&
        left <= PLAYING_NEXT_SECONDS;
      setRemaining(inWindow ? left : null);
    };
    update();
    const events = ["timeupdate", "seeked", "durationchange", "ended", "emptied"] as const;
    for (const name of events) video.addEventListener(name, update);
    return () => {
      for (const name of events) video.removeEventListener(name, update);
    };
  }, [videoRef, enabled, itemKey]);

  return {
    visible: remaining !== null && dismissedKey !== itemKey,
    remaining: remaining ?? 0,
    dismiss: () => setDismissedKey(itemKey),
  };
}
