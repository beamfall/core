import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  cancelFadeAdvance,
  fadeAdvance,
  fadeBaseVolume,
  LOAD_WAIT_MS,
  TRACK_FADE_MS,
} from "./trackFade";

// Play now's fade (CORE-3216): out, advance, back in once the next track plays.
// The element is muted at the page level in no test here: a detached element
// never makes a sound.

function playingElement(volume = 0.8) {
  const video = document.createElement("audio");
  Object.defineProperty(video, "paused", { configurable: true, value: false });
  video.volume = volume;
  return video;
}

describe("fadeAdvance", () => {
  beforeEach(() => {
    vi.useFakeTimers({
      toFake: [
        "setTimeout",
        "clearTimeout",
        "requestAnimationFrame",
        "cancelAnimationFrame",
        "performance",
      ],
    });
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("fades out, advances, then fades back to the listener's level once the next track plays", () => {
    const video = playingElement();
    const advance = vi.fn();
    fadeAdvance(video, advance);
    expect(advance).not.toHaveBeenCalled();
    vi.advanceTimersByTime(TRACK_FADE_MS / 2);
    expect(video.volume).toBeGreaterThan(0);
    expect(video.volume).toBeLessThan(0.8);
    vi.advanceTimersByTime(TRACK_FADE_MS);
    expect(advance).toHaveBeenCalledOnce();
    expect(video.volume).toBeCloseTo(0);

    video.dispatchEvent(new Event("loadstart"));
    video.dispatchEvent(new Event("playing"));
    vi.advanceTimersByTime(TRACK_FADE_MS * 2);
    expect(video.volume).toBeCloseTo(0.8);
  });

  it("fades back in when the skip loads nothing", () => {
    const video = playingElement();
    fadeAdvance(video, () => {});
    vi.advanceTimersByTime(TRACK_FADE_MS * 2);
    expect(video.volume).toBeCloseTo(0);
    vi.advanceTimersByTime(LOAD_WAIT_MS + TRACK_FADE_MS * 2);
    expect(video.volume).toBeCloseTo(0.8);
  });

  it("keeps a volume the listener sets mid-fade, and still skips", () => {
    const video = playingElement();
    const advance = vi.fn();
    fadeAdvance(video, advance);
    vi.advanceTimersByTime(TRACK_FADE_MS / 2);
    expect(fadeBaseVolume(video)).toBe(0.8);
    video.volume = 0.3;
    vi.advanceTimersByTime(LOAD_WAIT_MS * 4);
    expect(advance).toHaveBeenCalledOnce();
    expect(video.volume).toBe(0.3);
    expect(fadeBaseVolume(video)).toBe(0.3);
  });

  it("drops a skip still fading out when the track changes another way", () => {
    const video = playingElement();
    const advance = vi.fn();
    fadeAdvance(video, advance);
    vi.advanceTimersByTime(TRACK_FADE_MS / 2);
    cancelFadeAdvance(video);
    vi.advanceTimersByTime(LOAD_WAIT_MS * 4);
    expect(advance).not.toHaveBeenCalled();
    expect(video.volume).toBe(0.8);
  });

  it("keeps fading in after its own skip changes the track", () => {
    const video = playingElement();
    fadeAdvance(video, () => cancelFadeAdvance(video));
    vi.advanceTimersByTime(TRACK_FADE_MS * 2);
    video.dispatchEvent(new Event("playing"));
    vi.advanceTimersByTime(TRACK_FADE_MS * 2);
    expect(video.volume).toBeCloseTo(0.8);
  });

  it("advances at once when nothing is audible", () => {
    const paused = document.createElement("audio");
    const advance = vi.fn();
    fadeAdvance(paused, advance);
    expect(advance).toHaveBeenCalledOnce();
    fadeAdvance(null, advance);
    expect(advance).toHaveBeenCalledTimes(2);
  });

  it("finishes the first fade when a second skip comes mid-fade", () => {
    const video = playingElement();
    const first = vi.fn();
    fadeAdvance(video, first);
    vi.advanceTimersByTime(TRACK_FADE_MS / 2);
    const second = vi.fn();
    fadeAdvance(video, second);
    vi.advanceTimersByTime(TRACK_FADE_MS * 2);
    expect(first).not.toHaveBeenCalled();
    expect(second).toHaveBeenCalledOnce();
    vi.advanceTimersByTime(LOAD_WAIT_MS + TRACK_FADE_MS * 2);
    expect(video.volume).toBeCloseTo(0.8);
  });
});
