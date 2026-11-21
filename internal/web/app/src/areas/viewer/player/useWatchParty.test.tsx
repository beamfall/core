import { act, fireEvent, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setDisplayTrimMS } from "./displayTrim";
import { useWatchParty } from "./useWatchParty";
import { markViewerIntent } from "./viewerIntent";
import {
  followLeadParty,
  pendingPartyTransport,
  reportLeadPosition,
  reportPartyDrift,
  resumeFollowedParty,
  sendPartyTransport,
  startWatchParty,
  type WatchPartyState,
  watchForLeadParty,
} from "./watchPartyStore";
import { followerCorrection, type PartyTimeline } from "./watchPartySync";

let party: WatchPartyState;

vi.mock("./watchPartyStore", () => ({
  getWatchParty: () => party,
  useWatchPartyState: () => party,
  sendPartyTransport: vi.fn(async () => true),
  startWatchParty: vi.fn(async () => true),
  leaveParty: vi.fn(async () => true),
  reportPartyDrift: vi.fn(() => true),
  reportLeadPosition: vi.fn(() => true),
  rememberLeadPauseIntent: vi.fn(),
  partyNowMS: () => Date.now() + party.offsetMS,
  followLeadParty: vi.fn(async () => null),
  resumeFollowedParty: vi.fn(async () => null),
  watchForLeadParty: vi.fn(() => () => {}),
  pendingPartyTransport: vi.fn(() => undefined),
  submittedPartyTitleSwitch: () => undefined,
  getPartyTransportEpoch: () => 1,
  PARTY_TRANSPORT_TIMEOUT_MS: 15_000,
}));

let homeNetwork = false;
vi.mock("../lib/watchTogether", () => ({ useHomeNetwork: () => homeNetwork }));

// A <video> stand-in: jsdom implements neither play() nor a settable clock.
class FakeVideo extends EventTarget {
  currentTime = 0;
  paused = true;
  readyState = 4;
  seeking = false;
  playbackRate = 1;
  muted = false;
  ended = false;
  duration = Number.NaN;
  play() {
    this.paused = false;
    this.dispatchEvent(new Event("play"));
    return Promise.resolve();
  }
  pause() {
    this.paused = true;
    this.dispatchEvent(new Event("pause"));
  }
}

function activeParty(timeline: WatchPartyState["timeline"]): WatchPartyState {
  return {
    status: "active",
    partyID: "party-1",
    isHost: false,
    lead: false,
    leadOffer: null,
    timeline,
    members: ["A", "B"],
    invited: [],
    offsetMS: 0,
    error: "",
    errorDetail: "",
    notice: "",
    noticeName: "",
    requests: [],
  };
}

function mount(video: FakeVideo) {
  const videoRef = { current: video as unknown as HTMLVideoElement };
  return renderHook(() =>
    useWatchParty({ videoRef, attached: true, mediaId: "m1", onFollow: () => {} }),
  );
}

function deferredTransport() {
  let resolve!: (applied: boolean) => void;
  const promise = new Promise<boolean>((finish) => {
    resolve = finish;
  });
  vi.mocked(sendPartyTransport).mockReturnValueOnce(promise);
  return resolve;
}

function samplesPlayhead(positionMS: number) {
  return expect.toSatisfy(
    (sample: unknown) => typeof sample === "function" && sample() === positionMS,
  );
}

function holdTransport(positionMS: number, seekProtectedUntil: number) {
  vi.mocked(pendingPartyTransport).mockReturnValue({
    itemId: "m1",
    positionMS,
    playing: false,
    epoch: 1,
    seekProtectedUntil,
  });
}

describe("useWatchParty local transport", () => {
  beforeEach(() => {
    vi.mocked(sendPartyTransport).mockClear();
    vi.mocked(pendingPartyTransport).mockReset();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("ignores the browser's autoplay on a paused join, even right after the Join click", () => {
    party = activeParty({ item: "m1", positionMS: 0, presentAtMS: 0, paused: true });
    const video = new FakeVideo();
    // The notification's Join click happens before the player mounts.
    fireEvent.pointerDown(document.body);
    const view = mount(video);
    video.play();
    expect(sendPartyTransport).not.toHaveBeenCalled();
    view.unmount();
  });

  it("sends the viewer's Play right after a corrective pause", () => {
    party = activeParty({ item: "m1", positionMS: 0, presentAtMS: 0, paused: true });
    const video = new FakeVideo();
    video.paused = false;
    // Mounting on a paused party pauses the playing video: a correction, not transport.
    const view = mount(video);
    expect(video.paused).toBe(true);
    expect(sendPartyTransport).not.toHaveBeenCalled();
    // The viewer presses Play straight after.
    fireEvent.pointerDown(document.body);
    video.play();
    expect(sendPartyTransport).toHaveBeenCalledWith("m1", {
      playheadMS: samplesPlayhead(0),
      playing: true,
    });
    view.unmount();
  });

  it("sends the viewer's seek on a paused party", () => {
    party = activeParty({ item: "m1", positionMS: 0, presentAtMS: 0, paused: true });
    const video = new FakeVideo();
    const view = mount(video);
    fireEvent.keyDown(document.body, { key: "ArrowRight" });
    video.currentTime = 30;
    video.dispatchEvent(new Event("seeked"));
    expect(sendPartyTransport).toHaveBeenCalledWith(
      "m1",
      { positionMS: 30_000 },
      expect.any(Number),
    );
    view.unmount();
  });

  it("sends a scrubber seek committed long after the press began", () => {
    party = activeParty({ item: "m1", positionMS: 0, presentAtMS: 0, paused: true });
    const video = new FakeVideo();
    let now = 10_000;
    vi.spyOn(performance, "now").mockImplementation(() => now);
    const view = mount(video);
    now += 100;
    fireEvent.pointerDown(document.body);
    // The viewer drags for three seconds; PlayerTransport's seek marks the intent.
    now += 3000;
    markViewerIntent();
    video.currentTime = 45;
    video.dispatchEvent(new Event("seeked"));
    expect(sendPartyTransport).toHaveBeenCalledWith(
      "m1",
      { positionMS: 45_000 },
      expect.any(Number),
    );
    view.unmount();
  });

  it("keeps a seek taking over three seconds until the party acknowledges it (CORE-1805)", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "performance"] });
    const timeline = { item: "m1", positionMS: 0, presentAtMS: 0, paused: true };
    party = { ...activeParty(timeline), isHost: true };
    const video = new FakeVideo();
    const view = mount(video);
    const finish = deferredTransport();
    vi.advanceTimersByTime(1);
    markViewerIntent();
    video.currentTime = 45;
    video.seeking = true;
    video.dispatchEvent(new Event("seeking"));
    act(() => vi.advanceTimersByTime(3100));
    expect(sendPartyTransport).not.toHaveBeenCalled();

    video.seeking = false;
    video.dispatchEvent(new Event("seeked"));
    expect(sendPartyTransport).toHaveBeenCalledExactlyOnceWith(
      "m1",
      { positionMS: 45_000 },
      15_001,
    );
    holdTransport(45_000, 15_001);
    act(() => vi.advanceTimersByTime(500));
    expect(video.currentTime).toBe(45);

    await act(async () => {
      party = { ...party, timeline: { ...timeline, positionMS: 45_000 } };
      vi.mocked(pendingPartyTransport).mockReturnValue(undefined);
      finish(true);
      view.rerender();
    });
    act(() => vi.advanceTimersByTime(500));
    expect(video.currentTime).toBe(45);
  });

  it("resumes drift correction when a seek receives no new anchor (CORE-1805)", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "performance"] });
    party = activeParty({ item: "m1", positionMS: 0, presentAtMS: 0, paused: true });
    const video = new FakeVideo();
    mount(video);
    vi.advanceTimersByTime(1);
    markViewerIntent();
    video.currentTime = 45;
    video.dispatchEvent(new Event("seeking"));
    await act(async () => video.dispatchEvent(new Event("seeked")));
    expect(sendPartyTransport).toHaveBeenCalledExactlyOnceWith(
      "m1",
      { positionMS: 45_000 },
      15_001,
    );
    holdTransport(45_000, 15_001);
    act(() => vi.advanceTimersByTime(500));
    expect(video.currentTime).toBe(45);
    act(() => vi.advanceTimersByTime(15_000));
    expect(video.currentTime).toBe(0);
  });

  it.each([0, 60])(
    "keeps the latest seek to %i seconds through an earlier acknowledgement (CORE-1805)",
    async (target) => {
      vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "performance"] });
      const timeline = { item: "m1", positionMS: 0, presentAtMS: 0, paused: true };
      party = { ...activeParty(timeline), isHost: true };
      const video = new FakeVideo();
      const view = mount(video);
      const resolveFirst = deferredTransport();
      const resolveSecond = deferredTransport();
      for (const seconds of [30, target]) {
        vi.advanceTimersByTime(1);
        markViewerIntent();
        video.currentTime = seconds;
        video.dispatchEvent(new Event("seeking"));
        video.dispatchEvent(new Event("seeked"));
        holdTransport(seconds * 1000, performance.now() + 15_000);
      }
      expect(vi.mocked(sendPartyTransport).mock.calls).toEqual([
        ["m1", { positionMS: 30_000 }, 15_001],
        ["m1", { positionMS: target * 1000 }, 15_002],
      ]);

      const publish = (positionMS: number) => {
        party = { ...party, timeline: { ...timeline, positionMS } };
        view.rerender();
      };
      await act(async () => {
        publish(30_000);
        resolveFirst(true);
      });
      act(() => vi.advanceTimersByTime(3100));
      expect(video.currentTime).toBe(target);

      await act(async () => {
        vi.mocked(pendingPartyTransport).mockReturnValue(undefined);
        publish(target * 1000);
        resolveSecond(true);
      });
      expect(video.currentTime).toBe(target);
      act(() => publish(70_000));
      expect(video.currentTime).toBe(70);
    },
  );

  it("ignores autoplay after an unrelated long touch gesture", () => {
    party = activeParty({ item: "m1", positionMS: 0, presentAtMS: 0, paused: true });
    const video = new FakeVideo();
    let now = 10_000;
    vi.spyOn(performance, "now").mockImplementation(() => now);
    const view = mount(video);
    now += 100;
    // A long scroll: the browser cancels the pointer and the touch ends later.
    fireEvent.pointerDown(document.body);
    fireEvent.pointerCancel(document.body);
    now += 3000;
    fireEvent.touchEnd(document.body);
    fireEvent.pointerUp(document.body);
    // The media becomes playable and the player autoplays.
    video.play();
    expect(sendPartyTransport).not.toHaveBeenCalled();
    view.unmount();
  });

  it("sends a play from the OS media controls", () => {
    party = activeParty({ item: "m1", positionMS: 0, presentAtMS: 0, paused: true });
    const video = new FakeVideo();
    const view = mount(video);
    // useMediaSession's play handler marks the intent, then plays.
    markViewerIntent();
    video.play();
    expect(sendPartyTransport).toHaveBeenCalledWith("m1", {
      playheadMS: samplesPlayhead(0),
      playing: true,
    });
    view.unmount();
  });
});

describe("useWatchParty drift and advance (CORE-1766)", () => {
  beforeEach(() => {
    vi.mocked(sendPartyTransport).mockClear();
    vi.mocked(reportPartyDrift).mockClear();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("hands the measured drift of a playing party to the bounded reporter", () => {
    vi.spyOn(Date, "now").mockReturnValue(20_000);
    // Started 10 s ago from 0: the shared position is 10 s; the player is 120 ms ahead.
    party = activeParty({ item: "m1", positionMS: 0, presentAtMS: 10_000, paused: false });
    const video = new FakeVideo();
    video.paused = false;
    video.currentTime = 10.12;
    const view = mount(video);
    expect(reportPartyDrift).toHaveBeenCalled();
    expect(vi.mocked(reportPartyDrift).mock.calls[0]?.[0]).toBeCloseTo(120, 0);
    view.unmount();
  });

  it("does not report while the party is paused", () => {
    party = activeParty({ item: "m1", positionMS: 0, presentAtMS: 0, paused: true });
    const view = mount(new FakeVideo());
    expect(reportPartyDrift).not.toHaveBeenCalled();
    view.unmount();
  });

  it("moves everyone when the host's player advances, and follows on a member's", () => {
    const videoRef = { current: new FakeVideo() as unknown as HTMLVideoElement };
    const onFollow = vi.fn();
    party = {
      ...activeParty({ item: "m1", positionMS: 0, presentAtMS: 0, paused: true }),
      isHost: true,
    };
    const initialProps: { mediaId: string; titleSelectionEpoch?: number } = { mediaId: "m1" };
    const host = renderHook(
      ({ mediaId, titleSelectionEpoch }) =>
        useWatchParty({ videoRef, attached: true, mediaId, titleSelectionEpoch, onFollow }),
      { initialProps },
    );
    // The host's Up Next takes its player to the next item.
    host.rerender({ mediaId: "m2", titleSelectionEpoch: 1 });
    expect(sendPartyTransport).toHaveBeenCalledWith("m2", {
      positionMS: 0,
      playing: true,
      selection: expect.any(AbortSignal),
    });
    expect(onFollow).not.toHaveBeenCalled();
    host.unmount();

    vi.mocked(sendPartyTransport).mockClear();
    party = activeParty({ item: "m2", positionMS: 0, presentAtMS: 0, paused: false });
    const guest = renderHook(() =>
      useWatchParty({ videoRef, attached: true, mediaId: "m1", onFollow }),
    );
    expect(onFollow).toHaveBeenCalledWith("m2");
    expect(sendPartyTransport).not.toHaveBeenCalled();
    guest.unmount();
  });
});

describe("useWatchParty lead party (CORE-2245)", () => {
  // A lead timeline that started 10 s ago from 0, read at server time 20 s.
  const running = (extra: Partial<PartyTimeline> = {}): PartyTimeline => ({
    item: "m1",
    positionMS: 0,
    presentAtMS: 10_000,
    paused: false,
    lead: true,
    ...extra,
  });
  const leading = (timeline: PartyTimeline): WatchPartyState => ({
    ...activeParty(timeline),
    lead: true,
    isHost: true,
  });
  const following = (timeline: PartyTimeline): WatchPartyState => ({
    ...activeParty(timeline),
    lead: true,
  });
  const idle = (extra: Partial<WatchPartyState> = {}): WatchPartyState => ({
    ...activeParty(null),
    status: "idle",
    partyID: "",
    ...extra,
  });

  function playing(atSeconds: number): FakeVideo {
    const video = new FakeVideo();
    video.paused = false;
    video.currentTime = atSeconds;
    return video;
  }

  beforeEach(() => {
    vi.clearAllMocks();
    homeNetwork = false;
    vi.useFakeTimers();
    vi.setSystemTime(20_000);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
    setDisplayTrimMS(0);
  });

  describe("lead", () => {
    it("never moves, mutes or re-times the lead's player", () => {
      party = leading(running());
      // Nowhere near its own timeline, and at its own speed.
      const video = playing(3);
      video.playbackRate = 1.5;
      const view = mount(video);
      act(() => {
        vi.advanceTimersByTime(600);
      });

      expect(video).toMatchObject({
        currentTime: 3,
        playbackRate: 1.5,
        paused: false,
        muted: false,
      });
      expect(view.result.current).toMatchObject({ playback: "playing", driftMS: null });
      expect(reportPartyDrift).not.toHaveBeenCalled();

      // A timeline that says paused does not pause it either, nor does leaving reset its speed.
      party = leading(running({ paused: true }));
      view.rerender();
      expect(video.paused).toBe(false);
      party = idle();
      view.rerender();
      expect(video.playbackRate).toBe(1.5);
      view.unmount();
    });

    it("reports its position about once a second while it plays", () => {
      party = leading(running());
      const video = playing(12.5);
      const view = mount(video);
      expect(reportLeadPosition).not.toHaveBeenCalled();

      act(() => {
        vi.advanceTimersByTime(1000);
      });
      expect(reportLeadPosition).toHaveBeenCalledTimes(1);
      const [positionMS, atMS, rate] = vi.mocked(reportLeadPosition).mock.calls[0] ?? [];
      expect(positionMS).toBe(12_500);
      expect(atMS).toBeCloseTo(performance.timeOrigin + performance.now(), 0);
      expect(rate).toBe(1);

      // Nothing while it is paused or seeking.
      video.paused = true;
      act(() => {
        vi.advanceTimersByTime(1000);
      });
      video.paused = false;
      video.seeking = true;
      act(() => {
        vi.advanceTimersByTime(1000);
      });
      expect(reportLeadPosition).toHaveBeenCalledTimes(1);

      // A speed change is reported at once.
      video.seeking = false;
      video.playbackRate = 1.25;
      video.dispatchEvent(new Event("ratechange"));
      expect(reportLeadPosition).toHaveBeenCalledTimes(2);
      expect(vi.mocked(reportLeadPosition).mock.calls[1]?.[2]).toBe(1.25);

      view.unmount();
      act(() => {
        vi.advanceTimersByTime(3000);
      });
      expect(reportLeadPosition).toHaveBeenCalledTimes(2);
    });

    it("reports the presented frame when one is fresh, and the element's clock when not", () => {
      party = leading(running());
      const video = playing(12.5) as FakeVideo & {
        requestVideoFrameCallback: (cb: VideoFrameRequestCallback) => number;
        cancelVideoFrameCallback: (handle: number) => void;
      };
      let onFrame: VideoFrameRequestCallback | null = null;
      video.requestVideoFrameCallback = (cb) => {
        onFrame = cb;
        return 7;
      };
      video.cancelVideoFrameCallback = vi.fn();
      const view = mount(video);

      act(() => {
        vi.advanceTimersByTime(990);
      });
      const shownAt = performance.now() + 5;
      (onFrame as VideoFrameRequestCallback | null)?.(performance.now(), {
        mediaTime: 12.48,
        expectedDisplayTime: shownAt,
      } as VideoFrameCallbackMetadata);
      act(() => {
        vi.advanceTimersByTime(10);
      });
      expect(reportLeadPosition).toHaveBeenLastCalledWith(
        12_480,
        performance.timeOrigin + shownAt,
        1,
      );

      // No frame for a second: the sample is stale.
      act(() => {
        vi.advanceTimersByTime(1000);
      });
      expect(vi.mocked(reportLeadPosition).mock.calls[1]?.[0]).toBe(12_500);

      view.unmount();
      expect(video.cancelVideoFrameCallback).toHaveBeenCalledWith(7);
    });

    it("sends the lead's own play and seek as transport", () => {
      party = leading(running({ paused: true }));
      const video = new FakeVideo();
      const view = mount(video);
      fireEvent.pointerDown(document.body);
      video.play();
      expect(sendPartyTransport).toHaveBeenCalledWith("m1", {
        playheadMS: samplesPlayhead(0),
        playing: true,
      });

      fireEvent.keyDown(document.body, { key: "ArrowRight" });
      video.currentTime = 30;
      video.dispatchEvent(new Event("seeked"));
      expect(sendPartyTransport).toHaveBeenLastCalledWith(
        "m1",
        { positionMS: 30_000 },
        expect.any(Number),
      );
      view.unmount();
    });

    it("passes the lead and host-only options through on start (CORE-1858)", async () => {
      party = idle();
      const view = mount(playing(42));

      await expect(view.result.current.start(false, true)).resolves.toBe(true);
      expect(startWatchParty).toHaveBeenCalledWith("m1", expect.any(Function), false, true);
      expect(sendPartyTransport).not.toHaveBeenCalled();

      await view.result.current.start(false);
      expect(startWatchParty).toHaveBeenLastCalledWith("m1", expect.any(Function), false, false);
      view.unmount();
    });
  });

  describe("follower", () => {
    it("mutes the player while it follows and gives the listener's mute back", () => {
      party = following(running());
      const video = new FakeVideo();
      video.currentTime = 10;
      const view = mount(video);

      expect(video).toMatchObject({ muted: true, paused: false, playbackRate: 1 });
      expect(view.result.current).toMatchObject({ playback: "playing", driftMS: 0 });

      // Unmuting does not last: the sound belongs to the lead.
      video.muted = false;
      video.currentTime = 10.1;
      act(() => {
        vi.advanceTimersByTime(100);
      });
      expect(video.muted).toBe(true);

      party = idle();
      view.rerender();
      expect(video.muted).toBe(false);
      view.unmount();
    });

    it("pauses a follower whose party ended under it before the sound comes back", () => {
      party = following(running());
      const video = new FakeVideo();
      video.currentTime = 10;
      const view = mount(video);
      expect(video).toMatchObject({ muted: true, paused: false });

      party = idle({ notice: "ended" });
      view.rerender();
      expect(video).toMatchObject({ muted: false, paused: true });
      view.unmount();
    });

    it("keeps a follower that chose to leave playing", () => {
      party = following(running());
      const video = new FakeVideo();
      video.currentTime = 10;
      const view = mount(video);

      party = idle();
      view.rerender();
      expect(video).toMatchObject({ muted: false, paused: false });
      view.unmount();
    });

    it("mutes a screen while it is still joining a lead party, and at once when unmuted", () => {
      party = { ...idle(), status: "joining", partyID: "party-1", lead: true };
      const video = new FakeVideo();
      video.paused = false;
      const view = mount(video);
      expect(video.muted).toBe(true);

      video.muted = false;
      video.dispatchEvent(new Event("volumechange"));
      expect(video.muted).toBe(true);
      view.unmount();
    });

    it("holds the party when the lead stalls and resumes it when the lead plays again", () => {
      party = leading(running());
      const video = new FakeVideo();
      video.paused = false;
      video.currentTime = 20;
      const view = mount(video);

      video.readyState = 2;
      video.dispatchEvent(new Event("waiting"));
      expect(sendPartyTransport).toHaveBeenLastCalledWith("m1", {
        playheadMS: samplesPlayhead(20_000),
        playing: false,
      });
      vi.mocked(reportLeadPosition).mockClear();
      act(() => {
        vi.advanceTimersByTime(1000);
      });
      expect(reportLeadPosition).not.toHaveBeenCalled();

      party = leading(running({ paused: true }));
      view.rerender();
      video.readyState = 4;
      video.dispatchEvent(new Event("playing"));
      expect(sendPartyTransport).toHaveBeenLastCalledWith("m1", {
        playheadMS: samplesPlayhead(20_000),
        playing: true,
      });
      view.unmount();
    });

    it("sends a lead pause that had no input behind it", () => {
      party = leading(running());
      const video = new FakeVideo();
      video.paused = false;
      video.currentTime = 20;
      const view = mount(video);
      video.pause();
      expect(sendPartyTransport).toHaveBeenLastCalledWith("m1", {
        playheadMS: samplesPlayhead(20_000),
        playing: false,
      });
      view.unmount();
    });

    it("leaves a player the listener had muted muted", () => {
      party = following(running());
      const video = playing(10);
      video.muted = true;
      const view = mount(video);
      view.unmount();
      expect(video.muted).toBe(true);
    });

    it("reports no drift, sends no position and takes no transport from the viewer", () => {
      party = following(running({ paused: true, positionMS: 7000 }));
      const video = playing(3);
      const view = mount(video);

      // Held on the lead's paused frame.
      expect(video).toMatchObject({ paused: true, currentTime: 7, muted: true });
      expect(view.result.current.playback).toBe("paused");

      fireEvent.pointerDown(document.body);
      video.play();
      fireEvent.keyDown(document.body, { key: "ArrowRight" });
      video.currentTime = 30;
      video.dispatchEvent(new Event("seeked"));
      expect(sendPartyTransport).not.toHaveBeenCalled();

      // The next beat, 100 ms on, puts it back.
      act(() => {
        vi.advanceTimersByTime(100);
      });
      expect(video).toMatchObject({ paused: true, currentTime: 7 });
      act(() => {
        vi.advanceTimersByTime(5000);
      });
      expect(sendPartyTransport).not.toHaveBeenCalled();
      expect(reportPartyDrift).not.toHaveBeenCalled();
      expect(reportLeadPosition).not.toHaveBeenCalled();
      view.unmount();
    });

    it("nudges the rate around the lead's own speed", () => {
      const timeline = running({ ratePermille: 1250 });
      party = following(timeline);
      // The lead is at 12.5 s; this screen is 60 ms ahead.
      const video = playing(12.56);
      const view = mount(video);

      const expected = followerCorrection(12.56 * 1000 - 12_500, 12_500, timeline);
      expect(expected.kind).toBe("rate");
      expect(video.playbackRate).toBe(expected.kind === "rate" ? expected.rate : 0);
      expect(video.playbackRate).toBeLessThan(1.25);
      expect(video.playbackRate).toBeGreaterThan(1.25 * 0.9 - 1e-9);
      expect(video.currentTime).toBe(12.56);

      // Back in step, it runs at the lead's speed.
      video.currentTime = 12.625 + 0.004;
      act(() => {
        vi.advanceTimersByTime(100);
      });
      expect(video.playbackRate).toBe(1.25);
      view.unmount();
    });

    it("re-seeks a drift over 250 ms and aims the next seek ahead by what the last one missed", () => {
      party = following(running());
      const video = playing(9);
      const view = mount(video);
      expect(video.currentTime).toBe(10);

      // The seek took 100 ms: the picture landed that far behind.
      act(() => {
        vi.advanceTimersByTime(100);
      });
      expect(video.currentTime).toBe(10);
      expect(video.playbackRate).toBeGreaterThan(1);

      video.currentTime = 5;
      act(() => {
        vi.advanceTimersByTime(100);
      });
      expect(video.currentTime).toBeCloseTo(10.3, 6);
      expect(video.playbackRate).toBe(1);
      view.unmount();
    });

    it("shows the picture early by this screen's display delay", () => {
      setDisplayTrimMS(50);
      party = following(running());
      const video = playing(10.05);
      const view = mount(video);
      expect(video.playbackRate).toBe(1);
      expect(view.result.current.driftMS).toBe(0);

      // Without the trim's 50 ms it is behind, and speeds up.
      video.currentTime = 10.1;
      act(() => {
        vi.advanceTimersByTime(100);
      });
      expect(video.playbackRate).toBeGreaterThan(1);
      view.unmount();
    });

    it("does not seek past the end of the title", () => {
      party = following(running());
      const video = playing(4);
      video.duration = 8;
      const view = mount(video);
      expect(video.currentTime).toBe(8);
      view.unmount();
    });
  });

  describe("joining", () => {
    it("rejoins a followed party when the player opens", () => {
      party = idle();
      const view = mount(new FakeVideo());
      expect(resumeFollowedParty).toHaveBeenCalledTimes(1);
      view.unmount();
    });

    it("listens for a lead party only on the home network", () => {
      party = idle();
      const away = mount(new FakeVideo());
      expect(watchForLeadParty).not.toHaveBeenCalled();
      away.unmount();

      const stop = vi.fn();
      vi.mocked(watchForLeadParty).mockReturnValue(stop);
      homeNetwork = true;
      const home = mount(new FakeVideo());
      expect(watchForLeadParty).toHaveBeenCalledTimes(1);
      expect(stop).not.toHaveBeenCalled();
      home.unmount();
      expect(stop).toHaveBeenCalledTimes(1);
    });

    it("follows an offered lead party unasked only when it plays this title and was not declined", () => {
      party = idle({ leadOffer: { partyID: "p9", item: "m2", declined: false } });
      const other = mount(new FakeVideo());
      other.unmount();
      party = idle({ leadOffer: { partyID: "p9", item: "m1", declined: true } });
      const declined = mount(new FakeVideo());
      declined.unmount();
      expect(followLeadParty).not.toHaveBeenCalled();

      party = idle({ leadOffer: { partyID: "p9", item: "m1", declined: false } });
      const view = mount(new FakeVideo());
      expect(followLeadParty).toHaveBeenCalledTimes(1);
      expect(followLeadParty).toHaveBeenCalledWith("p9", true);
      view.unmount();
    });
  });
});
