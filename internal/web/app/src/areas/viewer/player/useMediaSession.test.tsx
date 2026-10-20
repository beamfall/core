import { renderHook } from "@testing-library/react";
import { createRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PlaybackNext } from "../lib/playback";
import { useMediaSession } from "./useMediaSession";

// Minimal stubs for the Media Session API surface jsdom lacks.
type Handler = ((d?: { seekOffset?: number; seekTime?: number }) => void) | null;

class FakeVideo {
  paused = true;
  currentTime = 0;
  duration = 100;
  playbackRate = 1;
  private listeners = new Map<string, Set<() => void>>();
  play = vi.fn(() => {
    this.paused = false;
    return Promise.resolve();
  });
  pause = vi.fn(() => {
    this.paused = true;
  });
  addEventListener(type: string, cb: () => void) {
    (this.listeners.get(type) ?? this.listeners.set(type, new Set()).get(type)!).add(cb);
  }
  removeEventListener(type: string, cb: () => void) {
    this.listeners.get(type)?.delete(cb);
  }
  emit(type: string) {
    this.listeners.get(type)?.forEach((cb) => cb());
  }
}

let handlers: Map<string, Handler>;
let fakeSession: {
  metadata: unknown;
  playbackState: string;
  setActionHandler: (a: string, h: Handler) => void;
  setPositionState: ReturnType<typeof vi.fn>;
};

beforeEach(() => {
  handlers = new Map();
  fakeSession = {
    metadata: null,
    playbackState: "none",
    setActionHandler: (a: string, h: Handler) => handlers.set(a, h),
    setPositionState: vi.fn(),
  };
  vi.stubGlobal(
    "MediaMetadata",
    class {
      title: string;
      album?: string;
      constructor(init: { title: string; album?: string }) {
        this.title = init.title;
        this.album = init.album;
      }
    },
  );
  Object.defineProperty(navigator, "mediaSession", {
    value: fakeSession,
    configurable: true,
    writable: true,
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
});

type Props = Parameters<typeof useMediaSession>[0];

function setup(over: Partial<Props> = {}) {
  const video = new FakeVideo();
  const ref = createRef<HTMLVideoElement>();
  (ref as { current: unknown }).current = video;
  const initialProps: Props = {
    videoRef: ref,
    active: true,
    title: "The Episode",
    next: null,
    ...over,
  } as Props;
  const view = renderHook((props: Props) => useMediaSession(props), {
    initialProps,
  });
  return { video, ref, view, initialProps };
}

describe("useMediaSession", () => {
  it("sets metadata from the Media title while active", () => {
    setup();
    expect((fakeSession.metadata as { title: string }).title).toBe("The Episode");
  });

  it("is a no-op when the Media Session API is absent", () => {
    Object.defineProperty(navigator, "mediaSession", {
      value: undefined,
      configurable: true,
      writable: true,
    });

    expect(() => setup()).not.toThrow();
  });

  it("routes play/pause intents through the engine-owned video element", () => {
    const { video } = setup();
    handlers.get("play")?.();
    expect(video.play).toHaveBeenCalled();
    handlers.get("pause")?.();
    expect(video.pause).toHaveBeenCalled();
  });

  it("keeps supported handlers when a browser rejects one action", () => {
    fakeSession.setActionHandler = (action: string, handler: Handler) => {
      if (action === "seekto") throw new Error("unsupported");
      handlers.set(action, handler);
    };

    const { video } = setup();
    handlers.get("play")?.();
    expect(video.play).toHaveBeenCalled();
    expect(handlers.has("seekto")).toBe(false);
  });

  it("seeks relative and absolute through currentTime, clamped", () => {
    const { video } = setup();
    video.currentTime = 50;
    handlers.get("seekforward")?.({ seekOffset: 10 });
    expect(video.currentTime).toBe(60);
    handlers.get("seekbackward")?.({ seekOffset: 100 });
    expect(video.currentTime).toBe(0); // clamped at 0
    handlers.get("seekto")?.({ seekTime: 42 });
    expect(video.currentTime).toBe(42);
    // Overshoot the duration clamps to the end.
    video.currentTime = 95;
    handlers.get("seekforward")?.({ seekOffset: 20 });
    expect(video.currentTime).toBe(100);
  });

  it("mirrors playbackState and position from video events", () => {
    const { video } = setup();
    video.paused = false;
    video.emit("play");
    expect(fakeSession.playbackState).toBe("playing");
    video.currentTime = 30;
    video.emit("timeupdate");
    expect(fakeSession.setPositionState).toHaveBeenCalledWith(
      expect.objectContaining({ duration: 100, position: 30 }),
    );
  });

  it("only wires nexttrack when a next item exists, routing through onNext", () => {
    setup();
    expect(handlers.get("nexttrack")).toBeNull();

    const onNext = vi.fn();
    const next: PlaybackNext = { mediaID: "m2", title: "Next" };
    setup({ next, onNext });
    handlers.get("nexttrack")?.();
    expect(onNext).toHaveBeenCalledWith(next);
  });

  it("clears metadata and handlers when no longer active (no leak)", () => {
    const { view, initialProps } = setup();
    expect(fakeSession.metadata).not.toBeNull();
    view.rerender({ ...initialProps, active: false });
    // After the active->inactive transition the metadata effect clears it and
    // the handler effect's cleanup nulls every action handler + playbackState.
    expect(fakeSession.metadata).toBeNull();
    expect(handlers.get("play")).toBeNull();
    expect(fakeSession.playbackState).toBe("none");
  });
});
