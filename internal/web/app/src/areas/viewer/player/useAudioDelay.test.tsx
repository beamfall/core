import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PlaybackStream } from "../lib/playback";
import { writeSync } from "./playbackSync";
import { audioDelayRoutable, playsNativeHls, useAudioDelay } from "./useAudioDelay";

const setValueAtTime = vi.fn();
const createMediaElementSource = vi.fn(() => ({ connect: vi.fn() }));

class FakeAudioContext {
  state = "running";
  currentTime = 0;
  destination = {};
  resume = vi.fn(async () => {});
  createMediaElementSource = createMediaElementSource;
  createDelay = vi.fn(() => ({ connect: vi.fn(), delayTime: { setValueAtTime } }));
}

beforeEach(() => {
  vi.stubGlobal("AudioContext", FakeAudioContext);
  setValueAtTime.mockClear();
  createMediaElementSource.mockClear();
});

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.clear();
});

function video(src: string) {
  const element = document.createElement("video");
  element.src = src;
  return element;
}

const direct: PlaybackStream = { kind: "direct-play", url: "/api/media/m1/stream" };
const hls: PlaybackStream = { kind: "hls", url: "/api/media/m1/master.m3u8" };

describe("audioDelayRoutable", () => {
  it("accepts same-origin and MSE sources only", () => {
    expect(audioDelayRoutable(video("/api/media/m1/stream"))).toBe(true);
    expect(audioDelayRoutable(video("blob:http://localhost/abc"))).toBe(true);
    expect(audioDelayRoutable(video("https://cdn.example.invalid/x.mp4"))).toBe(false);
    expect(audioDelayRoutable(video("/api/media/m1/stream"), true)).toBe(false);
  });

  it("refuses without Web Audio", () => {
    vi.stubGlobal("AudioContext", undefined);
    expect(audioDelayRoutable(video("/api/media/m1/stream"))).toBe(false);
  });
});

describe("playsNativeHls", () => {
  it("is true when an HLS stream would play through the browser's engine", () => {
    const element = video("/api/media/m1/stream");
    element.canPlayType = () => "maybe";
    expect(playsNativeHls(element, [direct, hls])).toBe(true);
    expect(playsNativeHls(element, [direct])).toBe(false);
  });
});

function render(element: HTMLVideoElement, mediaID = "m1", streams: PlaybackStream[] = [direct]) {
  return renderHook(() =>
    useAudioDelay({
      videoRef: { current: element },
      profileID: "p1",
      mediaID,
      stream: direct,
      streams,
    }),
  );
}

describe("useAudioDelay", () => {
  it("routes through a delay line only once a delay is wanted", () => {
    const { result } = render(video("/api/media/m1/stream"));
    expect(result.current.audioDelaySupported).toBe(true);
    expect(createMediaElementSource).not.toHaveBeenCalled();
    act(() => result.current.nudgeAudioDelay(0.1));
    expect(createMediaElementSource).toHaveBeenCalledTimes(1);
    expect(setValueAtTime).toHaveBeenLastCalledWith(0.1, 0);
    act(() => result.current.nudgeAudioDelay(0.5));
    expect(createMediaElementSource).toHaveBeenCalledTimes(1);
    expect(setValueAtTime).toHaveBeenLastCalledWith(0.6, 0);
    expect(result.current.audioDelay).toBe(0.6);
  });

  it("floors at zero and clamps at the limit, and persists", () => {
    const { result } = render(video("/api/media/m1/stream"), "m2");
    act(() => result.current.nudgeAudioDelay(-0.5));
    expect(result.current.audioDelay).toBe(0);
    for (let step = 0; step < 12; step++) act(() => result.current.nudgeAudioDelay(0.5));
    expect(result.current.audioDelay).toBe(5);
    expect(JSON.parse(window.localStorage.getItem("bf.playbackSync.p1.m2") ?? "{}")).toMatchObject({
      audioDelay: 5,
    });
    act(() => result.current.resetAudioDelay());
    expect(result.current.audioDelay).toBe(0);
    expect(window.localStorage.getItem("bf.playbackSync.p1.m2")).toBeNull();
  });

  it("shows a stored negative value from another platform as zero", () => {
    writeSync("p1", "m3", { audioDelay: -1 });
    const { result } = render(video("/api/media/m1/stream"), "m3");
    expect(result.current.audioDelay).toBe(0);
  });

  it("is unsupported where the item could play native HLS", () => {
    const element = video("/api/media/m1/stream");
    element.canPlayType = () => "maybe";
    const { result } = render(element, "m1", [direct, hls]);
    expect(result.current.audioDelaySupported).toBe(false);
    act(() => result.current.nudgeAudioDelay(0.5));
    expect(createMediaElementSource).not.toHaveBeenCalled();
  });
});
