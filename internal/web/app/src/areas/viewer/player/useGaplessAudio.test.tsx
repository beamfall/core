import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PlaybackGapless, PlaybackNext, PlaybackStream } from "../lib/playback";
import { declaredPadding, decodableStream, trimWindow, useGaplessAudio } from "./useGaplessAudio";

const RATE = 44100;
const gapless: PlaybackGapless = {
  sampleRate: RATE,
  encoderDelaySamples: 2112,
  encoderPaddingSamples: 1500,
};

// A mono decoded track: `lead` and `tail` samples at `edge`, music (0.5) between.
function buffer(seconds: number, lead = 0, tail = 0, edge = 0) {
  const samples = new Float32Array(Math.round(seconds * RATE)).fill(0.5);
  samples.fill(edge, 0, lead);
  samples.fill(edge, samples.length - tail);
  return {
    duration: samples.length / RATE,
    sampleRate: RATE,
    length: samples.length,
    numberOfChannels: 1,
    getChannelData: () => samples,
  } as unknown as AudioBuffer;
}

describe("trimWindow", () => {
  it("skips the declared delay and padding where they are silent", () => {
    const trim = trimWindow(buffer(1, 2112, 1500), gapless);
    expect(trim.offset).toBeCloseTo(2112 / RATE, 6);
    expect(trim.duration).toBeCloseTo((RATE - 2112 - 1500) / RATE, 6);
  });

  it("keeps edges the decoder already trimmed, where music sits", () => {
    expect(trimWindow(buffer(1), gapless)).toEqual({ offset: 0, duration: 1 });
    const tailOnly = trimWindow(buffer(1, 0, 1500), gapless);
    expect(tailOnly.offset).toBe(0);
    expect(tailOnly.duration).toBeCloseTo((RATE - 1500) / RATE, 6);
  });

  it("plays the whole track without gapless metadata", () => {
    expect(trimWindow(buffer(1, 2112, 1500))).toEqual({ offset: 0, duration: 1 });
  });
});

describe("declaredPadding", () => {
  it("converts the declared padding to seconds and ignores nonsense", () => {
    expect(declaredPadding(gapless)).toBeCloseTo(1500 / RATE, 9);
    expect(declaredPadding()).toBe(0);
    expect(declaredPadding({ ...gapless, sampleRate: 0 })).toBe(0);
    expect(declaredPadding({ ...gapless, encoderPaddingSamples: -1 })).toBe(0);
  });
});

describe("decodableStream", () => {
  it("picks a same-origin direct-play stream only", () => {
    const next = (streams: PlaybackStream[]): PlaybackNext =>
      ({ mediaID: "m2", streams }) as PlaybackNext;
    const direct: PlaybackStream = { kind: "direct-play", url: "/api/media/m2/stream" };
    expect(decodableStream(next([{ kind: "hls", url: "/x.m3u8" }, direct]))).toBe(direct);
    expect(decodableStream(next([{ kind: "hls", url: "/x.m3u8" }]))).toBeUndefined();
    expect(
      decodableStream(next([{ kind: "direct-play", url: "https://cdn.example.invalid/m2" }])),
    ).toBeUndefined();
    expect(decodableStream()).toBeUndefined();
  });
});

// A Web Audio fake that records what the hook schedules.
function param(value: number) {
  return { value, setValueAtTime: vi.fn(), cancelScheduledValues: vi.fn() };
}

const sources: { start: ReturnType<typeof vi.fn>; stop: ReturnType<typeof vi.fn> }[] = [];
const gains: ReturnType<typeof param>[] = [];

class FakeAudioContext {
  state = "running";
  currentTime = 5;
  destination = {};
  resume = vi.fn(async () => {});
  createMediaElementSource = vi.fn(() => ({ connect: vi.fn(), disconnect: vi.fn() }));
  createGain = vi.fn(() => {
    const gain = param(1);
    gains.push(gain);
    return { connect: vi.fn(), disconnect: vi.fn(), gain };
  });
  createBufferSource = vi.fn(() => {
    const source = {
      buffer: null,
      connect: vi.fn(),
      disconnect: vi.fn(),
      start: vi.fn(),
      stop: vi.fn(),
      onended: null,
    };
    sources.push(source);
    return source;
  });
  decodeAudioData = vi.fn(async () => buffer(2));
}

beforeEach(() => {
  sources.length = 0;
  gains.length = 0;
  vi.stubGlobal("AudioContext", FakeAudioContext);
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({ ok: true, arrayBuffer: async () => new ArrayBuffer(8) })),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

// A playing element 0.8 s from the end of a 10 s track.
function playing() {
  const element = document.createElement("video");
  element.src = "/api/media/m1/stream";
  Object.defineProperty(element, "paused", { configurable: true, get: () => false });
  Object.defineProperty(element, "duration", { configurable: true, get: () => 10 });
  let time = 9.2;
  Object.defineProperty(element, "currentTime", {
    configurable: true,
    get: () => time,
    set: (value: number) => {
      time = value;
    },
  });
  return element;
}

function nextItem(mediaID: string): PlaybackNext {
  return {
    mediaID,
    streams: [{ kind: "direct-play", url: `/api/media/${mediaID}/stream` }],
  } as PlaybackNext;
}

function render(element: HTMLVideoElement) {
  // Stable, as the player's own ref is.
  const videoRef = { current: element };
  return renderHook(
    ({ mediaID, next }: { mediaID: string; next: PlaybackNext }) =>
      useGaplessAudio({
        videoRef,
        mediaID,
        stream: { kind: "direct-play", url: `/api/media/${mediaID}/stream` },
        next,
        advanceID: next.mediaID,
        enabled: true,
      }),
    { initialProps: { mediaID: "m1", next: nextItem("m2") } },
  );
}

describe("useGaplessAudio", () => {
  it("starts the next track where the element ends and silences the element there", async () => {
    render(playing());
    await vi.waitFor(() => expect(sources).toHaveLength(1));
    // Context time 5 plus the 0.8 s the element has left; the whole 2 s track.
    const [at, offset, duration] = sources[0].start.mock.calls[0];
    expect(at).toBeCloseTo(5.8, 9);
    expect([offset, duration]).toEqual([0, 2]);
    const elementGain = gains[0];
    expect(elementGain.setValueAtTime).toHaveBeenCalledWith(0, at);
  });

  it("chains the track after a decoded one on the audio clock", async () => {
    const hook = render(playing());
    await vi.waitFor(() => expect(sources).toHaveLength(1));
    hook.rerender({ mediaID: "m2", next: nextItem("m3") });
    await vi.waitFor(() => expect(sources).toHaveLength(2));
    const [at] = sources[0].start.mock.calls[0];
    expect(sources[1].start).toHaveBeenCalledWith(at + 2, 0, 2);
    expect(sources[0].stop).not.toHaveBeenCalled();
  });

  it("hands the sound back to the element when the listener pauses", async () => {
    const element = playing();
    render(element);
    await vi.waitFor(() => expect(sources).toHaveLength(1));
    act(() => {
      element.dispatchEvent(new Event("pause"));
    });
    expect(sources[0].stop).toHaveBeenCalled();
    const elementGain = gains[0];
    expect(elementGain.cancelScheduledValues).toHaveBeenCalled();
    expect(elementGain.setValueAtTime).toHaveBeenLastCalledWith(1, 5);
  });

  it("hands the sound back when another item replaces the joined one", async () => {
    const hook = render(playing());
    await vi.waitFor(() => expect(sources).toHaveLength(1));
    hook.rerender({ mediaID: "m9", next: nextItem("m10") });
    expect(sources[0].stop).toHaveBeenCalled();
  });
});
