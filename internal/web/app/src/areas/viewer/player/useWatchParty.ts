import { type RefObject, useCallback, useEffect, useRef, useState } from "react";
import { lastViewerIntent, markViewerIntent } from "./viewerIntent";
import {
  getWatchParty,
  leaveParty,
  leavePartyOnPageHide,
  reportPartyDrift,
  sendPartyTransport,
  startWatchParty,
  useWatchPartyState,
  type WatchPartyState,
} from "./watchPartyStore";
import {
  correctionFor,
  expectedPositionMS,
  SEEK_THRESHOLD_MS,
  timelineStarted,
} from "./watchPartySync";

// Keeps the player's <video> on the Watch Party timeline (CORE-1677): holds it
// paused and positioned until the shared present time, then plays, re-seeking
// on a large drift and nudging the rate on a small one. The viewer's own play,
// pause and seek go to the party; the corrections this hook applies, and the
// browser's own autoplay, do not.

/** How often the drift is measured and corrected. */
const SYNC_INTERVAL_MS = 250;
/**
 * A media event counts as the viewer's own only within this long of their
 * input, and only if no correction came after that input.
 */
const INTENT_WINDOW_MS = 1500;

export type WatchPartyPlayback = "off" | "waiting" | "paused" | "playing";

export interface WatchPartyView {
  party: WatchPartyState;
  /** Local position minus the shared position, in ms; null when not measured. */
  driftMS: number | null;
  playback: WatchPartyPlayback;
  /** The browser blocked play(); a tap on resume() joins playback. */
  needsTap: boolean;
  resume: () => void;
  /** Start a party around this title, carrying the current position and pause state. */
  /** Start a party; `relaxed` lets every member control playback (CORE-1765). */
  start: (relaxed?: boolean) => Promise<boolean>;
  leave: () => Promise<void>;
}

// A leave scheduled by an unmount, cancelled by a remount (React StrictMode
// mounts twice in development; only a real exit from the player leaves).
let pendingLeave: ReturnType<typeof setTimeout> | null = null;

export function useWatchParty({
  videoRef,
  attached,
  mediaId,
  onFollow,
}: {
  videoRef: RefObject<HTMLVideoElement | null>;
  /** Whether the <video> is mounted (it is not while an error replaces the stage). */
  attached: boolean;
  mediaId: string;
  /** Move the player to the Media the party moved to. */
  onFollow: (item: string) => void;
}): WatchPartyView {
  const party = useWatchPartyState();
  const [driftMS, setDriftMS] = useState<number | null>(null);
  const [playback, setPlayback] = useState<WatchPartyPlayback>("off");
  const [needsTap, setNeedsTap] = useState(false);
  // performance.now() of the player's mount and of its last correction. Input
  // from before the mount (the notification's Join click) is not counted.
  const mountedAt = useRef(performance.now());
  const lastCorrection = useRef(Number.NEGATIVE_INFINITY);
  const mediaIdRef = useRef(mediaId);
  mediaIdRef.current = mediaId;

  const active = party.status === "active" && party.timeline !== null;
  const timeline = party.timeline;

  const correct = useCallback((change: () => void) => {
    lastCorrection.current = performance.now();
    change();
  }, []);

  const sync = useCallback(() => {
    const s = getWatchParty();
    const tl = s.timeline;
    const video = videoRef.current;
    if (s.status !== "active" || !tl || !video || tl.item !== mediaIdRef.current) return;
    if (video.readyState < 1 || video.seeking) return;
    const now = Date.now() + s.offsetMS;
    const expected = expectedPositionMS(tl, now);
    const drift = video.currentTime * 1000 - expected;
    if (!timelineStarted(tl, now)) {
      if (video.playbackRate !== 1) video.playbackRate = 1;
      if (!video.paused) correct(() => video.pause());
      if (Math.abs(drift) > SEEK_THRESHOLD_MS)
        correct(() => {
          video.currentTime = expected / 1000;
        });
      setPlayback(tl.paused ? "paused" : "waiting");
      setDriftMS(tl.paused ? Math.round(drift) : null);
      return;
    }
    if (video.paused)
      correct(() => {
        const started = video.play();
        if (started && typeof started.catch === "function")
          started.then(() => setNeedsTap(false)).catch(() => setNeedsTap(true));
      });
    const correction = correctionFor(drift, expected, false);
    if (correction.kind === "seek") {
      video.playbackRate = 1;
      correct(() => {
        video.currentTime = correction.toMS / 1000;
      });
    } else if (correction.kind === "rate") {
      video.playbackRate = correction.rate;
    } else if (video.playbackRate !== 1) {
      video.playbackRate = 1;
    }
    setPlayback("playing");
    setDriftMS(Math.round(drift));
    reportPartyDrift(drift);
  }, [videoRef, correct]);

  // Follow the party to another title, or, as host, take it to this one.
  useEffect(() => {
    if (!active || !timeline || !mediaId || timeline.item === mediaId) return;
    if (party.isHost) void sendPartyTransport(mediaId, 0, false);
    else onFollow(timeline.item);
  }, [active, timeline, mediaId, party.isHost, onFollow]);

  // Measure and correct on a steady beat, at each new timeline, and exactly at
  // a running timeline's present time.
  useEffect(() => {
    if (!active || !timeline) {
      setPlayback("off");
      setDriftMS(null);
      setNeedsTap(false);
      const video = videoRef.current;
      if (video && video.playbackRate !== 1) video.playbackRate = 1;
      return;
    }
    sync();
    const beat = setInterval(sync, SYNC_INTERVAL_MS);
    const untilStart = timeline.presentAtMS - (Date.now() + getWatchParty().offsetMS);
    const atStart = !timeline.paused && untilStart > 0 ? setTimeout(sync, untilStart) : null;
    return () => {
      clearInterval(beat);
      if (atStart) clearTimeout(atStart);
    };
  }, [active, timeline, sync, videoRef]);

  // Track the viewer's input, so a media event can be told apart from autoplay
  // and from the echo of a correction. A release is not input on its own: a
  // long scroll ends in one too. A scrub that commits long after its press is
  // marked by PlayerTransport's seek, and OS media controls by useMediaSession.
  useEffect(() => {
    const events = ["pointerdown", "keydown"] as const;
    for (const name of events) window.addEventListener(name, markViewerIntent, true);
    return () => {
      for (const name of events) window.removeEventListener(name, markViewerIntent, true);
    };
  }, []);

  // The viewer's own play, pause and seek become party transport. A media
  // event with no recent input behind it, or that agrees with the timeline, is
  // ignored.
  useEffect(() => {
    const video = videoRef.current;
    if (!active || !attached || !video) return;
    const onLocal = (event: Event) => {
      const input = lastViewerIntent();
      if (
        input < mountedAt.current ||
        performance.now() - input > INTENT_WINDOW_MS ||
        input < lastCorrection.current
      ) {
        return;
      }
      const s = getWatchParty();
      const tl = s.timeline;
      if (s.status !== "active" || !tl || tl.item !== mediaIdRef.current) return;
      const now = Date.now() + s.offsetMS;
      const local = video.currentTime * 1000;
      const agrees =
        event.type === "pause"
          ? !timelineStarted(tl, now)
          : event.type === "play"
            ? !tl.paused
            : Math.abs(local - expectedPositionMS(tl, now)) <= SEEK_THRESHOLD_MS;
      if (agrees) return;
      void sendPartyTransport(tl.item, local, video.paused);
    };
    video.addEventListener("play", onLocal);
    video.addEventListener("pause", onLocal);
    video.addEventListener("seeked", onLocal);
    return () => {
      video.removeEventListener("play", onLocal);
      video.removeEventListener("pause", onLocal);
      video.removeEventListener("seeked", onLocal);
    };
  }, [active, attached, videoRef]);

  // Closing the tab or navigating away leaves within seconds (CORE-1763).
  useEffect(() => {
    window.addEventListener("pagehide", leavePartyOnPageHide);
    return () => window.removeEventListener("pagehide", leavePartyOnPageHide);
  }, []);

  // Leaving the player leaves the party.
  useEffect(() => {
    if (pendingLeave) {
      clearTimeout(pendingLeave);
      pendingLeave = null;
    }
    return () => {
      pendingLeave = setTimeout(() => {
        pendingLeave = null;
        if (getWatchParty().status !== "idle") void leaveParty();
      }, 0);
    };
  }, []);

  const resume = useCallback(() => {
    const video = videoRef.current;
    if (!video) return;
    correct(() => {
      void video
        .play()
        ?.then(() => setNeedsTap(false))
        .catch(() => {});
    });
  }, [videoRef, correct]);

  const start = useCallback(
    async (relaxed = true) => {
      const video = videoRef.current;
      if (!mediaId) return false;
      const ok = await startWatchParty(mediaId, video ? video.currentTime * 1000 : 0, relaxed);
      if (ok && video) await sendPartyTransport(mediaId, video.currentTime * 1000, video.paused);
      return ok;
    },
    [mediaId, videoRef],
  );

  return { party, driftMS, playback, needsTap, resume, start, leave: leaveParty };
}
