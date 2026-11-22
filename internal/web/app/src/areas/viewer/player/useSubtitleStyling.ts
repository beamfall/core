import Hls from "hls.js";
import { useCallback, useEffect, useRef, useState } from "react";
import {
  clampSync,
  publishSubtitleOffset,
  readSync,
  SUBTITLE_OFFSET_LIMIT,
  writeSync,
} from "./playbackSync";

// Subtitle sync ("where supported", ADR-0007 transport spec), ported from
// applySubtitleSync / subtitleStylingAvailable / subtitleSyncSupported (app.js).
// Appearance (size, background, font, colour, edge, position) is applied by
// useSubtitleAppearance from the Profile's prefs. Sync shifts native TextTrack cue times,
// anchored against each cue's authored time so repeated nudges stay exact.
// hls.js renders through native TextTracks by default (renderTextTracksNatively)
// and appends cues as each subtitle fragment arrives, so on that route the
// offset is re-applied after every appended fragment (CORE-1671), and the
// styled ASS renderer follows the published offset; only a non-native hls.js
// renderer degrades honestly. The offset persists per Profile
// and Media (playbackSync.ts). The styling controls only surface once a real
// subtitle track exists.

interface CueWithBase extends TextTrackCue {
  baseStart?: number;
  baseEnd?: number;
}

export function useSubtitleStyling({
  videoRef,
  hlsRef,
  tracksVersion,
  profileID = "",
  mediaID = "",
}: {
  videoRef: React.RefObject<HTMLVideoElement | null>;
  hlsRef: React.RefObject<Hls | null>;
  /** Bumped by usePlayer when the live track set changes — re-checks support. */
  tracksVersion: number;
  /** Keys the persisted offset; empty leaves it unpersisted. */
  profileID?: string;
  mediaID?: string;
}) {
  const [syncOffset, setSyncOffset] = useState(() => readSync(profileID, mediaID).subtitleOffset);
  // Whether any subtitle track exists (styling) / a native one exists (sync).
  const [stylingAvailable, setStylingAvailable] = useState(false);
  const [syncSupported, setSyncSupported] = useState(false);
  const syncOffsetRef = useRef(syncOffset);
  syncOffsetRef.current = syncOffset;
  const keyRef = useRef({ profileID, mediaID });
  keyRef.current = { profileID, mediaID };

  const applySync = useCallback(() => {
    const video = videoRef.current;
    if (!video) return;
    const offset = syncOffsetRef.current;
    // The styled ASS route draws its own canvas and follows the published value.
    publishSubtitleOffset(video, offset);
    if (!video.textTracks) return;
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

  // A different item (or Profile) brings its own persisted offset.
  useEffect(() => {
    const stored = readSync(profileID, mediaID).subtitleOffset;
    if (stored === syncOffsetRef.current) return;
    syncOffsetRef.current = stored;
    setSyncOffset(stored);
    applySync();
  }, [profileID, mediaID, applySync]);

  // Re-check availability and re-apply sync whenever the live track set changes.
  useEffect(() => {
    const video = videoRef.current;
    const hls = hlsRef.current;
    const nativeCount = video && video.textTracks ? video.textTracks.length : 0;
    const hlsCount = hls && Array.isArray(hls.subtitleTracks) ? hls.subtitleTracks.length : 0;
    setStylingAvailable(nativeCount > 0 || hlsCount > 0);
    // Cue retiming works on native TextTracks, which hls.js fills unless it was
    // configured to render its own cue DOM.
    const hlsRendersNatively = !hls || hls.config?.renderTextTracksNatively !== false;
    setSyncSupported(nativeCount > 0 && hlsRendersNatively);
    if (syncOffsetRef.current) {
      applySync();
    }
  }, [videoRef, hlsRef, tracksVersion, applySync]);

  // Cues keep arriving after the track set settles: hls.js appends each
  // subtitle fragment's cues (and in-band captions) as it buffers, and a
  // sidecar <track> loads its file asynchronously. Re-apply after each so new
  // cues pick up the offset; anchored cues are left exactly where they are.
  useEffect(() => {
    const video = videoRef.current;
    const hls = hlsRef.current;
    const reapply = () => {
      if (syncOffsetRef.current) applySync();
    };
    // <track> "load" does not bubble, so listen in the capture phase.
    video?.addEventListener("load", reapply, true);
    const hlsEvents = [
      Hls.Events?.SUBTITLE_FRAG_PROCESSED,
      Hls.Events?.FRAG_PARSING_USERDATA,
      Hls.Events?.SUBTITLE_TRACK_SWITCH,
    ].filter((name): name is NonNullable<typeof name> => Boolean(name));
    if (hls && typeof hls.on === "function") {
      for (const name of hlsEvents) hls.on(name, reapply);
    }
    return () => {
      video?.removeEventListener("load", reapply, true);
      if (hls && typeof hls.off === "function") {
        for (const name of hlsEvents) hls.off(name, reapply);
      }
    };
  }, [videoRef, hlsRef, tracksVersion, applySync]);

  const commitOffset = useCallback(
    (next: number) => {
      syncOffsetRef.current = next;
      setSyncOffset(next);
      writeSync(keyRef.current.profileID, keyRef.current.mediaID, { subtitleOffset: next });
      applySync();
    },
    [applySync],
  );

  const nudgeSync = useCallback(
    (delta: number) => {
      commitOffset(
        clampSync(syncOffsetRef.current + delta, -SUBTITLE_OFFSET_LIMIT, SUBTITLE_OFFSET_LIMIT),
      );
    },
    [commitOffset],
  );

  const resetSync = useCallback(() => commitOffset(0), [commitOffset]);

  const reset = useCallback(() => {
    commitOffset(0);
  }, [commitOffset]);

  return {
    syncOffset,
    nudgeSync,
    resetSync,
    reset,
    applySync,
    stylingAvailable,
    syncSupported,
  };
}
