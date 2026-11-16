import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PlaybackGapless, PlaybackNext, PlaybackStream } from "../lib/playback";
import { listenerMuted, setListenerMuted } from "./listenerMute";
import {
  declaredPadding,
  decodableStream,
  MAX_DECODED_SECONDS,
  trimWindow,
  useGaplessAudio,
} from "./useGaplessAudio";

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

interface FakeSource {
  buffer: AudioBuffer | null;
  disconnect: ReturnType<typeof vi.fn>;
  start: ReturnType<typeof vi.fn>;
  stop: ReturnType<typeof vi.fn>;
  onended: (() => void) | null;
}

const sources: FakeSource[] = [];
/** What the next track's metadata declares, and what it decodes to (seconds). */
let probedSeconds = 2;
let decodedSeconds = 2;
const gains: ReturnType<typeof param>[] = [];
const constants: {
  offset: ReturnType<typeof param>;
  connect: ReturnType<typeof vi.fn>;
  start: ReturnType<typeof vi.fn>;
}[] = [];

let contextState: AudioContextState = "running";
const contexts: FakeAudioContext[] = [];

// No createMediaElementSource: the join must never route the element.
class FakeAudioContext {
  constructor() {
    contexts.push(this);
  }
  state = contextState;
  currentTime = 5;
  destination = {};
  resume = vi.fn(async () => {});
  close = vi.fn(async () => {});
  createGain = vi.fn(() => {
    const gain = param(1);
    gains.push(gain);
    return { connect: vi.fn(), disconnect: vi.fn(), gain };
  });
  createConstantSource = vi.fn(() => {
    const source = { offset: param(1), connect: vi.fn(), start: vi.fn() };
    constants.push(source);
    return source;
  });
  createBufferSource = vi.fn(() => {
    const source: FakeSource & { connect: ReturnType<typeof vi.fn> } = {
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
  // A long track is only its duration: the cap must drop it unread.
  decodeAudioData = vi.fn(async () =>
    decodedSeconds > 60 ? ({ duration: decodedSeconds } as AudioBuffer) : buffer(decodedSeconds),
  );
}

const createElement = document.createElement.bind(document);

beforeEach(() => {
  sources.length = 0;
  gains.length = 0;
  constants.length = 0;
  contexts.length = 0;
  contextState = "running";
  probedSeconds = 2;
  decodedSeconds = 2;
  // The duration probe: a detached audio element that reports its metadata at once.
  vi.spyOn(document, "createElement").mockImplementation(((tag: string) => {
    const element = createElement(tag);
    if (!(element instanceof HTMLAudioElement)) return element;
    element.load = vi.fn();
    Object.defineProperty(element, "duration", { get: () => probedSeconds });
    Object.defineProperty(element, "src", {
      get: () => "",
      set: () => queueMicrotask(() => element.dispatchEvent(new Event("loadedmetadata"))),
    });
    return element;
  }) as typeof document.createElement);
  vi.stubGlobal("AudioContext", FakeAudioContext);
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({ ok: true, arrayBuffer: async () => new ArrayBuffer(8) })),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

// Lets the probe, fetch and decode promises settle.
async function settle() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 10));
  });
}

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
  it("starts the next track where the element ends and mutes the element from its end", async () => {
    const element = playing();
    render(element);
    await vi.waitFor(() => expect(sources).toHaveLength(1));
    // Context time 5 plus the 0.8 s the element has left; the whole 2 s track.
    const [at, offset, duration] = sources[0].start.mock.calls[0];
    expect(at).toBeCloseTo(5.8, 9);
    expect([offset, duration]).toEqual([0, 2]);
    // The element plays out its own track, then shadows the decoded one unheard.
    expect(element.muted).toBe(false);
    act(() => {
      element.dispatchEvent(new Event("ended"));
    });
    expect(element.muted).toBe(true);
    expect(listenerMuted(element)).toBe(false);
  });

  it("keeps the context's output awake with a silent source", async () => {
    render(playing());
    await vi.waitFor(() => expect(sources).toHaveLength(1));
    expect(constants).toHaveLength(1);
    expect(constants[0].offset.value).toBe(0);
    expect(constants[0].start).toHaveBeenCalledTimes(1);
  });

  it("waits for a running context before it joins", async () => {
    contextState = "suspended";
    render(playing());
    await settle();
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(sources).toHaveLength(0);
  });

  it("applies the listener's mute to the decoded track while the element is held muted", async () => {
    const element = playing();
    render(element);
    await vi.waitFor(() => expect(sources).toHaveLength(1));
    act(() => {
      element.dispatchEvent(new Event("ended"));
    });
    const output = gains[0];
    act(() => setListenerMuted(element, true));
    expect(listenerMuted(element)).toBe(true);
    expect(output.value).toBe(0);
    act(() => setListenerMuted(element, false));
    expect(output.value).toBe(1);
    expect(element.muted).toBe(true);
    expect(sources[0].stop).not.toHaveBeenCalled();
  });

  it("hands the sound back when something else unmutes the held element", async () => {
    const element = playing();
    render(element);
    await vi.waitFor(() => expect(sources).toHaveLength(1));
    act(() => {
      element.dispatchEvent(new Event("ended"));
    });
    act(() => {
      element.muted = false;
      element.dispatchEvent(new Event("volumechange"));
    });
    expect(sources[0].stop).toHaveBeenCalled();
    expect(element.muted).toBe(false);
  });

  it("moves the silent element to what is heard at most once a second", async () => {
    const element = playing();
    const hook = render(element);
    await vi.waitFor(() => expect(sources).toHaveLength(1));
    const context = contexts[0];
    // The join at 5.8: the element loads m2 and starts it from 0.
    act(() => {
      element.dispatchEvent(new Event("ended"));
      element.currentTime = 0;
      element.dispatchEvent(new Event("loadstart"));
    });
    hook.rerender({ mediaID: "m2", next: nextItem("m3") });
    context.currentTime = 6.5;
    act(() => {
      element.dispatchEvent(new Event("playing"));
    });
    expect(element.currentTime).toBeCloseTo(0.7, 9);
    // An engine that reports the old position, and the seek more than once,
    // is neither sought again nor taken for the listener.
    const tick = (time: number, reported: number, ...events: string[]) =>
      act(() => {
        context.currentTime = time;
        element.currentTime = reported;
        for (const type of events) element.dispatchEvent(new Event(type));
      });
    tick(6.6, 0, "seeking", "seeking", "timeupdate");
    expect(element.currentTime).toBe(0);
    tick(6.9, 0, "seeked", "timeupdate");
    expect(element.currentTime).toBe(0);
    tick(7.6, 0, "timeupdate");
    expect(element.currentTime).toBeCloseTo(1.8, 9);
    expect(element.muted).toBe(true);
    expect(sources[0].stop).not.toHaveBeenCalled();
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
      element.dispatchEvent(new Event("ended"));
    });
    act(() => {
      element.dispatchEvent(new Event("pause"));
    });
    expect(sources[0].stop).toHaveBeenCalled();
    expect(element.muted).toBe(false);
  });

  it("hands the sound back when another item replaces the joined one", async () => {
    const hook = render(playing());
    await vi.waitFor(() => expect(sources).toHaveLength(1));
    hook.rerender({ mediaID: "m9", next: nextItem("m10") });
    expect(sources[0].stop).toHaveBeenCalled();
  });

  it("leaves a next track over the cap to the element advance, unfetched", async () => {
    probedSeconds = MAX_DECODED_SECONDS + 1;
    render(playing());
    await settle();
    expect(fetch).not.toHaveBeenCalled();
    expect(sources).toHaveLength(0);
  });

  it("leaves a next track without a known duration to the element advance", async () => {
    probedSeconds = Number.POSITIVE_INFINITY;
    render(playing());
    await settle();
    expect(fetch).not.toHaveBeenCalled();
  });

  it("drops a decoded track that runs over the cap its metadata hid", async () => {
    decodedSeconds = MAX_DECODED_SECONDS + 1;
    render(playing());
    await settle();
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(sources).toHaveLength(0);
  });

  it("releases a decoded track's audio as soon as its segment ends", async () => {
    render(playing());
    await vi.waitFor(() => expect(sources).toHaveLength(1));
    expect(sources[0].buffer).not.toBeNull();
    act(() => sources[0].onended?.());
    expect(sources[0].buffer).toBeNull();
    expect(sources[0].disconnect).toHaveBeenCalled();
  });

  it("holds at most the decoded track heard and the next", async () => {
    const element = playing();
    const hook = render(element);
    await vi.waitFor(() => expect(sources).toHaveLength(1));
    hook.rerender({ mediaID: "m2", next: nextItem("m3") });
    await vi.waitFor(() => expect(sources).toHaveLength(2));
    // m2 still sounds its last moment while the element already shows m3.
    hook.rerender({ mediaID: "m3", next: nextItem("m4") });
    await settle();
    expect(fetch).toHaveBeenCalledTimes(2);
    act(() => sources[0].onended?.());
    act(() => {
      element.dispatchEvent(new Event("timeupdate"));
    });
    await vi.waitFor(() => expect(sources).toHaveLength(3));
    expect(fetch).toHaveBeenCalledTimes(3);
  });
});
