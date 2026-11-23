import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PlaybackGapless, PlaybackNext, PlaybackStream } from "../lib/playback";
import { listenerMuted, setListenerMuted } from "./listenerMute";
import {
  declaredPadding,
  decodableStream,
  heardTime,
  MAX_DECODED_SECONDS,
  START_WAIT_SECONDS,
  skipJoin,
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
  stateListeners: (() => void)[] = [];
  addEventListener = vi.fn((type: string, listener: () => void) => {
    if (type === "statechange") this.stateListeners.push(listener);
  });
  /** What a resume a gesture allowed does. */
  run() {
    this.state = "running";
    for (const listener of this.stateListeners) listener();
  }
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

// A paused element at the start of a 2 s track; play() starts it.
function starting() {
  const element = document.createElement("video");
  element.src = "/api/media/m1/stream";
  let paused = true;
  let time = 0;
  Object.defineProperty(element, "paused", { configurable: true, get: () => paused });
  Object.defineProperty(element, "duration", { configurable: true, get: () => 2 });
  Object.defineProperty(element, "currentTime", {
    configurable: true,
    get: () => time,
    set: (value: number) => {
      time = value;
    },
  });
  const play = () =>
    act(() => {
      paused = false;
      element.dispatchEvent(new Event("play"));
      element.dispatchEvent(new Event("playing"));
    });
  const pause = () =>
    act(() => {
      paused = true;
      element.dispatchEvent(new Event("pause"));
    });
  return { element, play, pause };
}

function nextItem(mediaID: string): PlaybackNext {
  return {
    mediaID,
    streams: [{ kind: "direct-play", url: `/api/media/${mediaID}/stream` }],
  } as PlaybackNext;
}

interface Props {
  mediaID: string;
  next: PlaybackNext;
  /** The stream the element plays; a direct-play stream of the item by default. */
  stream?: PlaybackStream;
  enabled?: boolean;
  waitForAdvance?: boolean;
}

function render(element: HTMLVideoElement, waitForAdvance = false) {
  // Stable, as the player's own ref is.
  const videoRef = { current: element };
  return renderHook(
    ({ mediaID, next, stream, enabled = true, waitForAdvance }: Props) =>
      useGaplessAudio({
        videoRef,
        mediaID,
        stream: stream ?? { kind: "direct-play", url: `/api/media/${mediaID}/stream` },
        next,
        advanceID: next.mediaID,
        enabled,
        waitForAdvance,
      }),
    { initialProps: { mediaID: "m1", next: nextItem("m2"), waitForAdvance } as Props },
  );
}

// The next fetch waits until the returned function lets it answer.
function holdNextFetch(): () => void {
  let answer = () => {};
  vi.mocked(fetch).mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        answer = () =>
          resolve({ ok: true, arrayBuffer: async () => new ArrayBuffer(8) } as Response);
      }),
  );
  return () => answer();
}

describe("useGaplessAudio", () => {
  it("prepares early and plays only the confirmed server successor", async () => {
    const element = playing();
    Object.defineProperty(element, "duration", { value: 60 });
    element.currentTime = 29;
    const answerFetch = holdNextFetch();
    const hook = render(element, true);
    expect(fetch).not.toHaveBeenCalled();

    element.currentTime = 30;
    element.dispatchEvent(new Event("timeupdate"));
    await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
    element.currentTime = 34;
    answerFetch();
    await vi.waitFor(() => expect(contexts[0].decodeAudioData).toHaveBeenCalledOnce());
    expect(sources).toHaveLength(0);

    element.currentTime = 60;
    element.dispatchEvent(new Event("ended"));
    hook.result.current("m3");
    expect(sources).toHaveLength(0);
    hook.result.current("m2");
    expect(sources).toHaveLength(1);
    expect(sources[0].start.mock.calls[0][0]).toBe(5);
  });

  it("cancels a scheduled catalog successor when the player enters server queue mode", async () => {
    const element = playing();
    const hook = render(element);
    await vi.waitFor(() => expect(sources).toHaveLength(1));
    hook.rerender({ mediaID: "m1", next: nextItem("m2"), waitForAdvance: true });
    expect(sources[0].stop).toHaveBeenCalledOnce();
    expect(sources[0].disconnect).toHaveBeenCalledOnce();
    element.dispatchEvent(new Event("timeupdate"));
    expect(sources).toHaveLength(1);
  });

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

  it("starts a track that plays from silence on Web Audio, the element held muted", async () => {
    const { element, play } = starting();
    render(element);
    await settle();
    play();
    expect(element.muted).toBe(true);
    expect(listenerMuted(element)).toBe(false);
    await vi.waitFor(() => expect(sources).toHaveLength(1));
    // m1 from where the element starts, now; then m2 chained after it.
    expect(sources[0].start).toHaveBeenCalledWith(5, 0, 2);
    act(() => {
      element.dispatchEvent(new Event("timeupdate"));
    });
    expect(sources[1].start).toHaveBeenCalledWith(7, 0, 2);
  });

  it("stops the decoded track on pause and starts it again where the element resumes", async () => {
    const { element, play, pause } = starting();
    render(element);
    await settle();
    play();
    await vi.waitFor(() => expect(sources).toHaveLength(1));
    pause();
    expect(sources[0].stop).toHaveBeenCalled();
    expect(element.muted).toBe(true);
    element.currentTime = 1.25;
    play();
    // m1 from where the element resumes, m2 chained after it.
    expect(sources.at(-2)?.start).toHaveBeenCalledWith(5, 1.25, 0.75);
    expect(sources.at(-1)?.start).toHaveBeenCalledWith(5.75, 0, 2);
    expect(element.muted).toBe(true);
  });

  it("starts the decoded track again where the listener seeks", async () => {
    const { element, play } = starting();
    render(element);
    await settle();
    play();
    await vi.waitFor(() => expect(sources).toHaveLength(1));
    act(() => {
      element.currentTime = 0.5;
      element.dispatchEvent(new Event("seeking"));
    });
    expect(sources[0].stop).toHaveBeenCalled();
    act(() => {
      element.dispatchEvent(new Event("seeked"));
    });
    expect(sources.at(-1)?.start).toHaveBeenCalledWith(5, 0.5, 1.5);
    expect(element.muted).toBe(true);
  });

  it("lets the element play on its own from where it started when the decode is late", async () => {
    contextState = "suspended";
    const { element, play } = starting();
    render(element);
    await settle();
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    try {
      play();
      expect(element.muted).toBe(true);
      element.currentTime = 1.5;
      act(() => vi.advanceTimersByTime(START_WAIT_SECONDS * 1000));
    } finally {
      vi.useRealTimers();
    }
    expect(element.muted).toBe(false);
    expect(element.currentTime).toBe(0);
    expect(sources).toHaveLength(0);
  });

  it("starts the held track once a gesture's resume runs the context", async () => {
    contextState = "suspended";
    const { element, play } = starting();
    render(element);
    await settle();
    play();
    await settle();
    expect(sources).toHaveLength(0);
    expect(element.muted).toBe(true);
    act(() => contexts[0].run());
    expect(sources[0].start).toHaveBeenCalledWith(5, 0, 2);
  });

  it("restarts the wait where the listener seeks while the track still decodes", async () => {
    const { element, play } = starting();
    render(element);
    await settle();
    const answer = holdNextFetch();
    play();
    await settle();
    expect(sources).toHaveLength(0);
    act(() => {
      element.currentTime = 0.5;
      element.dispatchEvent(new Event("seeking"));
      element.dispatchEvent(new Event("seeked"));
    });
    answer();
    await vi.waitFor(() => expect(sources).toHaveLength(1));
    expect(sources[0].start).toHaveBeenCalledWith(5, 0.5, 1.5);
    expect(element.muted).toBe(true);
  });

  it("stops a scheduled join when the next item is replaced", async () => {
    const hook = render(playing());
    await vi.waitFor(() => expect(sources).toHaveLength(1));
    hook.rerender({ mediaID: "m1", next: nextItem("m3") });
    expect(sources[0].stop).toHaveBeenCalled();
    expect(sources[0].disconnect).toHaveBeenCalled();
    // The replacement is decoded and joined in its place.
    await vi.waitFor(() => expect(sources).toHaveLength(2));
    expect(sources[1].start.mock.calls[0][0]).toBeCloseTo(5.8, 9);
  });

  it("retires a scheduled join on Play now, until the item changes (CORE-3352)", async () => {
    const element = playing();
    const hook = render(element);
    await vi.waitFor(() => expect(sources).toHaveLength(1));
    act(() => skipJoin(element));
    expect(sources[0].stop).toHaveBeenCalled();
    // The outgoing track plays on until the skip lands: it schedules no join again.
    act(() => {
      element.dispatchEvent(new Event("timeupdate"));
      element.dispatchEvent(new Event("playing"));
    });
    expect(sources).toHaveLength(1);
    expect(element.muted).toBe(false);
    // The named item plays on the element; its own successor joins as usual.
    hook.rerender({ mediaID: "m2", next: nextItem("m3") });
    await vi.waitFor(() => expect(sources).toHaveLength(2));
  });

  it("hands the sound back when the join is turned off on the same item", async () => {
    const { element, play } = starting();
    const hook = render(element);
    await settle();
    play();
    await vi.waitFor(() => expect(sources).toHaveLength(1));
    expect(element.muted).toBe(true);
    // A Watch Party leads: the player stops following the queue.
    hook.rerender({ mediaID: "m1", next: nextItem("m2"), enabled: false });
    expect(sources[0].stop).toHaveBeenCalled();
    expect(element.muted).toBe(false);
  });

  it("schedules a join from the element again after it buffers", async () => {
    const element = playing();
    render(element);
    await vi.waitFor(() => expect(sources).toHaveLength(1));
    act(() => {
      element.dispatchEvent(new Event("waiting"));
    });
    expect(sources[0].stop).toHaveBeenCalled();
    // Playing again 0.5 s from the end, at context time 6.
    contexts[0].currentTime = 6;
    act(() => {
      element.currentTime = 9.5;
      element.dispatchEvent(new Event("playing"));
    });
    expect(sources).toHaveLength(2);
    expect(sources[1].start.mock.calls[0][0]).toBeCloseTo(6.5, 9);
  });

  it("schedules no join from a stalled element when the decode finishes", async () => {
    const element = playing();
    const answer = holdNextFetch();
    render(element);
    await settle();
    act(() => {
      element.dispatchEvent(new Event("waiting"));
    });
    answer();
    await settle();
    expect(sources).toHaveLength(0);
    act(() => {
      element.dispatchEvent(new Event("playing"));
    });
    expect(sources).toHaveLength(1);
  });

  it("starts nothing on Web Audio again after the join is turned off", async () => {
    const { element, play, pause } = starting();
    const hook = render(element);
    await settle();
    play();
    await vi.waitFor(() => expect(sources).toHaveLength(1));
    hook.rerender({ mediaID: "m1", next: nextItem("m2"), enabled: false });
    const scheduled = sources.length;
    pause();
    play();
    expect(element.muted).toBe(false);
    expect(sources).toHaveLength(scheduled);
  });

  it("drops the decoded tracks of a previous item that an HLS item replaces", async () => {
    const { element, play } = starting();
    const hook = render(element);
    await settle();
    play();
    await vi.waitFor(() => expect(sources).toHaveLength(1));
    hook.rerender({
      mediaID: "m5",
      next: nextItem("m6"),
      stream: { kind: "hls", url: "/api/media/m5/master.m3u8" },
    });
    await settle();
    expect(fetch).toHaveBeenCalledWith("/api/media/m6/stream", expect.anything());
  });

  it("lets the element play a track it cannot decode at once", async () => {
    const { element, play } = starting();
    render(element);
    await settle();
    probedSeconds = MAX_DECODED_SECONDS + 1;
    play();
    expect(element.muted).toBe(true);
    await vi.waitFor(() => expect(element.muted).toBe(false));
    expect(sources).toHaveLength(0);
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

  it("reports the decoded track's position as the time heard, not the shadowing element's", async () => {
    const element = playing();
    const hook = render(element);
    expect(heardTime(element, "m1")).toBe(element.currentTime);
    await vi.waitFor(() => expect(sources).toHaveLength(1));
    const context = contexts[0];
    // The join at 5.8: m2 sounds from there while the element still loads it.
    act(() => {
      element.dispatchEvent(new Event("ended"));
      element.currentTime = 0;
      element.dispatchEvent(new Event("loadstart"));
    });
    hook.rerender({ mediaID: "m2", next: nextItem("m3") });
    context.currentTime = 6;
    expect(element.currentTime).toBe(0);
    expect(heardTime(element, "m2")).toBeCloseTo(0.2, 9);
    // What the context has rendered reaches the speakers its output latency later.
    Object.assign(context, { outputLatency: 0.05 });
    expect(heardTime(element, "m2")).toBeCloseTo(0.15, 9);
    // Before the join the next track is at its start.
    context.currentTime = 5.5;
    expect(heardTime(element, "m2")).toBe(0);
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
