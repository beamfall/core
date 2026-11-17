// Watch Party sync math (CORE-1677). Pure functions so the timeline rules are
// tested without a <video>: parse the server's group.sync frames, project the
// shared anchor onto the server clock, and pick the correction for a drift.
// A lead party (CORE-2245) adds the follower's tighter correction and the
// clock estimate it stands on.

export {
  type ClockEstimate,
  type ClockExchange,
  estimateClock,
  localClockMS,
  mergeClockEstimate,
  type TimedClockEstimate,
} from "@/lib/clockEstimate";

export const GROUP_SYNC_EVENT = "group.sync";

/** Above this drift the player re-seeks; below it, a rate nudge is enough. */
export const SEEK_THRESHOLD_MS = 500;
/** Drift inside this band counts as in sync. */
export const IN_SYNC_MS = 40;
/** The furthest a rate nudge moves away from 1×. */
export const MAX_RATE_NUDGE = 0.05;
/** The rate nudge closes this share of the drift per second. */
const NUDGE_GAIN = 0.5;

/** A follower screen re-seeks above this drift from the lead's timeline. */
export const FOLLOW_SEEK_THRESHOLD_MS = 250;
/** Inside this band a follower plays at the timeline's own speed. */
export const FOLLOW_IN_SYNC_MS = 15;
/** A follower inside this drift is locked to the lead's audio. */
export const FOLLOW_LOCKED_MS = 40;
/** The furthest a follower's nudge moves away from the timeline's speed. */
export const FOLLOW_MAX_RATE_NUDGE = 0.1;
/** A follower's nudge closes this many times the drift per second. */
const FOLLOW_NUDGE_GAIN = 2;
/** The longest a follower aims ahead to cover the time a seek takes. */
export const MAX_SEEK_LEAD_MS = 2000;
/** Timeline speeds a player can follow, in thousandths of normal speed. */
const MIN_RATE_PERMILLE = 100;
const MAX_RATE_PERMILLE = 8000;

/** The party timeline: `item` at `positionMS` was (or will be) shown at server time `presentAtMS`. */
export interface PartyTimeline {
  item: string;
  positionMS: number;
  presentAtMS: number;
  paused: boolean;
  /**
   * A lead party's timeline (CORE-2245): the lead screen owns the audio, the
   * other screens follow it muted, and `presentAtMS` is the instant it was sent.
   */
  lead?: boolean;
  /** The timeline's speed in thousandths of normal speed; absent means 1000. */
  ratePermille?: number;
}

export type GroupSyncSignal = "transition" | "nudge" | "roster" | "ended" | "request";

const SIGNALS: ReadonlySet<string> = new Set<GroupSyncSignal>([
  "transition",
  "nudge",
  "roster",
  "ended",
  "request",
]);

export interface GroupSyncFrame extends PartyTimeline {
  groupID: string;
  signal: GroupSyncSignal;
  /** Sorted display names, on a roster frame only. */
  members?: string[];
  /** On a roster sent because the host left: the host role moved (CORE-1763). */
  handoff?: boolean;
  /** The new host's display name on a hand-off roster; who ended it on `ended`. */
  hostName?: string;
  /** The new host's session, only on the row for the new host's own Profile. */
  host?: string;
  /** On a `request` frame (CORE-1765): the id the host decides it by. */
  request?: string;
  /** On a `request` frame: the requesting member's display name. */
  from?: string;
}

interface GroupSyncPayload {
  groupID?: string;
  signal?: string;
  item?: string;
  positionMS?: number;
  presentAtMS?: number;
  paused?: boolean;
  lead?: boolean;
  ratePermille?: number;
  members?: string[];
  handoff?: boolean;
  hostName?: string;
  host?: string;
  request?: string;
  from?: string;
}

/** Reads a group.sync SSE frame; anything else, or a malformed one, is null. */
export function parseGroupSync(event: { type: string; data: unknown }): GroupSyncFrame | null {
  if (event.type !== GROUP_SYNC_EVENT || typeof event.data !== "string") return null;
  let payload: GroupSyncPayload | undefined;
  try {
    payload = (JSON.parse(event.data) as { payload?: GroupSyncPayload }).payload;
  } catch {
    return null;
  }
  if (!payload || typeof payload.groupID !== "string" || !payload.groupID) return null;
  const signal = payload.signal;
  if (typeof signal !== "string" || !SIGNALS.has(signal)) return null;
  return {
    groupID: payload.groupID,
    signal: signal as GroupSyncSignal,
    item: typeof payload.item === "string" ? payload.item : "",
    positionMS: finite(payload.positionMS),
    presentAtMS: finite(payload.presentAtMS),
    paused: payload.paused === true,
    ...(payload.lead === true ? { lead: true } : {}),
    ...ratePermilleOf(payload.ratePermille),
    ...(Array.isArray(payload.members)
      ? { members: payload.members.filter((m): m is string => typeof m === "string") }
      : {}),
    ...(payload.handoff === true ? { handoff: true } : {}),
    ...(typeof payload.hostName === "string" && payload.hostName
      ? { hostName: payload.hostName }
      : {}),
    ...(typeof payload.host === "string" && payload.host ? { host: payload.host } : {}),
    ...(typeof payload.request === "string" && payload.request ? { request: payload.request } : {}),
    ...(typeof payload.from === "string" && payload.from ? { from: payload.from } : {}),
  };
}

function finite(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

// A speed other than normal, when it is one a player can follow.
function ratePermilleOf(value: unknown): { ratePermille?: number } {
  if (typeof value !== "number" || !Number.isFinite(value)) return {};
  const ratePermille = Math.round(value);
  if (ratePermille === 1000) return {};
  if (ratePermille < MIN_RATE_PERMILLE || ratePermille > MAX_RATE_PERMILLE) return {};
  return { ratePermille };
}

/** The timeline's speed as a playback rate: 1 unless it says otherwise. */
export function timelineRate(timeline: PartyTimeline): number {
  return (timeline.ratePermille ?? 1000) / 1000;
}

/**
 * Where the shared timeline is at server time `serverNowMS`. A paused timeline
 * holds its position; a running one holds until its present time (the
 * pre-buffer window), then advances at the timeline's speed.
 */
export function expectedPositionMS(timeline: PartyTimeline, serverNowMS: number): number {
  if (timeline.paused) return timeline.positionMS;
  return (
    timeline.positionMS + Math.max(0, serverNowMS - timeline.presentAtMS) * timelineRate(timeline)
  );
}

/** Whether a running timeline has reached its present time. */
export function timelineStarted(timeline: PartyTimeline, serverNowMS: number): boolean {
  return !timeline.paused && serverNowMS >= timeline.presentAtMS;
}

export type Correction =
  | { kind: "none" }
  | { kind: "seek"; toMS: number }
  | { kind: "rate"; rate: number };

/**
 * The correction for a local player `driftMS` ahead (positive) or behind
 * (negative) the timeline at `expectedMS`: a large drift re-seeks, a small one
 * nudges the playback rate, and a drift inside the in-sync band plays at 1×.
 * A paused timeline never nudges: it only re-seeks.
 */
export function correctionFor(driftMS: number, expectedMS: number, paused: boolean): Correction {
  const magnitude = Math.abs(driftMS);
  if (magnitude > SEEK_THRESHOLD_MS) return { kind: "seek", toMS: expectedMS };
  if (paused || magnitude <= IN_SYNC_MS) return { kind: "none" };
  const nudge = Math.min(MAX_RATE_NUDGE, (magnitude / 1000) * NUDGE_GAIN);
  return { kind: "rate", rate: driftMS > 0 ? 1 - nudge : 1 + nudge };
}

/**
 * Where a follower screen should be at server time `serverNowMS`. `trimMS` is
 * this screen's display delay: the picture is shown that much earlier, so it
 * leaves the panel in step with the lead's audio. A paused timeline shows its
 * own frame whatever the delay.
 */
export function followerTargetMS(
  timeline: PartyTimeline,
  serverNowMS: number,
  trimMS: number,
): number {
  if (timeline.paused) return timeline.positionMS;
  return Math.max(0, expectedPositionMS(timeline, serverNowMS) + trimMS * timelineRate(timeline));
}

/**
 * The correction for a follower `driftMS` ahead (positive) or behind
 * (negative) its target `targetMS`: a large drift re-seeks, and a small one
 * nudges the playback rate around the timeline's own speed, in proportion to
 * the drift. Inside the in-sync band the follower plays at the timeline's
 * speed (`none`). A paused timeline never nudges: it re-seeks to the frame.
 */
export function followerCorrection(
  driftMS: number,
  targetMS: number,
  timeline: PartyTimeline,
): Correction {
  const magnitude = Math.abs(driftMS);
  if (magnitude > FOLLOW_SEEK_THRESHOLD_MS) return { kind: "seek", toMS: targetMS };
  if (magnitude <= FOLLOW_IN_SYNC_MS) return { kind: "none" };
  if (timeline.paused) return { kind: "seek", toMS: targetMS };
  const nudge = Math.min(FOLLOW_MAX_RATE_NUDGE, (magnitude / 1000) * FOLLOW_NUDGE_GAIN);
  return { kind: "rate", rate: timelineRate(timeline) * (driftMS > 0 ? 1 - nudge : 1 + nudge) };
}

/**
 * How far ahead of its target a follower aims its next seek. A seek takes
 * time, during which the timeline moves on; `driftAfterSeekMS`, measured right
 * after one lands, is what the last aim missed by, so the next one allows for it.
 */
export function nextSeekLeadMS(leadMS: number, driftAfterSeekMS: number): number {
  if (!Number.isFinite(driftAfterSeekMS)) return leadMS;
  return Math.min(MAX_SEEK_LEAD_MS, Math.max(0, leadMS - driftAfterSeekMS));
}

export type DriftLevel = "in-sync" | "adjusting" | "resyncing";

/** The drift indicator's level for a measured drift. */
export function driftLevel(driftMS: number): DriftLevel {
  const magnitude = Math.abs(driftMS);
  if (magnitude <= IN_SYNC_MS * 2) return "in-sync";
  if (magnitude <= SEEK_THRESHOLD_MS) return "adjusting";
  return "resyncing";
}

/** The indicator's level for a follower screen's drift from the lead. */
export function followerDriftLevel(driftMS: number): DriftLevel {
  const magnitude = Math.abs(driftMS);
  if (magnitude <= FOLLOW_LOCKED_MS) return "in-sync";
  if (magnitude <= FOLLOW_SEEK_THRESHOLD_MS) return "adjusting";
  return "resyncing";
}
