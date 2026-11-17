// Watch Party operations (ADR-0038, CORE-1677): form a party around a Media,
// invite a household Profile, accept from the invite, drive or request the
// shared timeline, and leave. Forming and joining are home-network only
// (CORE-0531); the server refuses them from anywhere else.

import { graphql, postJSON, postKeepalive } from "@/lib/api";
import { type ClockExchange, estimateClock, localClockMS } from "@/lib/clockEstimate";

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
  /** serverClock − localClock, in fractional ms; the local clock is localClockMS. */
  offsetMS: number;
  /** Request round trip, less server time, in ms. */
  roundTripMS: number;
}

const CREATE_WATCH_PARTY_MUTATION =
  "mutation CreateWatchParty($content: String!, $relaxed: Boolean!, $clockOffsetMS: Int!, $renderLatencyMS: Int!, $lead: Boolean) { createWatchParty(content: $content, relaxed: $relaxed, clockOffsetMS: $clockOffsetMS, renderLatencyMS: $renderLatencyMS, lead: $lead) { partyID content relaxed members { displayName present } } }";

const INVITE_TO_WATCH_PARTY_MUTATION =
  "mutation InviteToWatchParty($partyID: String!, $displayName: String!) { inviteToWatchParty(partyID: $partyID, displayName: $displayName) { partyID content relaxed members { displayName present } } }";

const ACCEPT_WATCH_PARTY_MUTATION =
  "mutation AcceptWatchParty($partyID: String!, $clockOffsetMS: Int!, $renderLatencyMS: Int!) { acceptWatchParty(partyID: $partyID, clockOffsetMS: $clockOffsetMS, renderLatencyMS: $renderLatencyMS) { partyID item positionMS presentAtMS degraded } }";

const WATCH_PARTY_TRANSPORT_MUTATION =
  "mutation WatchPartyTransport($partyID: String!, $item: String!, $positionMS: Int!, $paused: Boolean) { watchPartyTransport(partyID: $partyID, item: $item, positionMS: $positionMS, paused: $paused) { item positionMS presentAtMS } }";

const REQUEST_WATCH_PARTY_TRANSPORT_MUTATION =
  "mutation RequestWatchPartyTransport($partyID: String!, $item: String!, $positionMS: Int!, $paused: Boolean) { requestWatchPartyTransport(partyID: $partyID, item: $item, positionMS: $positionMS, paused: $paused) { partyID approved } }";

const DECIDE_WATCH_PARTY_REQUEST_MUTATION =
  "mutation DecideWatchPartyRequest($partyID: String!, $request: String, $decline: Boolean) { watchPartyTransport(partyID: $partyID, request: $request, decline: $decline) { item positionMS presentAtMS } }";

const REPORT_DRIFT_MUTATION =
  "mutation ReportDrift($groupID: String!, $driftMS: Int!, $positionMS: Int, $atMS: Int, $ratePermille: Int) { reportDrift(groupID: $groupID, driftMS: $driftMS, positionMS: $positionMS, atMS: $atMS, ratePermille: $ratePermille) { groupID action rate } }";

const LEAVE_WATCH_PARTY_MUTATION =
  "mutation LeaveWatchParty($partyID: String!, $end: Boolean) { leaveWatchParty(partyID: $partyID, end: $end) { partyID } }";

/**
 * CreateWatchParty — the caller's session hosts a party around `content`.
 * `lead` makes it a lead party (CORE-2245): host-only, with the caller's
 * session as the lead screen the other screens follow.
 */
export async function createWatchParty(vars: {
  content: string;
  relaxed: boolean;
  clockOffsetMS: number;
  renderLatencyMS: number;
  lead?: boolean;
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
 * DecideWatchPartyRequest — the host approves a member's pending request, which
 * moves every member, or declines it, which leaves the timeline as it is.
 */
export async function decideWatchPartyRequest(
  partyID: string,
  request: string,
  approve: boolean,
): Promise<WatchPartyAnchor> {
  const data = await graphql<{ watchPartyTransport: WatchPartyAnchor }>(
    DECIDE_WATCH_PARTY_REQUEST_MUTATION,
    { partyID, request, decline: !approve },
  );
  return data.watchPartyTransport;
}

/**
 * The server's read of a report: in sync, a rate nudge, or a re-seek for a
 * member's drift; `anchor` when a lead's report made it re-state the timeline.
 */
export interface DriftReport {
  groupID: string;
  action: "none" | "nudge" | "resync" | "anchor";
  rate: number;
}

/**
 * ReportDrift — tell the server how far this member's player is from the
 * party timeline, in ms (positive is ahead), so it can nudge it (CORE-1766).
 */
export async function reportWatchPartyDrift(
  partyID: string,
  driftMS: number,
): Promise<DriftReport> {
  const data = await graphql<{ reportDrift: DriftReport }>(REPORT_DRIFT_MUTATION, {
    groupID: partyID,
    driftMS: Math.round(driftMS),
  });
  return data.reportDrift;
}

/** Where a lead screen is, as it reports it. */
export interface LeadPosition {
  /** The position the lead is audibly at, in ms. */
  positionMS: number;
  /** The server-clock instant of that sample, in ms. */
  atMS: number;
  /** The lead's playback rate in thousandths of normal speed. */
  ratePermille: number;
}

/**
 * ReportDrift from the lead of a lead party (CORE-2245): where its player is,
 * when, and how fast it runs, so the server keeps the followers' timeline on
 * the lead's audio. Normal speed is left off the wire.
 */
export async function reportWatchPartyLeadPosition(
  partyID: string,
  position: LeadPosition,
): Promise<DriftReport> {
  const ratePermille = Math.round(position.ratePermille);
  const data = await graphql<{ reportDrift: DriftReport }>(REPORT_DRIFT_MUTATION, {
    groupID: partyID,
    driftMS: 0,
    positionMS: Math.max(0, Math.round(position.positionMS)),
    atMS: Math.round(position.atMS),
    ...(ratePermille > 0 && ratePermille !== 1000 ? { ratePermille } : {}),
  });
  return data.reportDrift;
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

/** How many exchanges one calibration makes; the quickest decides. */
export const CALIBRATION_EXCHANGES = 8;

interface CalibrationReply {
  t2MS: number;
  t3MS: number;
  /** The same instants in Unix microseconds, from a server that has them. */
  t2US?: number;
  t3US?: number;
}

function serverMS(us: number | undefined, ms: number): number {
  return typeof us === "number" && Number.isFinite(us) && us > 0 ? us / 1000 : ms;
}

/**
 * Four-timestamp clock calibration against POST /api/group/calibrate, which
 * answers only on the home network. It makes `exchanges` exchanges one after
 * another and estimates from the one with the shortest round trip (CORE-2244).
 * A refusal of the first exchange rejects; a later failure ends the run with
 * what it has. `now` is injectable for tests.
 */
export async function calibrateWatchPartyClock(
  options: { exchanges?: number; now?: () => number } = {},
): Promise<WatchPartyCalibration> {
  const now = options.now ?? localClockMS;
  const count = Math.max(1, Math.floor(options.exchanges ?? CALIBRATION_EXCHANGES));
  const exchanges: ClockExchange[] = [];
  for (let i = 0; i < count; i++) {
    const t1 = now();
    let reply: CalibrationReply;
    try {
      reply = await postJSON<CalibrationReply>("/api/group/calibrate", { t1MS: Math.round(t1) });
    } catch (err) {
      if (exchanges.length === 0) throw err;
      break;
    }
    const t4 = now();
    exchanges.push({
      t1,
      t2: serverMS(reply.t2US, reply.t2MS),
      t3: serverMS(reply.t3US, reply.t3MS),
      t4,
    });
  }
  const estimate = estimateClock(exchanges);
  if (!estimate) throw new Error("clock calibration returned no usable exchange");
  return estimate;
}
