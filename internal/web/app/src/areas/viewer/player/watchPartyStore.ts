import { useSyncExternalStore } from "react";
import { ApiError, getIdentityEpochSignal, subscribeEvents } from "@/lib/api";
import {
  acceptWatchParty,
  calibrateWatchPartyClock,
  createWatchParty,
  decideWatchPartyRequest,
  fetchMySessions,
  inviteToWatchParty,
  leaveWatchParty,
  leaveWatchPartyKeepalive,
  reportWatchPartyDrift,
  reportWatchPartyLeadPosition,
  requestWatchPartyTransport,
  type WatchPartyInfo,
  watchPartyAutoSkip,
  watchPartyTransport,
} from "@/lib/graphql";
import {
  expectedPositionMS,
  type GroupSyncFrame,
  IN_SYNC_MS,
  localClockMS,
  mergeClockEstimate,
  type PartyTimeline,
  parseGroupSync,
  type TimedClockEstimate,
} from "./watchPartySync";

// The web viewer's one Watch Party (CORE-1677). It lives outside React so the
// party, its clock offset and its group.sync subscription survive the jump
// from the invite notification to the player and from one title to the next.
// The player's useWatchParty hook reads it and keeps the <video> on the
// shared timeline.
//
// A lead party (CORE-2245) is one screen with the sound and others showing the
// picture: the lead is the host, every other member a follower. The store
// knows a party is one from its own start, from a remembered follow, or from
// the first timeline that says so.

export type WatchPartyStatus = "idle" | "starting" | "joining" | "active";

/** Why the last party action failed; the UI turns it into words. */
export type WatchPartyErrorKind = "" | "home-network" | "failed";

/** What last happened to the party's host (CORE-1763); the UI turns it into words. */
export type WatchPartyNotice = "" | "handoff" | "ended";

/** A member's play, pause or seek waiting for the host's decision (CORE-1765). */
export interface WatchPartyRequest {
  id: string;
  /** The requesting member's display name. */
  from: string;
  item: string;
  positionMS: number;
  paused: boolean;
}

/** A lead party this session could follow: one its own Profile leads elsewhere. */
export interface LeadPartyOffer {
  partyID: string;
  /** The Media the lead is playing. */
  item: string;
  /** This session left or failed to join it: offer it, but do not join unasked. */
  declined: boolean;
}

/** How many pending requests the host's player keeps; older ones fall off. */
export const MAX_PARTY_REQUESTS = 10;

export interface WatchPartyState {
  status: WatchPartyStatus;
  partyID: string;
  isHost: boolean;
  /** A lead party: the host is the lead screen and every other member follows it. */
  lead: boolean;
  /** A lead party seen while idle that this session may follow. */
  leadOffer: LeadPartyOffer | null;
  /** The shared timeline, once known. */
  timeline: PartyTimeline | null;
  /** Display names of the Profiles in the party, sorted. */
  members: string[];
  /** Display names invited but not yet joined. */
  invited: string[];
  /** serverClock − localClock, in fractional ms; the local clock is localClockMS. */
  offsetMS: number;
  error: WatchPartyErrorKind;
  /** The server's reason for a "failed" error. */
  errorDetail: string;
  /** The host handed the party off or ended it. */
  notice: WatchPartyNotice;
  /** The display name the notice is about: the new host, or the host who ended it. */
  noticeName: string;
  /** Members' requests waiting for this host's decision, oldest first. */
  requests: WatchPartyRequest[];
}

const IDLE: WatchPartyState = {
  status: "idle",
  partyID: "",
  isHost: false,
  lead: false,
  leadOffer: null,
  timeline: null,
  members: [],
  invited: [],
  offsetMS: 0,
  error: "",
  errorDetail: "",
  notice: "",
  noticeName: "",
  requests: [],
};

/** How long a join waits for the roster that tells it whether the party is paused. */
const ROSTER_WAIT_MS = 1500;

let state: WatchPartyState = IDLE;
const listeners = new Set<() => void>();
let closeStream: (() => void) | null = null;
let stopPartyLifetime: (() => void) | null = null;
let rosterWaiters: Array<() => void> = [];
// Bumped by every reset, so a start or join that a leave overtook knows it is stale.
let generation = 0;
// A membership or title change invalidates controls even if the viewer later returns.
let transportEpoch = 0;
// The latest shared rewind lets each member recognize a return into a segment.
let rewind: { fromMS: number; toMS: number } | null = null;

export const PARTY_TRANSPORT_TIMEOUT_MS = 15_000;
const TRANSPORT_RETRY_MS = 1000;
const PARTY_TITLE_NOT_VISIBLE = "cannot play this in the party: it is not visible to every member";

interface TransportIntent {
  readonly itemId: string;
  readonly positionMS: number;
  readonly playing: boolean;
  readonly epoch: number;
  readonly seekProtectedUntil: number;
}

interface TransportWork {
  intent: TransportIntent;
  autoSkip: boolean;
  titleSwitch: boolean;
  playbackChange?: { confirmed: boolean };
  isCurrent: () => boolean;
  submitted: boolean;
  settled: boolean;
  confirmed: boolean;
  run: () => Promise<void>;
  settle: (result: PartyAutoSkipOutcome) => void;
}

interface TransportQueue {
  inFlight: TransportWork | null;
  settled?: Promise<void>;
  pending: TransportWork | null;
}

const transportQueues = new Map<string, TransportQueue>();
let latestTransport: TransportWork | null = null;

export function getPartyTransportEpoch(): number {
  return transportEpoch;
}

/** Queued and submitted controls share the same target until its anchor arrives. */
export function pendingPartyTransport(itemId: string): TransportIntent | undefined {
  return pendingTransportWork(itemId)?.intent;
}

function pendingTransportWork(itemId: string): TransportWork | undefined {
  const work = latestTransport;
  if (work?.intent.itemId !== itemId || !work.isCurrent()) return;
  // A relaxed member can miss the stream acknowledgement after approval.
  if (work.settled && performance.now() >= work.intent.seekProtectedUntil) {
    latestTransport = null;
    return;
  }
  return work;
}

function matchesTransport(work: TransportWork, timeline: PartyTimeline): boolean {
  const { intent, autoSkip, playbackChange } = work;
  return (
    timeline.item === intent.itemId &&
    timeline.positionMS === intent.positionMS &&
    ((autoSkip && !playbackChange) || timeline.paused === !intent.playing)
  );
}

function pauseRetainsLeadSkip(work: TransportWork, next: TransportWork | null): boolean {
  return (
    state.lead &&
    state.isHost &&
    next !== null &&
    !next.intent.playing &&
    next.intent.itemId === work.intent.itemId &&
    next.intent.epoch === work.intent.epoch &&
    next.intent.positionMS === work.intent.positionMS
  );
}

function settleTransport(
  work: TransportWork,
  result: PartyAutoSkipOutcome,
  next: TransportWork | null,
): void {
  if (result === "unconfirmed" && work.autoSkip && next && pauseRetainsLeadSkip(work, next)) {
    // The retained target can still be acknowledged by the authoritative Pause.
    const settlePause = next.settle;
    next.settle = (outcome) => {
      work.settle(outcome === "applied" ? "applied" : "unconfirmed");
      settlePause(outcome);
    };
    return;
  }
  work.settle(result);
}

/** A superseded switch that was already sent can still move the shared title. */
export function submittedPartyTitleSwitch():
  | { itemId: string; settled: Promise<void> }
  | undefined {
  const queue = transportQueues.get(state.partyID);
  const work = queue?.inFlight;
  if (work?.submitted && work.titleSwitch && queue?.settled) {
    return { itemId: work.intent.itemId, settled: queue.settled };
  }
}

function runTransport(partyID: string, queue: TransportQueue, work: TransportWork): void {
  queue.inFlight = work;
  queue.settled = work.run().finally(() => {
    if (transportQueues.get(partyID) !== queue) return;
    const next = queue.pending;
    queue.pending = null;
    if (next) runTransport(partyID, queue, next);
    else transportQueues.delete(partyID);
  });
}

// A stream acknowledgement can settle playback early. Keep the slot until
// the mutation itself has settled so the server sees intents in order.
function enqueueTransport(
  intent: TransportIntent,
  autoSkip: boolean,
  playbackChange: TransportWork["playbackChange"],
  selection?: AbortSignal,
): Promise<PartyAutoSkipOutcome> {
  const { partyID } = state;
  const gen = generation;
  const isCurrent = () =>
    intent.epoch === transportEpoch &&
    state.partyID === partyID &&
    !(state.lead && !state.isHost) &&
    (!selection || (state.isHost && !selection.aborted));
  const running = transportQueues.get(partyID);
  const queue = running ?? { inFlight: null, pending: null };
  return new Promise((resolve) => {
    const work: TransportWork = {
      intent,
      autoSkip,
      titleSwitch: state.isHost && intent.itemId !== state.timeline?.item,
      playbackChange,
      isCurrent,
      submitted: false,
      settled: false,
      confirmed: false,
      run: async () => {
        const isLatest = () => isCurrent() && (queue.pending ?? queue.inFlight) === work;
        while (isLatest() && !work.confirmed) {
          const sentAsHost = state.isHost;
          try {
            work.submitted = true;
            let result: PartyAutoSkipOutcome;
            if (autoSkip) {
              result = await applyPartyAutoSkip(work, isLatest);
            } else {
              const applied = await applyPartyTransport(intent);
              if (applied) {
                if (playbackChange) playbackChange.confirmed = true;
                // A host response carries the acknowledged anchor even if the
                // stream delivered a newer one. A member's response has no anchor.
                if (sentAsHost && latestTransport === work) latestTransport = null;
              }
              result = applied ? "applied" : "denied";
            }
            if (result !== "applied" && latestTransport === work) latestTransport = null;
            work.settled = true;
            settleTransport(work, result, queue.pending);
            return;
          } catch (err) {
            const refusal = err instanceof ApiError ? err.message : undefined;
            const notMember = refusal === "not a member of this party";
            if (
              sentAsHost &&
              gen === generation &&
              (refusal === "not the party host" || notMember)
            ) {
              // The handoff may have happened while this tab's stream was closed.
              // Drop controls from the old role before requesting the live roster.
              transportEpoch++;
              latestTransport = null;
              queue.pending?.settle("unconfirmed");
              queue.pending = null;
              work.settle("unconfirmed");
              set({ isHost: false, requests: [] });
              await refreshPartyAfterHandoff(partyID, gen);
              return;
            }
            if (gen === generation && (refusal === "unknown party" || notMember)) {
              if (state.lead) declinedParties.add(partyID);
              forgetFollowing();
              reset();
              break;
            }
            if (!isLatest() || work.confirmed) break;
            // An explicit title refusal leaves the shared title where it was.
            if (err instanceof ApiError && err.message === PARTY_TITLE_NOT_VISIBLE) {
              set(failure(err));
              break;
            }
            await new Promise<void>((retry) => setTimeout(retry, TRANSPORT_RETRY_MS));
          }
        }
        if (latestTransport === work) latestTransport = null;
        settleTransport(work, work.confirmed ? "applied" : "unconfirmed", queue.pending);
      },
      settle: resolve,
    };
    latestTransport = work;
    if (running) {
      queue.pending?.settle("unconfirmed");
      queue.pending = work;
    } else {
      transportQueues.set(partyID, queue);
      runTransport(partyID, queue, work);
    }
  });
}

export function hasPartyTransportPending(): boolean {
  return (
    transportQueues.has(state.partyID) ||
    (state.timeline !== null && pendingPartyTransport(state.timeline.item) !== undefined)
  );
}

/** Date.now() of the last drift report; the rate bound counts from it. */
let lastDriftReport = Number.NEGATIVE_INFINITY;

/** A member's player reports its measured drift at most this often (CORE-1766). */
export const DRIFT_REPORT_INTERVAL_MS = 5000;

/** While a party is active its clock offset is re-estimated this often (CORE-2244). */
export const CLOCK_REFRESH_INTERVAL_MS = 30_000;

// The clock estimate the offset in the state comes from, and its refresh timer.
let clock: TimedClockEstimate | null = null;
let clockTimer: ReturnType<typeof setInterval> | null = null;
// How many players are listening for a lead party to follow.
let watching = 0;
// Lead parties this session left or could not join: offered, never joined unasked.
const declinedParties = new Set<string>();
// Parties this session started: their late frames are never an offer to follow.
const startedParties = new Set<string>();

/** Where a follower keeps the lead party it follows, to rejoin after a reload. */
const FOLLOWING_KEY = "beamfall.watchParty.following";

function followedParty(): string {
  try {
    return window.sessionStorage.getItem(FOLLOWING_KEY) ?? "";
  } catch {
    return "";
  }
}

function rememberFollowing(partyID: string): void {
  try {
    window.sessionStorage.setItem(FOLLOWING_KEY, partyID);
  } catch {
    // A blocked store only costs the rejoin after a reload.
  }
}

function forgetFollowing(): void {
  try {
    window.sessionStorage.removeItem(FOLLOWING_KEY);
  } catch {
    // Nothing was kept.
  }
}

// Called whenever this session may have become a follower of a lead party.
function noteFollowing(): void {
  if (state.status === "active" && state.lead && !state.isHost) rememberFollowing(state.partyID);
}

function set(patch: Partial<WatchPartyState>): void {
  const { timeline } = patch;
  if (timeline && timeline !== state.timeline) {
    const inFlight = transportQueues.get(state.partyID)?.inFlight;
    for (const work of new Set([inFlight, latestTransport])) {
      if (!work?.submitted || !work.isCurrent() || !matchesTransport(work, timeline)) continue;
      work.confirmed = true;
      if (work.playbackChange) work.playbackChange.confirmed = true;
      const latest = work === latestTransport;
      if (work.autoSkip && state.isHost && (latest || pauseRetainsLeadSkip(work, latestTransport)))
        work.settle("applied");
      if (latest) latestTransport = null;
    }
    noteTimeline(timeline);
  }
  state = { ...state, ...patch };
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function getWatchParty(): WatchPartyState {
  return state;
}

function noteTimeline(next: PartyTimeline): void {
  const previous = state.timeline;
  if (previous?.item !== next.item) {
    transportEpoch++;
    latestTransport = null;
    rewind = null;
    return;
  }
  const fromMS = expectedPositionMS(previous, Math.min(partyNowMS(), next.presentAtMS));
  const toMS = next.positionMS;
  // Play, pause and roster updates can restate the anchor during the rewind visit.
  if (rewind && fromMS < rewind.fromMS && toMS >= rewind.toMS && toMS < rewind.fromMS) return;
  rewind = toMS < fromMS ? { fromMS, toMS } : null;
}

/** A shared move from past this segment back into it suppresses this visit. */
export function partyRewoundIntoSegment(item: string, startMS: number, endMS: number): boolean {
  return (
    rewind !== null &&
    state.timeline?.item === item &&
    rewind.toMS >= startMS &&
    rewind.toMS < endMS &&
    rewind.fromMS >= endMS &&
    state.timeline.positionMS < endMS
  );
}

/** The current party state, re-rendering on every change. */
export function useWatchPartyState(): WatchPartyState {
  return useSyncExternalStore(subscribe, getWatchParty, getWatchParty);
}

function timelineOf(frame: GroupSyncFrame, lead: boolean): PartyTimeline {
  const timeline: PartyTimeline = {
    item: frame.item,
    positionMS: frame.positionMS,
    presentAtMS: frame.presentAtMS,
    paused: frame.paused,
  };
  if (lead) {
    timeline.lead = true;
    const rate = frame.ratePermille;
    if (rate !== undefined) timeline.ratePermille = rate;
  }
  return timeline;
}

// A roster re-states the live anchor with a fresh present time. When it
// describes the timeline this viewer already follows, keep the current one:
// adopting the new present time would hold a playing viewer for the pre-buffer.
// In a lead party the lead's own transitions keep the position, at least every
// few seconds, so a roster only matters when it changes the title or the pause.
function sameTimeline(
  current: PartyTimeline | null,
  frame: GroupSyncFrame,
  lead: boolean,
): boolean {
  if (current === null || current.item !== frame.item || current.paused !== frame.paused) {
    return false;
  }
  if (lead) return true;
  return Math.abs(expectedPositionMS(current, frame.presentAtMS) - frame.positionMS) <= IN_SYNC_MS;
}

// A frame for a party this session is not in. Sessions in a lead's own Profile
// receive its timeline, so a player that is listening can follow it.
function noteOtherParty(frame: GroupSyncFrame): void {
  if (watching === 0 || state.status !== "idle" || startedParties.has(frame.groupID)) return;
  const offer = state.leadOffer;
  if (frame.signal === "ended") {
    if (offer?.partyID === frame.groupID) set({ leadOffer: null });
    return;
  }
  if (frame.signal !== "transition" || !frame.lead || !frame.item) return;
  const declined = declinedParties.has(frame.groupID);
  if (
    offer &&
    offer.partyID === frame.groupID &&
    offer.item === frame.item &&
    offer.declined === declined
  ) {
    return;
  }
  set({ leadOffer: { partyID: frame.groupID, item: frame.item, declined } });
}

function applyFrame(frame: GroupSyncFrame): void {
  if (!state.partyID || frame.groupID !== state.partyID) {
    noteOtherParty(frame);
    return;
  }
  // A nudge carries only the server's suggested rate; the player measures and
  // corrects its own drift. Transitions and rosters both carry the live anchor,
  // in stream order, so each one is the timeline.
  if (frame.signal === "nudge") return;
  if (frame.signal === "request") {
    addRequest(frame);
    return;
  }
  if (frame.signal === "ended") {
    // The host ended the party for everyone; the local player keeps playing on its own.
    forgetFollowing();
    reset({ notice: "ended", noticeName: frame.hostName ?? "" });
    return;
  }
  const patch: Partial<WatchPartyState> = {};
  const lead = state.lead || frame.lead === true;
  if (lead && !state.lead) patch.lead = true;
  if (frame.item && !(frame.signal === "roster" && sameTimeline(state.timeline, frame, lead))) {
    patch.timeline = timelineOf(frame, lead);
  }
  if (frame.signal === "roster") {
    const members = frame.members ?? state.members;
    patch.members = members;
    patch.invited = state.invited.filter((name) => !members.includes(name));
    if (frame.handoff) {
      patch.notice = "handoff";
      patch.noticeName = frame.hostName ?? "";
      patch.isHost = false;
      // Requests were addressed to the old host; the server dropped them.
      patch.requests = [];
      // Only the new host's own Profile row names its session.
      if (frame.host) void adoptHost(frame.host, generation);
    }
  }
  set(patch);
  noteFollowing();
  if (frame.signal === "roster") {
    const waiters = rosterWaiters;
    rosterWaiters = [];
    for (const wake of waiters) wake();
  }
}

// A request frame reaches every session of the host's Profile; only the host acts on it.
function addRequest(frame: GroupSyncFrame): void {
  if (!state.isHost || !frame.request) return;
  const request: WatchPartyRequest = {
    id: frame.request,
    from: frame.from ?? "",
    item: frame.item,
    positionMS: frame.positionMS,
    paused: frame.paused,
  };
  const rest = state.requests.filter((r) => r.id !== request.id);
  set({ requests: [...rest, request].slice(-MAX_PARTY_REQUESTS) });
}

// This viewer's own session id, fetched once: a hand-off names the new host by session.
let ownSession: Promise<string> | null = null;

function ownSessionID(): Promise<string> {
  ownSession ??= fetchMySessions()
    .then((info) => info.sessions.find((s) => s.current)?.id ?? "")
    .catch(() => {
      ownSession = null;
      return "";
    });
  return ownSession;
}

async function adoptHost(host: string, gen: number): Promise<void> {
  const own = await ownSessionID();
  if (gen === generation && own && own === host) set({ isHost: true });
}

function openStream(): void {
  if (closeStream) return;
  closeStream = subscribeEvents((event) => {
    const frame = parseGroupSync(event);
    if (frame) applyFrame(frame);
  });
}

/**
 * Listen, while a player is open, for a lead party this session could follow:
 * one led from another screen of its own Profile (CORE-2245). What it finds is
 * the state's `leadOffer`. Returns the function that stops listening.
 */
export function watchForLeadParty(): () => void {
  watching++;
  openStream();
  let stopped = false;
  return () => {
    if (stopped) return;
    stopped = true;
    watching--;
    if (watching > 0 || state.status !== "idle") return;
    closeStream?.();
    closeStream = null;
    if (state.leadOffer) set({ leadOffer: null });
  };
}

// One calibration, stamped with the local clock reading it was taken at.
async function measureClock(): Promise<TimedClockEstimate> {
  const estimate = await calibrateWatchPartyClock();
  return { ...estimate, atMS: localClockMS() };
}

// Adopts the join-time estimate and keeps re-estimating while the party lasts.
function keepClock(measured: TimedClockEstimate): void {
  clock = measured;
  if (clockTimer) return;
  clockTimer = setInterval(() => void refreshClock(), CLOCK_REFRESH_INTERVAL_MS);
}

// Clocks drift apart and networks change: re-estimate, and keep whichever of
// the held and the new estimate is the better (mergeClockEstimate), so one
// slow sample cannot move the offset.
async function refreshClock(): Promise<void> {
  const gen = generation;
  let next: TimedClockEstimate;
  try {
    next = await measureClock();
  } catch {
    return;
  }
  if (gen !== generation || state.status !== "active") return;
  const kept = mergeClockEstimate(clock, next);
  if (kept === clock) return;
  clock = kept;
  if (kept.offsetMS !== state.offsetMS) set({ offsetMS: kept.offsetMS });
}

/** The server clock now, in fractional ms, by this party's clock estimate. */
export function partyNowMS(): number {
  return localClockMS() + state.offsetMS;
}

// The signed-in tab owns the membership across SPA routes. The player only
// controls the current media element.
function bindPartyLifetime(): void {
  const epoch = getIdentityEpochSignal();
  const onIdentityEnd = () => {
    forgetFollowing();
    closeStream?.();
    closeStream = null;
    leavePartyOnPageHide();
  };
  window.addEventListener("pagehide", leavePartyOnPageHide);
  epoch?.addEventListener("abort", onIdentityEnd, { once: true });
  stopPartyLifetime = () => {
    window.removeEventListener("pagehide", leavePartyOnPageHide);
    epoch?.removeEventListener("abort", onIdentityEnd);
  };
}

function reset(patch: Partial<WatchPartyState> = {}): void {
  stopPartyLifetime?.();
  stopPartyLifetime = null;
  ownSession = null;
  // A player still listening for a lead party keeps the stream.
  if (watching === 0) {
    closeStream?.();
    closeStream = null;
  }
  if (clockTimer) clearInterval(clockTimer);
  clockTimer = null;
  clock = null;
  generation++;
  transportEpoch++;
  latestTransport = null;
  const transport = transportQueues.get(state.partyID);
  if (transport?.pending) {
    transport.pending.settle("unconfirmed");
    transport.pending = null;
  }
  rewind = null;
  lastDriftReport = Number.NEGATIVE_INFINITY;
  const waiters = rosterWaiters;
  rosterWaiters = [];
  for (const wake of waiters) wake();
  state = { ...IDLE, ...patch };
  for (const listener of listeners) listener();
}

function failure(err: unknown): Partial<WatchPartyState> {
  const detail = err instanceof Error ? err.message : String(err);
  const homeOnly =
    (err instanceof ApiError && err.status === 403) || /home network only/i.test(detail);
  return { error: homeOnly ? "home-network" : "failed", errorDetail: homeOnly ? "" : detail };
}

function present(info: WatchPartyInfo): string[] {
  return info.members
    .filter((m) => m.present)
    .map((m) => m.displayName)
    .sort();
}

function invitedOf(info: WatchPartyInfo): string[] {
  return info.members.filter((m) => !m.present).map((m) => m.displayName);
}

type StartPlayback = { positionMS: number; paused: boolean; revision?: number };

/**
 * Start a party around `content`, hosted by this session. Set its initial
 * server anchor before exposing the party for invites. A number is a saved
 * position to start paused at. A player supplies a live sample after creation.
 * `lead` starts a host-only party with this session as the lead screen.
 */
export async function startWatchParty(
  content: string,
  playback: number | (() => StartPlayback) = 0,
  relaxed = true,
  lead = false,
): Promise<boolean> {
  if (state.status !== "idle") return false;
  set({ status: "starting", error: "", errorDetail: "", notice: "", noticeName: "" });
  bindPartyLifetime();
  const gen = generation;
  let partyID = "";
  try {
    const measured = await measureClock();
    if (gen !== generation) return false;
    // Relaxed: anyone in the household party may play, pause or seek. Host-only:
    // a member's play, pause or seek waits for the host's approval (CORE-1765).
    // A lead party is always host-only.
    const info = await createWatchParty({
      content,
      relaxed: lead ? false : relaxed,
      clockOffsetMS: Math.round(measured.offsetMS),
      renderLatencyMS: 0,
      ...(lead ? { lead: true } : {}),
    });
    partyID = info.partyID;
    startedParties.add(partyID);
    if (gen !== generation) {
      // The party was created after the viewer left, so the leave had no ID.
      await leaveWatchParty(partyID).catch(() => {});
      return false;
    }
    set({ partyID });
    const samplePlayback: () => StartPlayback =
      typeof playback === "function" ? playback : () => ({ positionMS: playback, paused: true });
    let intent = samplePlayback();
    let positionMS = Math.max(0, Math.round(intent.positionMS));
    let anchor = { item: content, positionMS, presentAtMS: 0 };
    if (positionMS !== 0 || !intent.paused) {
      while (true) {
        anchor = await watchPartyTransport({
          partyID,
          item: content,
          positionMS,
          paused: intent.paused,
        });
        if (gen !== generation) return false;
        const latest = samplePlayback();
        // The playhead advances naturally while the request is in flight.
        if (latest.paused === intent.paused && latest.revision === intent.revision) break;
        intent = latest;
        positionMS = Math.max(0, Math.round(intent.positionMS));
      }
    }
    if (gen !== generation) return false;
    set({
      status: "active",
      partyID,
      isHost: true,
      lead,
      leadOffer: null,
      offsetMS: measured.offsetMS,
      members: present(info),
      invited: invitedOf(info),
      timeline: {
        ...anchor,
        paused: intent.paused,
        ...(lead ? { lead: true } : {}),
      },
    });
    keepClock(measured);
    openStream();
    return true;
  } catch (err) {
    if (gen === generation) {
      reset(failure(err));
      // An initial transport can fail after the server has created the party.
      if (partyID) await leaveWatchParty(partyID).catch(() => {});
    }
    return false;
  }
}

function waitForRoster(ms: number): Promise<void> {
  return new Promise((resolve) => {
    const timer = setTimeout(done, ms);
    function done() {
      clearTimeout(timer);
      rosterWaiters = rosterWaiters.filter((w) => w !== done);
      resolve();
    }
    rosterWaiters.push(done);
  });
}

async function refreshPartyAfterHandoff(partyID: string, gen: number): Promise<void> {
  try {
    // The expired host's session can accept again as a member. The server
    // sends the current roster and timeline on the existing group.sync stream.
    await acceptWatchParty({
      partyID,
      clockOffsetMS: Math.round(state.offsetMS),
      renderLatencyMS: 0,
    });
  } catch (err) {
    if (
      gen === generation &&
      err instanceof ApiError &&
      (err.message === "unknown party" || err.message === "unknown invite")
    ) {
      forgetFollowing();
      reset();
    }
  }
}

/**
 * Join from an invite. Resolves to the Media the party is on, or null on
 * failure (the error is in the state). The stream opens before the accept so
 * the roster it triggers, which says whether the party is paused, is not missed.
 * `lead` joins a party known to be a lead party, as a follower from the first
 * frame; `quiet` keeps a failed join out of the state's error.
 */
export async function joinWatchParty(
  partyID: string,
  options: { lead?: boolean; quiet?: boolean } = {},
): Promise<string | null> {
  if (state.status !== "idle") {
    if (state.partyID === partyID && state.timeline) return state.timeline.item;
    return null;
  }
  set({
    status: "joining",
    partyID,
    isHost: false,
    lead: options.lead === true,
    leadOffer: null,
    error: "",
    errorDetail: "",
    notice: "",
    noticeName: "",
  });
  bindPartyLifetime();
  const gen = generation;
  openStream();
  try {
    const measured = await measureClock();
    const anchor = await acceptWatchParty({
      partyID,
      clockOffsetMS: Math.round(measured.offsetMS),
      renderLatencyMS: 0,
    });
    // A frame that arrived since joining is newer than the accept's anchor
    // (nothing reaches this Profile before it is a member); without one, wait
    // briefly for the roster the accept triggers.
    if (!state.timeline && gen === generation) await waitForRoster(ROSTER_WAIT_MS);
    if (gen !== generation) {
      await leaveWatchParty(partyID).catch(() => {});
      return null;
    }
    set({
      status: "active",
      offsetMS: measured.offsetMS,
      // Without a roster yet, hold paused at the anchor: the party is most
      // likely still waiting to start, and the roster, when it comes, moves it.
      timeline: state.timeline ?? {
        item: anchor.item,
        positionMS: anchor.positionMS,
        presentAtMS: anchor.presentAtMS,
        paused: true,
        ...(state.lead ? { lead: true } : {}),
      },
    });
    keepClock(measured);
    noteFollowing();
    return state.timeline?.item ?? anchor.item;
  } catch (err) {
    if (gen === generation) {
      if (options.lead) declinedParties.add(partyID);
      if (followedParty() === partyID) forgetFollowing();
      reset(options.quiet ? {} : failure(err));
    }
    return null;
  }
}

/**
 * Follow a lead party as a muted follower screen: the one offered while idle,
 * or `partyID`. `quiet` is for a join nobody asked for, whose failure is not
 * shown. Resolves to the Media the lead is on, or null.
 */
export function followLeadParty(partyID = "", quiet = false): Promise<string | null> {
  const id = partyID || state.leadOffer?.partyID || "";
  if (!id) return Promise.resolve(null);
  if (!quiet) declinedParties.delete(id);
  return joinWatchParty(id, { lead: true, quiet });
}

/**
 * After a reload, rejoin the lead party this screen was following. The invite
 * to a lead party stays valid, so the same accept works again; a refusal
 * forgets the party. Resolves to the Media the lead is on, or null.
 */
export function resumeFollowedParty(): Promise<string | null> {
  const partyID = followedParty();
  if (!partyID || state.status !== "idle") return Promise.resolve(null);
  return joinWatchParty(partyID, { lead: true, quiet: true });
}

/** Invite a household Profile by its display name. */
export async function inviteToParty(displayName: string): Promise<boolean> {
  const name = displayName.trim();
  if (state.status !== "active" || !name) return false;
  try {
    const info = await inviteToWatchParty(state.partyID, name);
    const joined = present(info);
    set({
      members: joined,
      invited: [...new Set([...state.invited, ...invitedOf(info), name])].filter(
        (n) => !joined.includes(n),
      ),
      error: "",
      errorDetail: "",
    });
    return true;
  } catch (err) {
    set(failure(err));
    return false;
  }
}

/** Put the viewer's changes on the shared timeline as a full playback state. */
export function sendPartyTransport(
  itemId: string,
  change: { positionMS?: number; playing?: boolean; playheadMS?: number; selection?: AbortSignal },
  expiresAt = performance.now() + PARTY_TRANSPORT_TIMEOUT_MS,
): Promise<boolean> {
  const { status, lead, isHost, partyID, timeline } = state;
  if (status !== "active" || !timeline || (lead && !isHost)) return Promise.resolve(false);
  const previousWork = pendingTransportWork(itemId);
  const previous = previousWork?.intent;
  const positionMS =
    change.positionMS ??
    previous?.positionMS ??
    change.playheadMS ??
    expectedPositionMS(timeline, partyNowMS());
  const intent = {
    itemId,
    positionMS: Math.max(0, Math.round(positionMS)),
    playing: change.playing ?? previous?.playing ?? !timeline.paused,
    epoch: transportEpoch,
    seekProtectedUntil: Math.max(expiresAt, previous?.seekProtectedUntil ?? 0),
  };
  // A playback toggle can replace a queued skip while carrying its position.
  const queued = transportQueues.get(partyID)?.pending;
  const autoSkip =
    queued?.autoSkip === true && queued === previousWork && change.positionMS === undefined;
  const playbackChange =
    change.playing === undefined ? previousWork?.playbackChange : { confirmed: false };
  return enqueueTransport(intent, autoSkip, playbackChange, change.selection).then(
    (result) => result === "applied",
  );
}

async function applyPartyTransport(intent: TransportIntent): Promise<boolean> {
  const { partyID, isHost, timeline } = state;
  const gen = generation;
  const { itemId: item, positionMS, playing } = intent;
  const paused = !playing;
  const vars = { partyID, item, positionMS, paused };
  if (!isHost) {
    const request = await requestWatchPartyTransport(vars);
    return gen === generation && request.approved;
  }
  const anchor = await watchPartyTransport(vars);
  const latest = state.timeline;
  // A same-title frame cannot invalidate a pending switch to another title.
  const switching = item !== timeline?.item;
  if (
    intent.epoch === transportEpoch &&
    (switching || latest === timeline || anchor.presentAtMS > (latest?.presentAtMS ?? 0))
  ) {
    const rate = latest?.ratePermille;
    set({
      timeline: {
        ...anchor,
        paused,
        ...(state.lead ? { lead: true } : {}),
        ...(state.lead && rate !== undefined ? { ratePermille: rate } : {}),
      },
    });
  }
  return gen === generation && (!switching || state.timeline?.item === item);
}

export type PartyAutoSkipOutcome = "applied" | "denied" | "unconfirmed";

/** The server decides whether the auto-skip can move the shared timeline. */
export function sendPartyAutoSkip(
  itemId: string,
  positionMS: number,
  expiresAt = performance.now() + PARTY_TRANSPORT_TIMEOUT_MS,
): Promise<PartyAutoSkipOutcome> {
  const { status, timeline, lead, isHost } = state;
  if (status !== "active" || timeline?.item !== itemId || (lead && !isHost)) {
    return Promise.resolve("unconfirmed");
  }
  const previousWork = pendingTransportWork(itemId);
  const previous = previousWork?.intent;
  return enqueueTransport(
    {
      itemId,
      positionMS: Math.max(0, Math.round(positionMS)),
      playing: previous?.playing ?? !timeline.paused,
      epoch: transportEpoch,
      seekProtectedUntil: Math.max(expiresAt, previous?.seekProtectedUntil ?? 0),
    },
    true,
    previousWork?.playbackChange,
  );
}

async function applyPartyAutoSkip(
  work: TransportWork,
  isLatest: () => boolean,
): Promise<PartyAutoSkipOutcome> {
  const { intent, playbackChange } = work;
  const { itemId: item, positionMS: target, playing } = intent;
  const { partyID, lead } = state;
  let timeline = state.timeline;
  if (timeline?.item !== item) return "unconfirmed";
  // The skip mutation moves position only. If needed, send the desired
  // playback state at the shared playhead first.
  if (playbackChange?.confirmed === false) {
    const applied = await applyPartyTransport({
      ...intent,
      positionMS: Math.round(expectedPositionMS(timeline, partyNowMS())),
    });
    if (!applied || !isLatest()) return "unconfirmed";
    playbackChange.confirmed = true;
    timeline = state.timeline;
  }
  const result = await watchPartyAutoSkip({ partyID, item, positionMS: target });
  const latest = state.timeline;
  // A stale-item reply can arrive before the title change on the stream. Do
  // not let the caller fall back to a scrub that would restore the old title.
  if (!work.isCurrent() || latest?.item !== item || result.item !== item) return "unconfirmed";
  if (!result.applied) return isLatest() && latest === timeline ? "denied" : "unconfirmed";
  const relevant = isLatest() || pauseRetainsLeadSkip(work, latestTransport);
  if (work.confirmed) return relevant ? "applied" : "unconfirmed";
  // A lead response can supersede an earlier heartbeat. Otherwise the stream
  // wins once it has published a newer anchor.
  if (latest !== timeline && (!lead || latest.presentAtMS >= result.presentAtMS)) {
    return relevant && matchesTransport(work, latest) ? "applied" : "unconfirmed";
  }
  set({
    timeline: {
      ...latest,
      positionMS: result.positionMS,
      presentAtMS: result.presentAtMS,
      ...(playbackChange ? { paused: !playing } : {}),
    },
  });
  return relevant ? "applied" : "unconfirmed";
}

/**
 * As host, approve a member's pending request, which moves every member, or
 * decline it. Resolves to false when this viewer is not the host or the
 * server refused (the error is in the state).
 */
export async function decidePartyRequest(requestID: string, approve: boolean): Promise<boolean> {
  if (state.status !== "active" || !state.isHost || !requestID) return false;
  const partyID = state.partyID;
  const gen = generation;
  set({ requests: state.requests.filter((r) => r.id !== requestID) });
  try {
    await decideWatchPartyRequest(partyID, requestID, approve);
    return true;
  } catch (err) {
    if (gen === generation) set(failure(err));
    return false;
  }
}

/**
 * Report the player's measured drift from the party timeline, at most once per
 * DRIFT_REPORT_INTERVAL_MS. The player corrects itself from the timeline; the
 * report lets the server see and nudge it, so a failed one is not surfaced.
 * Returns whether a report was sent.
 */
export function reportPartyDrift(driftMS: number, now: number = Date.now()): boolean {
  if (state.status !== "active" || !state.partyID || !Number.isFinite(driftMS)) return false;
  // A lead party has no drift reports: the lead reports its position instead.
  if (state.lead) return false;
  if (now - lastDriftReport < DRIFT_REPORT_INTERVAL_MS) return false;
  lastDriftReport = now;
  void reportWatchPartyDrift(state.partyID, driftMS).catch(() => {});
  return true;
}

/**
 * As the lead of a lead party, report where the player is: the position it is
 * audibly at, the local clock reading (localClockMS) of that sample, and its
 * playback rate. The server keeps the followers' timeline on it; a failed
 * report is not surfaced, the next one a second later stands in. Returns
 * whether a report was sent.
 */
export function reportLeadPosition(
  positionMS: number,
  atLocalMS: number,
  playbackRate: number,
): boolean {
  if (state.status !== "active" || !state.lead || !state.isHost || !state.partyID) return false;
  if (![positionMS, atLocalMS, playbackRate].every(Number.isFinite)) return false;
  void reportWatchPartyLeadPosition(state.partyID, {
    positionMS,
    atMS: atLocalMS + state.offsetMS,
    ratePermille: playbackRate * 1000,
  }).catch(() => {});
  return true;
}

/**
 * True when this viewer is a member, not the host, of an active party: its
 * player's Up Next and autoplay defer to the party, which the host advances
 * for everyone (CORE-1766).
 */
export function watchPartyDefersUpNext(party: WatchPartyState): boolean {
  return party.status === "active" && !party.isHost;
}

/**
 * Leave the party; the local player keeps playing on its own. A host's leave
 * hands the party to the earliest member still in it.
 */
export async function leaveParty(): Promise<void> {
  const partyID = state.partyID;
  // Leaving a lead party is a choice: do not rejoin it, or follow it unasked.
  if (partyID && state.lead) declinedParties.add(partyID);
  forgetFollowing();
  reset();
  if (partyID) await leaveWatchParty(partyID).catch(() => {});
}

/** As host, end the party for everyone in it. */
export async function endParty(): Promise<void> {
  if (!state.isHost) return;
  const partyID = state.partyID;
  reset();
  if (partyID) await leaveWatchParty(partyID, true).catch(() => {});
}

/**
 * The tab is closing or navigating away: leave with a keepalive request that
 * outlives the page. The server expires a member whose stream dropped anyway;
 * this makes it seconds rather than the expiry's grace. A follower screen
 * keeps the party it follows, so a reload rejoins it.
 */
export function leavePartyOnPageHide(): void {
  if (state.status === "idle") return;
  const partyID = state.partyID;
  reset();
  if (partyID) leaveWatchPartyKeepalive(partyID);
}

export function clearWatchPartyError(): void {
  if (state.error) set({ error: "", errorDetail: "" });
}

export function clearWatchPartyNotice(): void {
  if (state.notice) set({ notice: "", noticeName: "" });
}

/** Test seam: drop all party state without calling the server. */
export function resetWatchPartyForTest(): void {
  watching = 0;
  declinedParties.clear();
  startedParties.clear();
  reset();
  transportQueues.clear();
}
