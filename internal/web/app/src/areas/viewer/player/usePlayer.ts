import Hls from "hls.js";
import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, getIdentityEpochSignal, graphql, resetIdentityEpoch } from "@/lib/api";
import { fetchPlaybackMarkers } from "@/lib/graphql";
import type { PlaybackMarker, ReportPlaybackVars } from "@/lib/graphql/types";
import {
  fetchPlaybackInfo,
  isRevealTerminalStatus,
  type PlaybackInfo,
  type PlaybackStream,
} from "../lib/playback";
import { resumePositionFromInfo, type TrickplayManifest } from "../lib/transport";
import { freshRecoveryBudget, nextRecoveryStep, resetRecoveryBudget } from "./hlsRecovery";

// The player engine, ported from the vanilla play() / startPreparedPlayback /
// applyPlaybackSource / reportProgress (app.js). It owns the <video> element,
// the hls.js instance, playback-info negotiation, the direct-play vs HLS route
// decision, resume seeking, progress beaconing, and the terminal/error states.
// The transport UI reads its returned state and drives it through the video ref.

export type PlayerPhase = "loading" | "playing" | "message" | "terminal";

export interface PlayerState {
  phase: PlayerPhase;
  /** A non-fatal message shown in place of the player chrome. */
  message: string;
  /** A reveal-terminal block (relocked/hidden/expired) — offers a Back affordance. */
  terminal: boolean;
  info: PlaybackInfo | null;
  stream: PlaybackStream | null;
  decision: string;
  markers: PlaybackMarker[];
  trickplay: TrickplayManifest | null;
  trickplayBaseURL: string;
  /** True when playing through hls.js (vs native <video src>). */
  hlsActive: boolean;
  /** True while hls.js is retrying after a fatal error; playback stays mounted.
   *  Optional so fixtures that predate recovery still type as a PlayerState. */
  recovering?: boolean;
  /** Monotonic counter bumped when the live hls.js track set changes; lets the
   *  transport re-derive its audio/subtitle menus off the live tracks. */
  tracksVersion: number;
}

const REPORT_INTERVAL_MS = 15000;
const REPORT_PLAYBACK_MUTATION =
  "mutation ReportPlayback($mediaID: ID!, $positionSeconds: Int!, $durationSeconds: Int!, $finished: Boolean!, $eventTimeMS: Int!) { reportPlayback(mediaID: $mediaID, positionSeconds: $positionSeconds, durationSeconds: $durationSeconds, finished: $finished, eventTimeMS: $eventTimeMS) { mediaID } }";
let lastPlaybackEventTimeMS = 0;

interface ProgressQueue {
  active: boolean;
  finalQueued: boolean;
  inFlight: { controller: AbortController; progress: ReportPlaybackVars } | null;
  pending: ReportPlaybackVars | null;
}

function createProgressQueue(): ProgressQueue {
  return { active: true, finalQueued: false, inFlight: null, pending: null };
}

function enqueueProgress(queue: ProgressQueue, progress: ReportPlaybackVars): void {
  if (!queue.active) return;
  if (queue.finalQueued && !progress.finished) return;
  if (progress.finished) queue.finalQueued = true;
  if (queue.inFlight) {
    if (!queue.inFlight.progress.finished) queue.pending = progress;
    return;
  }

  const controller = new AbortController();
  queue.inFlight = { controller, progress };
  void graphql<{ reportPlayback: { mediaID: string } }>(
    REPORT_PLAYBACK_MUTATION,
    progress as unknown as Record<string, unknown>,
    controller.signal,
  )
    .catch(() => controller.abort())
    .then(() => completeProgress(queue, controller));
}

function completeProgress(queue: ProgressQueue, controller: AbortController): void {
  if (queue.inFlight?.controller !== controller) return;
  queue.inFlight = null;
  if (!queue.active) return;
  const pending = queue.pending;
  queue.pending = null;
  if (pending) enqueueProgress(queue, pending);
}

function stopProgressQueue(queue: ProgressQueue): void {
  queue.active = false;
  queue.pending = null;
  queue.inFlight?.controller.abort();
  queue.inFlight = null;
}

function nextPlaybackEventTimeMS(): number {
  const eventTimeMS = Math.max(Date.now(), lastPlaybackEventTimeMS);
  lastPlaybackEventTimeMS = eventTimeMS;
  return eventTimeMS;
}

function emptyState(): PlayerState {
  return {
    phase: "loading",
    message: "",
    terminal: false,
    info: null,
    stream: null,
    decision: "",
    markers: [],
    trickplay: null,
    trickplayBaseURL: "",
    hlsActive: false,
    recovering: false,
    tracksVersion: 0,
  };
}

// The state a reveal-terminal response leaves behind. It is built from
// emptyState() rather than spread over the live state so the previously
// revealed playback document — its stream URL, up-next issuance, markers and
// trickplay manifest — stops being addressable the moment the epoch retires;
// carrying the old fields forward would leave the revoked manifest replayable.
function terminalState(message: string): PlayerState {
  return { ...emptyState(), phase: "terminal", terminal: true, message };
}

// hls.js reports the HTTP status of a refused segment/key/manifest fetch on
// data.response.code, but its XHR loader surfaces some failures with only the
// raw XMLHttpRequest attached as data.networkDetails. Read both shapes so a
// reveal-terminal refusal retires the epoch either way.
function hlsErrorStatus(data: unknown): number | undefined {
  if (!data || typeof data !== "object") return undefined;
  const response = Reflect.get(data, "response") as { code?: number } | undefined;
  if (typeof response?.code === "number") return response.code;
  const networkDetails = Reflect.get(data, "networkDetails") as { status?: number } | undefined;
  return typeof networkDetails?.status === "number" ? networkDetails.status : undefined;
}

function choosePlaybackStream(info: PlaybackInfo): PlaybackStream | null {
  return (info.streams || [])[0] || null;
}

function playbackDecision(info: PlaybackInfo, stream: PlaybackStream | null): string {
  if (info.decision) return info.decision;
  return stream && stream.kind ? stream.kind : "";
}

export function usePlayer(
  mediaID: string,
  messages: {
    couldNotReachServer: string;
    playbackUnavailable: string;
    transcodedPlaybackUnsupported: string;
    playbackUnsupportedCodec: string;
    playbackFailed: string;
    terminalUnavailable: string;
  },
  startAtSeconds = 0,
) {
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const hlsRef = useRef<Hls | null>(null);
  const progressTimerRef = useRef<number | null>(null);
  const progressQueueRef = useRef<ProgressQueue | null>(null);
  const [state, setState] = useState<PlayerState>(emptyState);
  // Bumped by retry() so the load effect re-runs for the same mediaID after a
  // non-terminal failure.
  const [retryKey, setRetryKey] = useState(0);
  const messagesRef = useRef(messages);
  messagesRef.current = messages;

  const retry = useCallback(() => setRetryKey((key) => key + 1), []);

  const destroyHls = useCallback(() => {
    if (hlsRef.current) {
      hlsRef.current.destroy();
      hlsRef.current = null;
    }
  }, []);

  const reportProgress = useCallback(
    (finished: boolean) => {
      const video = videoRef.current;
      if (!video || !mediaID || !video.duration) return;
      const eventTimeMS = nextPlaybackEventTimeMS();
      const queue = progressQueueRef.current;
      if (!queue) return;
      enqueueProgress(queue, {
        mediaID,
        positionSeconds: Math.floor(video.currentTime),
        durationSeconds: Math.floor(video.duration),
        finished,
        eventTimeMS,
      });
    },
    [mediaID],
  );

  const stopProgressReporting = useCallback(
    (finished: boolean) => {
      if (progressTimerRef.current) {
        clearInterval(progressTimerRef.current);
        progressTimerRef.current = null;
      }
      reportProgress(finished);
    },
    [reportProgress],
  );

  // Load + start playback once per mediaID.
  useEffect(() => {
    if (!mediaID) return;
    let alive = true;
    let videoErrorHandler: (() => void) | null = null;
    let videoCanPlayHandler: (() => void) | null = null;
    let resumeMetadataHandler: (() => void) | null = null;
    let tracksMetadataHandler: (() => void) | null = null;
    let tokenRefreshMetadataHandler: (() => void) | null = null;
    // The <video> the listeners were bound to: on unmount React detaches the
    // ref before this effect's cleanup runs, so cleanup cannot re-read it.
    let boundVideo: HTMLVideoElement | null = null;
    let recoveryTimer: number | null = null;
    const progressQueue = createProgressQueue();
    const identitySignal = getIdentityEpochSignal();
    const stopForIdentityChange = () => stopProgressQueue(progressQueue);
    progressQueueRef.current = progressQueue;
    if (identitySignal?.aborted) {
      stopForIdentityChange();
    } else {
      identitySignal?.addEventListener("abort", stopForIdentityChange, { once: true });
    }
    const msg = messagesRef.current;
    setState(emptyState());

    // A Reveal-terminal playback response (401/403/410 — relocked, hidden, or
    // an expired reveal token) means the server has ENDED the revealed
    // identity. Showing the terminal card is the player-local half; the other
    // half is an identity-epoch reset, without which the revealed session,
    // manifest, and Profile set stay on screen and reusable once the viewer
    // backs out of the player. Reported at most once per load: hls.js emits its
    // ERROR event per failed segment, and one ended identity is one transition.
    let revealTerminalReported = false;
    const enterRevealTerminal = () => {
      setState(terminalState(messagesRef.current.terminalUnavailable));
      if (revealTerminalReported) return;
      revealTerminalReported = true;
      resetIdentityEpoch();
    };

    async function start() {
      let info: PlaybackInfo;
      try {
        info = await fetchPlaybackInfo(mediaID);
      } catch (err) {
        if (!alive) return;
        if (err instanceof ApiError && isRevealTerminalStatus(err.status)) {
          enterRevealTerminal();
        } else if (err instanceof ApiError) {
          setState((s) => ({
            ...s,
            phase: "message",
            message: msg.playbackUnavailable,
          }));
        } else {
          setState((s) => ({
            ...s,
            phase: "message",
            message: msg.couldNotReachServer,
          }));
        }
        return;
      }
      if (!alive) return;

      const stream = choosePlaybackStream(info);
      const decision = playbackDecision(info, stream);
      if (
        (decision !== "direct-play" && decision !== "direct-stream" && decision !== "transcode") ||
        !stream ||
        !stream.url
      ) {
        setState((s) => ({
          ...s,
          phase: "message",
          message: msg.playbackUnavailable,
        }));
        return;
      }

      // Markers + trickplay manifest (best-effort; never block playback).
      const markers = await fetchPlaybackMarkers(mediaID).catch(() => []);
      const { trickplay, trickplayBaseURL } = await loadTrickplay(
        info.assets?.trickplay,
        info.assets?.expiresAt,
      );
      if (!alive) return;

      const video = videoRef.current;
      if (!video) return;
      boundVideo = video;

      // HLS streams (server transcode and direct-stream audio-copy) need the
      // HLS path. Safari keeps native HLS; other browsers play the fMP4
      // segments through hls.js via MSE; native HLS is the no-MSE fallback;
      // otherwise the honest message.
      let useHlsJs = false;
      if (stream.kind === "hls") {
        const nativeHLS = video.canPlayType("application/vnd.apple.mpegurl");
        const hlsJsOK = Hls.isSupported();
        const isApple = navigator.vendor === "Apple Computer, Inc.";
        if (nativeHLS && (isApple || !hlsJsOK)) {
          useHlsJs = false;
        } else if (hlsJsOK) {
          useHlsJs = true;
        } else {
          setState((s) => ({
            ...s,
            phase: "message",
            message: msg.transcodedPlaybackUnsupported,
          }));
          return;
        }
      }

      const resume = startAtSeconds > 0 ? startAtSeconds : resumePositionFromInfo(info);
      if (resume > 0) {
        resumeMetadataHandler = () => {
          if (alive) video.currentTime = resume;
        };
        video.addEventListener("loadedmetadata", resumeMetadataHandler, { once: true });
      }

      // Bumps tracksVersion so the transport re-derives its audio/subtitle menus
      // off the live track set (hls.js reports tracks asynchronously after the
      // manifest parses, and again as alternate renditions load). Server track
      // preferences are applied exactly once per load — on the first
      // MANIFEST_PARSED / loadedmetadata — so a later track-list update never
      // reverts the viewer's manual audio/subtitle choice.
      const bumpTracks = () => {
        setState((s) => (s.phase === "playing" ? { ...s, tracksVersion: s.tracksVersion + 1 } : s));
      };
      const applyPreferencesThenBump = () => {
        applyTrackPreferences(video, hlsRef.current, info.trackPreferences);
        bumpTracks();
      };

      if (useHlsJs) {
        const hls = new Hls();
        hlsRef.current = hls;
        // Fatal-error recovery (policy in hlsRecovery.ts): network errors
        // re-arm loading after a backoff, media errors go through hls.js's own
        // recoverMediaError path, and only an exhausted budget ends playback.
        // The "recovering" flag drives the non-modal reconnecting state.
        const recovery = freshRecoveryBudget();
        const setRecovering = (recovering: boolean) =>
          setState((s) => (s.recovering === recovering ? s : { ...s, recovering }));
        const failPlayback = () => {
          destroyHls();
          setState((s) => ({
            ...s,
            phase: "message",
            recovering: false,
            message: messagesRef.current.playbackFailed,
          }));
        };
        // A 401 on a segment/playlist request means the Stream Token expired
        // mid-session (tokens live max(1h, 2*duration)) — the Profile is
        // still valid, only the token is stale. Re-request playback info once
        // for a fresh token and reload the source at the current position
        // instead of ending the session; only a failed refresh or a second
        // 401 on the refreshed stream is treated as reveal-terminal. 403/410
        // stay reveal-terminal immediately (relocked/hidden, not a stale
        // token).
        let streamTokenRefreshed = false;
        const refreshStreamToken = () => {
          const resumePosition = video.currentTime;
          void fetchPlaybackInfo(mediaID)
            .then((refreshedInfo) => {
              if (!alive) return;
              const refreshedStream = choosePlaybackStream(refreshedInfo);
              if (!refreshedStream || !refreshedStream.url) {
                destroyHls();
                enterRevealTerminal();
                return;
              }
              tokenRefreshMetadataHandler = () => {
                if (alive) video.currentTime = resumePosition;
              };
              video.addEventListener("loadedmetadata", tokenRefreshMetadataHandler, {
                once: true,
              });
              hls.loadSource(refreshedStream.url);
              setState((s) => ({ ...s, info: refreshedInfo, stream: refreshedStream }));
            })
            .catch(() => {
              if (!alive) return;
              destroyHls();
              enterRevealTerminal();
            });
        };
        // Where the two paths meet, HTTP-status routing runs before the
        // in-place budget: the first 401 takes the one-shot token refresh and
        // a later 401/403/410 goes reveal-terminal, neither consuming a
        // startLoad/recoverMediaError attempt. hls.js never retries a 4xx, so
        // a 401 arrives as a fatal NETWORK_ERROR; checked the other way round
        // an auth failure would burn the transient-fault budget first.
        hls.on(Hls.Events.ERROR, (_event, data) => {
          const status = hlsErrorStatus(data);
          if (status === 401 && !streamTokenRefreshed) {
            streamTokenRefreshed = true;
            refreshStreamToken();
            return;
          }
          if (status && isRevealTerminalStatus(status)) {
            destroyHls();
            enterRevealTerminal();
            return;
          }
          if (!data || !data.fatal) return;
          const step = nextRecoveryStep(recovery, data.type);
          if (step.kind === "destroy") {
            failPlayback();
            return;
          }
          setRecovering(true);
          if (step.kind === "network") {
            recoveryTimer = window.setTimeout(() => {
              recoveryTimer = null;
              if (alive && hlsRef.current === hls) hls.startLoad();
            }, step.delayMs);
            return;
          }
          if (step.swapAudioCodec) hls.swapAudioCodec();
          hls.recoverMediaError();
        });
        hls.on(Hls.Events.FRAG_BUFFERED, () => {
          resetRecoveryBudget(recovery);
          setRecovering(false);
        });
        hls.on(Hls.Events.MANIFEST_PARSED, applyPreferencesThenBump);
        hls.on(Hls.Events.AUDIO_TRACKS_UPDATED, bumpTracks);
        hls.on(Hls.Events.SUBTITLE_TRACKS_UPDATED, bumpTracks);
        hls.loadSource(stream.url);
        hls.attachMedia(video);
      } else {
        // Fail-closed unsupported-codec state (ADR-0084 §Amendment A): the
        // native <video src> route (direct-play, direct-stream, native HLS) is
        // the one place a codec the Capability Profile *claimed* but cannot
        // actually decode surfaces — Media Capabilities / canPlayType probing is
        // optimistic, and an untested stream variant or DRM-required source can
        // still fail at the decoder. A MediaError of SRC_NOT_SUPPORTED (4) or
        // DECODE (3) means the format is undecodable here; show the honest
        // unsupported-codec message instead of hanging on a black frame. Any
        // other fatal media error falls back to the generic playback-failed
        // message. hls.js owns error handling on the MSE path, so this listener
        // is attached only on the native route.
        videoErrorHandler = () => {
          if (!alive) return;
          const code = video.error?.code;
          const undecodable = code === 4 || code === 3;
          setState((s) => ({
            ...s,
            phase: "message",
            message: undecodable
              ? messagesRef.current.playbackUnsupportedCodec
              : messagesRef.current.playbackFailed,
          }));
        };
        video.addEventListener("error", videoErrorHandler);
        video.src = stream.url;
        videoCanPlayHandler = () => {
          if (alive) {
            void video.play().catch(() => {});
          }
        };
        video.addEventListener("canplay", videoCanPlayHandler, { once: true });
        // Native <video> exposes its audio/text tracks once metadata loads.
        tracksMetadataHandler = applyPreferencesThenBump;
        video.addEventListener("loadedmetadata", tracksMetadataHandler, { once: true });
      }

      setState({
        phase: "playing",
        message: "",
        terminal: false,
        info,
        stream,
        decision,
        markers,
        trickplay,
        trickplayBaseURL,
        hlsActive: useHlsJs,
        recovering: false,
        tracksVersion: 0,
      });

      if (useHlsJs) {
        void video.play().catch(() => {});
      }

      // Periodic progress beacon while playing.
      progressTimerRef.current = window.setInterval(() => {
        if (videoRef.current && !videoRef.current.paused) {
          reportProgress(false);
        }
      }, REPORT_INTERVAL_MS);
    }

    void start();

    return () => {
      alive = false;
      if (recoveryTimer !== null) {
        clearTimeout(recoveryTimer);
        recoveryTimer = null;
      }
      if (progressTimerRef.current) {
        clearInterval(progressTimerRef.current);
        progressTimerRef.current = null;
      }
      identitySignal?.removeEventListener("abort", stopForIdentityChange);
      stopProgressQueue(progressQueue);
      if (progressQueueRef.current === progressQueue) progressQueueRef.current = null;
      destroyHls();
      const video = boundVideo ?? videoRef.current;
      if (video) {
        if (videoErrorHandler) {
          video.removeEventListener("error", videoErrorHandler);
        }
        if (videoCanPlayHandler) {
          video.removeEventListener("canplay", videoCanPlayHandler);
        }
        if (resumeMetadataHandler) {
          video.removeEventListener("loadedmetadata", resumeMetadataHandler);
        }
        if (tracksMetadataHandler) {
          video.removeEventListener("loadedmetadata", tracksMetadataHandler);
        }
        if (tokenRefreshMetadataHandler) {
          video.removeEventListener("loadedmetadata", tokenRefreshMetadataHandler);
        }
        video.removeAttribute("src");
        video.load();
      }
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mediaID, startAtSeconds, retryKey]);

  // Report on pause / seek / end edges.
  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    const onPause = () => reportProgress(false);
    const onSeeked = () => reportProgress(false);
    const onEnded = () => stopProgressReporting(true);
    video.addEventListener("pause", onPause);
    video.addEventListener("seeked", onSeeked);
    video.addEventListener("ended", onEnded);
    return () => {
      video.removeEventListener("pause", onPause);
      video.removeEventListener("seeked", onSeeked);
      video.removeEventListener("ended", onEnded);
    };
  }, [reportProgress, stopProgressReporting]);

  return { videoRef, hlsRef, state, retry };
}

interface TrackPreferences {
  audioLanguage?: string;
  preferredAudioLanguage?: string;
  subtitleLanguage?: string;
  preferredSubtitleLanguage?: string;
  subtitleMode?: string;
}

// applyTrackPreferences selects the server-preferred audio/subtitle tracks once
// the live track set is known, ported from applyTrackPreferences (app.js). It
// runs on every track-update so a preference applies whichever event surfaces
// the matching track. Unknown/absent preferences are a no-op.
function applyTrackPreferences(video: HTMLVideoElement, hls: Hls | null, raw: unknown): void {
  const preferences = raw as TrackPreferences | null;
  if (!preferences) {
    return;
  }
  const audioLanguage = preferences.audioLanguage || preferences.preferredAudioLanguage;
  const subtitleLanguage = preferences.subtitleLanguage || preferences.preferredSubtitleLanguage;
  if (audioLanguage && hls && Array.isArray(hls.audioTracks)) {
    const index = hls.audioTracks.findIndex(
      (track) => track.lang === audioLanguage || track.name === audioLanguage,
    );
    if (index >= 0) {
      hls.audioTrack = index;
    }
  }
  if (subtitleLanguage && hls && Array.isArray(hls.subtitleTracks)) {
    const index = hls.subtitleTracks.findIndex(
      (track) => track.lang === subtitleLanguage || track.name === subtitleLanguage,
    );
    if (index >= 0) {
      hls.subtitleDisplay = preferences.subtitleMode !== "off";
      hls.subtitleTrack = index;
    }
  }
  if (video.textTracks && subtitleLanguage) {
    for (const track of Array.from(video.textTracks)) {
      track.mode =
        track.language === subtitleLanguage || track.label === subtitleLanguage
          ? "showing"
          : "disabled";
    }
  }
}

// Loads the trickplay manifest (best-effort), ported from loadTrickplayManifest
// (app.js): an absent URL or an expired asset token suppresses trickplay; a
// reveal-terminal response is left to the player's HLS/info error paths.
async function loadTrickplay(
  url: string | undefined,
  expiresAt: string | undefined,
): Promise<{ trickplay: TrickplayManifest | null; trickplayBaseURL: string }> {
  if (!url || assetsExpired(expiresAt)) {
    return { trickplay: null, trickplayBaseURL: "" };
  }
  try {
    const response = await fetch(url, {
      credentials: "same-origin",
      signal: getIdentityEpochSignal(),
    });
    if (!response.ok) {
      return { trickplay: null, trickplayBaseURL: "" };
    }
    const trickplay = (await response.json()) as TrickplayManifest;
    return { trickplay, trickplayBaseURL: url };
  } catch {
    return { trickplay: null, trickplayBaseURL: "" };
  }
}

function assetsExpired(expiresAt: string | undefined): boolean {
  if (!expiresAt) return false;
  const expires = new Date(expiresAt);
  return !Number.isNaN(expires.getTime()) && expires.getTime() <= Date.now();
}
