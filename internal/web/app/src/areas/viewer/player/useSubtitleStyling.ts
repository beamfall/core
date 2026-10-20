import type Hls from "hls.js";
import { useCallback, useEffect, useRef, useState } from "react";

// Subtitle styling + sync ("where supported", ADR-0007 transport spec), ported
// from applySubtitleStyling / applySubtitleSync / subtitleStylingAvailable /
// subtitleSyncSupported (app.js). Styling rides a CSS custom property
// (--bf-subtitle-size-scale) plus a background class on the stage (the ::cue
// rules live in player-deferred.css). Sync shifts native TextTrack cue times,
// anchored against each cue's authored time so repeated nudges stay exact;
// hls.js renders its own cue DOM, so sync is unsupported there and degrades
// honestly. The styling controls only surface once a real subtitle track exists.

export type SubtitleBackground = "none" | "dark" | "solid";

interface CueWithBase extends TextTrackCue {
  baseStart?: number;
  baseEnd?: number;
}

export function useSubtitleStyling({
  stageRef,
  videoRef,
  hlsRef,
  tracksVersion,
}: {
  stageRef: React.RefObject<HTMLElement | null>;
  videoRef: React.RefObject<HTMLVideoElement | null>;
  hlsRef: React.RefObject<Hls | null>;
  /** Bumped by usePlayer when the live track set changes — re-checks support. */
  tracksVersion: number;
}) {
  const [sizeScale, setSizeScale] = useState(1);
  const [background, setBackground] = useState<SubtitleBackground>("dark");
  const [syncOffset, setSyncOffset] = useState(0);
  // Whether any subtitle track exists (styling) / a native one exists (sync).
  const [stylingAvailable, setStylingAvailable] = useState(false);
  const [syncSupported, setSyncSupported] = useState(false);
  const syncOffsetRef = useRef(0);
  syncOffsetRef.current = syncOffset;

  // Apply size + background to the stage element whenever they change.
  useEffect(() => {
    const stage = stageRef.current;
    if (!stage) return;
    stage.style.setProperty("--bf-subtitle-size-scale", String(sizeScale));
    stage.classList.remove("bf-subtitle-bg-none", "bf-subtitle-bg-dark", "bf-subtitle-bg-solid");
    stage.classList.add(`bf-subtitle-bg-${background}`);
  }, [stageRef, sizeScale, background]);

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
    setSizeScale(1);
    setBackground("dark");
    setSyncOffset(0);
    syncOffsetRef.current = 0;
    applySync();
  }, [applySync]);

  return {
    sizeScale,
    setSizeScale,
    background,
    setBackground,
    syncOffset,
    nudgeSync,
    reset,
    applySync,
    stylingAvailable,
    syncSupported,
  };
}
