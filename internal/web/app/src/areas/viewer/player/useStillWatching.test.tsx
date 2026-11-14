import "@testing-library/jest-dom/vitest";
import { act, fireEvent, render, renderHook, screen } from "@testing-library/react";
import { createRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import type { PlaybackNext } from "../lib/playback";
import {
  clearStillWatchingForTest,
  noteAutoAdvance,
  noteItemLeft,
  noteItemStarted,
  stillWatchingStreak,
} from "../lib/stillWatching";
import { StillWatchingPrompt } from "./StillWatchingPrompt";
import { useStillWatching } from "./useStillWatching";
import { useUpNext } from "./useUpNext";

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
  emit(type: string, currentTime = this.currentTime) {
    this.currentTime = currentTime;
    if (type === "ended") this.ended = true;
    this.listeners.get(type)?.forEach((cb) => cb());
  }
}

function nextItem(id: string): PlaybackNext {
  return { mediaID: id, title: `Item ${id}`, streams: [{ kind: "direct-play", url: `/m/${id}` }] };
}

function videoRef() {
  const video = new FakeVideo();
  const ref = createRef<HTMLVideoElement>();
  (ref as { current: unknown }).current = video;
  return { video, ref };
}

// Plays `ids` in order: the first picked by the viewer, the rest started by the
// Up Next countdown, as the player reports them.
function autoplayRun(ids: string[]) {
  noteItemStarted(ids[0]);
  for (const id of ids.slice(1)) {
    noteAutoAdvance(id);
    noteItemStarted(id);
  }
}

describe("still-watching streak", () => {
  beforeEach(() => clearStillWatchingForTest());

  it("counts only countdown-started items and ends the run on any other start", () => {
    autoplayRun(["1", "2", "3", "4"]);
    expect(stillWatchingStreak()).toBe(3);
    noteItemStarted("4");
    expect(stillWatchingStreak()).toBe(3);
    noteItemStarted("9");
    expect(stillWatchingStreak()).toBe(0);
  });
  it("keeps the run across a development double effect on the same item", async () => {
    autoplayRun(["1", "2"]);
    noteItemLeft("2");
    noteItemStarted("2");
    await Promise.resolve();
    expect(stillWatchingStreak()).toBe(1);
  });

  it("ends the run when the viewer leaves and reopens the same item", async () => {
    autoplayRun(["1", "2", "3"]);
    noteItemLeft("3");
    await Promise.resolve();
    noteItemStarted("3");
    expect(stillWatchingStreak()).toBe(0);
  });
});

describe("useStillWatching", () => {
  beforeEach(() => clearStillWatchingForTest());

  function setup(opts: { after?: number; enabled?: boolean; next?: PlaybackNext | null } = {}) {
    const { video, ref } = videoRef();
    const onContinue = vi.fn();
    const view = renderHook(() =>
      useStillWatching({
        videoRef: ref,
        mediaID: "4",
        after: opts.after ?? 3,
        next: opts.next === undefined ? nextItem("5") : opts.next,
        enabled: opts.enabled ?? true,
        onContinue,
      }),
    );
    return { video, view, onContinue };
  }

  it("with N = 3, asks at the end of the third autoplayed item instead of starting the next", () => {
    autoplayRun(["1", "2", "3", "4"]);
    const { video, view, onContinue } = setup();
    expect(view.result.current.armed).toBe(true);
    expect(view.result.current.prompting).toBe(false);

    act(() => video.emit("ended", 100));
    expect(view.result.current.prompting).toBe(true);
    expect(view.result.current.title).toBe("Item 5");

    act(() => view.result.current.continueWatching());
    expect(onContinue).toHaveBeenCalledWith(nextItem("5"));
    expect(view.result.current.prompting).toBe(false);
    expect(stillWatchingStreak()).toBe(0);
  });

  it("does not arm before the run reaches N", () => {
    autoplayRun(["1", "2", "3"]);
    const { video, view } = setup();
    expect(view.result.current.armed).toBe(false);
    act(() => video.emit("ended", 100));
    expect(view.result.current.prompting).toBe(false);
  });

  it("resets the run on a tap or a key", () => {
    autoplayRun(["1", "2", "3", "4"]);
    const { view } = setup();
    expect(view.result.current.armed).toBe(true);
    act(() => {
      fireEvent.keyDown(window, { key: "ArrowRight" });
    });
    expect(view.result.current.armed).toBe(false);

    autoplayRun(["4", "5", "6", "7"]);
    act(() => {
      fireEvent.pointerDown(window);
    });
    expect(stillWatchingStreak()).toBe(0);
  });

  it("Stop closes the prompt without starting the next item", () => {
    autoplayRun(["1", "2", "3", "4"]);
    const { video, view, onContinue } = setup();
    act(() => video.emit("ended", 100));
    act(() => view.result.current.stop());
    expect(view.result.current.prompting).toBe(false);
    expect(onContinue).not.toHaveBeenCalled();
  });

  it("never asks when the setting is off, nothing is queued, or the path has no countdown", () => {
    autoplayRun(["1", "2", "3", "4"]);
    for (const opts of [{ after: 0 }, { next: null }, { enabled: false }]) {
      const { video, view } = setup(opts);
      expect(view.result.current.armed).toBe(false);
      act(() => video.emit("ended", 100));
      expect(view.result.current.prompting).toBe(false);
      view.unmount();
    }
  });
});

describe("useUpNext onAutoAdvance", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("reports the countdown's own advance, not Play now", () => {
    const { video, ref } = videoRef();
    const onAdvance = vi.fn();
    const onAutoAdvance = vi.fn();
    const next = nextItem("2");
    const view = renderHook(() => useUpNext({ videoRef: ref, next, onAdvance, onAutoAdvance }));

    act(() => video.emit("timeupdate", 90));
    act(() => view.result.current.advance());
    expect(onAdvance).toHaveBeenCalledTimes(1);
    expect(onAutoAdvance).not.toHaveBeenCalled();

    act(() => video.emit("timeupdate", 91));
    act(() => vi.advanceTimersByTime(10_000));
    expect(onAutoAdvance).toHaveBeenCalledWith(next);
  });
});

describe("StillWatchingPrompt", () => {
  it("asks, names the next item and focuses Continue", () => {
    const onContinue = vi.fn();
    const onStop = vi.fn();
    render(
      <I18nProvider initialLocale="en">
        <StillWatchingPrompt title="Item 5" onContinue={onContinue} onStop={onStop} />
      </I18nProvider>,
    );
    const dialog = screen.getByRole("alertdialog", { name: "Are you still watching?" });
    expect(dialog).toHaveTextContent("Item 5");
    const proceed = screen.getByRole("button", { name: "Continue watching" });
    expect(proceed).toHaveFocus();
    fireEvent.click(proceed);
    fireEvent.click(screen.getByRole("button", { name: "Stop" }));
    expect(onContinue).toHaveBeenCalledTimes(1);
    expect(onStop).toHaveBeenCalledTimes(1);
  });
});
