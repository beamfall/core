import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { holdMute, setListenerMuted } from "./listenerMute";
import { fadeAdvance, holdVolume } from "./trackFade";
import { useSleepTimer } from "./useListening";

// The sleep timer's fade: equal power over the last stretch, then a pause and
// the listener's level back. A detached, muted element never makes a sound.

function element(volume = 0.8) {
  const video = document.createElement("video");
  video.muted = true;
  let paused = false;
  Object.defineProperty(video, "paused", { configurable: true, get: () => paused });
  video.pause = () => {
    if (paused) return;
    paused = true;
    video.dispatchEvent(new Event("pause"));
  };
  video.play = () => {
    paused = false;
    video.dispatchEvent(new Event("play"));
    return Promise.resolve();
  };
  video.volume = volume;
  return video;
}

// The element stays silent while the listener counts as unmuted, so a skip fades.
function audible(video: HTMLVideoElement) {
  holdMute(video);
  setListenerMuted(video, false);
  return video;
}

function render(video: HTMLVideoElement, durationSeconds?: number) {
  const ref = { current: video };
  return renderHook(() => useSleepTimer(ref, "m1", true, durationSeconds));
}

function startMinutes(hook: ReturnType<typeof render>, minutes: number) {
  act(() =>
    hook.result.current.setTimer({
      kind: "minutes",
      minutes,
      until: Date.now() + minutes * 60_000,
    }),
  );
}

function advance(ms: number) {
  act(() => {
    vi.advanceTimersByTime(ms);
  });
}

describe("useSleepTimer fade", () => {
  beforeEach(() => {
    vi.useFakeTimers({
      toFake: [
        "setTimeout",
        "clearTimeout",
        "setInterval",
        "clearInterval",
        "Date",
        "requestAnimationFrame",
        "cancelAnimationFrame",
        "performance",
      ],
    });
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("fades with equal power over the last 45 seconds, pauses, then gives the level back", () => {
    const video = element();
    const hook = render(video);
    startMinutes(hook, 15);
    advance(15 * 60_000 - 46_000);
    expect(video.volume).toBe(0.8);
    advance(23_500); // 22.5 s left, half way through the fade
    expect(video.volume).toBeCloseTo(0.8 * Math.SQRT1_2, 2);
    advance(22_000); // 0.5 s left
    expect(video.volume).toBeLessThan(0.05);
    expect(video.paused).toBe(false);
    advance(1_000);
    expect(video.paused).toBe(true);
    expect(video.volume).toBe(0.8);
    expect(hook.result.current.timer).toBeNull();
  });

  it("steps the volume finely enough to sound smooth", () => {
    const video = element();
    const hook = render(video);
    startMinutes(hook, 15);
    advance(15 * 60_000 - 10_000);
    const before = video.volume;
    advance(50);
    const step = before - video.volume;
    expect(step).toBeGreaterThan(0);
    expect(step).toBeLessThan(0.01);
  });

  it("gives the level straight back when the timer is cancelled or extended mid-fade", () => {
    const video = element();
    const hook = render(video);
    startMinutes(hook, 15);
    advance(15 * 60_000 - 20_000);
    expect(video.volume).toBeLessThan(0.8);
    act(() => hook.result.current.setTimer(null));
    expect(video.volume).toBe(0.8);

    startMinutes(hook, 15);
    advance(15 * 60_000 - 20_000);
    expect(video.volume).toBeLessThan(0.8);
    startMinutes(hook, 30);
    expect(video.volume).toBe(0.8);
    advance(60_000);
    expect(video.volume).toBe(0.8);
  });

  it("keeps a volume the listener sets mid-fade, and fades what is left from it", () => {
    const video = element();
    const hook = render(video);
    startMinutes(hook, 15);
    advance(15 * 60_000 - 20_000);
    act(() => {
      video.volume = 0.5;
    });
    advance(50);
    expect(video.volume).toBeCloseTo(0.5, 2);
    advance(10_000); // half of the 20 s left when they set it
    expect(video.volume).toBeCloseTo(0.5 * Math.SQRT1_2, 2);
    advance(10_500);
    expect(video.paused).toBe(true);
    expect(video.volume).toBe(0.5);
  });

  it("gives the level back on a pause, and fades afresh from it on play", () => {
    const video = element();
    const hook = render(video);
    startMinutes(hook, 15);
    advance(15 * 60_000 - 20_000);
    expect(video.volume).toBeLessThan(0.8);
    act(() => video.pause());
    expect(video.volume).toBe(0.8);
    advance(4_000);
    expect(video.volume).toBe(0.8);
    act(() => void video.play());
    expect(video.volume).toBeCloseTo(0.8, 2);
    advance(8_000); // half of the 16 s left at play
    expect(video.volume).toBeCloseTo(0.8 * Math.SQRT1_2, 2);
  });

  it("fades an end-of-item timer over the end of the track", () => {
    const video = element();
    let time = 0;
    Object.defineProperty(video, "currentTime", { configurable: true, get: () => time });
    const hook = render(video, 200.3);
    act(() => hook.result.current.setTimer({ kind: "item" }));
    // 200 s left when set: the fade takes the last 45.
    time = 200.3 - 0.3 - 46;
    advance(250);
    expect(video.volume).toBe(0.8);
    // Playback runs on in 250 ms ticks to half way through the fade.
    while (time < 200.3 - 0.3 - 22.5) {
      time += 0.25;
      advance(250);
    }
    expect(video.volume).toBeCloseTo(0.8 * Math.SQRT1_2, 2);
    time = 200.3;
    advance(250);
    expect(video.paused).toBe(true);
    expect(video.volume).toBe(0.8);
  });

  it("waits while a skip's fade holds the volume, then fades from the listener's level", () => {
    const video = element();
    const hook = render(video);
    startMinutes(hook, 15);
    advance(15 * 60_000 - 20_000);
    const skip = holdVolume(video);
    expect(skip.base).toBe(0.8);
    act(() => skip.write(0.1));
    advance(1_000);
    expect(video.volume).toBeCloseTo(0.08);
    act(() => skip.release(true));
    advance(50);
    expect(video.volume).toBeCloseTo(0.8, 2);
    advance(19_500);
    expect(video.paused).toBe(true);
    expect(video.volume).toBe(0.8);
  });
  it("fades a skip out from the sleep fade's level, with no jump back up", () => {
    const video = audible(element());
    const hook = render(video);
    startMinutes(hook, 15);
    advance(15 * 60_000 - 2_000);
    const faded = video.volume;
    expect(faded).toBeLessThan(0.1);
    const next = vi.fn();
    act(() => fadeAdvance(video, next));
    expect(next).not.toHaveBeenCalled();
    for (let i = 0; i < 10; i++) {
      advance(16);
      expect(video.volume).toBeLessThanOrEqual(faded + 1e-9);
    }
    advance(400);
    expect(next).toHaveBeenCalledOnce();
  });

  it("drops a skip still fading out when the timer stops, so nothing plays on", () => {
    const video = audible(element());
    const hook = render(video);
    startMinutes(hook, 15);
    advance(15 * 60_000 - 100);
    const next = vi.fn();
    act(() => fadeAdvance(video, next));
    advance(1_000);
    expect(video.paused).toBe(true);
    expect(next).not.toHaveBeenCalled();
    expect(video.volume).toBe(0.8);
  });
});
