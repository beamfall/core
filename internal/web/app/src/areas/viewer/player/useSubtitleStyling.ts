import type Hls from "hls.js";
import { useCallback, useEffect, useRef, useState } from "react";

// Subtitle sync ("where supported", ADR-0007 transport spec), ported from
// applySubtitleSync / subtitleStylingAvailable / subtitleSyncSupported (app.js).
// Appearance (size, background, font, colour, edge, position) is applied by
// useSubtitleAppearance from the Profile's prefs. Sync shifts native TextTrack cue times,
// anchored against each cue's authored time so repeated nudges stay exact;
// hls.js renders its own cue DOM, so sync is unsupported there and degrades
// honestly. The styling controls only surface once a real subtitle track exists.

interface CueWithBase extends TextTrackCue {
  baseStart?: number;
  baseEnd?: number;
}

export function useSubtitleStyling({
  videoRef,
  hlsRef,
  tracksVersion,
}: {
  videoRef: React.RefObject<HTMLVideoElement | null>;
  hlsRef: React.RefObject<Hls | null>;
  /** Bumped by usePlayer when the live track set changes — re-checks support. */
  tracksVersion: number;
}) {
  const [syncOffset, setSyncOffset] = useState(0);
  // Whether any subtitle track exists (styling) / a native one exists (sync).
  const [stylingAvailable, setStylingAvailable] = useState(false);
  const [syncSupported, setSyncSupported] = useState(false);
  const syncOffsetRef = useRef(0);
  syncOffsetRef.current = syncOffset;

  const applySync = useCallback(() => {
    const video = videoRef.current;
    if (!video || !video.textTracks) return;
    const offset = syncOffsetRef.current;
    for (const track of Array.from(video.textTracks)) {
      const cues = track.cues ? Array.from(track.cues) : [];
      for (const raw of cues) {
        const cue = raw as CueWithBase;
        // Anchor against the cue's authored times so repeated nudges stay exact.
        if (cue.baseStart === undefined) {
          cue.baseStart = cue.startTime;
          cue.baseEnd = cue.endTime;
        }
        cue.startTime = Math.max(0, cue.baseStart + offset);
        cue.endTime = Math.max(cue.startTime, (cue.baseEnd ?? cue.endTime) + offset);
      }
    }
  }, [videoRef]);

  // Re-check availability and re-apply sync whenever the live track set changes.
  useEffect(() => {
    const video = videoRef.current;
    const hls = hlsRef.current;
    const nativeCount = video && video.textTracks ? video.textTracks.length : 0;
    const hlsCount = hls && Array.isArray(hls.subtitleTracks) ? hls.subtitleTracks.length : 0;
    setStylingAvailable(nativeCount > 0 || hlsCount > 0);
    // Native cue retiming works on TextTracks; hls.js owns its own rendering.
    setSyncSupported(!hls && nativeCount > 0);
    if (syncOffsetRef.current) {
      applySync();
    }
  }, [videoRef, hlsRef, tracksVersion, applySync]);

  const nudgeSync = useCallback(
    (delta: number) => {
      setSyncOffset((prev) => {
        const next = Math.round((prev + delta) * 10) / 10;
        syncOffsetRef.current = next;
        return next;
      });
      // applySync reads the ref, which we just updated synchronously.
      applySync();
    },
    [applySync],
  );

  const reset = useCallback(() => {
    setSyncOffset(0);
    syncOffsetRef.current = 0;
    applySync();
  }, [applySync]);

  return {
    syncOffset,
    nudgeSync,
    reset,
    applySync,
    stylingAvailable,
    syncSupported,
  };
}
