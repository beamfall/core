import { useEffect } from "react";
import type { PlaybackNext } from "../lib/playback";

// Browser Media Session API integration (ADR-0084 §43). The Media Session is a
// *surface, not authority*: it MIRRORS the player's transport state to the OS
// (lock screen / media keys / hardware buttons) and routes the user's transport
// intents back through the SAME <video> element the rest of the player drives.
// It therefore inherits — and never bypasses — Stream Tokens, Profile-switch
// invalidation, Reveal, and queue rules, because every intent lands on the
// engine-owned <video> whose listeners already enforce them (progress beacon,
// relock-pause, token expiry). When the API is absent (older/locked-down
// engines) this is a silent no-op; the rest of the player is unaffected.
//
// Lifecycle: handlers + metadata are installed only while playing and fully
// cleared on unmount / phase change. A Profile switch unmounts the player
// route, so this cleanup runs and no previous-Profile metadata lingers on the
// OS surface.

const SEEK_STEP_SECONDS = 10;

export interface MediaSessionOptions {
  videoRef: React.RefObject<HTMLVideoElement | null>;
  /** Whether the player is in the playing phase (gates installation). */
  active: boolean;
  /** The current Media title (the only honest source; the wire omits it). */
  title: string;
  /** The pre-minted next item, when the server issued one (enables nexttrack). */
  next?: PlaybackNext | null;
  /** Advance to the next item — routed through the player's own nav/engine. */
  onNext?: (next: PlaybackNext) => void;
}

export function useMediaSession({
  videoRef,
  active,
  title,
  next,
  onNext,
}: MediaSessionOptions): void {
  // Metadata: set when active, cleared when not. Kept in its own effect so a
  // title change updates the OS surface without re-binding the action handlers.
  useEffect(() => {
    const ms = navigator.mediaSession;
    if (!ms || typeof MediaMetadata === "undefined") return;
    if (!active) {
      ms.metadata = null;
      return;
    }
    ms.metadata = new MediaMetadata({
      title: title || "Beamfall",
      album: "Beamfall",
    });
    return () => {
      ms.metadata = null;
    };
  }, [active, title]);

  // Transport action handlers + playbackState/position mirroring.
  useEffect(() => {
    const ms = navigator.mediaSession;
    const video = videoRef.current;
    if (!ms || !active || !video) return;

    const safeSet = (action: MediaSessionAction, handler: MediaSessionActionHandler | null) => {
      // Engines vary in which actions they accept; an unsupported action throws.
      try {
        ms.setActionHandler(action, handler);
      } catch {
        /* unsupported action on this engine — skip it */
      }
    };

    safeSet("play", () => void video.play().catch(() => {}));
    safeSet("pause", () => video.pause());
    safeSet("seekbackward", (details) => {
      const step = details?.seekOffset ?? SEEK_STEP_SECONDS;
      video.currentTime = Math.max(0, video.currentTime - step);
    });
    safeSet("seekforward", (details) => {
      const step = details?.seekOffset ?? SEEK_STEP_SECONDS;
      const end = Number.isFinite(video.duration) ? video.duration : Infinity;
      video.currentTime = Math.min(end, video.currentTime + step);
    });
    safeSet("seekto", (details) => {
      if (details?.seekTime != null) video.currentTime = details.seekTime;
    });
    // nexttrack only when the server pre-minted a next item; the advance is
    // routed through the player's own handler (Stream-Token'd, queue-aware).
    if (next && onNext) {
      safeSet("nexttrack", () => onNext(next));
    } else {
      safeSet("nexttrack", null);
    }

    const syncPlayback = () => {
      ms.playbackState = video.paused ? "paused" : "playing";
    };
    const syncPosition = () => {
      if (typeof ms.setPositionState !== "function") return;
      if (!Number.isFinite(video.duration) || video.duration <= 0) return;
      try {
        ms.setPositionState({
          duration: video.duration,
          position: Math.min(video.currentTime, video.duration),
          playbackRate: video.playbackRate || 1,
        });
      } catch {
        /* invalid state mid-load — ignore, the next tick corrects it */
      }
    };

    video.addEventListener("play", syncPlayback);
    video.addEventListener("pause", syncPlayback);
    video.addEventListener("timeupdate", syncPosition);
    video.addEventListener("loadedmetadata", syncPosition);
    video.addEventListener("durationchange", syncPosition);
    syncPlayback();
    syncPosition();

    return () => {
      video.removeEventListener("play", syncPlayback);
      video.removeEventListener("pause", syncPlayback);
      video.removeEventListener("timeupdate", syncPosition);
      video.removeEventListener("loadedmetadata", syncPosition);
      video.removeEventListener("durationchange", syncPosition);
      for (const action of [
        "play",
        "pause",
        "seekbackward",
        "seekforward",
        "seekto",
        "nexttrack",
      ] as MediaSessionAction[]) {
        safeSet(action, null);
      }
      ms.playbackState = "none";
    };
  }, [videoRef, active, next, onNext]);
}
