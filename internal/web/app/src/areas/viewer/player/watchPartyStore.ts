import { useSyncExternalStore } from "react";
import { ApiError, subscribeEvents } from "@/lib/api";
import {
  acceptWatchParty,
  calibrateWatchPartyClock,
  createWatchParty,
  decideWatchPartyRequest,
  fetchMySessions,
  inviteToWatchParty,
  leaveWatchParty,
  leaveWatchPartyKeepalive,
  requestWatchPartyTransport,
  type WatchPartyInfo,
  watchPartyTransport,
} from "@/lib/graphql";
import {
  expectedPositionMS,
  type GroupSyncFrame,
  IN_SYNC_MS,
  type PartyTimeline,
  parseGroupSync,
} from "./watchPartySync";

// The web viewer's one Watch Party (CORE-1677). It lives outside React so the
// party, its clock offset and its group.sync subscription survive the jump
// from the invite notification to the player and from one title to the next.
// The player's useWatchParty hook reads it and keeps the <video> on the
// shared timeline.

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

/** How many pending requests the host's player keeps; older ones fall off. */
export const MAX_PARTY_REQUESTS = 10;

export interface WatchPartyState {
  status: WatchPartyStatus;
  partyID: string;
  isHost: boolean;
  /** The shared timeline, once known. */
  timeline: PartyTimeline | null;
  /** Display names of the Profiles in the party, sorted. */
  members: string[];
  /** Display names invited but not yet joined. */
  invited: string[];
  /** serverClock − localClock, in ms. */
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
let rosterWaiters: Array<() => void> = [];
// Bumped by every reset, so a start or join that a leave overtook knows it is stale.
let generation = 0;
// Counts timeline frames, so a host's transport response older than a frame is dropped.
let timelineFrames = 0;

function set(patch: Partial<WatchPartyState>): void {
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

/** The current party state, re-rendering on every change. */
export function useWatchPartyState(): WatchPartyState {
  return useSyncExternalStore(subscribe, getWatchParty, getWatchParty);
}

function timelineOf(frame: GroupSyncFrame): PartyTimeline {
  return {
    item: frame.item,
    positionMS: frame.positionMS,
    presentAtMS: frame.presentAtMS,
    paused: frame.paused,
  };
}

// A roster re-states the live anchor with a fresh present time. When it
// describes the timeline this viewer already follows, keep the current one:
// adopting the new present time would hold a playing viewer for the pre-buffer.
function sameTimeline(current: PartyTimeline | null, frame: GroupSyncFrame): boolean {
  return (
    current !== null &&
    current.item === frame.item &&
    current.paused === frame.paused &&
    Math.abs(expectedPositionMS(current, frame.presentAtMS) - frame.positionMS) <= IN_SYNC_MS
  );
}

function applyFrame(frame: GroupSyncFrame): void {
  if (!state.partyID || frame.groupID !== state.partyID) return;
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
    reset({ notice: "ended", noticeName: frame.hostName ?? "" });
    return;
  }
  const patch: Partial<WatchPartyState> = {};
  if (frame.item && !(frame.signal === "roster" && sameTimeline(state.timeline, frame))) {
    patch.timeline = timelineOf(frame);
    timelineFrames++;
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

function reset(patch: Partial<WatchPartyState> = {}): void {
  closeStream?.();
  closeStream = null;
  generation++;
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

/**
 * Start a party around `content`, hosted by this session. The timeline holds
 * at `positionMS` until the host's first transport puts it in motion.
 */
export async function startWatchParty(
  content: string,
  positionMS = 0,
  relaxed = true,
): Promise<boolean> {
  if (state.status !== "idle") return false;
  set({ status: "starting", error: "", errorDetail: "", notice: "", noticeName: "" });
  const gen = generation;
  try {
    const clock = await calibrateWatchPartyClock();
    // Relaxed: anyone in the household party may play, pause or seek. Host-only:
    // a member's play, pause or seek waits for the host's approval (CORE-1765).
    const info = await createWatchParty({
      content,
      relaxed,
      clockOffsetMS: clock.offsetMS,
      renderLatencyMS: 0,
    });
    if (gen !== generation) {
      // Left while the party was being created: end it on the server too.
      await leaveWatchParty(info.partyID).catch(() => {});
      return false;
    }
    set({
      status: "active",
      partyID: info.partyID,
      isHost: true,
      offsetMS: clock.offsetMS,
      members: present(info),
      invited: invitedOf(info),
      timeline: {
        item: content,
        positionMS: Math.max(0, Math.round(positionMS)),
        presentAtMS: 0,
        paused: true,
      },
    });
    openStream();
    return true;
  } catch (err) {
    if (gen === generation) reset(failure(err));
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

/**
 * Join from an invite. Resolves to the Media the party is on, or null on
 * failure (the error is in the state). The stream opens before the accept so
 * the roster it triggers, which says whether the party is paused, is not missed.
 */
export async function joinWatchParty(partyID: string): Promise<string | null> {
  if (state.status !== "idle") {
    if (state.partyID === partyID && state.timeline) return state.timeline.item;
    return null;
  }
  set({
    status: "joining",
    partyID,
    isHost: false,
    error: "",
    errorDetail: "",
    notice: "",
    noticeName: "",
  });
  const gen = generation;
  openStream();
  try {
    const clock = await calibrateWatchPartyClock();
    const anchor = await acceptWatchParty({
      partyID,
      clockOffsetMS: clock.offsetMS,
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
      offsetMS: clock.offsetMS,
      // Without a roster yet, hold paused at the anchor: the party is most
      // likely still waiting to start, and the roster, when it comes, moves it.
      timeline: state.timeline ?? {
        item: anchor.item,
        positionMS: anchor.positionMS,
        presentAtMS: anchor.presentAtMS,
        paused: true,
      },
    });
    return state.timeline?.item ?? anchor.item;
  } catch (err) {
    if (gen === generation) reset(failure(err));
    return null;
  }
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

/**
 * Put this viewer's play, pause or seek on the shared timeline: the host's is
 * authoritative, a member's is a request the relaxed party applies.
 */
export async function sendPartyTransport(
  item: string,
  positionMS: number,
  paused: boolean,
): Promise<void> {
  if (state.status !== "active") return;
  const partyID = state.partyID;
  const vars = { partyID, item, positionMS: Math.max(0, Math.round(positionMS)), paused };
  const gen = generation;
  const frames = timelineFrames;
  try {
    if (state.isHost) {
      const anchor = await watchPartyTransport(vars);
      // The stream's own transition may already have overtaken this response.
      if (gen === generation && frames === timelineFrames) set({ timeline: { ...anchor, paused } });
    } else {
      await requestWatchPartyTransport(vars);
    }
  } catch (err) {
    if (gen === generation) set(failure(err));
  }
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
 * Leave the party; the local player keeps playing on its own. A host's leave
 * hands the party to the earliest member still in it.
 */
export async function leaveParty(): Promise<void> {
  const partyID = state.partyID;
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
 * this makes it seconds rather than the expiry's grace.
 */
export function leavePartyOnPageHide(): void {
  if (state.status === "idle" || !state.partyID) return;
  const partyID = state.partyID;
  reset();
  leaveWatchPartyKeepalive(partyID);
}

export function clearWatchPartyError(): void {
  if (state.error) set({ error: "", errorDetail: "" });
}

export function clearWatchPartyNotice(): void {
  if (state.notice) set({ notice: "", noticeName: "" });
}

/** Test seam: drop all party state without calling the server. */
export function resetWatchPartyForTest(): void {
  reset();
  ownSession = null;
}
