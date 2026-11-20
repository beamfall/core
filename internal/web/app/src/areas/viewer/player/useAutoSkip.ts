import { useCallback, useEffect, useRef, useState } from "react";
import type { PlaybackMarker } from "@/lib/graphql/types";
import {
  type AutoSkipPrefs,
  type AutoSkipSegment,
  autoSkipSegmentAt,
  autoSkipSegmentKey,
} from "../lib/transport";
import { markViewerIntent } from "./viewerIntent";
import { getWatchParty, sendPartyAutoSkip } from "./watchPartyStore";

// Auto-skip playback (ADR-0048 Decision 6): while a per-profile
// autoskip.intro/recap/credits pref is on, the engine seeks straight past a
// matching marker segment and the transport surfaces a brief "Skipped <kind> ·
// Undo" overlay. Composes with (never replaces) the separate end-of-file
// auto-advance (useUpNext) — this hook only ever seeks within the current
// Media.
//
// One-shot re-fire guard: each segment instance is keyed by kind+start. Once a
// segment has been auto-skipped, that key stays suppressed — including
// through Undo's seek back into the segment — until the playhead genuinely
// leaves the segment's range without Undo being pressed again. A `mediaID`
// change (new Media loaded) always clears the guard.
//
// Markers are in song seconds. Under an applied tempo (CORE-1659) a stream
// second covers `songScale` song seconds, so the playhead is compared in song
// time and every seek converts back to stream time.
export function useAutoSkip({
  videoRef,
  markers,
  mediaID,
  prefs,
  songScale = 1,
  autoSkipLead,
}: {
  videoRef: React.RefObject<HTMLVideoElement | null>;
  markers: PlaybackMarker[];
  mediaID: string;
  prefs: AutoSkipPrefs;
  /** Song seconds per stream second: the tempo the server applied; 1 when none. */
  songScale?: number;
  /** Coordinate the lead's skip with its position reports. */
  autoSkipLead?: (positionMS: number) => Promise<boolean>;
}): {
  skipped: AutoSkipSegment | null;
  undo: () => void;
  dismiss: () => void;
} {
  const [skipped, setSkipped] = useState<AutoSkipSegment | null>(null);
  const suppressedKeyRef = useRef<string | null>(null);
  const undoSegmentRef = useRef<AutoSkipSegment | null>(null);
  const undoPartyRef = useRef("");
  // Consumes the single timeupdate tick that follows our own programmatic
  // seek (skip or Undo) so it is never mistaken for a fresh entry/exit.
  const ignoreNextTickRef = useRef(false);
  const markersRef = useRef(markers);
  markersRef.current = markers;
  const prefsRef = useRef(prefs);
  prefsRef.current = prefs;
  const songScaleRef = useRef(1);
  songScaleRef.current = songScale > 0 && Number.isFinite(songScale) ? songScale : 1;

  // A new Media re-arms everything.
  useEffect(() => {
    suppressedKeyRef.current = null;
    undoSegmentRef.current = null;
    ignoreNextTickRef.current = false;
    setSkipped(null);
  }, [mediaID]);

  const dismiss = useCallback(() => {
    setSkipped(null);
  }, []);

  const undo = useCallback(() => {
    const video = videoRef.current;
    const segment = undoSegmentRef.current;
    if (!video || !segment) return;
    const party = getWatchParty();
    if (party.partyID !== undoPartyRef.current) {
      setSkipped(null);
      return;
    }
    // Re-arm the guard for this exact segment before seeking back into it —
    // otherwise the very next tick (now inside the segment again) would
    // immediately re-trigger the auto-skip we're trying to undo.
    suppressedKeyRef.current = autoSkipSegmentKey(segment);
    ignoreNextTickRef.current = true;
    if (party.status === "active") markViewerIntent();
    video.currentTime = segment.startSeconds / songScaleRef.current;
    setSkipped(null);
  }, [videoRef]);

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    let cancelled = false;
    let skipPending = false;

    const showSkip = (segment: AutoSkipSegment, partyID: string) => {
      undoSegmentRef.current = segment;
      undoPartyRef.current = partyID;
      setSkipped(segment);
    };

    const onTime = () => {
      if (skipPending || video.seeking) return;
      if (ignoreNextTickRef.current) {
        ignoreNextTickRef.current = false;
        return;
      }
      const scale = songScaleRef.current;
      const segment = autoSkipSegmentAt(
        markersRef.current,
        video.currentTime * scale,
        prefsRef.current,
      );
      if (!segment) {
        // A genuine exit-without-undo: the segment is no longer under us and
        // we didn't just seek it ourselves. Clear the guard so a later
        // rewatch of this same segment can auto-skip again.
        suppressedKeyRef.current = null;
        return;
      }
      const key = autoSkipSegmentKey(segment);
      if (suppressedKeyRef.current === key) return;
      const party = getWatchParty();
      if (party.status === "starting" || party.status === "joining") return;
      suppressedKeyRef.current = key;
      if (party.status === "active") {
        const target = segment.endSeconds * 1000;
        const request = party.lead
          ? party.isHost && autoSkipLead?.(target)
          : sendPartyAutoSkip(mediaID, target);
        if (!request) return;
        skipPending = true;
        void request.then((applied) => {
          skipPending = false;
          if (!applied || cancelled || getWatchParty().partyID !== party.partyID) return;
          showSkip(segment, party.partyID);
        });
        return;
      }
      ignoreNextTickRef.current = true;
      video.currentTime = segment.endSeconds / scale;
      showSkip(segment, "");
    };

    video.addEventListener("timeupdate", onTime);
    return () => {
      cancelled = true;
      video.removeEventListener("timeupdate", onTime);
    };
  }, [videoRef, mediaID, autoSkipLead]);

  return { skipped, undo, dismiss };
}
