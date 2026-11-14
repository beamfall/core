import { describe, expect, it, vi } from "vitest";
import {
  applyPlayerCommand,
  parseSessionControlFrame,
  type RemotePlayerTarget,
  STALE_AFTER_MS,
} from "./remoteControl";

const NOW = Date.parse("2026-11-14T12:00:00Z");

function frame(payload: Record<string, unknown>, overrides: Record<string, unknown> = {}) {
  return JSON.stringify({
    kind: "session.control",
    aggregateType: "session",
    aggregateID: "session-b",
    occurredAt: new Date(NOW - 1000).toISOString(),
    payload,
    ...overrides,
  });
}

function fakeVideo() {
  return {
    pause: vi.fn(),
    play: vi.fn(() => Promise.resolve()),
    currentTime: 0,
    volume: 1,
    muted: false,
  } as unknown as HTMLVideoElement & { pause: ReturnType<typeof vi.fn> };
}

describe("parseSessionControlFrame", () => {
  it("decodes the target and the command's arguments", () => {
    expect(
      parseSessionControlFrame(frame({ command: "seek", positionMs: 90_000, sessionID: "x" }), NOW),
    ).toEqual({
      target: "session-b",
      command: {
        command: "seek",
        direction: undefined,
        positionMs: 90_000,
        volume: undefined,
        trackID: undefined,
        mediaID: undefined,
        text: undefined,
      },
    });
  });

  it("keeps only a known direction", () => {
    const parsed = (direction: unknown) =>
      parseSessionControlFrame(frame({ command: "skip-track", direction }), NOW)?.command.direction;
    expect(parsed("previous")).toBe("previous");
    expect(parsed("sideways")).toBeUndefined();
  });

  it("drops a command replayed after the staleness window", () => {
    const old = new Date(NOW - STALE_AFTER_MS - 1).toISOString();
    expect(
      parseSessionControlFrame(frame({ command: "pause" }, { occurredAt: old }), NOW),
    ).toBeNull();
  });

  it("rejects malformed frames", () => {
    expect(parseSessionControlFrame("not json", NOW)).toBeNull();
    expect(parseSessionControlFrame(undefined, NOW)).toBeNull();
    expect(parseSessionControlFrame(frame({ command: "" }), NOW)).toBeNull();
    expect(
      parseSessionControlFrame(frame({ command: "pause" }, { kind: "client.invalidate" }), NOW),
    ).toBeNull();
    expect(
      parseSessionControlFrame(frame({ command: "pause" }, { occurredAt: "soon" }), NOW),
    ).toBeNull();
  });
});

describe("applyPlayerCommand", () => {
  function target(overrides: Partial<RemotePlayerTarget> = {}) {
    const video = fakeVideo();
    return { video, target: { video: () => video, ...overrides } as RemotePlayerTarget };
  }

  it("drives the video element for transport verbs", () => {
    const { video, target: t } = target();
    expect(applyPlayerCommand({ command: "pause" }, t)).toBe(true);
    expect(video.pause).toHaveBeenCalled();
    expect(applyPlayerCommand({ command: "resume" }, t)).toBe(true);
    expect(video.play).toHaveBeenCalled();
    expect(applyPlayerCommand({ command: "seek", positionMs: 90_500 }, t)).toBe(true);
    expect(video.currentTime).toBe(90.5);
    video.muted = true;
    expect(applyPlayerCommand({ command: "set-volume", volume: 40 }, t)).toBe(true);
    expect(video.volume).toBeCloseTo(0.4);
    expect(video.muted).toBe(false);
    expect(applyPlayerCommand({ command: "mute" }, t)).toBe(true);
    expect(video.muted).toBe(true);
    expect(applyPlayerCommand({ command: "unmute" }, t)).toBe(true);
    expect(video.muted).toBe(false);
  });

  it("steps the queue by direction and reports a missing step", () => {
    const next = vi.fn();
    const previous = vi.fn();
    const { target: t } = target({ next, previous });
    expect(applyPlayerCommand({ command: "skip-media", direction: "previous" }, t)).toBe(true);
    expect(previous).toHaveBeenCalledOnce();
    expect(applyPlayerCommand({ command: "skip-track" }, t)).toBe(true);
    expect(next).toHaveBeenCalledOnce();
    expect(applyPlayerCommand({ command: "skip-media" }, target().target)).toBe(false);
  });

  it("stops through the player's exit, or pauses without one", () => {
    const stop = vi.fn();
    expect(applyPlayerCommand({ command: "stop" }, target({ stop }).target)).toBe(true);
    expect(stop).toHaveBeenCalledOnce();
    const { video, target: t } = target();
    expect(applyPlayerCommand({ command: "stop" }, t)).toBe(true);
    expect(video.pause).toHaveBeenCalled();
  });

  it("selects tracks by index and turns subtitles off for an empty id", () => {
    const selectAudio = vi.fn();
    const selectSubtitle = vi.fn();
    const { target: t } = target({ selectAudio, selectSubtitle });
    expect(applyPlayerCommand({ command: "select-audio-track", trackID: "1" }, t)).toBe(true);
    expect(selectAudio).toHaveBeenCalledWith("1");
    expect(applyPlayerCommand({ command: "select-subtitle-track", trackID: "" }, t)).toBe(true);
    expect(selectSubtitle).toHaveBeenCalledWith("off");
    expect(applyPlayerCommand({ command: "select-subtitle-track", trackID: "2" }, t)).toBe(true);
    expect(selectSubtitle).toHaveBeenCalledWith("2");
    expect(applyPlayerCommand({ command: "select-audio-track", trackID: "eng" }, t)).toBe(false);
    expect(applyPlayerCommand({ command: "select-subtitle-track", trackID: "x" }, t)).toBe(false);
  });

  it("ignores verbs that are not player commands", () => {
    const { target: t } = target();
    expect(applyPlayerCommand({ command: "display-message", text: "hi" }, t)).toBe(false);
    expect(applyPlayerCommand({ command: "play-media", mediaID: "m" }, t)).toBe(false);
  });

  it("does nothing without a video element", () => {
    expect(applyPlayerCommand({ command: "pause" }, { video: () => null })).toBe(false);
  });
});
