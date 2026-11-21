// Shared timing for the party host and its stage screens (CORE-1662): where
// the anchor says the song is, how far a screen is from it, and the bounds the
// host page and stage act on.

import type { PartyPlayback, PartyScreen } from "@/lib/graphql";

/** A screen within this many ms of the anchor is shown as in sync. */
export const IN_SYNC_MS = 40;
/** A stage seeks when it drifts further than this from the anchor. */
export const SEEK_DRIFT_MS = 250;
/** Host and stage poll at about this interval while visible. */
export const POLL_MS = 1000;
/** A stage reports its offset this often, well inside the 2-minute drop window. */
export const REPORT_MS = 5000;

/**
 * The song position, in ms, the anchor puts at server time `nowMS`:
 * positionMS + (now − anchorAtMS) while playing, else positionMS.
 */
export function anchorTargetMS(playback: PartyPlayback, nowMS: number): number {
  if (playback.state !== "playing") return Math.max(0, playback.positionMS);
  return Math.max(0, playback.positionMS + (nowMS - playback.anchorAtMS));
}

/**
 * Whether the host may say a screen is in sync: only after the screen has
 * reported, and only when its reported offset is within IN_SYNC_MS.
 */
export function screenInSync(screen: PartyScreen): boolean {
  return screen.reportedAt !== "" && Math.abs(screen.offsetMS) <= IN_SYNC_MS;
}

/** The stage window's name for the session, so a second Pop out reuses it. */
export function stageWindowName(sessionID: string): string {
  return `beamfall-party-stage-${sessionID}`;
}

/** The viewer path of a session's stage; `joined` registers it as a joined screen. */
export function stagePath(sessionID: string, joined = false): string {
  const path = `/party/stage/${encodeURIComponent(sessionID)}`;
  return joined ? `${path}?joined=1` : path;
}
