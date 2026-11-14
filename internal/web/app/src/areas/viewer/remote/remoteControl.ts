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
  text?: string;
}

export interface SessionControlFrame {
  /** The target session id (the envelope's aggregateID). */
  target: string;
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
    command: {
      command: payload.command,
      direction,
      positionMs: optionalNumber(payload.positionMs),
      volume: optionalNumber(payload.volume),
      trackID: optionalString(payload.trackID),
      mediaID: optionalString(payload.mediaID),
      text: optionalString(payload.text),
    },
  };
}

/** What a running player exposes so remote commands can drive it. */
export interface RemotePlayerTarget {
  video: () => HTMLVideoElement | null;
  /** Audio menu option id: the zero-based track index. */
  selectAudio?: (id: string) => void;
  /** Subtitle menu option id: "off" or the zero-based track index. */
  selectSubtitle?: (id: string) => void;
  next?: () => void;
  previous?: () => void;
  stop?: () => void;
}

// Another client's track ids are not this player's; only a plain index applies.
function isTrackIndex(id: string | undefined): id is string {
  return id !== undefined && /^\d{1,3}$/.test(id);
}

/**
 * Applies a transport command to a player. Returns false when the command is
 * not a player command or the player cannot honour it (no next item, say).
 * A remote trackID is the receiver's zero-based track index; an empty
 * subtitle trackID turns subtitles off.
 */
export function applyPlayerCommand(
  command: SessionControlCommand,
  target: RemotePlayerTarget,
): boolean {
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
    case "seek":
      if (!video || command.positionMs === undefined) return false;
      video.currentTime = command.positionMs / 1000;
      return true;
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
    return () => {
      if (active === latest) active = null;
    };
  }, []);
}
