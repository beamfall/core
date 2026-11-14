import { useSyncExternalStore } from "react";
import { ApiError, subscribeEvents } from "@/lib/api";
import {
  acceptWatchParty,
  calibrateWatchPartyClock,
  createWatchParty,
  inviteToWatchParty,
  leaveWatchParty,
  requestWatchPartyTransport,
  type WatchPartyInfo,
  watchPartyTransport,
} from "@/lib/graphql";
import { type GroupSyncFrame, type PartyTimeline, parseGroupSync } from "./watchPartySync";

// The web viewer's one Watch Party (CORE-1677). It lives outside React so the
// party, its clock offset and its group.sync subscription survive the jump
// from the invite notification to the player and from one title to the next.
// The player's useWatchParty hook reads it and keeps the <video> on the
// shared timeline.

export type WatchPartyStatus = "idle" | "starting" | "joining" | "active";

/** Why the last party action failed; the UI turns it into words. */
export type WatchPartyErrorKind = "" | "home-network" | "failed";

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

function applyFrame(frame: GroupSyncFrame): void {
  if (!state.partyID || frame.groupID !== state.partyID) return;
  // A nudge carries only the server's suggested rate; the player measures and
  // corrects its own drift. Transitions and rosters both carry the live anchor,
  // in stream order, so each one is the timeline.
  if (frame.signal === "nudge") return;
  const patch: Partial<WatchPartyState> = {};
  if (frame.item) {
    patch.timeline = timelineOf(frame);
    timelineFrames++;
  }
  if (frame.signal === "roster") {
    const members = frame.members ?? state.members;
    patch.members = members;
    patch.invited = state.invited.filter((name) => !members.includes(name));
  }
  set(patch);
  if (frame.signal === "roster") {
    const waiters = rosterWaiters;
    rosterWaiters = [];
    for (const wake of waiters) wake();
  }
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
export async function startWatchParty(content: string, positionMS = 0): Promise<boolean> {
  if (state.status !== "idle") return false;
  set({ status: "starting", error: "", errorDetail: "" });
  const gen = generation;
  try {
    const clock = await calibrateWatchPartyClock();
    // Relaxed: anyone in the household party may play, pause or seek.
    const info = await createWatchParty({
      content,
      relaxed: true,
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
  set({ status: "joining", partyID, isHost: false, error: "", errorDetail: "" });
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

/** Leave the party; the local player keeps playing on its own. */
export async function leaveParty(): Promise<void> {
  const partyID = state.partyID;
  reset();
  if (partyID) await leaveWatchParty(partyID).catch(() => {});
}

export function clearWatchPartyError(): void {
  if (state.error) set({ error: "", errorDetail: "" });
}

/** Test seam: drop all party state without calling the server. */
export function resetWatchPartyForTest(): void {
  reset();
}
