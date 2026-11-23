import { useEffect } from "react";
import { useT } from "@/lib/i18n";
import type { PlaybackNext } from "../lib/playback";
import { resetStillWatching } from "../lib/stillWatching";
import { markViewerIntent } from "./viewerIntent";

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
  /** Queue-aware next: when set it wins over `next`/`onNext` for nexttrack. */
  onNextTrack?: () => void;
  /** Queue-aware previous (enables previoustrack). */
  onPreviousTrack?: () => void;
  /** Credited people, shown as the artist line. */
  artist?: string;
  /** The container Entity title (the album, the show). */
  album?: string;
  /** Artwork for the OS surface. */
  artworkURL?: string;
  /**
   * Song seconds per stream second: the tempo the server applied (CORE-1659).
   * The OS surface shows song time, so a lock-screen seek is in song seconds
   * and lands at that song position in the stream. 1 when nothing was applied.
   */
  songScale?: number;
  /**
   * The listener's skip interval in seconds (global or per container,
   * CORE-2960): the step for an OS seek that names no offset. 10 by default.
   */
  skipSeconds?: number;
}

export function useMediaSession({
  videoRef,
  active,
  title,
  next,
  onNext,
  onNextTrack,
  onPreviousTrack,
  artist,
  album,
  artworkURL,
  songScale = 1,
  skipSeconds = SEEK_STEP_SECONDS,
}: MediaSessionOptions): void {
  const t = useT();
  const brand = t("brand");
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
      title: title || brand,
      artist: artist || "",
      album: album || brand,
      ...(artworkURL ? { artwork: [{ src: artworkURL }] } : {}),
    });
    return () => {
      ms.metadata = null;
    };
  }, [active, title, artist, album, artworkURL, brand]);

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

    // Each transport intent is marked as the viewer's own, so a Watch Party
    // sends it on like an in-page control. An OS transport control is viewer
    // input, so it also ends the autoplay run (CORE-1689); next and previous
    // start another item, which ends it anyway.
    safeSet("play", () => {
      markViewerIntent();
      resetStillWatching();
      void video.play().catch(() => {});
    });
    safeSet("pause", () => {
      markViewerIntent();
      resetStillWatching();
      video.pause();
    });
    // OS offsets and seek times are song seconds; the <video> plays stream seconds.
    const scale = songScale > 0 && Number.isFinite(songScale) ? songScale : 1;
    const skip = skipSeconds > 0 && Number.isFinite(skipSeconds) ? skipSeconds : SEEK_STEP_SECONDS;
    safeSet("seekbackward", (details) => {
      markViewerIntent();
      resetStillWatching();
      const step = details?.seekOffset ?? skip;
      video.currentTime = Math.max(0, video.currentTime - step / scale);
    });
    safeSet("seekforward", (details) => {
      markViewerIntent();
      resetStillWatching();
      const step = details?.seekOffset ?? skip;
      const end = Number.isFinite(video.duration) ? video.duration : Infinity;
      video.currentTime = Math.min(end, video.currentTime + step / scale);
    });
    safeSet("seekto", (details) => {
      markViewerIntent();
      resetStillWatching();
      if (details?.seekTime != null) video.currentTime = details.seekTime / scale;
    });
    // nexttrack only when the server pre-minted a next item; the advance is
    // routed through the player's own handler (Stream-Token'd, queue-aware).
    if (onNextTrack) {
      safeSet("nexttrack", onNextTrack);
    } else if (next && onNext) {
      safeSet("nexttrack", () => onNext(next));
    } else {
      safeSet("nexttrack", null);
    }
    safeSet("previoustrack", onPreviousTrack ?? null);

    const syncPlayback = () => {
      ms.playbackState = video.paused ? "paused" : "playing";
    };
    const syncPosition = () => {
      if (typeof ms.setPositionState !== "function") return;
      if (!Number.isFinite(video.duration) || video.duration <= 0) return;
      try {
        // Song time: a stream second covers `scale` song seconds, so the
        // song advances at the element's rate times the applied tempo.
        ms.setPositionState({
          duration: video.duration * scale,
          position: Math.min(video.currentTime, video.duration) * scale,
          playbackRate: (video.playbackRate || 1) * scale,
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
        "previoustrack",
      ] as MediaSessionAction[]) {
        safeSet(action, null);
      }
      ms.playbackState = "none";
    };
  }, [videoRef, active, next, onNext, onNextTrack, onPreviousTrack, songScale, skipSeconds]);
}
