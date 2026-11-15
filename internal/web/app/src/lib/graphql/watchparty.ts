// Watch Party operations (ADR-0038, CORE-1677): form a party around a Media,
// invite a household Profile, accept from the invite, drive or request the
// shared timeline, and leave. Forming and joining are home-network only
// (CORE-0531); the server refuses them from anywhere else.

import { graphql, postJSON, postKeepalive } from "@/lib/api";

export interface WatchPartyParticipant {
  displayName: string;
  present: boolean;
}

export interface WatchPartyInfo {
  partyID: string;
  content: string;
  relaxed: boolean;
  members: WatchPartyParticipant[];
}

/** The shared timeline: `item` at `positionMS` is shown at server time `presentAtMS`. */
export interface WatchPartyAnchor {
  item: string;
  positionMS: number;
  presentAtMS: number;
}

export interface AcceptWatchPartyResult extends WatchPartyAnchor {
  partyID: string;
  degraded?: boolean;
}

export interface WatchPartyRequestResult {
  partyID: string;
  approved: boolean;
}

export interface WatchPartyCalibration {
  /** serverClock − localClock, in ms. */
  offsetMS: number;
  /** Request round trip, less server time, in ms. */
  roundTripMS: number;
}

const CREATE_WATCH_PARTY_MUTATION =
  "mutation CreateWatchParty($content: String!, $relaxed: Boolean!, $clockOffsetMS: Int!, $renderLatencyMS: Int!) { createWatchParty(content: $content, relaxed: $relaxed, clockOffsetMS: $clockOffsetMS, renderLatencyMS: $renderLatencyMS) { partyID content relaxed members { displayName present } } }";

const INVITE_TO_WATCH_PARTY_MUTATION =
  "mutation InviteToWatchParty($partyID: String!, $displayName: String!) { inviteToWatchParty(partyID: $partyID, displayName: $displayName) { partyID content relaxed members { displayName present } } }";

const ACCEPT_WATCH_PARTY_MUTATION =
  "mutation AcceptWatchParty($partyID: String!, $clockOffsetMS: Int!, $renderLatencyMS: Int!) { acceptWatchParty(partyID: $partyID, clockOffsetMS: $clockOffsetMS, renderLatencyMS: $renderLatencyMS) { partyID item positionMS presentAtMS degraded } }";

const WATCH_PARTY_TRANSPORT_MUTATION =
  "mutation WatchPartyTransport($partyID: String!, $item: String!, $positionMS: Int!, $paused: Boolean) { watchPartyTransport(partyID: $partyID, item: $item, positionMS: $positionMS, paused: $paused) { item positionMS presentAtMS } }";

const REQUEST_WATCH_PARTY_TRANSPORT_MUTATION =
  "mutation RequestWatchPartyTransport($partyID: String!, $item: String!, $positionMS: Int!, $paused: Boolean) { requestWatchPartyTransport(partyID: $partyID, item: $item, positionMS: $positionMS, paused: $paused) { partyID approved } }";

const LEAVE_WATCH_PARTY_MUTATION =
  "mutation LeaveWatchParty($partyID: String!, $end: Boolean) { leaveWatchParty(partyID: $partyID, end: $end) { partyID } }";

/** CreateWatchParty — the caller's session hosts a party around `content`. */
export async function createWatchParty(vars: {
  content: string;
  relaxed: boolean;
  clockOffsetMS: number;
  renderLatencyMS: number;
}): Promise<WatchPartyInfo> {
  const data = await graphql<{ createWatchParty: WatchPartyInfo }>(
    CREATE_WATCH_PARTY_MUTATION,
    vars,
  );
  return data.createWatchParty;
}

/** InviteToWatchParty — invite a household Profile by its display name. */
export async function inviteToWatchParty(
  partyID: string,
  displayName: string,
): Promise<WatchPartyInfo> {
  const data = await graphql<{ inviteToWatchParty: WatchPartyInfo }>(
    INVITE_TO_WATCH_PARTY_MUTATION,
    { partyID, displayName },
  );
  return data.inviteToWatchParty;
}

/** AcceptWatchParty — join from an invite; returns the live anchor to land on. */
export async function acceptWatchParty(vars: {
  partyID: string;
  clockOffsetMS: number;
  renderLatencyMS: number;
}): Promise<AcceptWatchPartyResult> {
  const data = await graphql<{ acceptWatchParty: AcceptWatchPartyResult }>(
    ACCEPT_WATCH_PARTY_MUTATION,
    vars,
  );
  return data.acceptWatchParty;
}

/** WatchPartyTransport — the host's play, pause or seek, authoritative for all. */
export async function watchPartyTransport(vars: {
  partyID: string;
  item: string;
  positionMS: number;
  paused: boolean;
}): Promise<WatchPartyAnchor> {
  const data = await graphql<{ watchPartyTransport: WatchPartyAnchor }>(
    WATCH_PARTY_TRANSPORT_MUTATION,
    vars,
  );
  return data.watchPartyTransport;
}

/** RequestWatchPartyTransport — a member asks; a relaxed party applies it. */
export async function requestWatchPartyTransport(vars: {
  partyID: string;
  item: string;
  positionMS: number;
  paused: boolean;
}): Promise<WatchPartyRequestResult> {
  const data = await graphql<{ requestWatchPartyTransport: WatchPartyRequestResult }>(
    REQUEST_WATCH_PARTY_TRANSPORT_MUTATION,
    vars,
  );
  return data.requestWatchPartyTransport;
}

/**
 * LeaveWatchParty — the caller's session leaves the party. A host's leave
 * hands the party to the member who joined first; `end` ends it for everyone.
 */
export async function leaveWatchParty(partyID: string, end = false): Promise<void> {
  await graphql<{ leaveWatchParty: { partyID: string } }>(LEAVE_WATCH_PARTY_MUTATION, {
    partyID,
    ...(end ? { end: true } : {}),
  });
}

/** LeaveWatchParty sent with keepalive from pagehide, so a closing tab still leaves. */
export function leaveWatchPartyKeepalive(partyID: string): void {
  postKeepalive("/api/graphql", { query: LEAVE_WATCH_PARTY_MUTATION, variables: { partyID } });
}

/**
 * Four-timestamp clock calibration against POST /api/group/calibrate, which
 * answers only on the home network. `now` is injectable for tests.
 */
export async function calibrateWatchPartyClock(
  now: () => number = Date.now,
): Promise<WatchPartyCalibration> {
  const t1MS = Math.round(now());
  const reply = await postJSON<{ t1MS: number; t2MS: number; t3MS: number }>(
    "/api/group/calibrate",
    { t1MS },
  );
  const t4MS = Math.round(now());
  return {
    offsetMS: Math.round((reply.t2MS - t1MS + (reply.t3MS - t4MS)) / 2),
    roundTripMS: Math.max(0, t4MS - t1MS - (reply.t3MS - reply.t2MS)),
  };
}
