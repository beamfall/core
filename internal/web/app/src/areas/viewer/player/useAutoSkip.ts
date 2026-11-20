import { type RefObject, useCallback, useEffect, useRef, useState } from "react";
import type { PlaybackMarker } from "@/lib/graphql/types";
import {
  type AutoSkipPrefs,
  type AutoSkipSegment,
  autoSkipSegmentAt,
  autoSkipSegmentKey,
} from "../lib/transport";
import { markViewerIntent } from "./viewerIntent";
import {
  getPartyTransportEpoch,
  getWatchParty,
  hasPartyTransportPending,
  type PartyAutoSkipOutcome,
  partyRewoundIntoSegment,
  sendPartyAutoSkip,
} from "./watchPartyStore";

export type PartySeekAutoSkip = (
  video: HTMLVideoElement,
  expiresAt: number,
) => Promise<PartyAutoSkipOutcome> | undefined;

// Auto-skip playback (ADR-0048 Decision 6): while a per-profile
// autoskip.intro/recap/credits pref is on, the engine seeks straight past a
// matching marker segment and the transport surfaces a brief "Skipped <kind> ·
// Undo" overlay. Composes with (never replaces) the separate end-of-file
// auto-advance (useUpNext) — this hook only ever seeks within the current
// Media.
//
// One-shot re-fire guard: each segment instance is keyed by party and kind+start.
// Once a segment has been auto-skipped, that key stays suppressed — including
// through Undo's seek back into the segment — until the playhead genuinely
// leaves the segment's range without Undo being pressed again, or the party or
// Media changes.
//
// Markers are in song seconds. Under an applied tempo (CORE-1659) a stream
// second covers `songScale` song seconds, so the playhead is compared in song
// time and every seek converts back to stream time.
export function useAutoSkip({
  videoRef,
  attached = true,
  markers,
  mediaID,
  prefs,
  songScale = 1,
  autoSkipLead,
  seekToPartySkip,
  partySeekAutoSkip,
}: {
  videoRef: React.RefObject<HTMLVideoElement | null>;
  attached?: boolean;
  markers: PlaybackMarker[];
  mediaID: string;
  prefs: AutoSkipPrefs;
  /** Song seconds per stream second: the tempo the server applied; 1 when none. */
  songScale?: number;
  /** Coordinate the lead's skip with its position reports. */
  autoSkipLead?: (positionMS: number, expiresAt?: number) => Promise<PartyAutoSkipOutcome>;
  /** Apply an acknowledged member skip as a party correction. */
  seekToPartySkip?: (video: HTMLVideoElement, streamSeconds: number) => void;
  /** Give party transport the opportunity to skip a segment reached by a scrub. */
  partySeekAutoSkip?: RefObject<PartySeekAutoSkip | null>;
}): {
  skipped: AutoSkipSegment | null;
  undo: () => void;
  dismiss: () => void;
} {
  const [skipped, setSkipped] = useState<AutoSkipSegment | null>(null);
  const suppressedKeyRef = useRef<{ partyID: string; segmentKey: string } | null>(null);
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
    suppressedKeyRef.current = { partyID: party.partyID, segmentKey: autoSkipSegmentKey(segment) };
    ignoreNextTickRef.current = true;
    if (party.status === "active") markViewerIntent();
    video.currentTime = segment.startSeconds / songScaleRef.current;
    setSkipped(null);
  }, [videoRef]);

  useEffect(() => {
    const video = videoRef.current;
    if (!attached || !video) return;
    let cancelled = false;
    let seeking = false;

    const showSkip = (segment: AutoSkipSegment, partyID: string) => {
      undoSegmentRef.current = segment;
      undoPartyRef.current = partyID;
      setSkipped(segment);
    };

    const trySkip = (expiresAt?: number) => {
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
      const party = getWatchParty();
      const key = { partyID: party.partyID, segmentKey: autoSkipSegmentKey(segment) };
      const suppressed = suppressedKeyRef.current;
      if (suppressed?.partyID === key.partyID && suppressed.segmentKey === key.segmentKey) return;
      if (party.status === "starting" || party.status === "joining") return;
      if (party.status === "active") {
        const target = Math.round(segment.endSeconds * 1000);
        if (partyRewoundIntoSegment(mediaID, Math.round(segment.startSeconds * 1000), target)) {
          return;
        }
        // A playback tick must not displace a newer queued scrub.
        if (expiresAt === undefined && hasPartyTransportPending()) return;
        suppressedKeyRef.current = key;
        const epoch = getPartyTransportEpoch();
        const request = party.lead
          ? party.isHost && autoSkipLead?.(target, expiresAt)
          : sendPartyAutoSkip(mediaID, target, expiresAt);
        if (!request) return;
        return request.then((outcome) => {
          const latest = getWatchParty();
          if (cancelled || videoRef.current !== video || getPartyTransportEpoch() !== epoch)
            return "unconfirmed";
          if (outcome === "applied") {
            if (!party.lead) {
              const anchor = latest.timeline;
              if (anchor?.item !== mediaID || anchor.positionMS !== target) return "unconfirmed";
              const streamSeconds = anchor.positionMS / (1000 * songScaleRef.current);
              if (video.currentTime !== streamSeconds) {
                ignoreNextTickRef.current = true;
                if (seekToPartySkip) seekToPartySkip(video, streamSeconds);
                else video.currentTime = streamSeconds;
              }
            }
            showSkip(segment, party.partyID);
          }
          return outcome;
        });
      }
      suppressedKeyRef.current = key;
      ignoreNextTickRef.current = true;
      video.currentTime = segment.endSeconds / scale;
      showSkip(segment, "");
    };

    const onSeeking = () => {
      seeking = true;
    };
    const onSeeked = () => {
      seeking = false;
    };
    const onTime = () => {
      const party = getWatchParty();
      // The browser can clear video.seeking and fire timeupdate before seeked.
      // Let the party's seeked handler decide whether to skip or send transport.
      if (video.seeking || (seeking && party.status === "active")) return;
      if (ignoreNextTickRef.current) {
        ignoreNextTickRef.current = false;
        if (suppressedKeyRef.current?.partyID === party.partyID) return;
      }
      void trySkip();
    };
    if (partySeekAutoSkip) {
      partySeekAutoSkip.current = (element, expiresAt) =>
        element === video ? trySkip(expiresAt) : undefined;
    }

    video.addEventListener("timeupdate", onTime);
    video.addEventListener("seeking", onSeeking);
    video.addEventListener("seeked", onSeeked);
    return () => {
      cancelled = true;
      if (partySeekAutoSkip) partySeekAutoSkip.current = null;
      video.removeEventListener("timeupdate", onTime);
      video.removeEventListener("seeking", onSeeking);
      video.removeEventListener("seeked", onSeeked);
    };
  }, [videoRef, attached, mediaID, autoSkipLead, seekToPartySkip, partySeekAutoSkip]);

  return { skipped, undo, dismiss };
}
