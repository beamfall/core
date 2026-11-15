// Watch Party sync math (CORE-1677). Pure functions so the timeline rules are
// tested without a <video>: parse the server's group.sync frames, project the
// shared anchor onto the server clock, and pick the correction for a drift.

export const GROUP_SYNC_EVENT = "group.sync";

/** Above this drift the player re-seeks; below it, a rate nudge is enough. */
export const SEEK_THRESHOLD_MS = 500;
/** Drift inside this band counts as in sync. */
export const IN_SYNC_MS = 40;
/** The furthest a rate nudge moves away from 1×. */
export const MAX_RATE_NUDGE = 0.05;
/** The rate nudge closes this share of the drift per second. */
const NUDGE_GAIN = 0.5;

/** The party timeline: `item` at `positionMS` was (or will be) shown at server time `presentAtMS`. */
export interface PartyTimeline {
  item: string;
  positionMS: number;
  presentAtMS: number;
  paused: boolean;
}

export type GroupSyncSignal = "transition" | "nudge" | "roster" | "ended";

const SIGNALS: ReadonlySet<string> = new Set<GroupSyncSignal>([
  "transition",
  "nudge",
  "roster",
  "ended",
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
}

interface GroupSyncPayload {
  groupID?: string;
  signal?: string;
  item?: string;
  positionMS?: number;
  presentAtMS?: number;
  paused?: boolean;
  members?: string[];
  handoff?: boolean;
  hostName?: string;
  host?: string;
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
    ...(Array.isArray(payload.members)
      ? { members: payload.members.filter((m): m is string => typeof m === "string") }
      : {}),
    ...(payload.handoff === true ? { handoff: true } : {}),
    ...(typeof payload.hostName === "string" && payload.hostName
      ? { hostName: payload.hostName }
      : {}),
    ...(typeof payload.host === "string" && payload.host ? { host: payload.host } : {}),
  };
}

function finite(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

/**
 * Where the shared timeline is at server time `serverNowMS`. A paused timeline
 * holds its position; a running one holds until its present time (the
 * pre-buffer window), then advances in real time.
 */
export function expectedPositionMS(timeline: PartyTimeline, serverNowMS: number): number {
  if (timeline.paused) return timeline.positionMS;
  return timeline.positionMS + Math.max(0, serverNowMS - timeline.presentAtMS);
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

export type DriftLevel = "in-sync" | "adjusting" | "resyncing";

/** The drift indicator's level for a measured drift. */
export function driftLevel(driftMS: number): DriftLevel {
  const magnitude = Math.abs(driftMS);
  if (magnitude <= IN_SYNC_MS * 2) return "in-sync";
  if (magnitude <= SEEK_THRESHOLD_MS) return "adjusting";
  return "resyncing";
}
