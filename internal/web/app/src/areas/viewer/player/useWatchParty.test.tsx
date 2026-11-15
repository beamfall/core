import { fireEvent, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useWatchParty } from "./useWatchParty";
import { markViewerIntent } from "./viewerIntent";
import { leavePartyOnPageHide, sendPartyTransport, type WatchPartyState } from "./watchPartyStore";

let party: WatchPartyState;

vi.mock("./watchPartyStore", () => ({
  getWatchParty: () => party,
  useWatchPartyState: () => party,
  sendPartyTransport: vi.fn(async () => {}),
  startWatchParty: vi.fn(async () => true),
  leaveParty: vi.fn(async () => {}),
  leavePartyOnPageHide: vi.fn(),
}));

// A <video> stand-in: jsdom implements neither play() nor a settable clock.
class FakeVideo extends EventTarget {
  currentTime = 0;
  paused = true;
  readyState = 4;
  seeking = false;
  playbackRate = 1;
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

describe("useWatchParty local transport", () => {
  beforeEach(() => {
    vi.mocked(sendPartyTransport).mockClear();
  });

  afterEach(() => {
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
    expect(sendPartyTransport).toHaveBeenCalledWith("m1", 0, false);
    view.unmount();
  });

  it("sends the viewer's seek on a paused party", () => {
    party = activeParty({ item: "m1", positionMS: 0, presentAtMS: 0, paused: true });
    const video = new FakeVideo();
    const view = mount(video);
    fireEvent.keyDown(document.body, { key: "ArrowRight" });
    video.currentTime = 30;
    video.dispatchEvent(new Event("seeked"));
    expect(sendPartyTransport).toHaveBeenCalledWith("m1", 30_000, true);
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
    expect(sendPartyTransport).toHaveBeenCalledWith("m1", 45_000, true);
    view.unmount();
  });

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
    expect(sendPartyTransport).toHaveBeenCalledWith("m1", 0, false);
    view.unmount();
  });
});

describe("useWatchParty page exit", () => {
  it("leaves the party when the tab is closed or navigated away", () => {
    party = activeParty({ item: "m1", positionMS: 0, presentAtMS: 0, paused: true });
    const view = mount(new FakeVideo());
    window.dispatchEvent(new Event("pagehide"));
    expect(leavePartyOnPageHide).toHaveBeenCalledTimes(1);
    view.unmount();
    window.dispatchEvent(new Event("pagehide"));
    expect(leavePartyOnPageHide).toHaveBeenCalledTimes(1);
  });
});
