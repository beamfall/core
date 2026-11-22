import "@testing-library/jest-dom/vitest";
import { act, fireEvent, render, renderHook, screen } from "@testing-library/react";
import { createRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { PlayingNextCard } from "./PlayingNextCard";
import { UpNextDrawer } from "./UpNextDrawer";
import {
  AMBIENT_IDLE_MS,
  AMBIENT_TRACK_WAKE_MS,
  useAmbientChoice,
  useAmbientIdle,
} from "./useAmbient";
import { usePlayingNext } from "./usePlayingNext";

// Now Playing neighbours, Playing next and Ambient mode (CORE-3216).

class FakeVideo {
  currentTime = 0;
  duration = 200;
  ended = false;
  private listeners = new Map<string, Set<() => void>>();
  addEventListener(type: string, cb: () => void) {
    if (!this.listeners.has(type)) this.listeners.set(type, new Set());
    this.listeners.get(type)?.add(cb);
  }
  removeEventListener(type: string, cb: () => void) {
    this.listeners.get(type)?.delete(cb);
  }
  emit(type: string) {
    for (const cb of this.listeners.get(type) ?? []) cb();
  }
  at(currentTime: number) {
    this.currentTime = currentTime;
    this.emit("timeupdate");
  }
}

function playingNext(enabled = true, itemKey = "a") {
  const video = new FakeVideo();
  const ref = createRef<HTMLVideoElement>();
  (ref as { current: unknown }).current = video;
  const view = renderHook(
    ({ key, on }) => usePlayingNext({ videoRef: ref, itemKey: key, enabled: on }),
    { initialProps: { key: itemKey, on: enabled } },
  );
  return { video, view };
}

describe("usePlayingNext", () => {
  it("shows over the final 12 seconds and reports what is left", () => {
    const { video, view } = playingNext();
    act(() => video.at(150));
    expect(view.result.current.visible).toBe(false);
    act(() => video.at(190));
    expect(view.result.current.visible).toBe(true);
    expect(view.result.current.remaining).toBe(10);
  });

  it("hides on a seek back out of the window and once the item ended", () => {
    const { video, view } = playingNext();
    act(() => video.at(195));
    act(() => video.at(20));
    expect(view.result.current.visible).toBe(false);
    act(() => video.at(199));
    video.ended = true;
    act(() => video.emit("ended"));
    expect(view.result.current.visible).toBe(false);
  });

  it("stays hidden for items shorter than the window and when disabled", () => {
    const short = playingNext();
    short.video.duration = 8;
    act(() => short.video.at(4));
    expect(short.view.result.current.visible).toBe(false);
    const off = playingNext(false);
    act(() => off.video.at(195));
    expect(off.view.result.current.visible).toBe(false);
  });

  it("dismisses for the rest of the item and re-arms on the next one", () => {
    const { video, view } = playingNext();
    act(() => video.at(195));
    act(() => view.result.current.dismiss());
    act(() => video.at(196));
    expect(view.result.current.visible).toBe(false);
    view.rerender({ key: "b", on: true });
    act(() => video.at(196));
    expect(view.result.current.visible).toBe(true);
  });
});

describe("PlayingNextCard", () => {
  it("names the region once and keeps the ticking seconds visual only", () => {
    const onPlay = vi.fn();
    const onDismiss = vi.fn();
    render(
      <I18nProvider initialLocale="en">
        <PlayingNextCard
          title="Closing Track"
          remaining={6.2}
          onPlay={onPlay}
          onDismiss={onDismiss}
        />
      </I18nProvider>,
    );
    const region = screen.getByRole("region", { name: "Playing next: Closing Track" });
    expect(region.querySelector(".bf-playing-next-ring")).toHaveAttribute("aria-hidden", "true");
    expect(region).toHaveTextContent("7");
    fireEvent.click(screen.getByRole("button", { name: "Play now" }));
    expect(onPlay).toHaveBeenCalledOnce();
    fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(onDismiss).toHaveBeenCalledOnce();
  });
});

describe("UpNextDrawer", () => {
  const items = [
    { id: "1", title: "One" },
    { id: "2", title: "Two" },
    { id: "3", title: "Three" },
    { id: "4", title: "Four" },
  ];

  it("lists played before, playing now and the rows still to come", () => {
    const onPick = vi.fn();
    const onClose = vi.fn();
    render(
      <I18nProvider initialLocale="en">
        <UpNextDrawer items={items} currentID="2" onPick={onPick} onClose={onClose} />
      </I18nProvider>,
    );
    const drawer = screen.getByRole("complementary", { name: "Up next list" });
    expect(drawer).toHaveTextContent("Played before");
    expect(screen.getByRole("button", { name: "Two" })).toHaveAttribute("aria-current", "true");
    expect(screen.getByRole("button", { name: "Two" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Four" }));
    expect(onPick).toHaveBeenCalledWith(items[3]);
    expect(screen.getByRole("button", { name: "Dismiss" })).toHaveFocus();
    fireEvent.keyDown(drawer, { key: "Escape" });
    expect(onClose).toHaveBeenCalledOnce();
  });
});

describe("Ambient mode", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    window.localStorage.clear();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("remembers the choice in this browser", () => {
    const first = renderHook(() => useAmbientChoice());
    expect(first.result.current[0]).toBe(false);
    act(() => first.result.current[1](true));
    expect(renderHook(() => useAmbientChoice()).result.current[0]).toBe(true);
    act(() => first.result.current[1](false));
    expect(renderHook(() => useAmbientChoice()).result.current[0]).toBe(false);
  });

  it("goes idle without input and wakes on any", () => {
    const view = renderHook(() => useAmbientIdle(true, "a"));
    act(() => vi.advanceTimersByTime(AMBIENT_IDLE_MS - 1));
    expect(view.result.current).toBe(false);
    act(() => vi.advanceTimersByTime(1));
    expect(view.result.current).toBe(true);
    act(() => {
      window.dispatchEvent(new Event("pointermove"));
    });
    expect(view.result.current).toBe(false);
  });

  it("wakes on a track change and settles again sooner", () => {
    const view = renderHook(({ key }) => useAmbientIdle(true, key), {
      initialProps: { key: "a" },
    });
    act(() => vi.advanceTimersByTime(AMBIENT_IDLE_MS));
    expect(view.result.current).toBe(true);
    view.rerender({ key: "b" });
    expect(view.result.current).toBe(false);
    act(() => vi.advanceTimersByTime(AMBIENT_TRACK_WAKE_MS));
    expect(view.result.current).toBe(true);
  });

  it("never reports idle while off", () => {
    const view = renderHook(() => useAmbientIdle(false, "a"));
    act(() => vi.advanceTimersByTime(AMBIENT_IDLE_MS * 2));
    expect(view.result.current).toBe(false);
  });
});
