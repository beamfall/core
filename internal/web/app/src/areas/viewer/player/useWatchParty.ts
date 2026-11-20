import { type RefObject, useCallback, useEffect, useRef, useState } from "react";
import { useHomeNetwork } from "../lib/watchTogether";
import { getDisplayTrimMS } from "./displayTrim";
import { listenerMuted, setListenerMuted } from "./listenerMute";
import { lastViewerIntent, markViewerIntent } from "./viewerIntent";
import {
  followLeadParty,
  getWatchParty,
  leaveParty,
  leavePartyOnPageHide,
  partyNowMS,
  reportLeadPosition,
  reportPartyDrift,
  resumeFollowedParty,
  sendPartyTransport,
  startWatchParty,
  useWatchPartyState,
  type WatchPartyState,
  watchForLeadParty,
} from "./watchPartyStore";
import {
  correctionFor,
  expectedPositionMS,
  followerCorrection,
  followerDriftLevel,
  followerTargetMS,
  localClockMS,
  nextSeekLeadMS,
  type PartyTimeline,
  SEEK_THRESHOLD_MS,
  timelineRate,
  timelineStarted,
} from "./watchPartySync";

// Keeps the player's <video> on the Watch Party timeline (CORE-1677): holds it
// paused and positioned until the shared present time, then plays, re-seeking
// on a large drift and nudging the rate on a small one. The viewer's own play,
// pause and seek go to the party; the corrections this hook applies, and the
// browser's own autoplay, do not.
//
// In a lead party (CORE-2245) the lead's player is the timeline: this hook
// never moves it, and reports where it is about once a second. A follower's
// player is muted, has no transport of its own, and is held to the lead's
// timeline on a faster beat, shifted by this screen's display delay trim.

/** How often the drift is measured and corrected. */
const SYNC_INTERVAL_MS = 250;
/** How often a follower screen measures and corrects. */
const FOLLOW_INTERVAL_MS = 100;
/** How often the lead reports its position while it plays. */
const LEAD_REPORT_INTERVAL_MS = 1000;
/** A presented-frame sample older than this is not used for the lead's report. */
const FRAME_SAMPLE_MAX_AGE_MS = 250;
/** A follower's drift readout changes at most this often, unless its level changes. */
const FOLLOW_READOUT_INTERVAL_MS = 500;
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
  /**
   * Start a party around this title, carrying the current position and pause
   * state. `relaxed` lets every member control playback (CORE-1765); `lead`
   * starts a lead party with this screen as the lead (CORE-2245).
   */
  start: (relaxed?: boolean, lead?: boolean) => Promise<boolean>;
  leave: () => Promise<void>;
  seekLeadAfterAutoSkip: (streamSeconds: number) => void;
}

/** What this player is in the party: an ordinary member, the lead screen or a follower. */
type PartyRole = "off" | "member" | "lead" | "follower";

function roleOf(party: WatchPartyState): PartyRole {
  if (party.status !== "active" || !party.timeline) return "off";
  if (!party.lead) return "member";
  return party.isHost ? "lead" : "follower";
}

/** A frame the lead presented: its media position and the local clock when it shows. */
interface FrameSample {
  positionMS: number;
  atMS: number;
}

// A leave scheduled by an unmount, cancelled by a remount (React StrictMode
// mounts twice in development; only a real exit from the player leaves).
let pendingLeave: ReturnType<typeof setTimeout> | null = null;

export function useWatchParty({
  videoRef,
  attached,
  mediaId,
  onFollow,
  songScale = 1,
}: {
  videoRef: RefObject<HTMLVideoElement | null>;
  /** Whether the <video> is mounted (it is not while an error replaces the stage). */
  attached: boolean;
  mediaId: string;
  /** Move the player to the Media the party moved to. */
  onFollow: (item: string) => void;
  /** Original media seconds per stream second, after any applied tempo. */
  songScale?: number;
}): WatchPartyView {
  const party = useWatchPartyState();
  const [driftMS, setDriftMS] = useState<number | null>(null);
  const [playback, setPlayback] = useState<WatchPartyPlayback>("off");
  const [needsTap, setNeedsTap] = useState(false);
  // performance.now() of the player's mount and of its last correction. Input
  // from before the mount (the notification's Join click) is not counted.
  const mountedAt = useRef(performance.now());
  const lastCorrection = useRef(Number.NEGATIVE_INFINITY);
  const leadAutoSkip = useRef<number | null>(null);
  const mediaIdRef = useRef(mediaId);
  mediaIdRef.current = mediaId;
  // The element a follower muted, and the listener's own mute to give back.
  const followMute = useRef<{ video: HTMLVideoElement; muted: boolean } | null>(null);
  // How far ahead of its target a follower aims a seek, learned from where
  // seeks land, and whether one is waiting to be measured.
  const seekLead = useRef(0);
  const seekPending = useRef(false);
  // What this player last was in a party.
  const lastRole = useRef<PartyRole>("off");
  // The follower's last published drift readout.
  const readout = useRef({ at: Number.NEGATIVE_INFINITY, level: "" });

  const role = roleOf(party);
  const active = role !== "off";
  const timeline = party.timeline;
  const positionScale = 1000 * songScale;
  const baseRate = 1 / songScale;

  const correct = useCallback((change: () => void) => {
    lastCorrection.current = performance.now();
    change();
  }, []);

  const play = useCallback(
    (video: HTMLVideoElement) => {
      correct(() => {
        const started = video.play();
        if (started && typeof started.catch === "function")
          started.then(() => setNeedsTap(false)).catch(() => setNeedsTap(true));
      });
    },
    [correct],
  );

  // Hold a follower screen to the lead's timeline: muted, paused where the
  // lead is paused, and otherwise within the in-sync band of its target.
  const follow = useCallback(
    (video: HTMLVideoElement, tl: PartyTimeline) => {
      if (followMute.current?.video !== video) {
        followMute.current = { video, muted: listenerMuted(video) };
      }
      if (!listenerMuted(video)) setListenerMuted(video, true);
      if (video.readyState < 1 || video.seeking) return;
      const end = Number.isFinite(video.duration)
        ? video.duration * positionScale
        : Number.POSITIVE_INFINITY;
      const target = Math.min(end, followerTargetMS(tl, partyNowMS(), getDisplayTrimMS()));
      const drift = video.currentTime * positionScale - target;
      const base = timelineRate(tl);
      const streamRate = base / songScale;
      const correction = followerCorrection(drift, target, tl);
      if (tl.paused) {
        seekPending.current = false;
        if (video.playbackRate !== streamRate) video.playbackRate = streamRate;
        if (!video.paused) correct(() => video.pause());
        if (correction.kind === "seek")
          correct(() => {
            video.currentTime = correction.toMS / positionScale;
          });
        setPlayback("paused");
        setDriftMS(null);
        return;
      }
      if (seekPending.current) {
        // The first reading after a seek says how long the seek took.
        seekPending.current = false;
        // Drift is in media time; the lead is in wall time.
        seekLead.current = nextSeekLeadMS(seekLead.current, drift / base);
      }
      if (video.paused && !video.ended) play(video);
      if (correction.kind === "seek") {
        if (video.playbackRate !== streamRate) video.playbackRate = streamRate;
        seekPending.current = true;
        correct(() => {
          video.currentTime =
            Math.min(end, correction.toMS + seekLead.current * base) / positionScale;
        });
      } else {
        const rate = (correction.kind === "rate" ? correction.rate : base) / songScale;
        if (video.playbackRate !== rate) video.playbackRate = rate;
      }
      setPlayback("playing");
      const level = followerDriftLevel(drift);
      const now = performance.now();
      if (
        level !== readout.current.level ||
        now - readout.current.at >= FOLLOW_READOUT_INTERVAL_MS
      ) {
        readout.current = { at: now, level };
        setDriftMS(Math.round(drift));
      }
    },
    [correct, play, positionScale, songScale],
  );

  const sync = useCallback(() => {
    const s = getWatchParty();
    const tl = s.timeline;
    const video = videoRef.current;
    if (s.status !== "active" || !tl || !video || tl.item !== mediaIdRef.current) return;
    if (s.lead && s.isHost) {
      // The lead's player is the timeline: it is shown, never moved.
      setPlayback(video.paused ? "paused" : "playing");
      setDriftMS(null);
      return;
    }
    if (s.lead) {
      follow(video, tl);
      return;
    }
    if (video.readyState < 1 || video.seeking) return;
    const now = partyNowMS();
    const expected = expectedPositionMS(tl, now);
    const drift = video.currentTime * positionScale - expected;
    if (!timelineStarted(tl, now)) {
      if (video.playbackRate !== baseRate) video.playbackRate = baseRate;
      if (!video.paused) correct(() => video.pause());
      if (Math.abs(drift) > SEEK_THRESHOLD_MS)
        correct(() => {
          video.currentTime = expected / positionScale;
        });
      setPlayback(tl.paused ? "paused" : "waiting");
      setDriftMS(tl.paused ? Math.round(drift) : null);
      return;
    }
    if (video.paused) play(video);
    const correction = correctionFor(drift, expected, false);
    if (correction.kind === "seek") {
      video.playbackRate = baseRate;
      correct(() => {
        video.currentTime = correction.toMS / positionScale;
      });
    } else if (correction.kind === "rate") {
      video.playbackRate = correction.rate * baseRate;
    } else if (video.playbackRate !== baseRate) {
      video.playbackRate = baseRate;
    }
    setPlayback("playing");
    setDriftMS(Math.round(drift));
    reportPartyDrift(drift);
  }, [videoRef, correct, play, follow, positionScale, baseRate]);

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
      // Give back the speed this hook set; a lead's speed was always its own.
      const video = videoRef.current;
      if (video && lastRole.current !== "lead" && video.playbackRate !== 1) video.playbackRate = 1;
      lastRole.current = "off";
      return;
    }
    lastRole.current = role;
    sync();
    const beat = setInterval(sync, role === "follower" ? FOLLOW_INTERVAL_MS : SYNC_INTERVAL_MS);
    const untilStart = timeline.presentAtMS - partyNowMS();
    const atStart =
      role === "member" && !timeline.paused && untilStart > 0 ? setTimeout(sync, untilStart) : null;
    return () => {
      clearInterval(beat);
      if (atStart) clearTimeout(atStart);
    };
  }, [active, role, timeline, sync, videoRef]);

  // A follower screen is muted for as long as it follows, and from the moment
  // it starts joining a lead party: the sound belongs to the lead. The
  // listener's own mute comes back when it stops. A follower whose party ended
  // under it is paused first, so a screen never starts playing sound unasked.
  const silent = role === "follower" || (party.status === "joining" && party.lead);
  useEffect(() => {
    const video = videoRef.current;
    if (!silent || !attached || !video) return;
    const mute = () => {
      if (followMute.current?.video !== video) {
        followMute.current = { video, muted: listenerMuted(video) };
      }
      if (!listenerMuted(video)) setListenerMuted(video, true);
    };
    mute();
    video.addEventListener("volumechange", mute);
    return () => {
      video.removeEventListener("volumechange", mute);
      const held = followMute.current;
      followMute.current = null;
      seekPending.current = false;
      if (!held) return;
      const after = getWatchParty();
      const stillSilent = after.status === "joining" || (after.status === "active" && after.lead);
      const ownLeave = after.status === "idle" && !after.notice && !after.error;
      if (lastRole.current === "follower" && !stillSilent && !ownLeave && !held.video.paused) {
        held.video.pause();
      }
      setListenerMuted(held.video, held.muted);
    };
  }, [silent, attached, videoRef]);

  // The lead reports where its player is, about once a second while it plays
  // and whenever its speed changes. A presented frame gives the position and
  // the instant together; without one, the element's clock read now does.
  useEffect(() => {
    const video = videoRef.current;
    if (role !== "lead" || !attached || !video) return;
    let sample: FrameSample | null = null;
    let handle = 0;
    const frames = typeof video.requestVideoFrameCallback === "function";
    const onFrame: VideoFrameRequestCallback = (_now, frame) => {
      sample = {
        positionMS: frame.mediaTime * positionScale,
        atMS: performance.timeOrigin + frame.expectedDisplayTime,
      };
      handle = video.requestVideoFrameCallback(onFrame);
    };
    if (frames) handle = video.requestVideoFrameCallback(onFrame);
    const report = () => {
      // A stalled player has no position to report: its clock is not running.
      if (video.paused || video.seeking || video.readyState < 3) return;
      if (getWatchParty().timeline?.item !== mediaIdRef.current) return;
      const now = localClockMS();
      const fresh = sample && Math.abs(now - sample.atMS) <= FRAME_SAMPLE_MAX_AGE_MS;
      const at =
        fresh && sample ? sample : { positionMS: video.currentTime * positionScale, atMS: now };
      reportLeadPosition(at.positionMS, at.atMS, video.playbackRate * songScale);
    };
    // A stall holds the party where the lead stopped, and its end resumes it.
    const onStall = () => {
      const tl = getWatchParty().timeline;
      if (!tl || tl.paused || tl.item !== mediaIdRef.current || video.paused) return;
      void sendPartyTransport(tl.item, video.currentTime * positionScale, true);
    };
    const onPlaying = () => {
      const tl = getWatchParty().timeline;
      if (!tl || !tl.paused || tl.item !== mediaIdRef.current || video.paused) return;
      void sendPartyTransport(tl.item, video.currentTime * positionScale, false);
    };
    const beat = setInterval(report, LEAD_REPORT_INTERVAL_MS);
    const onSeeking = () => {
      sample = null;
    };
    video.addEventListener("ratechange", report);
    video.addEventListener("seeking", onSeeking);
    video.addEventListener("waiting", onStall);
    video.addEventListener("playing", onPlaying);
    return () => {
      clearInterval(beat);
      video.removeEventListener("ratechange", report);
      video.removeEventListener("seeking", onSeeking);
      video.removeEventListener("waiting", onStall);
      video.removeEventListener("playing", onPlaying);
      if (frames) video.cancelVideoFrameCallback(handle);
    };
  }, [role, attached, videoRef, positionScale, songScale]);

  // After a reload, a follower screen rejoins the lead party it was following.
  useEffect(() => {
    void resumeFollowedParty();
  }, []);

  // On the home network, listen for a lead party on another screen of this
  // Profile, and follow it unasked when this player is on the title it plays.
  // A party this screen left or could not join is only offered.
  const home = useHomeNetwork();
  useEffect(() => {
    if (!home) return;
    return watchForLeadParty();
  }, [home]);
  const offer = party.leadOffer;
  useEffect(() => {
    if (!offer || offer.declined || !mediaId || offer.item !== mediaId) return;
    void followLeadParty(offer.partyID, true);
  }, [offer, mediaId]);

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
      const s = getWatchParty();
      const tl = s.timeline;
      if (s.status !== "active" || !tl || tl.item !== mediaIdRef.current) return;
      if (event.type === "seeked" && leadAutoSkip.current !== null) {
        const target = leadAutoSkip.current;
        leadAutoSkip.current = null;
        if (Math.abs(video.currentTime - target) * 1000 <= SEEK_THRESHOLD_MS) return;
      }
      // The lead's player is the timeline however it was moved (a remote
      // control, a relock): nothing corrects it, so every event is its own.
      const input = lastViewerIntent();
      if (
        !(s.lead && s.isHost) &&
        (input < mountedAt.current ||
          performance.now() - input > INTENT_WINDOW_MS ||
          input < lastCorrection.current)
      ) {
        return;
      }
      // A follower screen has no transport: the next beat puts it back.
      if (s.lead && !s.isHost) return;
      const now = partyNowMS();
      const local = video.currentTime * positionScale;
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
  }, [active, attached, videoRef, positionScale]);

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
    async (relaxed = true, lead = false) => {
      const video = videoRef.current;
      if (!mediaId) return false;
      const position = video ? video.currentTime * positionScale : 0;
      const ok = lead
        ? await startWatchParty(mediaId, position, false, true)
        : await startWatchParty(mediaId, position, relaxed);
      if (ok && video)
        await sendPartyTransport(mediaId, video.currentTime * positionScale, video.paused);
      return ok;
    },
    [mediaId, videoRef, positionScale],
  );

  const seekLeadAfterAutoSkip = useCallback(
    (streamSeconds: number) => {
      const video = videoRef.current;
      const s = getWatchParty();
      if (!video || !s.lead || !s.isHost || s.timeline?.item !== mediaIdRef.current) return;
      leadAutoSkip.current = streamSeconds;
      video.currentTime = streamSeconds;
    },
    [videoRef],
  );

  return {
    party,
    driftMS,
    playback,
    needsTap,
    resume,
    start,
    leave: leaveParty,
    seekLeadAfterAutoSkip,
  };
}
