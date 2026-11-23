import Hls from "hls.js";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ApiError,
  getExpectedProfile,
  getIdentityEpochSignal,
  graphql,
  resetIdentityEpoch,
} from "@/lib/api";
import { fetchPlaybackMarkers } from "@/lib/graphql";
import type { PlaybackMarker, ReportPlaybackVars } from "@/lib/graphql/types";
import {
  endPlayback,
  fetchPlaybackInfo,
  hlsStreamOf,
  isRevealTerminalStatus,
  type PlaybackAdjustment,
  type PlaybackCaptionTrack,
  type PlaybackInfo,
  type PlaybackMessageAction,
  type PlaybackRendition,
  type PlaybackStream,
  serverErrorReason,
  songSecondsPerStreamSecond,
  type UnavailableReasonMessages,
  unavailableReason,
} from "../lib/playback";
import {
  hlsLevelIndex,
  playbackDurationSeconds,
  renditionIndex,
  resumePositionFromInfo,
  type TrickplayManifest,
} from "../lib/transport";
import type { StartTracks } from "../lib/watchQueue";
import { remoteHoldsAutoplay, remotePlayStateMark } from "../remote/remoteControl";
import {
  freshRecoveryBudget,
  HLS_FRAG_LOAD_ERROR,
  HLS_NETWORK_ERROR,
  nextRecoveryStep,
  resetRecoveryBudget,
} from "./hlsRecovery";
import { loadSleepMark, sleepHoldsAutoplay, sleepStopMark } from "./sleepStop";
import {
  attachCaptionElements,
  captionLocale,
  isBurnInCaption,
  matchSelectedCaption,
  preferredLanguageIndex,
  removeCaptionElements,
  showCaptionElement,
  shownCaptionElement,
  trackPreferencePolicy,
} from "./streamTracks";
import { getLeadPauseIntent, getWatchParty, partyNowMS } from "./watchPartyStore";
import { expectedPositionMS } from "./watchPartySync";

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
  /** The Media that info came from. Right after a route change the page renders
   *  once with the previous Media's state, so info describes another Media
   *  whenever this differs from the one asked for. */
  mediaID?: string;
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
  /** The key and tempo this load asked playback-info for (CORE-1659); the
   *  echo in info.adjustment says whether the server applied them. */
  adjustment?: PlaybackAdjustment;
  /** Monotonic counter bumped when the live hls.js track set changes; lets the
   *  transport re-derive its audio/subtitle menus off the live tracks. */
  tracksVersion: number;
  /** Picks among the file's audio and caption tracks (CORE-1709). Optional so
   *  fixtures that predate it still type as a PlayerState. */
  trackControl?: StreamTrackControl;
}

/**
 * The file-level track choice (CORE-1709): the audio and caption tracks the
 * playback info lists, whichever route plays them. An audio pick or a burned-in
 * subtitle asks the server for a stream built for it and reloads at the
 * playhead; a text caption toggles a <track> in place.
 */
export interface StreamTrackControl {
  /** The caption track showing (or burned in) now; null when subtitles are off. */
  activeCaptionID(): string | null;
  /** Plays the audio stream with this file stream index. */
  selectAudio(streamIndex: number): void;
  /** Shows the caption track with this id; null turns subtitles off. */
  selectCaption(id: string | null): void;
}

/** The viewer's track picks for one Media; unset fields leave the server's choice. */
interface TrackChoice {
  mediaID: string;
  audioIndex?: number;
  burnIndex?: number;
  /** The caption picked: an id, null for off, undefined for no pick yet. */
  captionID?: string | null;
  /** The server's start-of-playback subtitle pick has been taken up (CORE-1680). */
  startCaptionAdopted?: boolean;
}

/** The picks a detail page made before playback (CORE-2442), as this Media's first choice. */
function startChoice(mediaID: string, start: StartTracks | undefined): TrackChoice {
  if (!start || start.mediaID !== mediaID) return { mediaID };
  return { mediaID, audioIndex: start.audioIndex, captionID: start.captionID };
}

/**
 * Takes up the start-of-playback subtitle, once per Media and only before the
 * viewer picks in the player: the caption picked on the detail page when this
 * file has it, else the server's pick. A text track is shown when the stream
 * attaches, and a picture-based one needs the stream asked for again with it
 * burned in, which the return value reports.
 */
function adoptStartCaption(choice: TrackChoice, info: PlaybackInfo): boolean {
  if (choice.startCaptionAdopted) return false;
  choice.startCaptionAdopted = true;
  if (choice.burnIndex !== undefined || choice.captionID === null) return false;
  let track: PlaybackCaptionTrack | undefined;
  if (choice.captionID !== undefined) {
    track = info.captionTracks?.find((candidate) => candidate.id === choice.captionID);
    // A pick this file does not hold (another Version played) falls back.
    if (!track) choice.captionID = undefined;
  }
  track ??= matchSelectedCaption(info.captionTracks, info.selectedSubtitleTrack);
  if (!track) return false;
  choice.captionID = track.id;
  if (!isBurnInCaption(track)) return false;
  choice.burnIndex = track.streamIndex;
  return true;
}

const REPORT_INTERVAL_MS = 15000;
/** The song as recorded: no pitch shift, the recorded tempo. */
export const DEFAULT_ADJUSTMENT: PlaybackAdjustment = { pitchShiftSemitones: 0, tempo: 1 };
const REPORT_PLAYBACK_MUTATION =
  "mutation ReportPlayback($mediaID: ID!, $positionSeconds: Int!, $durationSeconds: Int!, $finished: Boolean!, $eventTimeMS: Int!, $paused: Boolean, $playbackID: String, $playedMS: Int) { reportPlayback(mediaID: $mediaID, positionSeconds: $positionSeconds, durationSeconds: $durationSeconds, finished: $finished, eventTimeMS: $eventTimeMS, paused: $paused, playbackID: $playbackID, playedMS: $playedMS) { mediaID } }";
let lastPlaybackEventTimeMS = 0;

/** A random ID for one play of an item (CORE-1703): play history groups the
 *  play's reports by it. getRandomValues, unlike randomUUID, works on a server
 *  reached over plain HTTP. */
function newPlaybackID(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
}

interface QueuedProgress {
  progress: ReportPlaybackVars;
  // Sent by the periodic timer rather than a play, pause, seek or end.
  heartbeat: boolean;
}

interface ProgressQueue {
  playbackID: string;
  // How long this play has actually played, in milliseconds, and the
  // performance.now() it last started playing (null: not playing now); see
  // syncPlayedClock. Play history credits the growth of the count it sends.
  playedMS: number;
  playingSince: number | null;
  active: boolean;
  finalQueued: boolean;
  // Set when the player moved on (another item or play, unmount): no new
  // reports are accepted, but the ones it holds are still sent, in order.
  retiring: boolean;
  // A replay's queue waits behind the previous play's until that one has
  // delivered its finish, so the finish always arrives first (CORE-1703).
  blocked: boolean;
  next: ProgressQueue | null;
  onDrained: (() => void) | null;
  inFlight: { controller: AbortController; progress: ReportPlaybackVars } | null;
  // Reports waiting for the one in flight, sent one at a time in the order
  // they were made: play history reads each step between them, so none is
  // dropped or reordered (CORE-1703).
  queued: QueuedProgress[];
}

function createProgressQueue(playbackID: string): ProgressQueue {
  return {
    playbackID,
    playedMS: 0,
    playingSince: null,
    active: true,
    finalQueued: false,
    retiring: false,
    blocked: false,
    next: null,
    onDrained: null,
    inFlight: null,
    queued: [],
  };
}

// openProgressQueue starts the reporting for a new play: a new playback ID
// and queue, stopped outright if the revealed identity changes (CORE-1143).
// Given a previous play still delivering its reports, the new queue sends
// nothing until that one is done.
function openProgressQueue(after?: ProgressQueue): ProgressQueue {
  const queue = createProgressQueue(newPlaybackID());
  if (after?.active) {
    queue.blocked = true;
    after.next = queue;
  }
  const identitySignal = getIdentityEpochSignal();
  const stopForIdentityChange = () => stopProgressQueue(queue);
  queue.onDrained = () => identitySignal?.removeEventListener("abort", stopForIdentityChange);
  if (identitySignal?.aborted) {
    stopForIdentityChange();
  } else {
    identitySignal?.addEventListener("abort", stopForIdentityChange, { once: true });
  }
  return queue;
}

function enqueueProgress(
  queue: ProgressQueue,
  progress: ReportPlaybackVars,
  heartbeat: boolean,
): void {
  if (!queue.active || queue.retiring) return;
  if (queue.finalQueued && !progress.finished) return;
  if (progress.finished) queue.finalQueued = true;
  if (!queue.inFlight && !queue.blocked) {
    sendProgress(queue, progress);
    return;
  }
  // The one merge allowed: a heartbeat replaces the heartbeat queued just
  // before it, in the same state, since the later one says all it did.
  const last = queue.queued.at(-1);
  if (
    heartbeat &&
    last?.heartbeat &&
    last.progress.paused === progress.paused &&
    last.progress.playbackID === progress.playbackID
  ) {
    last.progress = progress;
    return;
  }
  queue.queued.push({ progress, heartbeat });
}

function sendProgress(queue: ProgressQueue, progress: ReportPlaybackVars): void {
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
  advanceProgressQueue(queue);
}

// advanceProgressQueue sends a queue's next report once nothing is in flight
// and no earlier play holds it back. A retired queue that has sent everything
// stops, and the replay waiting behind it may send. A failed report is not
// sent again, so a failed finish releases the replay too and can never arrive
// after it. A queue stopped by an identity change releases nothing; the
// replay's own queue stops for the same change.
function advanceProgressQueue(queue: ProgressQueue): void {
  if (!queue.active || queue.blocked || queue.inFlight) return;
  const next = queue.queued.shift();
  if (next) {
    sendProgress(queue, next.progress);
    return;
  }
  if (!queue.retiring) return;
  stopProgressQueue(queue);
  const after = queue.next;
  queue.next = null;
  if (!after) return;
  after.blocked = false;
  advanceProgressQueue(after);
}

function stopProgressQueue(queue: ProgressQueue): void {
  queue.active = false;
  queue.queued = [];
  queue.inFlight?.controller.abort();
  queue.inFlight = null;
  const onDrained = queue.onDrained;
  queue.onDrained = null;
  onDrained?.();
}

// retireProgressQueue ends a play's queue when playback moves to another
// item or play. It accepts no new reports but never drops one it holds: they
// are sent in order, a finish last, even while the queue still waits behind an
// earlier play's. An identity change still stops it outright (CORE-1143).
function retireProgressQueue(queue: ProgressQueue): void {
  if (!queue.active || queue.retiring) return;
  queue.retiring = true;
  advanceProgressQueue(queue);
}

// syncPlayedClock brings a play's played count up to now, then keeps it
// running only while the video actually plays: not paused, seeking, ended or
// waiting for data. A seek adds nothing, and the wall clock plays no part
// (CORE-1703).
function syncPlayedClock(queue: ProgressQueue, video: HTMLVideoElement): void {
  const now = performance.now();
  if (queue.playingSince !== null) queue.playedMS += now - queue.playingSince;
  const playing =
    !video.paused &&
    !video.seeking &&
    !video.ended &&
    video.readyState >= HTMLMediaElement.HAVE_FUTURE_DATA;
  queue.playingSince = playing ? now : null;
}

// nextPlaybackEventTimeMS is the wall clock, never before the last time sent,
// so the server's latest-report-wins check never sees this player go
// backward. A clock set back holds it only until the wall clock passes the
// last time sent again, so a corrected fast clock stops leading. It orders
// and dates reports; play history measures a play by its played count.
function nextPlaybackEventTimeMS(): number {
  const eventTimeMS = Math.max(lastPlaybackEventTimeMS, Date.now());
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
export function engineFor(video: HTMLVideoElement, stream: PlaybackStream): Engine {
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
  /** Where to open, in seconds; undefined resumes from saved progress. */
  startAtSeconds?: number,
  /** A card queue's next item, declared to playback-info for a pre-minted handoff; "" when that queue has none. */
  nextMediaID?: string,
  /** The Version id the viewer picked for this Media (CORE-1676); "" lets the server choose. */
  selectedVersion = "",
  /** Audio and caption picks made before playback (CORE-2442); used only for their Media. */
  startTracks?: StartTracks,
  /** One explicit start's identity (PlayerNavState.startID): a new one loads afresh. */
  startID?: string,
  /** Restore the lead's shared playback when returning to the player. */
  restorePartyPlayback = false,
) {
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const hlsRef = useRef<Hls | null>(null);
  const progressTimerRef = useRef<number | null>(null);
  const progressQueueRef = useRef<ProgressQueue | null>(null);
  // The playback ID of the play under way and the item it is for.
  // The play under way and its queue. A reload of the same item (Try again, a
  // quality or key change) carries on this play and its queue, so the queued
  // reports go out in order before the reload's (CORE-1703).
  const playRef = useRef<{ mediaID: string; queue: ProgressQueue } | null>(null);
  const [state, setState] = useState<PlayerState>(emptyState);
  // Bumped by retry() so the load effect re-runs for the same mediaID after a
  // non-terminal failure.
  const [retryKey, setRetryKey] = useState(0);
  // The <video> the current load plays on. Try again after an error plays on
  // a new element, so the reporting listeners follow this, not the first
  // element the ref held (CORE-1703). Each load sets a new object, so the
  // listeners rebind even when two loads in a row start with no element (an
  // error screen shows none) and the ref fills in on the next render.
  const [loadedVideo, setLoadedVideo] = useState<{ element: HTMLVideoElement | null } | null>(null);
  const messagesRef = useRef(messages);
  messagesRef.current = messages;
  // A ref, not an effect dependency: the next item rides along with the load
  // for this mediaID and never restarts playback on its own.
  const nextMediaIDRef = useRef(nextMediaID);
  nextMediaIDRef.current = nextMediaID;
  // Also a ref: a new pick reloads through retry(), so the reload resumes at
  // the playhead, and a renewed token keeps the Version that is playing.
  const selectedVersionRef = useRef(selectedVersion);
  selectedVersionRef.current = selectedVersion;
  // The viewer's audio and caption picks: refs, since a pick that needs a new
  // stream reloads through retry() at the playhead. Another Media starts with
  // the server's choice.
  const trackChoiceRef = useRef<TrackChoice>(startChoice(mediaID, startTracks));
  if (trackChoiceRef.current.mediaID !== mediaID) {
    trackChoiceRef.current = startChoice(mediaID, startTracks);
  }
  // Read by the load effect: every explicit start (a new startID or ?t= for
  // the same Media, as Start over while docked) begins from the picks it was
  // handed.
  const startTracksRef = useRef(startTracks);
  startTracksRef.current = startTracks;
  const captionTracksRef = useRef<PlaybackCaptionTrack[]>([]);
  const stateRef = useRef(state);
  stateRef.current = state;
  // Bound by the load effect once a stream is attached.
  const switchRef = useRef<((renditionKey?: string) => SwitchResult) | null>(null);
  const switchToHls = useCallback(
    (renditionKey?: string): SwitchResult => switchRef.current?.(renditionKey) ?? false,
    [],
  );

  // The last presented position in song seconds, so Try again resumes where
  // the viewer was rather than at the opening ?t= or server resume point
  // (CORE-1364).
  const lastPositionRef = useRef(0);
  const retryFromRef = useRef<{ mediaID: string; position: number; paused?: boolean } | null>(null);
  // A quality change (CORE-1971) reloads this way too and passes `paused`, so
  // a paused player stays paused where Try again would start it.
  const retry = useCallback(
    (options?: { paused?: boolean }) => {
      const paused = options?.paused === true;
      retryFromRef.current = { mediaID, position: lastPositionRef.current, paused };
      setRetryKey((key) => key + 1);
    },
    [mediaID],
  );
  // A restart (another device's play-media for this Media) sets the player up
  // afresh: a new progress queue and reporting timer, and an opening position
  // that replaces any resume seek still waiting on metadata. An undefined
  // position resumes from saved progress, as a fresh open does.
  const restartRef = useRef<{ mediaID: string; at: number | undefined } | null>(null);
  const restart = useCallback(
    (atSeconds?: number) => {
      retryFromRef.current = null;
      restartRef.current = { mediaID, at: atSeconds };
      setRetryKey((key) => key + 1);
    },
    [mediaID],
  );

  // The key and tempo asked for this Media (CORE-1659); another Media starts
  // as recorded. A ref feeds the load effect, which a change re-runs.
  const [requested, setRequested] = useState<{
    mediaID: string;
    adjustment: PlaybackAdjustment;
  } | null>(null);
  const adjustment = requested?.mediaID === mediaID ? requested.adjustment : DEFAULT_ADJUSTMENT;
  const adjustmentRef = useRef(adjustment);
  adjustmentRef.current = adjustment;
  // Song seconds per stream second of the stream playing now: the tempo the
  // server applied. Progress reports and the resume point stay in song time.
  const songScaleRef = useRef(1);
  // Restarts the stream with a new key or tempo at the same song position.
  const adjust = useCallback(
    (next: PlaybackAdjustment) => {
      retryFromRef.current = { mediaID, position: lastPositionRef.current };
      setRequested({ mediaID, adjustment: next });
      setRetryKey((key) => key + 1);
    },
    [mediaID],
  );

  const destroyHls = useCallback(() => {
    if (hlsRef.current) {
      hlsRef.current.destroy();
      hlsRef.current = null;
    }
  }, []);

  // The last position whose frame was presented. While a seek is pending,
  // video.currentTime is the seek target, which a stalled stream may never
  // present, so progress reports this instead and is never ahead of what the
  // viewer saw (CORE-0044). Null until a frame is presented in this load.
  const presentedPositionRef = useRef<number | null>(null);
  const notePresentedPosition = useCallback((video: HTMLVideoElement) => {
    if (!video.seeking && video.readyState >= HTMLMediaElement.HAVE_CURRENT_DATA) {
      presentedPositionRef.current = video.currentTime;
    }
  }, []);

  const reportProgress = useCallback(
    (finished: boolean, heartbeat = false) => {
      const video = videoRef.current;
      if (!video || !mediaID || !video.duration) return;
      notePresentedPosition(video);
      const position = presentedPositionRef.current;
      if (position === null) return;
      const queue = progressQueueRef.current;
      if (!queue) return;
      syncPlayedClock(queue, video);
      const eventTimeMS = nextPlaybackEventTimeMS();
      const songScale = songScaleRef.current;
      // The probed length, not a live transcode's growing video.duration:
      // the server derives "time left" from this (CORE-2314).
      const durationSeconds = playbackDurationSeconds(
        stateRef.current.info?.timeline?.durationSeconds,
        video.duration * songScale,
      );
      enqueueProgress(
        queue,
        {
          mediaID,
          positionSeconds: Math.floor(position * songScale),
          durationSeconds: Math.floor(durationSeconds),
          finished,
          eventTimeMS,
          paused: video.paused,
          playbackID: queue.playbackID,
          playedMS: Math.round(queue.playedMS),
        },
        heartbeat,
      );
    },
    [mediaID, notePresentedPosition],
  );

  // Periodic progress beacon while playing.
  const startHeartbeat = useCallback(() => {
    if (progressTimerRef.current) return;
    progressTimerRef.current = window.setInterval(() => {
      if (videoRef.current && !videoRef.current.paused) {
        reportProgress(false, true);
      }
    }, REPORT_INTERVAL_MS);
  }, [reportProgress]);

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

  // End the server session when the player closes, moves to another Media or
  // the page goes away (CORE-2305), so the transcode stops and live activity
  // clears without waiting for the idle expiry. A page restored from the
  // back/forward cache reloads, since its session was ended on the way out.
  useEffect(() => {
    if (!mediaID) return;
    let ended = false;
    // The Profile this player opened for (null: none); see endPlayback.
    const profile = getExpectedProfile() ?? null;
    const onPageHide = () => {
      ended = true;
      endPlayback(mediaID, profile);
    };
    const onPageShow = (event: PageTransitionEvent) => {
      if (!event.persisted || !ended) return;
      ended = false;
      retryFromRef.current = { mediaID, position: lastPositionRef.current, paused: true };
      setRetryKey((key) => key + 1);
    };
    window.addEventListener("pagehide", onPageHide);
    window.addEventListener("pageshow", onPageShow);
    return () => {
      window.removeEventListener("pagehide", onPageHide);
      window.removeEventListener("pageshow", onPageShow);
      if (!ended) endPlayback(mediaID, profile);
    };
  }, [mediaID]);

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
    // Point the reporting listeners at this load's element before anything
    // can play on it.
    setLoadedVideo({ element: videoRef.current });
    // A reload of this item (Try again, a quality or key change) carries on
    // the same play and its queue, still sending what it holds; anything else
    // starts a new play, with a new playback ID and queue.
    const previousPlay = playRef.current;
    if (
      retryFromRef.current?.mediaID === mediaID &&
      previousPlay?.mediaID === mediaID &&
      previousPlay.queue.active &&
      !previousPlay.queue.retiring
    ) {
      progressQueueRef.current = previousPlay.queue;
    } else {
      // A play the last cleanup kept for a reload that did not come.
      if (previousPlay) retireProgressQueue(previousPlay.queue);
      const queue = openProgressQueue();
      playRef.current = { mediaID, queue };
      progressQueueRef.current = queue;
    }
    const msg = messagesRef.current;
    // A pause sent from another device after this load began, or after
    // playback last started (here or remotely), holds this load's own starts,
    // reloads (recovery, a renewed token) included.
    let startMark = remotePlayStateMark();
    // Likewise a sleep timer that stopped playback after this load began, or
    // after playback last started: a skip still loading must not play on (CORE-3365).
    // A skip's load begins from when the skip moved on.
    let sleepMark = loadSleepMark();
    setState(emptyState());
    const reloading = retryFromRef.current?.mediaID === mediaID;
    const retryFrom = reloading ? (retryFromRef.current?.position ?? 0) : 0;
    const retryPaused = reloading && !!retryFromRef.current?.paused;
    retryFromRef.current = null;
    const restartFrom = restartRef.current?.mediaID === mediaID ? restartRef.current : null;
    restartRef.current = null;
    // A reload (Try again, an in-player pick, a key or quality change) keeps
    // the picks made since; an explicit start here takes the picks it was
    // handed again. Another device's restart keeps them too.
    if (!reloading && !restartFrom) {
      trackChoiceRef.current = startChoice(mediaID, startTracksRef.current);
    }
    const startAt = restartFrom ? restartFrom.at : startAtSeconds;
    lastPositionRef.current = retryFrom;
    presentedPositionRef.current = null;
    const requestedAdjustment = adjustmentRef.current;
    songScaleRef.current = 1;
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

    // The audio stream and burned-in subtitle this load asks for; undefined
    // leaves both to the server.
    const trackRequest = () => {
      const { audioIndex, burnIndex } = trackChoiceRef.current;
      if (audioIndex === undefined && burnIndex === undefined) return undefined;
      return { selectedAudioIndex: audioIndex, selectedSubtitleIndex: burnIndex };
    };

    async function start() {
      // Markers do not depend on playback info, so their request runs in
      // parallel with it; neither markers nor trickplay ever gate attaching
      // the stream (best-effort, applied when they arrive).
      const markersRequest = fetchPlaybackMarkers(mediaID).catch((): PlaybackMarker[] => []);
      const loadInfo = async (): Promise<PlaybackInfo | null> => {
        try {
          return await fetchPlaybackInfo(
            mediaID,
            nextMediaIDRef.current,
            requestedAdjustment,
            selectedVersionRef.current,
            trackRequest(),
          );
        } catch (err) {
          if (!alive) return null;
          if (!(err instanceof ApiError) && offline) {
            waitForNetwork(() => void start());
            return null;
          }
          showPlaybackInfoError(err);
          return null;
        }
      };
      let loaded = await loadInfo();
      if (!loaded || !alive) return;
      // The server burns a picture-based subtitle in only when asked by
      // stream index, so its start pick of one is asked for once more.
      if (adoptStartCaption(trackChoiceRef.current, loaded)) {
        loaded = await loadInfo();
        if (!loaded || !alive) return;
      }
      let info: PlaybackInfo = loaded;

      const stream = choosePlaybackStream(info);
      const decision = playbackDecision(info, stream);
      if (
        (decision !== "direct-play" &&
          decision !== "direct-stream" &&
          decision !== "transcode" &&
          // A Ready precomputed rendition (a composed picture) is HLS like a transcode.
          decision !== "precomputed-rendition") ||
        !stream ||
        !stream.url
      ) {
        showUnavailable(info.reason, msg.playbackUnavailable);
        return;
      }

      const video = videoRef.current;
      if (!video) return;
      boundVideo = video;
      // Every caption track with a WebVTT URL rides as a <track> on every
      // route; a burned-in pick is in the picture, so none of them shows.
      captionTracksRef.current = info.captionTracks ?? [];
      attachCaptionElements(video, info.captionTracks, captionLocale());
      {
        const choice = trackChoiceRef.current;
        if (choice.captionID !== undefined && choice.burnIndex === undefined) {
          showCaptionElement(video, choice.captionID);
        }
      }
      // Stream seconds are song seconds divided by the applied tempo (CORE-1658).
      // A renewal may come back with another tempo (or none), so this changes
      // with the stream actually playing.
      let songScale = songSecondsPerStreamSecond(info);
      songScaleRef.current = songScale;

      // A returning lead opens at the shared position. Its own play/pause
      // intent survives a silence hold while the player was closed.
      const party = getWatchParty();
      const leadOpening =
        restorePartyPlayback &&
        !reloading &&
        !restartFrom &&
        party.status === "active" &&
        party.lead &&
        party.isHost &&
        party.timeline?.item === mediaID
          ? party.timeline
          : null;
      const startPaused =
        retryPaused || (leadOpening !== null && (getLeadPauseIntent() ?? leadOpening.paused));

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
          lastPositionRef.current = video.currentTime * songScale;
        }
      };
      const onPlay = () => {
        // The play event is queued: one from before a remote pause fires after
        // that pause has applied, and a paused element has not started.
        if (!video.paused) {
          startMark = remotePlayStateMark();
          sleepMark = sleepStopMark();
        }
      };
      videoListeners.push(["playing", onPlaying], ["timeupdate", onTimeUpdate], ["play", onPlay]);
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
        // In song seconds: the renewed stream may apply another tempo, or none.
        const songPosition = playheadPosition() * songScale;
        setRecovering(true);
        // The refresh replaces info, so it declares the same next item: Up Next
        // and auto-advance survive a renewed token.
        void fetchPlaybackInfo(
          mediaID,
          nextMediaIDRef.current,
          requestedAdjustment,
          selectedVersionRef.current,
          trackRequest(),
        )
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
            // The renewed token re-signs the caption URLs too.
            const shownCaption = shownCaptionElement(video);
            captionTracksRef.current = refreshed.captionTracks ?? [];
            attachCaptionElements(video, refreshed.captionTracks, captionLocale());
            showCaptionElement(video, shownCaption);
            songScale = songSecondsPerStreamSecond(refreshed);
            songScaleRef.current = songScale;
            reload(fresh, songPosition / songScale);
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
        const shouldPlay = () =>
          options.play &&
          !remoteHoldsAutoplay(mediaID, startMark) &&
          !sleepHoldsAutoplay(sleepMark);
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
          applyTrackPreferences(video, hlsRef.current, info);
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
          if (shouldPlay()) void video.play().catch(() => {});
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
              // A paused quality change (CORE-1971) leaves `played` empty too.
              const play = !video.paused || (!startPaused && video.played.length === 0);
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
            // No HLS to fall over to because the Profile disallows converting
            // (CORE-2181): say so, not that the format alone is the problem.
            const transcodeDisallowed = messagesRef.current.transcodeDisallowed;
            if (undecodable && info.reason === "transcode-disallowed" && transcodeDisallowed) {
              showMessage(transcodeDisallowed, { action: "back" });
              return;
            }
            showMessage(
              undecodable
                ? messagesRef.current.playbackUnsupportedCodec
                : messagesRef.current.playbackFailed,
            );
          };
          video.addEventListener("error", videoErrorHandler);
          video.src = streamURL;
          videoCanPlayHandler = () => {
            if (alive && shouldPlay()) {
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
        const play =
          !video.paused ||
          (renditionKey === undefined && !startPaused && video.played.length === 0);
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

      // The opening song position, as a stream position.
      const localOpening =
        retryFrom > 0 ? retryFrom : startAt !== undefined ? startAt : resumePositionFromInfo(info);
      const opening = leadOpening
        ? expectedPositionMS(leadOpening, partyNowMS()) / 1000
        : localOpening;
      attachEngine(stream, initialEngine, opening / songScale, { play: !startPaused });

      setState({
        phase: "playing",
        message: "",
        terminal: false,
        info,
        mediaID,
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
        adjustment: requestedAdjustment,
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

      startHeartbeat();
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
      // A reload of this item keeps the play's queue for the next load, which
      // sends after what it holds; any other teardown retires it.
      progressQueueRef.current = null;
      const play = playRef.current;
      if (play && retryFromRef.current?.mediaID !== mediaID) retireProgressQueue(play.queue);
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
        removeCaptionElements(video);
        video.removeAttribute("src");
        video.load();
      }
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mediaID, startAtSeconds, retryKey, startID]);

  // Report on pause / seek / end edges of the element the load plays on.
  useEffect(() => {
    const video = loadedVideo?.element ?? videoRef.current;
    if (!video) return;
    // Play history credits time only after a playing report, so resuming
    // reports too (CORE-1703).
    // Playing again after the finish (Play on an ended item, in place) is a
    // new play: a new playback ID and queue, and the beacon restarted. The
    // old queue still delivers its finish, before any of the new play's
    // reports. The end position is forgotten, so the new play reports only a
    // presented frame: none while the seek back to the start is pending, the
    // start once `seeked` fires.
    const onPlay = () => {
      const play = playRef.current;
      if (play && play.queue === progressQueueRef.current && play.queue.finalQueued) {
        retireProgressQueue(play.queue);
        presentedPositionRef.current = null;
        const queue = openProgressQueue(play.queue);
        playRef.current = { mediaID: play.mediaID, queue };
        progressQueueRef.current = queue;
        startHeartbeat();
      }
      reportProgress(false);
    };
    const onPause = () => reportProgress(false);
    const onSeeked = () => reportProgress(false);
    const onEnded = () => stopProgressReporting(true);
    // The played clock starts and stops with every change in whether the
    // video is actually playing; a report brings it up to date as well.
    const syncPlayed = () => {
      const queue = progressQueueRef.current;
      if (queue) syncPlayedClock(queue, video);
    };
    const onTimeUpdate = () => {
      syncPlayed();
      notePresentedPosition(video);
    };
    const listeners: Array<[string, () => void]> = [
      ["play", onPlay],
      ["pause", onPause],
      ["seeked", onSeeked],
      ["ended", onEnded],
      ["timeupdate", onTimeUpdate],
      ["playing", syncPlayed],
      ["waiting", syncPlayed],
      ["seeking", syncPlayed],
      ["emptied", syncPlayed],
    ];
    for (const [type, listener] of listeners) video.addEventListener(type, listener);
    return () => {
      for (const [type, listener] of listeners) video.removeEventListener(type, listener);
    };
  }, [loadedVideo, reportProgress, startHeartbeat, stopProgressReporting]);

  const trackControl = useMemo<StreamTrackControl>(() => {
    const bumpTracks = () =>
      setState((s) => (s.phase === "playing" ? { ...s, tracksVersion: s.tracksVersion + 1 } : s));
    const activeCaptionID = () => {
      const choice = trackChoiceRef.current;
      if (choice.burnIndex !== undefined) return choice.captionID ?? null;
      const video = videoRef.current;
      return video ? shownCaptionElement(video) : null;
    };
    return {
      activeCaptionID,
      selectAudio(streamIndex) {
        const playing = stateRef.current.info?.audioTracks?.find((track) => track.selected);
        if (playing?.streamIndex === streamIndex) return;
        const choice = trackChoiceRef.current;
        choice.captionID = activeCaptionID();
        choice.audioIndex = streamIndex;
        retry();
      },
      selectCaption(id) {
        const choice = trackChoiceRef.current;
        const video = videoRef.current;
        const track = id === null ? undefined : captionTracksRef.current.find((t) => t.id === id);
        if (id !== null && !track) return;
        if (track && isBurnInCaption(track)) {
          if (choice.burnIndex === track.streamIndex) return;
          choice.burnIndex = track.streamIndex;
          choice.captionID = id;
          if (video) showCaptionElement(video, null);
          retry();
          return;
        }
        choice.captionID = id;
        if (choice.burnIndex !== undefined) {
          // The burned-in subtitle leaves the picture: a new stream without it.
          choice.burnIndex = undefined;
          retry();
          return;
        }
        if (video) showCaptionElement(video, id);
        bumpTracks();
      },
    };
  }, [retry]);
  const exposedState = useMemo<PlayerState>(
    () => ({ ...state, trackControl }),
    [state, trackControl],
  );

  return {
    videoRef,
    hlsRef,
    state: exposedState,
    retry,
    restart,
    switchToHls,
    adjustment,
    adjust,
  };
}

// applyTrackPreferences applies the Profile's track policy to the live track
// set once per load, for streams whose tracks the server does not list: an
// hls.js audio rendition in the most preferred language, and, with subtitles
// always on, a live text track in the most preferred subtitle language. When
// playbackInfo lists the file's tracks the server has already delivered the
// preferred audio stream and named the starting subtitle (adoptStartCaption),
// so only the hls.js audio choice can still apply.
function applyTrackPreferences(video: HTMLVideoElement, hls: Hls | null, info: PlaybackInfo): void {
  const preferences = trackPreferencePolicy(info.trackPreferences);
  if (!preferences) {
    return;
  }
  if (!info.audioTracks?.length && hls && Array.isArray(hls.audioTracks)) {
    const index = preferredLanguageIndex(
      hls.audioTracks.map((track) => track.lang),
      preferences.audioLanguages,
    );
    if (index >= 0 && hls.audioTrack !== index) {
      hls.audioTrack = index;
    }
  }
  if (info.captionTracks?.length || preferences.subtitleMode !== "always") {
    return;
  }
  if (hls && Array.isArray(hls.subtitleTracks)) {
    const index = preferredLanguageIndex(
      hls.subtitleTracks.map((track) => track.lang),
      preferences.subtitleLanguages,
    );
    if (index >= 0) {
      hls.subtitleDisplay = true;
      hls.subtitleTrack = index;
    }
  }
  if (video.textTracks) {
    const tracks = Array.from(video.textTracks);
    const index = preferredLanguageIndex(
      tracks.map((track) => track.language),
      preferences.subtitleLanguages,
    );
    if (index >= 0) {
      tracks.forEach((track, i) => {
        track.mode = i === index ? "showing" : "disabled";
      });
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
