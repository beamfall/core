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
// The paused flag from a roster that arrived before the join's accept returned.
let earlyRosterPaused: boolean | null = null;
let rosterWaiters: Array<() => void> = [];

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

function applyFrame(frame: GroupSyncFrame): void {
  if (frame.groupID !== state.partyID) return;
  if (frame.signal === "roster") {
    const members = frame.members ?? state.members;
    const patch: Partial<WatchPartyState> = {
      members,
      invited: state.invited.filter((name) => !members.includes(name)),
    };
    if (state.status === "joining") {
      if (state.timeline) patch.timeline = { ...state.timeline, paused: frame.paused };
      else earlyRosterPaused = frame.paused;
    }
    set(patch);
    const waiters = rosterWaiters;
    rosterWaiters = [];
    for (const wake of waiters) wake();
    return;
  }
  // A nudge carries only the server's suggested rate; the player measures and
  // corrects its own drift, so only transitions move the timeline.
  if (frame.signal === "transition") {
    set({
      timeline: {
        item: frame.item,
        positionMS: frame.positionMS,
        presentAtMS: frame.presentAtMS,
        paused: frame.paused,
      },
    });
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
  earlyRosterPaused = null;
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
  try {
    const clock = await calibrateWatchPartyClock();
    // Relaxed: anyone in the household party may play, pause or seek.
    const info = await createWatchParty({
      content,
      relaxed: true,
      clockOffsetMS: clock.offsetMS,
      renderLatencyMS: 0,
    });
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
    reset(failure(err));
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
  earlyRosterPaused = null;
  set({ status: "joining", partyID, isHost: false, error: "", errorDetail: "" });
  openStream();
  try {
    const clock = await calibrateWatchPartyClock();
    const anchor = await acceptWatchParty({
      partyID,
      clockOffsetMS: clock.offsetMS,
      renderLatencyMS: 0,
    });
    if (earlyRosterPaused === null) await waitForRoster(ROSTER_WAIT_MS);
    if (state.partyID !== partyID) return null;
    set({
      status: "active",
      offsetMS: clock.offsetMS,
      timeline: {
        item: anchor.item,
        positionMS: anchor.positionMS,
        presentAtMS: anchor.presentAtMS,
        // Without a roster the party is most likely still waiting to start.
        paused: earlyRosterPaused ?? true,
      },
    });
    earlyRosterPaused = null;
    return anchor.item;
  } catch (err) {
    reset(failure(err));
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
  try {
    if (state.isHost) {
      const anchor = await watchPartyTransport(vars);
      if (state.partyID === partyID) set({ timeline: { ...anchor, paused } });
    } else {
      await requestWatchPartyTransport(vars);
    }
  } catch (err) {
    if (state.partyID === partyID) set(failure(err));
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
