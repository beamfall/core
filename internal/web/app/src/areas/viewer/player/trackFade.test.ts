import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  cancelFadeAdvance,
  fadeAdvance,
  fadeBaseVolume,
  finishFadeAdvance,
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
    cancelFadeAdvance();
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

  it("moves a skip on at once when the track ends mid-fade, with no animation frame", () => {
    const video = playingElement();
    const advance = vi.fn();
    expect(finishFadeAdvance()).toBe(false);
    fadeAdvance(video, advance);
    vi.advanceTimersByTime(TRACK_FADE_MS - 10);
    expect(video.volume).toBeLessThan(0.1);
    // A hidden tab runs no animation frames; the end alone moves the skip on,
    // once, with the listener's level back at once.
    expect(finishFadeAdvance()).toBe(true);
    expect(advance).toHaveBeenCalledOnce();
    expect(video.volume).toBeCloseTo(0.8);
    expect(finishFadeAdvance()).toBe(false);
    cancelFadeAdvance();
    vi.advanceTimersByTime(TRACK_FADE_MS * 2);
    expect(advance).toHaveBeenCalledOnce();

    video.dispatchEvent(new Event("loadstart"));
    video.dispatchEvent(new Event("playing"));
    expect(video.volume).toBeCloseTo(0.8);
  });

  it("owns what plays next until the item changes or the wait runs out (CORE-3356)", () => {
    const video = playingElement();
    const advance = vi.fn();
    fadeAdvance(video, advance);
    vi.advanceTimersByTime(TRACK_FADE_MS * 2);
    expect(advance).toHaveBeenCalledOnce();
    // Advanced, it still owns: the outgoing track's own end moves nothing on again.
    expect(finishFadeAdvance()).toBe(true);
    expect(advance).toHaveBeenCalledOnce();
    // The item changed: the player drops the skip, which keeps fading in.
    cancelFadeAdvance();
    expect(finishFadeAdvance()).toBe(false);

    fadeAdvance(playingElement(), () => {});
    vi.advanceTimersByTime(TRACK_FADE_MS * 2 + LOAD_WAIT_MS + TRACK_FADE_MS * 2);
    expect(finishFadeAdvance()).toBe(false);
  });

  it("skips at once, at the listener's level, when the tab hides mid-fade (CORE-3356)", () => {
    const hidden = vi.spyOn(document, "hidden", "get").mockReturnValue(false);
    try {
      const video = playingElement();
      const advance = vi.fn();
      fadeAdvance(video, advance);
      vi.advanceTimersByTime(TRACK_FADE_MS / 2);
      // A hidden tab runs no frames: the skip must not wait for them.
      hidden.mockReturnValue(true);
      document.dispatchEvent(new Event("visibilitychange"));
      expect(advance).toHaveBeenCalledOnce();
      expect(video.volume).toBe(0.8);
      // Until the item changes, the outgoing track's end does not advance again.
      expect(finishFadeAdvance()).toBe(true);
      // A level the listener sets now, even the fade's own last one, stays theirs.
      video.volume = 0.5;
      vi.advanceTimersByTime(LOAD_WAIT_MS);
      expect(video.volume).toBe(0.5);
      expect(finishFadeAdvance()).toBe(false);
      hidden.mockReturnValue(false);
      document.dispatchEvent(new Event("visibilitychange"));
      vi.advanceTimersByTime(TRACK_FADE_MS * 4);
      expect(advance).toHaveBeenCalledOnce();

      // Started while hidden, it is a cut.
      hidden.mockReturnValue(true);
      const later = vi.fn();
      fadeAdvance(playingElement(), later);
      expect(later).toHaveBeenCalledOnce();
    } finally {
      hidden.mockRestore();
    }
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
    vi.advanceTimersByTime(50);
    expect(advance).toHaveBeenCalledOnce();
    // Skipped early, it still owns what plays next: the outgoing track's end must not skip again.
    expect(finishFadeAdvance()).toBe(true);
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
    cancelFadeAdvance();
    vi.advanceTimersByTime(LOAD_WAIT_MS * 4);
    expect(advance).not.toHaveBeenCalled();
    expect(video.volume).toBe(0.8);
  });

  it("keeps fading in after its own skip changes the track", () => {
    const video = playingElement();
    fadeAdvance(video, () => cancelFadeAdvance());
    vi.advanceTimersByTime(TRACK_FADE_MS * 2);
    video.dispatchEvent(new Event("playing"));
    vi.advanceTimersByTime(TRACK_FADE_MS / 2);
    expect(video.volume).toBeGreaterThan(0);
    expect(video.volume).toBeLessThan(0.8);
    vi.advanceTimersByTime(TRACK_FADE_MS * 2);
    expect(video.volume).toBeCloseTo(0.8);
  });

  it("saves a level the listener sets while the next track loads", () => {
    const video = playingElement();
    fadeAdvance(video, () => {});
    vi.advanceTimersByTime(TRACK_FADE_MS * 2);
    video.volume = 0.3;
    expect(fadeBaseVolume(video)).toBe(0.3);
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
