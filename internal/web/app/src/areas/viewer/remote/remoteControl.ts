import { useEffect, useRef } from "react";

// The receiving side of cross-device session control (CORE-1678). The server
// publishes a `session.control` event per command to every subscriber in the
// target's Profile (reveal-filtered); the envelope's aggregateID names the
// target session, so each client applies only the commands addressed to it.

export const SESSION_CONTROL_KIND = "session.control";

// A reconnect resumes from Last-Event-ID and replays what it missed. A pause
// sent minutes ago must not land now, so commands older than this are dropped.
export const STALE_AFTER_MS = 60_000;

export interface SessionControlCommand {
  command: string;
  direction?: "next" | "previous";
  positionMs?: number;
  volume?: number;
  trackID?: string;
  mediaID?: string;
  /** Every Media the target was playing when the command was authorized. */
  playingMediaIDs?: string[];
  text?: string;
}

export interface SessionControlFrame {
  /** The target session id (the envelope's aggregateID). */
  target: string;
  /** When the server issued the command, in epoch milliseconds. */
  occurredAt: number;
  command: SessionControlCommand;
}

interface Envelope {
  kind?: unknown;
  aggregateID?: unknown;
  occurredAt?: unknown;
  payload?: Record<string, unknown> | null;
}

function optionalString(value: unknown): string | undefined {
  return typeof value === "string" ? value : undefined;
}

function optionalStrings(value: unknown): string[] | undefined {
  if (!Array.isArray(value)) return undefined;
  const strings = value.filter((item): item is string => typeof item === "string");
  return strings.length > 0 ? strings : undefined;
}

function optionalNumber(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

/** Decodes a `session.control` SSE frame; null for a malformed or stale one. */
export function parseSessionControlFrame(data: unknown, now: number): SessionControlFrame | null {
  if (typeof data !== "string") return null;
  let envelope: Envelope;
  try {
    envelope = JSON.parse(data) as Envelope;
  } catch {
    return null;
  }
  if (envelope.kind !== SESSION_CONTROL_KIND || typeof envelope.aggregateID !== "string") {
    return null;
  }
  const occurred = typeof envelope.occurredAt === "string" ? Date.parse(envelope.occurredAt) : NaN;
  if (!Number.isFinite(occurred) || now - occurred > STALE_AFTER_MS) return null;
  const payload = envelope.payload;
  if (!payload || typeof payload.command !== "string" || payload.command === "") return null;
  const direction =
    payload.direction === "next" || payload.direction === "previous"
      ? payload.direction
      : undefined;
  return {
    target: envelope.aggregateID,
    occurredAt: occurred,
    command: {
      command: payload.command,
      direction,
      positionMs: optionalNumber(payload.positionMs),
      volume: optionalNumber(payload.volume),
      trackID: optionalString(payload.trackID),
      mediaID: optionalString(payload.mediaID),
      playingMediaIDs: optionalStrings(payload.playingMediaIDs),
      text: optionalString(payload.text),
    },
  };
}

/** What a running player exposes so remote commands can drive it. */
export interface RemotePlayerTarget {
  video: () => HTMLVideoElement | null;
  /** The Media this player is playing: the anchor a transport command must name. */
  mediaID?: string;
  /** Audio menu option id: the zero-based track index. */
  selectAudio?: (id: string) => void;
  /** Subtitle menu option id: "off" or the zero-based track index. */
  selectSubtitle?: (id: string) => void;
  next?: () => void;
  previous?: () => void;
  stop?: () => void;
  /** Sets this player up afresh at a position in seconds (undefined: saved progress). */
  restart?: (atSeconds?: number) => void;
}

// Another client's track ids are not this player's; only a plain index applies.
function isTrackIndex(id: string | undefined): id is string {
  return id !== undefined && /^\d{1,3}$/.test(id);
}

function anchoredTo(command: SessionControlCommand, target: RemotePlayerTarget): boolean {
  if (command.playingMediaIDs) {
    return target.mediaID !== undefined && command.playingMediaIDs.includes(target.mediaID);
  }
  return command.mediaID === undefined || command.mediaID === target.mediaID;
}

// A seek that waits for its player's metadata. A newer seek or a play-media
// supersedes it, so it is kept to be cancelled.
let deferredSeek: { video: HTMLVideoElement; listener: () => void } | null = null;

function cancelDeferredSeek(): void {
  deferredSeek?.video.removeEventListener("loadedmetadata", deferredSeek.listener);
  deferredSeek = null;
}

/**
 * Applies a transport command to a player. Returns false when the command is
 * not a player command or the player cannot honour it (no next item, say).
 * A command was authorized against the Media the target was playing then (a
 * session can hold several, a prefetched next item say), so it is refused when
 * this player has moved on to one outside that set. A remote trackID is
 * the receiver's zero-based track index; an empty subtitle trackID turns
 * subtitles off.
 */
export function applyPlayerCommand(
  command: SessionControlCommand,
  target: RemotePlayerTarget,
): boolean {
  if (!anchoredTo(command, target)) return false;
  const video = target.video();
  switch (command.command) {
    case "pause":
      if (!video) return false;
      video.pause();
      return true;
    case "resume":
      if (!video) return false;
      void video.play()?.catch(() => {});
      return true;
    case "stop":
      if (target.stop) target.stop();
      else video?.pause();
      return Boolean(target.stop || video);
    case "skip-media":
    case "skip-track": {
      const step = command.direction === "previous" ? target.previous : target.next;
      step?.();
      return Boolean(step);
    }
    case "seek": {
      if (!video || command.positionMs === undefined) return false;
      const at = command.positionMs / 1000;
      // Before metadata the player's own opening seek is still to come; seek
      // after it so the newer position wins.
      cancelDeferredSeek();
      if (video.readyState >= HTMLMediaElement.HAVE_METADATA) {
        video.currentTime = at;
        return true;
      }
      const listener = () => {
        deferredSeek = null;
        video.currentTime = at;
      };
      deferredSeek = { video, listener };
      video.addEventListener("loadedmetadata", listener, { once: true });
      return true;
    }
    case "set-volume":
      if (!video || command.volume === undefined) return false;
      video.volume = Math.max(0, Math.min(100, command.volume)) / 100;
      if (command.volume > 0) video.muted = false;
      return true;
    case "mute":
    case "unmute":
      if (!video) return false;
      video.muted = command.command === "mute";
      return true;
    case "select-audio-track":
      if (!target.selectAudio || !isTrackIndex(command.trackID)) return false;
      target.selectAudio(command.trackID);
      return true;
    case "select-subtitle-track":
      if (!target.selectSubtitle) return false;
      if (!command.trackID) {
        target.selectSubtitle("off");
        return true;
      }
      if (!isTrackIndex(command.trackID)) return false;
      target.selectSubtitle(command.trackID);
      return true;
    default:
      return false;
  }
}

/**
 * Applies play-media in place when the player is already on that Media: the
 * player sets itself up afresh at the requested position (saved progress when
 * none), as opening it would. False when the receiver must navigate instead.
 */
export function restartInPlace(
  command: SessionControlCommand,
  target: RemotePlayerTarget,
): boolean {
  if (!command.mediaID || command.mediaID !== target.mediaID || !target.restart) return false;
  target.restart(command.positionMs === undefined ? undefined : command.positionMs / 1000);
  return true;
}

// The events this tab applied. A remounted viewer subscribes without a cursor
// and is sent recent events again; without this, leaving and returning to the
// viewer would replay a play-media sent a moment ago. It is kept in
// sessionStorage too, so a reload in the same tab does not replay it.
//
// Two records together decide. The recent applied events themselves (id and
// issue time) catch an exact replay, whatever order ids and issue times came
// in: concurrent commands take their issue time before they are written, so a
// higher id can carry an earlier time. A watermark, the id of the last event
// applied and the latest issue time applied, drops an older event this tab
// never saw. An event is fresh unless it is in the recent set or both its id
// and its issue time are at or below the watermark. A server restored from an
// older backup (lower ids, later times) is still obeyed, and because the
// watermark then takes the restored id, so are its later events.
const APPLIED_KEY = "bf.remote.appliedEvent";
const APPLIED_RECENT_LIMIT = 64;
interface AppliedEvents {
  id: number;
  at: number;
  recent: string[];
}
let applied: AppliedEvents | null | undefined;

function readApplied(): AppliedEvents | null {
  if (applied === undefined) {
    applied = null;
    try {
      const stored = JSON.parse(window.sessionStorage.getItem(APPLIED_KEY) ?? "null") as unknown;
      if (stored !== null && typeof stored === "object") {
        const { id, at, recent } = stored as Partial<AppliedEvents>;
        if (Number.isSafeInteger(id) && Number.isFinite(at) && Array.isArray(recent)) {
          applied = {
            id: id as number,
            at: at as number,
            recent: recent.filter((key): key is string => typeof key === "string"),
          };
        }
      }
    } catch {
      // Storage is unavailable or holds junk; the in-memory record still applies.
    }
  }
  return applied;
}

/**
 * Records an SSE event as applied. False when it was already applied, so the
 * caller drops the replay. A frame without a numeric id cannot be tracked and
 * is accepted (the staleness window still bounds it).
 */
export function claimControlEvent(eventID: string, occurredAt: number): boolean {
  const id = Number(eventID);
  if (!eventID || !Number.isSafeInteger(id) || id <= 0) return true;
  const key = `${id}@${occurredAt}`;
  const last = readApplied();
  if (last && (last.recent.includes(key) || (id <= last.id && occurredAt <= last.at))) {
    return false;
  }
  applied = {
    id,
    at: Math.max(occurredAt, last?.at ?? occurredAt),
    recent: [...(last?.recent ?? []), key].slice(-APPLIED_RECENT_LIMIT),
  };
  try {
    window.sessionStorage.setItem(APPLIED_KEY, JSON.stringify(applied));
  } catch {
    // Storage is unavailable; the in-memory record still applies.
  }
  return true;
}

/** Test seam: forgets the applied events and held commands. */
export function resetAppliedControlEvents(): void {
  applied = undefined;
  pendingCommands = [];
  lastPlayState = null;
  cancelDeferredSeek();
  try {
    window.sessionStorage.removeItem(APPLIED_KEY);
  } catch {
    // Storage is unavailable.
  }
}

// Transport commands that arrived while no player was registered: one is
// still setting up (after a play-media, the player unmounts its transport
// until the new playback is ready). The next player to register applies them
// in order, each if it is still recent and anchored to that player's Media.
const PENDING_COMMAND_MS = 15_000;
const PENDING_COMMAND_LIMIT = 16;
let pendingCommands: Array<{ command: SessionControlCommand; at: number }> = [];

// The last pause, resume or stop this session was sent, numbered in arrival
// order. A player about to start itself asks whether one arrived after its
// mark, so a pause sent while it loads holds.
let playStateSeq = 0;
let lastPlayState: { command: SessionControlCommand; seq: number } | null = null;

/**
 * Drives the registered player with a transport command, or keeps the command
 * for the next player to register when none is.
 */
export function deliverPlayerCommand(command: SessionControlCommand): void {
  const at = Date.now();
  if (command.command === "pause" || command.command === "resume" || command.command === "stop") {
    playStateSeq += 1;
    lastPlayState = { command, seq: playStateSeq };
  }
  const player = remotePlayerTarget();
  if (player) {
    applyPlayerCommand(command, player);
    return;
  }
  pendingCommands = [...pendingCommands, { command, at }].slice(-PENDING_COMMAND_LIMIT);
}

/** Drops held commands: a play-media replaces whatever was sent before it. */
export function forgetPendingPlayerCommands(): void {
  pendingCommands = [];
  cancelDeferredSeek();
}

/** Marks the pause, resume and stop commands received so far. */
export function remotePlayStateMark(): number {
  return playStateSeq;
}

/**
 * Whether a player for `mediaID` must not start itself: the last pause, resume
 * or stop received after `mark` was a pause or stop authorized for that Media.
 */
export function remoteHoldsAutoplay(mediaID: string, mark: number): boolean {
  const last = lastPlayState;
  if (!last || last.seq <= mark) return false;
  if (last.command.command === "resume") return false;
  return anchoredTo(last.command, { video: () => null, mediaID });
}

function applyPendingCommands(target: RemotePlayerTarget): void {
  const held = pendingCommands;
  pendingCommands = [];
  const now = Date.now();
  for (const { command, at } of held) {
    if (now - at <= PENDING_COMMAND_MS) applyPlayerCommand(command, target);
  }
}

// One player runs per tab; the last one mounted is the one commands reach.
let active: { current: RemotePlayerTarget } | null = null;

/** The player remote commands currently drive, if one is running. */
export function remotePlayerTarget(): RemotePlayerTarget | null {
  return active?.current ?? null;
}

/** Registers the mounted player as the target of remote commands. */
export function useRemotePlayerTarget(target: RemotePlayerTarget): void {
  const latest = useRef(target);
  latest.current = target;
  useEffect(() => {
    active = latest;
    applyPendingCommands(latest.current);
    return () => {
      if (active === latest) {
        active = null;
        cancelDeferredSeek();
      }
    };
  }, []);
}
