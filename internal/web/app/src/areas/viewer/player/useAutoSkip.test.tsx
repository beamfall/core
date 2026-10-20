import { act, renderHook } from "@testing-library/react";
import { createRef } from "react";
import { describe, expect, it } from "vitest";
import type { PlaybackMarker } from "@/lib/graphql/types";
import { useAutoSkip } from "./useAutoSkip";

// A minimal fake <video> — just enough of the EventTarget + currentTime
// surface useAutoSkip drives (mirrors useMediaSession.test.tsx's FakeVideo).
class FakeVideo {
  currentTime = 0;
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

function tick(video: FakeVideo, currentTime: number) {
  act(() => video.emitTimeUpdate(currentTime));
}

const markers: PlaybackMarker[] = [
  { title: "Intro", startSeconds: 0, endSeconds: 30, segmentKind: "intro" },
];

type Props = Parameters<typeof useAutoSkip>[0];

function setup(over: Partial<Props> = {}) {
  const video = new FakeVideo();
  const ref = createRef<HTMLVideoElement>();
  (ref as { current: unknown }).current = video;
  const initialProps: Props = {
    videoRef: ref,
    markers,
    mediaID: "m1",
    prefs: { intro: true, recap: true, credits: true },
    ...over,
  } as Props;
  const view = renderHook((props: Props) => useAutoSkip(props), { initialProps });
  return { video, view, initialProps };
}

describe("useAutoSkip", () => {
  it("auto-seeks past the segment when the matching pref is on", () => {
    const { video, view } = setup();
    tick(video, 10);
    expect(video.currentTime).toBe(30);
    expect(view.result.current.skipped).toEqual(
      expect.objectContaining({ segmentKind: "intro", endSeconds: 30 }),
    );
  });

  it("does not seek when the matching pref is off", () => {
    const { video, view } = setup({ prefs: { intro: false, recap: true, credits: true } });
    tick(video, 10);
    expect(video.currentTime).toBe(10);
    expect(view.result.current.skipped).toBeNull();
  });

  it("Undo seeks back to the segment start and does not immediately re-fire", () => {
    const { video, view } = setup();
    tick(video, 10); // triggers the auto-skip to 30
    expect(video.currentTime).toBe(30);

    act(() => view.result.current.undo());
    expect(video.currentTime).toBe(0);
    expect(view.result.current.skipped).toBeNull();

    // The tick right after Undo's own seek is consumed (it's our seek, not a
    // fresh entry); the next real tick lands back inside the segment — this
    // must NOT re-trigger the skip.
    tick(video, 0);
    tick(video, 5);
    expect(video.currentTime).toBe(5);
    expect(view.result.current.skipped).toBeNull();
  });

  it("re-arms once the segment is genuinely exited without Undo, allowing a later rewatch to auto-skip again", () => {
    const { video, view } = setup();
    tick(video, 10); // auto-skip fires, jumps to 30
    tick(video, 31); // the tick from our own seek is ignored...
    tick(video, 35); // ...this one is a genuine exit — clears the guard

    // A manual rewind back into the same segment now auto-skips again.
    tick(video, 5);
    expect(video.currentTime).toBe(30);
    expect(view.result.current.skipped).toEqual(expect.objectContaining({ segmentKind: "intro" }));
  });

  it("clears the guard and overlay on a media change", () => {
    const { video, view, initialProps } = setup();
    tick(video, 10);
    expect(view.result.current.skipped).not.toBeNull();

    act(() => view.rerender({ ...initialProps, mediaID: "m2" }));
    expect(view.result.current.skipped).toBeNull();

    // The guard reset means the same segment shape on the new Media can
    // auto-skip immediately rather than staying suppressed from the old one.
    tick(video, 10);
    expect(video.currentTime).toBe(30);
  });
});
