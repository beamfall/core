import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PlaybackMarker } from "@/lib/graphql/types";
import type { MenuOption } from "./useLiveTracks";
import { type PlayerShortcutHandlers, usePlayerShortcuts } from "./usePlayerShortcuts";

const SPEEDS = [0.5, 0.75, 1, 1.25, 1.5, 2];

function option(id: string, selected: boolean): MenuOption {
  return { id, label: id, selected, disabled: false, reason: "" } as MenuOption;
}

type Overrides = Partial<Omit<PlayerShortcutHandlers, "videoRef">>;

function Probe(props: Overrides) {
  const videoRef = useRef<HTMLVideoElement | null>(null);
  usePlayerShortcuts({
    videoRef,
    skipInterval: 15,
    speeds: SPEEDS,
    markers: [],
    togglePlay: vi.fn(),
    toggleFullscreen: vi.fn(),
    toggleMute: vi.fn(),
    seekBy: (seconds) => {
      const video = videoRef.current;
      if (video)
        video.currentTime = Math.max(0, Math.min(video.duration, video.currentTime + seconds));
    },
    setVolume: vi.fn(),
    subtitleOptions: [],
    audioOptions: [],
    selectSubtitle: vi.fn(),
    selectAudio: vi.fn(),
    onHelp: vi.fn(),
    ...props,
  });
  return (
    <>
      <video data-testid="video" ref={videoRef} />
      <input aria-label="Search" />
    </>
  );
}

function setup(props: Overrides = {}, { currentTime = 30, duration = 200, paused = false } = {}) {
  const view = render(<Probe {...props} />);
  const video = screen.getByTestId("video") as HTMLVideoElement;
  Object.defineProperties(video, {
    currentTime: { configurable: true, writable: true, value: currentTime },
    duration: { configurable: true, writable: true, value: duration },
    playbackRate: { configurable: true, writable: true, value: 1 },
    paused: { configurable: true, writable: true, value: paused },
  });
  return { ...view, video };
}

function press(key: string, init: KeyboardEventInit = {}) {
  const event = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true, ...init });
  fireEvent(window, event);
  return event;
}

describe("usePlayerShortcuts (CORE-1682)", () => {
  it("jumps 10 s with J and L, independent of the skip interval", () => {
    const { video } = setup();
    expect(press("l").defaultPrevented).toBe(true);
    expect(video.currentTime).toBe(40);
    press("j");
    expect(video.currentTime).toBe(30);
  });

  it("seeks to a tenth of the duration per digit", () => {
    const { video } = setup();
    press("5");
    expect(video.currentTime).toBe(100);
    press("0");
    expect(video.currentTime).toBe(0);
    press("9");
    expect(video.currentTime).toBe(180);
  });

  it("leaves digits alone while the duration is unknown", () => {
    const { video } = setup({}, { duration: Number.NaN });
    press("5");
    expect(video.currentTime).toBe(30);
  });

  it("steps the speed through the menu values and stops at the ends", () => {
    const { video } = setup();
    press(">", { shiftKey: true });
    expect(video.playbackRate).toBe(1.25);
    press("<", { shiftKey: true });
    press("<", { shiftKey: true });
    press("<", { shiftKey: true });
    expect(video.playbackRate).toBe(0.5);
    press("<", { shiftKey: true });
    expect(video.playbackRate).toBe(0.5);
  });

  it("frame-steps only while paused", () => {
    const { video } = setup({}, { paused: false });
    press(".");
    expect(video.currentTime).toBe(30);
    Object.defineProperty(video, "paused", { configurable: true, value: true });
    press(".");
    expect(video.currentTime).toBeCloseTo(30 + 1 / 30);
    press(",");
    expect(video.currentTime).toBeCloseTo(30);
  });

  describe("with presented-frame timing", () => {
    const callbacks: VideoFrameRequestCallback[] = [];
    const proto = HTMLVideoElement.prototype as Partial<HTMLVideoElement>;
    const present = (mediaTime: number, presentedFrames: number) => {
      const callback = callbacks.shift();
      callback?.(0, { mediaTime, presentedFrames } as VideoFrameCallbackMetadata);
    };
    const pauseAt = (video: HTMLVideoElement, currentTime: number) => {
      Object.defineProperty(video, "paused", { configurable: true, value: true });
      video.currentTime = currentTime;
    };
    beforeEach(() => {
      callbacks.length = 0;
      proto.requestVideoFrameCallback = (callback) => callbacks.push(callback);
      proto.cancelVideoFrameCallback = () => {};
    });
    afterEach(() => {
      cleanup();
      Reflect.deleteProperty(proto, "requestVideoFrameCallback");
      Reflect.deleteProperty(proto, "cancelVideoFrameCallback");
    });

    it("steps by the frame interval the browser presents", () => {
      const { video } = setup({}, { currentTime: 10, paused: false });
      // Playing at 24 fps, then a dropped frame that must not stretch the step.
      present(10, 100);
      present(10 + 1 / 24, 101);
      present(10 + 3 / 24, 102);
      pauseAt(video, 10 + 3 / 24);
      press(".");
      expect(video.currentTime).toBeCloseTo(10 + 4 / 24 + 1 / 48);
    });

    it("measures low frame rates too", () => {
      const { video } = setup({}, { currentTime: 0, paused: false });
      present(0, 1);
      present(0.2, 2);
      pauseAt(video, 0.2);
      press(".");
      expect(video.currentTime).toBeCloseTo(0.5);
    });

    it("steps forward from a shown frame ahead of the playhead", () => {
      const { video } = setup({}, { currentTime: 0, paused: false });
      present(0.7, 1);
      present(0.7 + 1 / 120, 2);
      present(0.725, 3);
      pauseAt(video, 0.712065);
      press(".");
      expect(video.currentTime).toBeGreaterThan(0.725);
    });

    it("steps from the seek target until the seek shows its frame", () => {
      const { video } = setup({}, { currentTime: 0, paused: false });
      present(5, 1);
      present(5 + 1 / 24, 2);
      pauseAt(video, 5 + 1 / 24);
      fireEvent(video, new Event("seeking"));
      video.currentTime = 40;
      press(".");
      expect(video.currentTime).toBeCloseTo(40 + 1 / 24);
    });
  });

  it("moves between chapters with Page Up and Page Down", () => {
    const markers: PlaybackMarker[] = [
      { startSeconds: 0, segmentKind: "chapter", title: "Opening" },
      { startSeconds: 120, segmentKind: "chapter", title: "Middle" },
    ];
    const { video } = setup({ markers });
    press("PageDown");
    expect(video.currentTime).toBe(120);
    press("PageDown");
    expect(video.currentTime).toBe(120);
    press("PageUp");
    expect(video.currentTime).toBe(0);
  });

  it("toggles captions off and back to the last shown track", () => {
    const selectSubtitle = vi.fn();
    const shown = [option("off", false), option("0", false), option("1", true)];
    const { rerender } = setup({ subtitleOptions: shown, selectSubtitle });
    press("c");
    expect(selectSubtitle).toHaveBeenLastCalledWith("off");

    const off = [option("off", true), option("0", false), option("1", false)];
    rerender(<Probe subtitleOptions={off} selectSubtitle={selectSubtitle} />);
    press("c");
    expect(selectSubtitle).toHaveBeenLastCalledWith("1");
  });

  it("cycles subtitle and audio tracks with S and A", () => {
    const selectSubtitle = vi.fn();
    const selectAudio = vi.fn();
    setup({
      subtitleOptions: [option("off", false), option("0", false), option("1", true)],
      audioOptions: [option("0", true), option("1", false)],
      selectSubtitle,
      selectAudio,
    });
    press("s");
    expect(selectSubtitle).toHaveBeenLastCalledWith("off");
    press("a");
    expect(selectAudio).toHaveBeenLastCalledWith("1");
  });

  it("calls the queue callbacks for Shift+N and Shift+P only", () => {
    const onNextInQueue = vi.fn();
    const onPreviousInQueue = vi.fn();
    setup({ onNextInQueue, onPreviousInQueue });
    expect(press("n").defaultPrevented).toBe(false);
    expect(onNextInQueue).not.toHaveBeenCalled();
    press("N", { shiftKey: true });
    press("P", { shiftKey: true });
    expect(onNextInQueue).toHaveBeenCalledOnce();
    expect(onPreviousInQueue).toHaveBeenCalledOnce();
  });

  it("steps the visual with V and Shift+V, never inside a text field (CORE-2814)", () => {
    const onNextVisual = vi.fn();
    const onPreviousVisual = vi.fn();
    setup({ onNextVisual, onPreviousVisual });
    expect(press("v").defaultPrevented).toBe(true);
    press("V", { shiftKey: true });
    expect(onNextVisual).toHaveBeenCalledOnce();
    expect(onPreviousVisual).toHaveBeenCalledOnce();

    screen.getByRole("textbox", { name: "Search" }).focus();
    expect(press("v").defaultPrevented).toBe(false);
    press("V", { shiftKey: true });
    expect(onNextVisual).toHaveBeenCalledOnce();
    expect(onPreviousVisual).toHaveBeenCalledOnce();
  });

  it("opens help on ? and never fires inside a text field", () => {
    const onHelp = vi.fn();
    const { video } = setup({ onHelp });
    expect(press("?", { shiftKey: true }).defaultPrevented).toBe(true);
    expect(onHelp).toHaveBeenCalledOnce();

    screen.getByRole("textbox", { name: "Search" }).focus();
    for (const key of ["l", "5", "?", ">", "c"]) {
      expect(press(key).defaultPrevented).toBe(false);
    }
    expect(video.currentTime).toBe(30);
    expect(video.playbackRate).toBe(1);
    expect(onHelp).toHaveBeenCalledOnce();
  });
});
