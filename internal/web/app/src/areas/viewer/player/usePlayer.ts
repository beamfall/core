import Hls from "hls.js";
import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, getIdentityEpochSignal, graphql, resetIdentityEpoch } from "@/lib/api";
import { fetchPlaybackMarkers } from "@/lib/graphql";
import type { PlaybackMarker, ReportPlaybackVars } from "@/lib/graphql/types";
import {
  fetchPlaybackInfo,
  hlsStreamOf,
  isRevealTerminalStatus,
  type PlaybackInfo,
  type PlaybackMessageAction,
  type PlaybackRendition,
  type PlaybackStream,
  serverErrorReason,
  type UnavailableReasonMessages,
  unavailableReason,
} from "../lib/playback";
import {
  hlsLevelIndex,
  renditionIndex,
  resumePositionFromInfo,
  type TrickplayManifest,
} from "../lib/transport";
import {
  freshRecoveryBudget,
  HLS_FRAG_LOAD_ERROR,
  HLS_NETWORK_ERROR,
  nextRecoveryStep,
  resetRecoveryBudget,
} from "./hlsRecovery";

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
  /** The stream the engine is playing now. */
  stream: PlaybackStream | null;
  /** The whole stream inventory of the playback info (direct play plus the
   *  HLS manual-rendition pin). Optional so fixtures that predate it still
   *  type as a PlayerState. */
  streams?: PlaybackStream[];
  decision: string;
  markers: PlaybackMarker[];
  trickplay: TrickplayManifest | null;
  trickplayBaseURL: string;
  /** True when playing through hls.js (vs native <video src>). */
  hlsActive: boolean;
  /** True while the engine is retrying after a network or media fault; playback
   *  stays mounted. Optional so fixtures that predate recovery still type as a
   *  PlayerState. */
  recovering?: boolean;
  /** True while the browser reports no network: retries wait for it to return
   *  instead of spending their budget (CORE-1362). */
  waitingForNetwork?: boolean;
  /** What a message offers: Try again (the default) or Back. */
  action?: PlaybackMessageAction;
  /** The server's own reason for a refused request, shown under the message. */
  detail?: string;
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
  // Set when the player moved on (track change, unmount) with a finish report
  // still owed: no new reports are accepted, but the queued final one is sent.
  retiring: boolean;
  onDrained: (() => void) | null;
  inFlight: { controller: AbortController; progress: ReportPlaybackVars } | null;
  pending: ReportPlaybackVars | null;
}

function createProgressQueue(): ProgressQueue {
  return {
    active: true,
    finalQueued: false,
    retiring: false,
    onDrained: null,
    inFlight: null,
    pending: null,
  };
}

function enqueueProgress(queue: ProgressQueue, progress: ReportPlaybackVars): void {
  if (!queue.active || queue.retiring) return;
  if (queue.finalQueued && !progress.finished) return;
  if (progress.finished) queue.finalQueued = true;
  sendProgress(queue, progress);
}

function sendProgress(queue: ProgressQueue, progress: ReportPlaybackVars): void {
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
  if (pending) {
    sendProgress(queue, pending);
    return;
  }
  if (queue.retiring) stopProgressQueue(queue);
}

function stopProgressQueue(queue: ProgressQueue): void {
  queue.active = false;
  queue.pending = null;
  queue.inFlight?.controller.abort();
  queue.inFlight = null;
  const onDrained = queue.onDrained;
  queue.onDrained = null;
  onDrained?.();
}

// retireProgressQueue ends a queue when playback moves to another item. A
// finish report already queued (an auto-advance at `ended`) is still delivered,
// so a fully played track counts as finished; anything else is cancelled. An
// identity change still stops the queue outright (CORE-1143).
function retireProgressQueue(queue: ProgressQueue, onDrained: () => void): void {
  queue.onDrained = onDrained;
  const finalOwed =
    queue.active &&
    queue.finalQueued &&
    (queue.inFlight?.progress.finished === true || queue.pending?.finished === true);
  if (!finalOwed) {
    stopProgressQueue(queue);
    return;
  }
  queue.retiring = true;
  // Drop the in-flight periodic report so the final one goes out now.
  if (queue.inFlight && !queue.inFlight.progress.finished) {
    const final = queue.pending;
    queue.pending = null;
    queue.inFlight.controller.abort();
    queue.inFlight = null;
    if (final) sendProgress(queue, final);
  }
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
    streams: [],
    decision: "",
    markers: [],
    trickplay: null,
    trickplayBaseURL: "",
    hlsActive: false,
    recovering: false,
    waitingForNetwork: false,
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

// A fragment's media-timeline span from an hls.js FRAG_BUFFERED or
// FRAG_LOAD_ERROR payload (data.frag.start / duration).
function hlsFragSpan(data: unknown): { start: number; end: number } | undefined {
  if (!data || typeof data !== "object") return undefined;
  const frag = Reflect.get(data, "frag") as { start?: number; duration?: number } | undefined;
  if (typeof frag?.start !== "number" || !Number.isFinite(frag.start)) return undefined;
  const duration = typeof frag.duration === "number" && frag.duration > 0 ? frag.duration : 0;
  return { start: frag.start, end: frag.start + duration };
}

// Seconds of media buffered ahead of the playhead.
function bufferedAhead(video: HTMLVideoElement): number {
  const ranges = video.buffered;
  const now = video.currentTime;
  for (let i = 0; i < (ranges?.length ?? 0); i += 1) {
    if (ranges.start(i) <= now + 0.25 && ranges.end(i) > now) return ranges.end(i) - now;
  }
  return 0;
}

// A stream-bytes refusal: 401 for a stale or relocked Stream Token, 410 for a
// stream session the server ended (revocation, idle reap, preemption). On its
// own it is never proof that the revealed identity ended (CORE-1359): the
// player renegotiates through playback-info, whose answer decides.
function isStreamRefusalStatus(status: number): boolean {
  return status === 401 || status === 403 || status === 410;
}

function choosePlaybackStream(info: PlaybackInfo): PlaybackStream | null {
  return (info.streams || [])[0] || null;
}

// The stream of the same kind in a refreshed playback info: a token refresh
// must reload the route that is playing, which is not always streams[0] once
// direct play has switched to its HLS sibling. A missing kind is no match: the
// running engine cannot load a stream of another kind.
function streamOfKind(info: PlaybackInfo, kind: string | undefined): PlaybackStream | null {
  return (info.streams || []).find((entry) => entry.kind === kind) || null;
}

type Engine = "hlsjs" | "native" | "unsupported";

/** The engine that plays `stream` in this browser. */
function engineFor(video: HTMLVideoElement, stream: PlaybackStream): Engine {
  if (stream.kind !== "hls") return "native";
  const nativeHLS = video.canPlayType("application/vnd.apple.mpegurl");
  const hlsJsOK = Hls.isSupported();
  const isApple = navigator.vendor === "Apple Computer, Inc.";
  if (nativeHLS && (isApple || !hlsJsOK)) return "native";
  return hlsJsOK ? "hlsjs" : "unsupported";
}

/** The engine now playing after a switch to HLS, or false when none was possible. */
export type SwitchResult = "hlsjs" | "native" | false;

// The hls.js level to pin at an instance's next manifest parse (the rendition the
// viewer picked on direct play, or the manual level carried across a token refresh);
// consumed by that parse.
// A picked rendition is resolved against hls.js's level list at the parse, because the
// server's rendition order is not hls.js's level order.
const pendingLevelPins = new WeakMap<Hls, number | PlaybackRendition>();

/**
 * Applies the viewer's quality choice (-1 is Auto) to hls.js. A pin still waiting for
 * the manifest is replaced (or cancelled by Auto), so the parse cannot restore an
 * earlier choice over this one.
 */
export function selectHlsLevel(hls: Hls, level: number) {
  if (pendingLevelPins.has(hls)) {
    if (level >= 0) pendingLevelPins.set(hls, level);
    else pendingLevelPins.delete(hls);
  }
  hls.currentLevel = level;
}

/**
 * Applies a picked rendition to hls.js, resolving it against the level list. While the
 * manifest is still loading the list is empty, so the pick becomes the pending pin the
 * parse resolves (the latest pick wins). False when the rendition matches no level.
 */
export function selectHlsRendition(hls: Hls, rendition: PlaybackRendition | undefined): boolean {
  if (!rendition) return false;
  if (!hls.levels || hls.levels.length === 0) {
    pendingLevelPins.set(hls, rendition);
    return true;
  }
  const level = hlsLevelIndex(hls.levels, rendition);
  if (level < 0) return false;
  selectHlsLevel(hls, level);
  return true;
}

// Ranged GET of the first byte, aborted once headers arrive: the status of a
// native-route stream URL after MEDIA_ERR_NETWORK, which hides the HTTP status.
// The media routes are GET-only, so a HEAD probe would 405.
const STREAM_PROBE_TIMEOUT_MS = 5000;

async function probeStreamStatus(url: string): Promise<number | undefined> {
  const controller = new AbortController();
  // A hung probe must still end in a message, so it is bounded.
  const timeout = window.setTimeout(() => controller.abort(), STREAM_PROBE_TIMEOUT_MS);
  try {
    const response = await fetch(url, {
      headers: { Range: "bytes=0-0" },
      credentials: "same-origin",
      signal: controller.signal,
    });
    return response.status;
  } catch {
    return undefined;
  } finally {
    window.clearTimeout(timeout);
    controller.abort();
  }
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
    /** The item's file is missing on the server (reason media-file-unavailable). */
    mediaFileMissing?: string;
    /** The item is gone or outside this Profile (reason media-unavailable / 404). */
    mediaNotAvailable?: string;
    /** The stream stops before the item's end: the file is truncated or damaged. */
    sourceEndsEarly?: string;
  } & UnavailableReasonMessages,
  startAtSeconds = 0,
  /** A card queue's next item, declared to playback-info for a pre-minted handoff; "" when that queue has none. */
  nextMediaID?: string,
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
  // A ref, not an effect dependency: the next item rides along with the load
  // for this mediaID and never restarts playback on its own.
  const nextMediaIDRef = useRef(nextMediaID);
  nextMediaIDRef.current = nextMediaID;
  // Bound by the load effect once a stream is attached.
  const switchRef = useRef<((renditionKey?: string) => SwitchResult) | null>(null);
  const switchToHls = useCallback(
    (renditionKey?: string): SwitchResult => switchRef.current?.(renditionKey) ?? false,
    [],
  );

  // The last presented position, so Try again resumes where the viewer was
  // rather than at the opening ?t= or server resume point (CORE-1364).
  const lastPositionRef = useRef(0);
  const retryFromRef = useRef<{ mediaID: string; position: number } | null>(null);
  const retry = useCallback(() => {
    retryFromRef.current = { mediaID, position: lastPositionRef.current };
    setRetryKey((key) => key + 1);
  }, [mediaID]);

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
    // The native resume point not yet applied: until loadedmetadata seeks to it,
    // video.currentTime is 0 and must not stand in for the viewer's position.
    let pendingNativeResume = 0;
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
    const retryFrom = retryFromRef.current?.mediaID === mediaID ? retryFromRef.current.position : 0;
    retryFromRef.current = null;
    lastPositionRef.current = retryFrom;
    // Listeners bound to the <video> for the whole load (not one engine).
    const videoListeners: Array<[string, () => void]> = [];

    const setRecovering = (recovering: boolean) =>
      setState((s) => (s.recovering === recovering ? s : { ...s, recovering }));
    const showMessage = (
      message: string,
      options: { action?: PlaybackMessageAction; detail?: string } = {},
    ) =>
      setState((s) => ({
        ...s,
        phase: "message",
        message,
        recovering: false,
        waitingForNetwork: false,
        action: options.action ?? "retry",
        detail: options.detail ?? "",
      }));
    const showUnavailable = (reason: string | undefined, fallback: string) => {
      const { message, action } = unavailableReason(reason, messagesRef.current);
      showMessage(message || fallback, { action });
    };

    // Connectivity (CORE-1362): while the browser reports no network, a failed
    // load waits for it to come back instead of spending its retry budget, and
    // the 'online' event resumes it at the playhead.
    let offline = navigator.onLine === false;
    let resumeWhenOnline: (() => void) | null = null;
    let restartLoadWhenOnline: (() => void) | null = null;
    const setWaitingForNetwork = (waiting: boolean) =>
      setState((s) => (s.waitingForNetwork === waiting ? s : { ...s, waitingForNetwork: waiting }));
    const waitForNetwork = (resume: () => void) => {
      resumeWhenOnline = resume;
      setState((s) => ({ ...s, recovering: false, waitingForNetwork: true }));
    };
    const onOffline = () => {
      offline = true;
      setWaitingForNetwork(true);
    };
    const onOnline = () => {
      offline = false;
      setWaitingForNetwork(false);
      const resume = resumeWhenOnline;
      resumeWhenOnline = null;
      if (alive) (resume ?? restartLoadWhenOnline)?.();
    };
    window.addEventListener("offline", onOffline);
    window.addEventListener("online", onOnline);

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

    // A refused or failed playback-info request. Only playback-info itself
    // answering 401/403/410 is the explicit sign that the server ended the
    // revealed identity (CORE-1359); every other failure can be tried again,
    // except an item that is gone (404), which offers Back.
    const showPlaybackInfoError = (err: unknown) => {
      const m = messagesRef.current;
      if (err instanceof ApiError && isRevealTerminalStatus(err.status)) {
        enterRevealTerminal();
      } else if (err instanceof ApiError && err.status === 404) {
        showMessage(m.mediaNotAvailable || m.playbackUnavailable, { action: "back" });
      } else if (err instanceof ApiError) {
        showMessage(m.playbackUnavailable, { detail: serverErrorReason(err) });
      } else {
        showMessage(m.couldNotReachServer);
      }
    };

    async function start() {
      // Markers do not depend on playback info, so their request runs in
      // parallel with it; neither markers nor trickplay ever gate attaching
      // the stream (best-effort, applied when they arrive).
      const markersRequest = fetchPlaybackMarkers(mediaID).catch((): PlaybackMarker[] => []);
      let info: PlaybackInfo;
      try {
        info = await fetchPlaybackInfo(mediaID, nextMediaIDRef.current);
      } catch (err) {
        if (!alive) return;
        if (!(err instanceof ApiError) && offline) {
          waitForNetwork(() => void start());
          return;
        }
        showPlaybackInfoError(err);
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
        showUnavailable(info.reason, msg.playbackUnavailable);
        return;
      }

      const video = videoRef.current;
      if (!video) return;
      boundVideo = video;

      // The playhead: the native resume point not yet applied, else the
      // element's own position.
      const playheadPosition = () =>
        pendingNativeResume > 0
          ? pendingNativeResume
          : Number.isFinite(video.currentTime)
            ? video.currentTime
            : 0;
      // The native route's network retry budget (CORE-1361): hls.js keeps its
      // own per instance.
      const nativeRecovery = freshRecoveryBudget();
      // Real playback is progress: it refills the native retry budget and
      // re-arms the automatic renegotiation (hls.js counts FRAG_BUFFERED).
      const onPlaying = () => {
        setRecovering(false);
        if (hlsRef.current) return;
        resetRecoveryBudget(nativeRecovery);
        renegotiatedSinceProgress = false;
      };
      const onTimeUpdate = () => {
        if (pendingNativeResume === 0 && video.currentTime > 0) {
          lastPositionRef.current = video.currentTime;
        }
      };
      videoListeners.push(["playing", onPlaying], ["timeupdate", onTimeUpdate]);
      for (const [type, listener] of videoListeners) video.addEventListener(type, listener);

      // HLS streams (server transcode and direct-stream audio-copy) need the
      // HLS path. Safari keeps native HLS; other browsers play the fMP4
      // segments through hls.js via MSE; native HLS is the no-MSE fallback;
      // otherwise the honest message.
      const initialEngine = engineFor(video, stream);
      if (initialEngine === "unsupported") {
        showMessage(msg.transcodedPlaybackUnsupported);
        return;
      }
      let activeStream: PlaybackStream = stream;

      // Tears the current engine down: hls.js, every native listener, and the
      // <video> source. Used before an engine switch and on cleanup.
      const detachEngine = () => {
        if (recoveryTimer !== null) {
          clearTimeout(recoveryTimer);
          recoveryTimer = null;
        }
        destroyHls();
        if (videoErrorHandler) video.removeEventListener("error", videoErrorHandler);
        if (videoCanPlayHandler) video.removeEventListener("canplay", videoCanPlayHandler);
        if (resumeMetadataHandler)
          video.removeEventListener("loadedmetadata", resumeMetadataHandler);
        if (tracksMetadataHandler)
          video.removeEventListener("loadedmetadata", tracksMetadataHandler);
        if (tokenRefreshMetadataHandler) {
          video.removeEventListener("loadedmetadata", tokenRefreshMetadataHandler);
        }
        videoErrorHandler = null;
        videoCanPlayHandler = null;
        resumeMetadataHandler = null;
        tracksMetadataHandler = null;
        tokenRefreshMetadataHandler = null;
        video.removeAttribute("src");
        video.load();
      };

      // Stream renegotiation (CORE-1359). A refused stream request (see
      // isStreamRefusalStatus) asks playback-info again for a fresh stream of
      // the same kind and reloads it at the playhead; playback-info's answer
      // decides whether the identity ended. Concurrent refusals share one
      // request, and a second refusal before any playback progress fails with
      // Try again, so a stream the server keeps refusing cannot loop.
      let renegotiating = false;
      let renegotiatedSinceProgress = false;
      const renegotiate = (reload: (fresh: PlaybackStream, position: number) => void) => {
        if (renegotiating) return;
        if (renegotiatedSinceProgress) {
          detachEngine();
          showMessage(messagesRef.current.playbackFailed);
          return;
        }
        renegotiating = true;
        renegotiatedSinceProgress = true;
        const kind = activeStream.kind;
        const position = playheadPosition();
        setRecovering(true);
        // The refresh replaces info, so it declares the same next item: Up Next
        // and auto-advance survive a renewed token.
        void fetchPlaybackInfo(mediaID, nextMediaIDRef.current)
          .then((refreshed) => {
            if (!alive) return;
            const fresh = streamOfKind(refreshed, kind);
            if (!fresh?.url) {
              // Never load a stream of another kind into the running engine.
              detachEngine();
              showUnavailable(refreshed.reason, messagesRef.current.playbackFailed);
              return;
            }
            info = refreshed;
            activeStream = fresh;
            reload(fresh, position);
            // The refreshed info only renews the token: its decision may still name
            // the direct play this session switched away from, so the details keep
            // the decision of the stream actually playing.
            setState((s) => ({
              ...s,
              info: { ...refreshed, decision: s.decision },
              stream: fresh,
              streams: refreshed.streams ?? [],
            }));
          })
          .catch((err: unknown) => {
            if (!alive) return;
            detachEngine();
            showPlaybackInfoError(err);
          })
          .finally(() => {
            renegotiating = false;
          });
      };

      // Binds `target` to the <video> through `engine`, starting at `resume`.
      // `pinLevel` pins an hls.js level once the manifest parses; `play`
      // starts playback when the engine is ready.
      const attachEngine = (
        target: PlaybackStream,
        engine: "hlsjs" | "native",
        resume: number,
        options: { play: boolean; pinRendition?: PlaybackRendition },
      ) => {
        const useHlsJs = engine === "hlsjs";
        const streamURL = target.url ?? "";
        // hls.js takes the resume point as its startPosition and loads (and
        // seeks to) it directly; only the native <video src> route seeks on
        // metadata, so a resumed HLS session never fetches from 0 first.
        pendingNativeResume = 0;
        if (resume > 0 && !useHlsJs) {
          pendingNativeResume = resume;
          resumeMetadataHandler = () => {
            pendingNativeResume = 0;
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
          setState((s) =>
            s.phase === "playing" ? { ...s, tracksVersion: s.tracksVersion + 1 } : s,
          );
        };
        const applyPreferencesThenBump = () => {
          applyTrackPreferences(video, hlsRef.current, info.trackPreferences);
          bumpTracks();
        };

        if (useHlsJs) {
          const hls = new Hls(resume > 0 ? { startPosition: resume } : undefined);
          if (options.pinRendition) pendingLevelPins.set(hls, options.pinRendition);
          hlsRef.current = hls;
          // Fatal-error recovery (policy in hlsRecovery.ts): network errors
          // re-arm loading after a backoff, media errors go through hls.js's own
          // recoverMediaError path, and only an exhausted budget ends playback.
          // The "recovering" flag drives the non-modal reconnecting state.
          const recovery = freshRecoveryBudget();
          const failPlayback = () => {
            destroyHls();
            showMessage(messagesRef.current.playbackFailed);
          };
          // Re-arms loading once the network is back, with a full budget.
          const resumeLoad = () => {
            if (!alive || hlsRef.current !== hls) return;
            resetRecoveryBudget(recovery);
            setRecovering(true);
            hls.startLoad();
          };
          // A refused segment/playlist request renegotiates the stream (see
          // renegotiate) and reloads it in this instance at the playhead,
          // keeping the viewer's manual rendition.
          const reloadHls = (fresh: PlaybackStream, position: number) => {
            if (hlsRef.current !== hls) return;
            tokenRefreshMetadataHandler = () => {
              if (alive) video.currentTime = position;
            };
            video.addEventListener("loadedmetadata", tokenRefreshMetadataHandler, { once: true });
            // loadSource re-runs hls.js's auto startLoad with the configured
            // startPosition; point it at the playhead rather than the original
            // resume point.
            hls.config.startPosition = position;
            // The reload re-parses the manifest: carry the viewer's current manual
            // rendition (Auto is -1, so nothing is re-pinned) onto the new parse.
            if (hls.manualLevel >= 0) pendingLevelPins.set(hls, hls.manualLevel);
            hls.loadSource(fresh.url ?? "");
          };
          // HTTP-status routing runs before the in-place budget: a refusal
          // renegotiates without consuming a startLoad/recoverMediaError
          // attempt. hls.js never retries a 4xx, so a 401 arrives as a fatal
          // NETWORK_ERROR; checked the other way round an auth failure would
          // burn the transient-fault budget first. Offline network faults wait
          // for the network without spending the budget at all.
          hls.on(Hls.Events.ERROR, (_event, data) => {
            // A fresh stream is on its way: the old source's failures are moot.
            if (renegotiating) return;
            const status = hlsErrorStatus(data);
            if (status && isStreamRefusalStatus(status)) {
              renegotiate(reloadHls);
              return;
            }
            if (status === 404 && data?.details === HLS_FRAG_LOAD_ERROR) {
              stopAtSourceEnd(hlsFragSpan(data)?.start ?? video.currentTime + bufferedAhead(video));
              return;
            }
            if (!data || !data.fatal) return;
            if (offline && data.type === HLS_NETWORK_ERROR) {
              waitForNetwork(resumeLoad);
              return;
            }
            const step = nextRecoveryStep(recovery, data.type);
            if (step.kind === "destroy") {
              failPlayback();
              return;
            }
            setRecovering(true);
            if (step.kind === "network") {
              recoveryTimer = window.setTimeout(() => {
                recoveryTimer = null;
                if (!alive || hlsRef.current !== hls) return;
                if (offline) waitForNetwork(resumeLoad);
                else hls.startLoad();
              }, step.delayMs);
              return;
            }
            if (step.swapAudioCodec) hls.swapAudioCodec();
            hls.recoverMediaError();
          });
          // Only forward progress refills the budget: a fragment buffered past
          // the furthest point reached since the last seek. A recovery that
          // re-buffers the same fragment and fails again on the next must run
          // the budget down, not loop on it forever.
          let progressMark = Number.NEGATIVE_INFINITY;
          hls.on(Hls.Events.FRAG_BUFFERED, (_event, data) => {
            const span = hlsFragSpan(data);
            if (span) {
              if (span.end <= progressMark) return;
              progressMark = span.end;
            }
            resetRecoveryBudget(recovery);
            renegotiatedSinceProgress = false;
            setRecovering(false);
          });
          // A fragment 404 is the server saying the source has no media there
          // (a truncated or damaged file; past-the-end segments are refused
          // with 404). hls.js would step down every rendition and then fail
          // with no reason, so stop loading at the cut, play what is buffered,
          // and say why once the playhead reaches it. A seek back before the
          // cut loads again; a seek past it explains at once.
          let sourceEndsAt: number | null = null;
          let stoppedAtSourceEnd = false;
          const showSourceEndsEarly = () => {
            destroyHls();
            showMessage(messagesRef.current.sourceEndsEarly ?? messagesRef.current.playbackFailed);
          };
          // With loading stopped, nothing more will arrive: an empty buffer
          // ahead is the cut even when the last fragment ends short of it.
          const atSourceEnd = () =>
            sourceEndsAt !== null &&
            (video.currentTime >= sourceEndsAt - 0.5 ||
              (stoppedAtSourceEnd && bufferedAhead(video) < 0.5));
          const stopAtSourceEnd = (at: number) => {
            sourceEndsAt = sourceEndsAt === null ? at : Math.min(sourceEndsAt, at);
            stoppedAtSourceEnd = true;
            hls.stopLoad();
            setRecovering(false);
            if (video.paused ? video.currentTime >= sourceEndsAt - 0.5 : atSourceEnd()) {
              showSourceEndsEarly();
            }
          };
          const onSourceEndWaiting = () => {
            if (hlsRef.current === hls && atSourceEnd()) showSourceEndsEarly();
          };
          const onSourceEndSeeking = () => {
            if (hlsRef.current !== hls) return;
            progressMark = video.currentTime;
            if (sourceEndsAt === null) return;
            if (video.currentTime >= sourceEndsAt - 0.5) showSourceEndsEarly();
            else {
              stoppedAtSourceEnd = false;
              hls.startLoad(video.currentTime);
            }
          };
          videoListeners.push(["waiting", onSourceEndWaiting], ["seeking", onSourceEndSeeking]);
          // Back online with no fatal error parked, hls.js may still be in its
          // own retry backoff (up to 8 s). While the buffer is short, restart
          // the load at the playhead now instead of waiting out that timer.
          restartLoadWhenOnline = () => {
            if (hlsRef.current !== hls || stoppedAtSourceEnd || video.ended) return;
            if (bufferedAhead(video) >= (hls.config?.maxBufferLength ?? 30)) return;
            hls.startLoad(video.currentTime);
          };
          video.addEventListener("waiting", onSourceEndWaiting);
          video.addEventListener("seeking", onSourceEndSeeking);
          hls.on(Hls.Events.MANIFEST_PARSED, () => {
            // One-shot: a later manifest parse (token refresh) must not restore an
            // earlier pick over the viewer's current choice (see reloadHls).
            const pin = pendingLevelPins.get(hls);
            pendingLevelPins.delete(hls);
            if (pin !== undefined) {
              const level = typeof pin === "number" ? pin : hlsLevelIndex(hls.levels, pin);
              if (level >= 0) hls.currentLevel = level;
            }
            applyPreferencesThenBump();
          });
          hls.on(Hls.Events.AUDIO_TRACKS_UPDATED, bumpTracks);
          hls.on(Hls.Events.SUBTITLE_TRACKS_UPDATED, bumpTracks);
          hls.loadSource(streamURL);
          hls.attachMedia(video);
          if (options.play) void video.play().catch(() => {});
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
            // MEDIA_ERR_NETWORK hides the HTTP status (CORE-1361). Offline, wait
            // for the network; otherwise probe the stream URL: a refusal
            // renegotiates the stream, and any other fault reloads the source at
            // the playhead on the same 1/2/4 s backoff hls.js uses.
            if (code === 2) {
              const play = !video.paused || video.played.length === 0;
              const position = playheadPosition();
              const reloadAtPlayhead = () => {
                if (alive && activeStream === target) reloadNative(target, position, play);
              };
              if (offline) {
                waitForNetwork(reloadAtPlayhead);
                return;
              }
              void probeStreamStatus(streamURL).then((status) => {
                if (!alive || activeStream !== target || renegotiating) return;
                if (status !== undefined && isStreamRefusalStatus(status)) {
                  renegotiate((fresh, at) => reloadNative(fresh, at, play));
                  return;
                }
                if (offline) {
                  waitForNetwork(reloadAtPlayhead);
                  return;
                }
                const step = nextRecoveryStep(nativeRecovery, HLS_NETWORK_ERROR);
                if (step.kind !== "network") {
                  showMessage(messagesRef.current.playbackFailed);
                  return;
                }
                setRecovering(true);
                recoveryTimer = window.setTimeout(() => {
                  recoveryTimer = null;
                  if (offline) waitForNetwork(reloadAtPlayhead);
                  else reloadAtPlayhead();
                }, step.delayMs);
              });
              return;
            }
            const undecodable = code === 4 || code === 3;
            // Direct play the browser cannot decode falls over to the HLS
            // sibling once, at the position playback had reached.
            if (undecodable && target.kind !== "hls" && switchToHls()) return;
            showMessage(
              undecodable
                ? messagesRef.current.playbackUnsupportedCodec
                : messagesRef.current.playbackFailed,
            );
          };
          video.addEventListener("error", videoErrorHandler);
          video.src = streamURL;
          videoCanPlayHandler = () => {
            if (alive && options.play) {
              void video.play().catch(() => {});
            }
          };
          video.addEventListener("canplay", videoCanPlayHandler, { once: true });
          // Native <video> exposes its audio/text tracks once metadata loads.
          tracksMetadataHandler = applyPreferencesThenBump;
          video.addEventListener("loadedmetadata", tracksMetadataHandler, { once: true });
        }
      };

      // Reloads a native-route stream at `position` (a retry or a renewed token).
      const reloadNative = (target: PlaybackStream, position: number, play: boolean) => {
        detachEngine();
        activeStream = target;
        attachEngine(target, "native", position, { play });
      };

      // Switches direct play to the HLS stream of the same inventory at the
      // current position: a rendition pick (`renditionKey`) or the failover
      // from a decode error. Returns the engine now playing, or false when
      // there is nothing playable to switch to (state is left untouched).
      const switchToHls = (renditionKey?: string): SwitchResult => {
        if (!alive || activeStream.kind === "hls") return false;
        const sibling = hlsStreamOf(info.streams);
        if (!sibling?.url) return false;
        const engine = engineFor(video, sibling);
        if (engine === "unsupported") return false;
        let pinRendition: PlaybackRendition | undefined;
        if (renditionKey !== undefined) {
          const at = renditionIndex(sibling.renditions, renditionKey);
          if (at < 0) return false;
          pinRendition = sibling.renditions?.[at];
        }
        const position = playheadPosition();
        // A rendition pick keeps the viewer's pause. The failover also plays when
        // playback never began: the error can fire before `canplay`, while the
        // element is still paused only because autoplay had not yet kicked in.
        const play = !video.paused || (renditionKey === undefined && video.played.length === 0);
        detachEngine();
        activeStream = sibling;
        attachEngine(sibling, engine, position, { play, pinRendition });
        setState((s) => ({
          ...s,
          stream: sibling,
          decision: "transcode",
          // Keep the info in step: the details dialog reads info.decision first.
          info: s.info ? { ...s.info, decision: "transcode" } : s.info,
          hlsActive: engine === "hlsjs",
          recovering: false,
          tracksVersion: s.tracksVersion + 1,
        }));
        return engine;
      };
      switchRef.current = switchToHls;

      const opening =
        retryFrom > 0
          ? retryFrom
          : startAtSeconds > 0
            ? startAtSeconds
            : resumePositionFromInfo(info);
      attachEngine(stream, initialEngine, opening, { play: true });

      setState({
        phase: "playing",
        message: "",
        terminal: false,
        info,
        stream,
        streams: info.streams ?? [],
        decision,
        markers: [],
        trickplay: null,
        trickplayBaseURL: "",
        hlsActive: initialEngine === "hlsjs",
        recovering: false,
        waitingForNetwork: offline,
        tracksVersion: 0,
      });

      // Applied only while still playing: a terminal or message state must not
      // pick the revealed markers/trickplay back up.
      void markersRequest.then((markers) => {
        if (alive) setState((s) => (s.phase === "playing" ? { ...s, markers } : s));
      });
      void loadTrickplay(info.assets?.trickplay, info.assets?.expiresAt).then(
        ({ trickplay, trickplayBaseURL }) => {
          if (alive) {
            setState((s) => (s.phase === "playing" ? { ...s, trickplay, trickplayBaseURL } : s));
          }
        },
      );

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
      switchRef.current = null;
      window.removeEventListener("offline", onOffline);
      window.removeEventListener("online", onOnline);
      if (recoveryTimer !== null) {
        clearTimeout(recoveryTimer);
        recoveryTimer = null;
      }
      if (progressTimerRef.current) {
        clearInterval(progressTimerRef.current);
        progressTimerRef.current = null;
      }
      retireProgressQueue(progressQueue, () =>
        identitySignal?.removeEventListener("abort", stopForIdentityChange),
      );
      if (progressQueueRef.current === progressQueue) progressQueueRef.current = null;
      destroyHls();
      const video = boundVideo ?? videoRef.current;
      if (video) {
        for (const [type, listener] of videoListeners) video.removeEventListener(type, listener);
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

  return { videoRef, hlsRef, state, retry, switchToHls };
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
