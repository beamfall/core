import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PlaybackStream } from "../lib/playback";
import { writeSync } from "./playbackSync";
import { audioDelayRoutable, browserPlaysNativeHls, useAudioDelay } from "./useAudioDelay";

const setValueAtTime = vi.fn();
const createMediaElementSource = vi.fn(() => ({ connect: vi.fn(), disconnect: vi.fn() }));

class FakeAudioContext {
  state = "running";
  currentTime = 0;
  destination = {};
  resume = vi.fn(async () => {});
  suspend = vi.fn(async () => {
    this.state = "suspended";
  });
  createMediaElementSource = createMediaElementSource;
  createDelay = vi.fn(() => ({
    connect: vi.fn(),
    disconnect: vi.fn(),
    delayTime: { value: 0, setValueAtTime },
  }));
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

// A playing element: the hook leaves the graph alone while the element is paused.
function video(src: string, paused = false) {
  const element = document.createElement("video");
  element.src = src;
  Object.defineProperty(element, "paused", { configurable: true, get: () => paused });
  return element;
}

const direct: PlaybackStream = { kind: "direct-play", url: "/api/media/m1/stream" };

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

describe("browserPlaysNativeHls", () => {
  it("follows the browser's HLS engine, not the item", () => {
    const element = video("/api/media/m1/stream");
    expect(browserPlaysNativeHls(element)).toBe(false);
    element.canPlayType = () => "maybe";
    expect(browserPlaysNativeHls(element)).toBe(true);
  });
});

function render(element: HTMLVideoElement, mediaID = "m1", stream: PlaybackStream = direct) {
  return renderHook(() =>
    useAudioDelay({
      videoRef: { current: element },
      profileID: "p1",
      mediaID,
      stream,
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

  it("is unsupported on a browser that plays HLS natively, even for direct play", () => {
    const element = video("/api/media/m1/stream");
    element.canPlayType = () => "maybe";
    const { result } = render(element, "m1", direct);
    expect(result.current.audioDelaySupported).toBe(false);
    act(() => result.current.nudgeAudioDelay(0.5));
    expect(createMediaElementSource).not.toHaveBeenCalled();
  });

  it("resumes an existing graph after an interruption even at zero delay", () => {
    const element = video("/api/media/m1/stream");
    const { result } = render(element, "m4");
    act(() => result.current.nudgeAudioDelay(0.5));
    act(() => result.current.resetAudioDelay());
    const context = createMediaElementSource.mock.contexts[0] as FakeAudioContext;
    context.state = "suspended";
    context.resume.mockClear();
    act(() => {
      element.dispatchEvent(new Event("play"));
    });
    expect(context.resume).toHaveBeenCalled();
  });

  it("holds the delay line while paused and flushes it on seek", () => {
    let paused = false;
    const element = video("/api/media/m1/stream");
    Object.defineProperty(element, "paused", { configurable: true, get: () => paused });
    const { result } = render(element, "m5");
    act(() => result.current.nudgeAudioDelay(0.5));
    const context = createMediaElementSource.mock.contexts[0] as FakeAudioContext;
    paused = true;
    act(() => {
      element.dispatchEvent(new Event("pause"));
    });
    expect(context.suspend).toHaveBeenCalled();
    context.resume.mockClear();
    act(() => {
      document.dispatchEvent(new Event("pointerdown"));
    });
    expect(context.resume).not.toHaveBeenCalled();
    const delays = context.createDelay.mock.calls.length;
    act(() => {
      element.dispatchEvent(new Event("seeking"));
    });
    expect(context.createDelay.mock.calls.length).toBe(delays + 1);
  });

  it("settles suspended when a pause lands while a resume is in flight", async () => {
    let paused = false;
    const element = video("/api/media/m1/stream");
    Object.defineProperty(element, "paused", { configurable: true, get: () => paused });
    const { result } = render(element, "m6");
    act(() => result.current.nudgeAudioDelay(0.5));
    const context = createMediaElementSource.mock.contexts[0] as FakeAudioContext;
    context.state = "suspended";
    let finishResume = () => {};
    context.resume.mockImplementation(
      () =>
        new Promise<void>((resolve) => {
          finishResume = () => {
            context.state = "running";
            resolve();
          };
        }),
    );
    act(() => {
      element.dispatchEvent(new Event("play"));
    });
    paused = true;
    act(() => {
      element.dispatchEvent(new Event("pause"));
    });
    context.suspend.mockClear();
    await act(async () => {
      finishResume();
    });
    expect(context.suspend).toHaveBeenCalled();
  });

  it("lets the delayed tail play out when the element ends", () => {
    const element = video("/api/media/m1/stream");
    const { result } = render(element, "m7");
    act(() => result.current.nudgeAudioDelay(0.5));
    const context = createMediaElementSource.mock.contexts[0] as FakeAudioContext;
    Object.defineProperty(element, "paused", { configurable: true, get: () => true });
    Object.defineProperty(element, "ended", { configurable: true, get: () => true });
    act(() => {
      element.dispatchEvent(new Event("pause"));
    });
    expect(context.suspend).not.toHaveBeenCalled();
  });
});
