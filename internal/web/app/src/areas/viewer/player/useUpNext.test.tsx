import "@testing-library/jest-dom/vitest";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { act, render, renderHook, screen } from "@testing-library/react";
import { createRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import type { PlaybackNext } from "../lib/playback";
import { UpNextCard } from "./UpNextCard";
import { useUpNext } from "./useUpNext";

// A minimal fake <video> — the EventTarget + time surface useUpNext drives
// (mirrors useAutoSkip.test.tsx's FakeVideo).
class FakeVideo {
  currentTime = 0;
  duration = 100;
  ended = false;
  private listeners = new Map<string, Set<() => void>>();
  addEventListener(type: string, cb: () => void) {
    (this.listeners.get(type) ?? this.listeners.set(type, new Set()).get(type)!).add(cb);
  }
  removeEventListener(type: string, cb: () => void) {
    this.listeners.get(type)?.delete(cb);
  }
  emitTimeUpdate(currentTime: number) {
    this.currentTime = currentTime;
    this.listeners.get("timeupdate")?.forEach((cb) => cb());
  }
}

const next: PlaybackNext = {
  mediaID: "media-next",
  title: "Next Episode",
  streams: [{ kind: "direct-play", url: "/api/media/next" }],
};

function setup() {
  const video = new FakeVideo();
  const ref = createRef<HTMLVideoElement>();
  (ref as { current: unknown }).current = video;
  const onAdvance = vi.fn();
  const view = renderHook(() => useUpNext({ videoRef: ref, next, onAdvance }));
  return { video, view, onAdvance };
}

function tick(video: FakeVideo, currentTime: number) {
  act(() => video.emitTimeUpdate(currentTime));
}

describe("useUpNext", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("keeps the card visible and clickable while chrome auto-hide is active", () => {
    // jsdom cannot parse the whole sheet, so load every rule whose selector
    // names the auto-hide state straight from the shipped stylesheet.
    const css = readFileSync(join(__dirname, "player-deferred.css"), "utf8");
    const autoHideRules = css.match(/[^{}]*\.is-chrome-hidden[^{}]*\{[^}]*\}/g) ?? [];
    expect(autoHideRules.length).toBeGreaterThan(0);
    const style = document.createElement("style");
    style.textContent = autoHideRules.join("\n");
    document.head.append(style);
    try {
      render(
        <I18nProvider initialLocale="en">
          <div className="bf-player-stage is-chrome-hidden">
            <div className="bf-transport" data-testid="transport" />
            <UpNextCard title="Next Episode" countdown={7} onPlay={() => {}} onDismiss={() => {}} />
          </div>
        </I18nProvider>,
      );
      const card = screen.getByRole("region", { name: "Up Next" });
      const transport = screen.getByTestId("transport");
      // The transport proves the hidden-chrome rule is in effect in this DOM...
      expect(getComputedStyle(transport).pointerEvents).toBe("none");
      expect(getComputedStyle(transport).opacity).toBe("0");
      // ...and the card is exempt from it.
      expect(getComputedStyle(card).pointerEvents).not.toBe("none");
      expect(getComputedStyle(card).opacity).not.toBe("0");
      expect(screen.getByRole("button", { name: "Dismiss" })).toBeVisible();
    } finally {
      style.remove();
    }
  });

  it("cancels the countdown when playback seeks back out of the end window", () => {
    const { video, view, onAdvance } = setup();
    tick(video, 90);
    expect(view.result.current.visible).toBe(true);
    act(() => vi.advanceTimersByTime(3000));
    expect(view.result.current.countdown).toBe(7);

    tick(video, 20);
    expect(view.result.current.visible).toBe(false);
    act(() => vi.advanceTimersByTime(20_000));
    expect(onAdvance).not.toHaveBeenCalled();

    // Undismissed: crossing the window again re-arms a fresh countdown.
    tick(video, 92);
    expect(view.result.current.visible).toBe(true);
    expect(view.result.current.countdown).toBe(10);
  });

  it("cancels from the interval tick when the seek lands between timeupdate events", () => {
    const { video, view, onAdvance } = setup();
    tick(video, 90);
    video.currentTime = 10;
    act(() => vi.advanceTimersByTime(1000));
    expect(view.result.current.visible).toBe(false);
    act(() => vi.advanceTimersByTime(20_000));
    expect(onAdvance).not.toHaveBeenCalled();
  });

  it("clears the countdown interval on unmount", () => {
    const clearInterval = vi.spyOn(window, "clearInterval");
    const { video, view, onAdvance } = setup();
    tick(video, 90);
    expect(view.result.current.visible).toBe(true);
    expect(vi.getTimerCount()).toBe(1);

    view.unmount();
    expect(clearInterval).toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
    act(() => vi.advanceTimersByTime(20_000));
    expect(onAdvance).not.toHaveBeenCalled();
  });
});
