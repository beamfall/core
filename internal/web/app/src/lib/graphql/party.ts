// Party session operations (CORE-1662, openspec core-1662-party-session): the
// host's live session, its rotation and playback anchor, and the stage
// screens that follow it. Core words stay domain-neutral (session,
// participant, entry, screen); plugin labels live only in web copy.

import { graphql } from "@/lib/api";

export type PartyFairness = "round-robin" | "fifo";
export type PartyEntryState =
  | "pending"
  | "waiting"
  | "current"
  | "done"
  | "removed"
  | "declined"
  | "skipped";
export type PartyPlaybackState = "playing" | "paused";
export type PartyScreenKind = "popout" | "joined";
export type PartyScreenCarrier = "local" | "relay";

export interface PartyEntry {
  id: string;
  mediaID: string;
  title: string;
  subtitle: string;
  /** 0 when unknown. */
  durationSeconds: number;
  artworkURL: string;
  participantID: string;
  participantName: string;
  state: PartyEntryState;
  pinned: boolean;
  /** 1-based among waiting entries, 0 otherwise. */
  position: number;
  etaSeconds: number;
  /** The key the singer asked for, in semitones; the turn starts at it. */
  pitchSemitones: number;
  addedAt: string;
  /** When this Media was last current earlier in the session, else "". */
  lastPlayedAt: string;
}

export interface PartyParticipant {
  id: string;
  name: string;
  guest: boolean;
  waiting: number;
  lastTurnAt: string;
}

/** The shared anchor: `positionMS` (song ms) at server time `anchorAtMS`. */
export interface PartyPlayback {
  entryID: string;
  state: PartyPlaybackState;
  positionMS: number;
  anchorAtMS: number;
  /** 1 = as recorded. */
  tempo: number;
  pitchSemitones: number;
}

export interface PartyScreen {
  id: string;
  name: string;
  kind: PartyScreenKind;
  carrier: PartyScreenCarrier;
  lead: boolean;
  ticker: boolean;
  offsetMS: number;
  rttMS: number;
  /** "" before the first report. */
  reportedAt: string;
}

export interface PartySession {
  id: string;
  joinCode: string;
  /** "/web/join/<CODE>". */
  joinPath: string;
  fairness: PartyFairness;
  autoAdd: boolean;
  joinOpen: boolean;
  libraryIDs: string[];
  startedAt: string;
  expiresAt: string;
  changeoverSeconds: number;
  current: PartyEntry | null;
  playback: PartyPlayback | null;
  entries: PartyEntry[];
  requests: PartyEntry[];
  recent: PartyEntry[];
  removed: PartyEntry[];
  participants: PartyParticipant[];
  screens: PartyScreen[];
  skipped: PartyEntry[];
}

export interface PartyStage {
  sessionID: string;
  serverNowMS: number;
  current: PartyEntry | null;
  playback: PartyPlayback | null;
  /** At most 3. */
  upNext: PartyEntry[];
  /** For the screen asked about; true when none was named. */
  ticker: boolean;
  joinCode: string;
  joinPath: string;
}

const ENTRY =
  "id mediaID title subtitle durationSeconds artworkURL participantID participantName state pinned position etaSeconds pitchSemitones addedAt lastPlayedAt";
const PLAYBACK = "entryID state positionMS anchorAtMS tempo pitchSemitones";
const SCREEN = "id name kind carrier lead ticker offsetMS rttMS reportedAt";
const SESSION = `id joinCode joinPath fairness autoAdd joinOpen libraryIDs startedAt expiresAt changeoverSeconds current { ${ENTRY} } playback { ${PLAYBACK} } entries { ${ENTRY} } requests { ${ENTRY} } recent { ${ENTRY} } removed { ${ENTRY} } participants { id name guest waiting lastTurnAt } screens { ${SCREEN} } skipped { ${ENTRY} }`;
const STAGE = `sessionID serverNowMS current { ${ENTRY} } playback { ${PLAYBACK} } upNext { ${ENTRY} } ticker joinCode joinPath`;

const PARTY_SESSION_QUERY = `query PartySession { partySession { ${SESSION} } }`;
const PARTY_STAGE_QUERY = `query PartyStage($sessionID: String!, $screenID: String) { partyStage(sessionID: $sessionID, screenID: $screenID) { ${STAGE} } }`;
const PARTY_START_MUTATION = `mutation PartyStart($libraryIDs: [String!]!, $fairness: String, $autoAdd: Boolean) { partyStart(libraryIDs: $libraryIDs, fairness: $fairness, autoAdd: $autoAdd) { ${SESSION} } }`;
const PARTY_END_MUTATION = "mutation PartyEnd { partyEnd }";
const PARTY_UPDATE_MUTATION = `mutation PartyUpdate($fairness: String, $autoAdd: Boolean, $joinOpen: Boolean) { partyUpdate(fairness: $fairness, autoAdd: $autoAdd, joinOpen: $joinOpen) { ${SESSION} } }`;
const PARTY_ROTATE_CODE_MUTATION = `mutation PartyRotateCode { partyRotateCode { ${SESSION} } }`;
const PARTY_ADD_MUTATION = `mutation PartyAdd($mediaID: String!, $participantID: String, $participantName: String) { partyAdd(mediaID: $mediaID, participantID: $participantID, participantName: $participantName) { ${SESSION} } }`;
const PARTY_REVIEW_MUTATION = `mutation PartyReview($entryID: String!, $approve: Boolean!) { partyReview(entryID: $entryID, approve: $approve) { ${SESSION} } }`;
const PARTY_MOVE_MUTATION = `mutation PartyMove($entryID: String!, $position: Int!) { partyMove(entryID: $entryID, position: $position) { ${SESSION} } }`;
const PARTY_REMOVE_MUTATION = `mutation PartyRemove($entryID: String!) { partyRemove(entryID: $entryID) { ${SESSION} } }`;
const PARTY_RESTORE_MUTATION = `mutation PartyRestore($entryID: String!) { partyRestore(entryID: $entryID) { ${SESSION} } }`;
const PARTY_ADVANCE_MUTATION = `mutation PartyAdvance($fromEntryID: String) { partyAdvance(fromEntryID: $fromEntryID) { ${SESSION} } }`;
const PARTY_PLAYBACK_MUTATION = `mutation PartyPlayback($entryID: String!, $state: String!, $positionMS: Int!, $tempo: Float, $pitchSemitones: Int) { partyPlayback(entryID: $entryID, state: $state, positionMS: $positionMS, tempo: $tempo, pitchSemitones: $pitchSemitones) { ${SESSION} } }`;
const PARTY_REMOVE_PARTICIPANT_MUTATION = `mutation PartyRemoveParticipant($participantID: String!) { partyRemoveParticipant(participantID: $participantID) { ${SESSION} } }`;
const PARTY_SCREEN_REGISTER_MUTATION = `mutation PartyScreenRegister($sessionID: String!, $name: String!, $kind: String!, $carrier: String!) { partyScreenRegister(sessionID: $sessionID, name: $name, kind: $kind, carrier: $carrier) { ${SCREEN} } }`;
const PARTY_SCREEN_REPORT_MUTATION =
  "mutation PartyScreenReport($screenID: String!, $offsetMS: Int!, $rttMS: Int!) { partyScreenReport(screenID: $screenID, offsetMS: $offsetMS, rttMS: $rttMS) }";
const PARTY_SCREEN_TICKER_MUTATION = `mutation PartyScreenTicker($screenID: String!, $ticker: Boolean!) { partyScreenTicker(screenID: $screenID, ticker: $ticker) { ${SESSION} } }`;
const PARTY_SCREEN_DROP_MUTATION = `mutation PartyScreenDrop($screenID: String!) { partyScreenDrop(screenID: $screenID) { ${SESSION} } }`;

// Optional arguments are sent only when given, so an omitted one keeps the
// server's current value rather than arriving as null.
function defined(vars: Record<string, unknown>): Record<string, unknown> {
  return Object.fromEntries(Object.entries(vars).filter(([, value]) => value !== undefined));
}

async function sessionOp<K extends string>(
  field: K,
  query: string,
  vars: Record<string, unknown> = {},
  signal?: AbortSignal,
): Promise<PartySession> {
  const data = await graphql<Record<K, PartySession>>(query, defined(vars), signal);
  return data[field];
}

/** The caller's live party session, or null when it has none. */
export async function fetchPartySession(signal?: AbortSignal): Promise<PartySession | null> {
  const data = await graphql<{ partySession: PartySession | null }>(
    PARTY_SESSION_QUERY,
    {},
    signal,
  );
  return data.partySession ?? null;
}

/** What a stage screen shows; `screenID` reads that screen's ticker flag. */
export async function fetchPartyStage(
  sessionID: string,
  screenID: string,
  signal?: AbortSignal,
): Promise<PartyStage> {
  const data = await graphql<{ partyStage: PartyStage }>(
    PARTY_STAGE_QUERY,
    { sessionID, screenID },
    signal,
  );
  return data.partyStage;
}

/** Starts a session; a live one is returned unchanged. */
export function partyStart(vars: {
  libraryIDs: string[];
  fairness?: PartyFairness;
  autoAdd?: boolean;
}): Promise<PartySession> {
  return sessionOp("partyStart", PARTY_START_MUTATION, vars);
}

/** Ends the live session and revokes every guest token. */
export async function partyEnd(): Promise<boolean> {
  const data = await graphql<{ partyEnd: boolean }>(PARTY_END_MUTATION);
  return data.partyEnd;
}

export function partyUpdate(vars: {
  fairness?: PartyFairness;
  autoAdd?: boolean;
  joinOpen?: boolean;
}): Promise<PartySession> {
  return sessionOp("partyUpdate", PARTY_UPDATE_MUTATION, vars);
}

/** Issues a new join code; the old one stops admitting guests. */
export function partyRotateCode(): Promise<PartySession> {
  return sessionOp("partyRotateCode", PARTY_ROTATE_CODE_MUTATION);
}

/** Adds an entry for an existing participant or a named one (created on first use). */
export function partyAdd(vars: {
  mediaID: string;
  participantID?: string;
  participantName?: string;
}): Promise<PartySession> {
  return sessionOp("partyAdd", PARTY_ADD_MUTATION, vars);
}

/** Approves or declines a pending request. */
export function partyReview(entryID: string, approve: boolean): Promise<PartySession> {
  return sessionOp("partyReview", PARTY_REVIEW_MUTATION, { entryID, approve });
}

/** Pins an entry at a 1-based waiting position. */
export function partyMove(entryID: string, position: number): Promise<PartySession> {
  return sessionOp("partyMove", PARTY_MOVE_MUTATION, { entryID, position });
}

export function partyRemove(entryID: string): Promise<PartySession> {
  return sessionOp("partyRemove", PARTY_REMOVE_MUTATION, { entryID });
}

export function partyRestore(entryID: string): Promise<PartySession> {
  return sessionOp("partyRestore", PARTY_RESTORE_MUTATION, { entryID });
}

/** Moves to the next entry; idempotent when `fromEntryID` names the entry advanced from. */
export function partyAdvance(fromEntryID?: string): Promise<PartySession> {
  return sessionOp("partyAdvance", PARTY_ADVANCE_MUTATION, { fromEntryID });
}

/** Sets the current entry's anchor; the server stamps it with its clock. */
export function partyPlayback(vars: {
  entryID: string;
  state: PartyPlaybackState;
  positionMS: number;
  tempo?: number;
  pitchSemitones?: number;
}): Promise<PartySession> {
  return sessionOp("partyPlayback", PARTY_PLAYBACK_MUTATION, {
    ...vars,
    positionMS: Math.max(0, Math.round(vars.positionMS)),
  });
}

export function partyRemoveParticipant(participantID: string): Promise<PartySession> {
  return sessionOp("partyRemoveParticipant", PARTY_REMOVE_PARTICIPANT_MUTATION, {
    participantID,
  });
}

/** Registers this window or client as a stage screen of the session. */
export async function partyScreenRegister(vars: {
  sessionID: string;
  name: string;
  kind: PartyScreenKind;
  carrier: PartyScreenCarrier;
}): Promise<PartyScreen> {
  const data = await graphql<{ partyScreenRegister: PartyScreen }>(
    PARTY_SCREEN_REGISTER_MUTATION,
    vars,
  );
  return data.partyScreenRegister;
}

/** Reports a screen's measured offset from the anchor and its round trip. */
export async function partyScreenReport(
  screenID: string,
  offsetMS: number,
  rttMS: number,
): Promise<boolean> {
  const data = await graphql<{ partyScreenReport: boolean }>(PARTY_SCREEN_REPORT_MUTATION, {
    screenID,
    offsetMS: Math.round(offsetMS),
    rttMS: Math.max(0, Math.round(rttMS)),
  });
  return data.partyScreenReport;
}

export function partyScreenTicker(screenID: string, ticker: boolean): Promise<PartySession> {
  return sessionOp("partyScreenTicker", PARTY_SCREEN_TICKER_MUTATION, { screenID, ticker });
}

export function partyScreenDrop(screenID: string): Promise<PartySession> {
  return sessionOp("partyScreenDrop", PARTY_SCREEN_DROP_MUTATION, { screenID });
}
