import { act, renderHook } from "@testing-library/react";
import { createRef } from "react";
import { describe, expect, it, vi } from "vitest";
import * as api from "@/lib/api";
import * as graphql from "@/lib/graphql";
import type { PlaybackMarker } from "@/lib/graphql/types";
import { useAutoSkip } from "./useAutoSkip";
import * as host from "./watchPartyStore";

vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  subscribeEvents: vi.fn(),
}));
vi.mock("@/lib/graphql", () => ({
  acceptWatchParty: vi.fn(),
  calibrateWatchPartyClock: vi.fn(),
  createWatchParty: vi.fn(),
  leaveWatchParty: vi.fn(async () => {}),
  watchPartyAutoSkip: vi.fn(),
  watchPartyTransport: vi.fn(),
}));

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

function setup(over: Partial<Props> = {}, useSkip = useAutoSkip) {
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
  const view = renderHook((props: Props) => useSkip(props), { initialProps });
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

  // CORE-1659: markers are song seconds; a stream second covers songScale of them.
  it("compares song time and seeks in stream time under a tempo", () => {
    const credits: PlaybackMarker[] = [
      { title: "Credits", startSeconds: 40, endSeconds: 60, segmentKind: "credits" },
    ];
    const { video, view } = setup({ markers: credits, songScale: 1.25 });
    tick(video, 30); // song 37.5: before the segment
    expect(video.currentTime).toBe(30);
    tick(video, 34); // song 42.5: inside it
    expect(video.currentTime).toBe(48); // song 60
    expect(view.result.current.skipped).toEqual(
      expect.objectContaining({ segmentKind: "credits", endSeconds: 60 }),
    );
    act(() => view.result.current.undo());
    expect(video.currentTime).toBe(32); // song 40
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

  it("can skip the same segment locally on the first tick after leaving a party", async () => {
    vi.mocked(api.subscribeEvents).mockReturnValueOnce(() => {});
    vi.mocked(graphql.calibrateWatchPartyClock).mockResolvedValueOnce({
      offsetMS: 0,
      roundTripMS: 1,
    });
    vi.mocked(graphql.createWatchParty).mockResolvedValueOnce({
      partyID: "p1",
      content: "m1",
      relaxed: true,
      members: [],
    });
    const skip = vi.mocked(graphql.watchPartyAutoSkip).mockClear().mockResolvedValueOnce({
      partyID: "p1",
      item: "m1",
      positionMS: 30_000,
      presentAtMS: 0,
      applied: true,
    });
    try {
      expect(await host.startWatchParty("m1")).toBe(true);
      const { video } = setup();
      await act(async () => video.emitTimeUpdate(10));
      expect(skip).toHaveBeenCalledTimes(1);

      await act(async () => host.leaveParty());
      tick(video, 11);
      expect(video.currentTime).toBe(30);
      expect(skip).toHaveBeenCalledTimes(1);
    } finally {
      host.resetWatchPartyForTest();
      vi.mocked(api.subscribeEvents).mockReset();
      skip.mockReset();
    }
  });

  it("lets two auto-skip-enabled party members watch the segment after Undo", async () => {
    const streams = new Set<Parameters<typeof api.subscribeEvents>[0]>();
    const stream = vi.spyOn(api, "subscribeEvents").mockImplementation((listener) => {
      streams.add(listener);
      return () => streams.delete(listener);
    });
    // Load a second store and hook as if the follower were in another browser.
    vi.resetModules();
    const follower = await import("./watchPartyStore");
    const { useAutoSkip: useFollowerAutoSkip } = await import("./useAutoSkip");
    const anchor = { partyID: "p1", item: "m1", positionMS: 0, presentAtMS: 0 };
    const publish = (positionMS: number, signal = "transition") => {
      const event = new MessageEvent("group.sync", {
        data: JSON.stringify({
          payload: { ...anchor, groupID: "p1", positionMS, paused: true, signal },
        }),
      });
      for (const listener of streams) listener(event);
      return { ...anchor, positionMS };
    };
    vi.spyOn(graphql, "calibrateWatchPartyClock").mockResolvedValue({
      offsetMS: 0,
      roundTripMS: 1,
    });
    vi.spyOn(graphql, "createWatchParty").mockResolvedValue({
      partyID: "p1",
      content: "m1",
      relaxed: true,
      members: [],
    });
    vi.spyOn(graphql, "acceptWatchParty").mockImplementation(async () => publish(0, "roster"));
    const skip = vi
      .spyOn(graphql, "watchPartyAutoSkip")
      .mockImplementation(async ({ positionMS }) => ({ ...publish(positionMS), applied: true }));
    vi.spyOn(graphql, "watchPartyTransport").mockImplementation(async ({ positionMS }) =>
      publish(positionMS),
    );
    try {
      expect(await host.startWatchParty("m1")).toBe(true);
      expect(await follower.joinWatchParty("p1")).toBe("m1");
      expect(follower.getWatchParty().isHost).toBe(false);
      expect(stream).toHaveBeenCalledTimes(2);
      const markers = [{ title: "Intro", startSeconds: 5, endSeconds: 30, segmentKind: "intro" }];
      const first = setup({ markers });
      const second = setup({ markers }, useFollowerAutoSkip);
      await act(async () => first.video.emitTimeUpdate(10));
      expect(first.view.result.current.skipped?.segmentKind).toBe("intro");
      expect(follower.getWatchParty().timeline?.positionMS).toBe(30_000);
      tick(second.video, 30);
      tick(second.video, 32);

      act(() => first.view.result.current.undo());
      expect(first.video.currentTime).toBe(5);
      // The browser's seeked event sends this rewind as ordinary party transport.
      await host.sendPartyTransport("m1", { positionMS: first.video.currentTime * 1000 });
      expect(follower.getWatchParty().timeline?.positionMS).toBe(5000);
      await act(async () => {
        first.video.emitTimeUpdate(5);
        first.video.emitTimeUpdate(6);
        second.video.emitTimeUpdate(5);
        second.video.emitTimeUpdate(6);
      });
      expect(skip).toHaveBeenCalledTimes(1);
      expect(follower.getWatchParty().timeline?.positionMS).toBe(5000);

      // A later entry from before the segment can skip again.
      publish(0);
      tick(second.video, 0);
      await act(async () => second.video.emitTimeUpdate(6));
      expect(skip).toHaveBeenCalledTimes(2);
    } finally {
      host.resetWatchPartyForTest();
      follower.resetWatchPartyForTest();
      vi.restoreAllMocks();
    }
  });
});
