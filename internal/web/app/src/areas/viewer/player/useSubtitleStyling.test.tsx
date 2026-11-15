import { act, renderHook } from "@testing-library/react";
import Hls from "hls.js";
import { afterEach, describe, expect, it } from "vitest";
import { writeSync } from "./playbackSync";
import { useSubtitleStyling } from "./useSubtitleStyling";

afterEach(() => window.localStorage.clear());

type FakeCue = { startTime: number; endTime: number };

function fakeVideo(cues: FakeCue[]) {
  const track = { cues, mode: "showing" };
  const video = document.createElement("video");
  Object.defineProperty(video, "textTracks", { value: [track] });
  return { video, track };
}

function fakeHls(config: Record<string, unknown> = {}) {
  const handlers = new Map<string, () => void>();
  return {
    hls: {
      config,
      subtitleTracks: [{ name: "Track 1" }],
      on: (name: string, fn: () => void) => handlers.set(name, fn),
      off: (name: string) => handlers.delete(name),
    } as unknown as Hls,
    handlers,
  };
}

function render(video: HTMLVideoElement, hls: Hls | null, profileID = "p1", mediaID = "m1") {
  return renderHook(
    (props: { mediaID: string }) =>
      useSubtitleStyling({
        videoRef: { current: video },
        hlsRef: { current: hls },
        tracksVersion: 0,
        profileID,
        mediaID: props.mediaID,
      }),
    { initialProps: { mediaID } },
  );
}

describe("useSubtitleStyling sync", () => {
  it("supports the hls.js route when it renders native TextTracks", () => {
    const { video } = fakeVideo([{ startTime: 10, endTime: 12 }]);
    const { result } = render(video, fakeHls().hls);
    expect(result.current.syncSupported).toBe(true);
  });

  it("degrades when hls.js renders its own cue DOM", () => {
    const { video } = fakeVideo([{ startTime: 10, endTime: 12 }]);
    const { result } = render(video, fakeHls({ renderTextTracksNatively: false }).hls);
    expect(result.current.syncSupported).toBe(false);
  });

  it("shifts cues from their authored times and persists the offset", () => {
    const cue = { startTime: 10, endTime: 12 };
    const { video } = fakeVideo([cue]);
    const { result } = render(video, null);
    act(() => result.current.nudgeSync(0.5));
    act(() => result.current.nudgeSync(0.1));
    expect(cue.startTime).toBeCloseTo(10.6);
    expect(cue.endTime).toBeCloseTo(12.6);
    expect(result.current.syncOffset).toBe(0.6);
    expect(JSON.parse(window.localStorage.getItem("bf.playbackSync.p1.m1") ?? "{}")).toMatchObject({
      subtitleOffset: 0.6,
    });
    act(() => result.current.resetSync());
    expect(cue.startTime).toBe(10);
    expect(window.localStorage.getItem("bf.playbackSync.p1.m1")).toBeNull();
  });

  it("re-applies the offset to cues hls.js appends with each fragment", () => {
    writeSync("p1", "m1", { subtitleOffset: -1 });
    const cues: FakeCue[] = [{ startTime: 10, endTime: 12 }];
    const { video } = fakeVideo(cues);
    const { hls, handlers } = fakeHls();
    render(video, hls);
    expect(cues[0].startTime).toBe(9);
    const late = { startTime: 20, endTime: 22 };
    cues.push(late);
    act(() => handlers.get(Hls.Events.SUBTITLE_FRAG_PROCESSED)?.());
    expect(late.startTime).toBe(19);
    // Already-anchored cues stay put.
    expect(cues[0].startTime).toBe(9);
  });

  it("loads the next item's own offset", () => {
    writeSync("p1", "m2", { subtitleOffset: 2 });
    const { video } = fakeVideo([]);
    const { result, rerender } = render(video, null);
    expect(result.current.syncOffset).toBe(0);
    rerender({ mediaID: "m2" });
    expect(result.current.syncOffset).toBe(2);
  });

  it("clamps at the subtitle range", () => {
    writeSync("p1", "m1", { subtitleOffset: 59.8 });
    const { video } = fakeVideo([]);
    const { result } = render(video, null);
    act(() => result.current.nudgeSync(0.5));
    expect(result.current.syncOffset).toBe(60);
  });
});
