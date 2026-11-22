import "@testing-library/jest-dom/vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import Hls from "hls.js";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  ApiDeadlineError,
  graphql,
  setIdentityEpochReset,
  setIdentityEpochSignal,
} from "@/lib/api";
import { fetchPlaybackMarkers } from "@/lib/graphql";
import type { PlaybackMarker } from "@/lib/graphql/types";
import { ApiError, fetchPlaybackInfo } from "../lib/playback";
import type { StartTracks } from "../lib/watchQueue";
import { deliverPlayerCommand, resetAppliedControlEvents } from "../remote/remoteControl";
import { DEFAULT_ADJUSTMENT, selectHlsLevel, selectHlsRendition, usePlayer } from "./usePlayer";

vi.mock("@/lib/graphql", () => ({
  fetchPlaybackMarkers: vi.fn().mockResolvedValue([]),
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    graphql: vi.fn().mockResolvedValue({ reportPlayback: { mediaID: "media-alpha" } }),
  };
});

const resetIdentityEpoch = vi.fn();

/**
 * Drive a terminal response and observe the production reset seam. The
 * SessionProvider integration test exercises the registered callback itself,
 * including aborting the old epoch and reloading session/manifest state.
 */
async function expectEpochReset(terminate: () => void): Promise<void> {
  const resets = resetIdentityEpoch.mock.calls.length;
  terminate();
  await waitFor(() => expect(resetIdentityEpoch).toHaveBeenCalledTimes(resets + 1));
}

vi.mock("../lib/playback", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/playback")>();
  return {
    ...actual,
    fetchPlaybackInfo: vi.fn(),
  };
});

const { fakeHlsInstances } = vi.hoisted(() => ({
  fakeHlsInstances: [] as Array<{
    emitError: (data: unknown) => void;
    emit: (event: string, data?: unknown) => void;
    destroyed: () => boolean;
    tracks: {
      subtitleTrack: number;
      audioTrack: number;
      currentLevel: number;
      levels: Array<{ height?: number; bitrate?: number }>;
      subtitleTracks: Array<{ lang?: string; name?: string }>;
      audioTracks: Array<{ lang?: string; name?: string }>;
    };
    emitFragBuffered: (frag?: { start: number; duration: number }) => void;
    startLoad: ReturnType<typeof vi.fn>;
    stopLoad: ReturnType<typeof vi.fn>;
    recoverMediaError: ReturnType<typeof vi.fn>;
    swapAudioCodec: ReturnType<typeof vi.fn>;
    config: { startPosition: number };
    sources: string[];
  }>,
}));

vi.mock("hls.js", () => {
  class FakeHls {
    static Events = {
      ERROR: "error",
      FRAG_BUFFERED: "fragBuffered",
      MANIFEST_PARSED: "manifestParsed",
      AUDIO_TRACKS_UPDATED: "audioTracksUpdated",
      SUBTITLE_TRACKS_UPDATED: "subtitleTracksUpdated",
    };
    static isSupported = vi.fn(() => false);
    private handlers = new Map<string, Array<(event: string, data: unknown) => void>>();
    private isDestroyed = false;
    subtitleTrack = -1;
    // hls.js: assigning currentLevel makes it the manual level (-1 is Auto).
    currentLevel = -1;
    levels: Array<{ height?: number; bitrate?: number }> = [];
    get manualLevel() {
      return this.currentLevel;
    }
    subtitleDisplay = false;
    audioTrack = -1;
    subtitleTracks: Array<{ lang?: string; name?: string }> = [];
    audioTracks: Array<{ lang?: string; name?: string }> = [];
    startLoad = vi.fn();
    stopLoad = vi.fn();
    recoverMediaError = vi.fn();
    swapAudioCodec = vi.fn();
    config: { startPosition: number };
    sources: string[] = [];
    private nextFragStart = 0;

    constructor(userConfig?: { startPosition?: number }) {
      this.config = { startPosition: userConfig?.startPosition ?? -1 };
      const emit = (event: string, data?: unknown) =>
        this.handlers.get(event)?.forEach((handler) => handler(event, data));
      fakeHlsInstances.push({
        emitError: (data: unknown) => emit("error", data),
        emit,
        destroyed: () => this.isDestroyed,
        tracks: this,
        // Each call without a frag buffers the next 4s fragment: forward progress.
        emitFragBuffered: (frag?: { start: number; duration: number }) => {
          const next = frag ?? { start: this.nextFragStart, duration: 4 };
          this.nextFragStart = Math.max(this.nextFragStart, next.start + next.duration);
          emit("fragBuffered", { frag: next });
        },
        startLoad: this.startLoad,
        stopLoad: this.stopLoad,
        recoverMediaError: this.recoverMediaError,
        swapAudioCodec: this.swapAudioCodec,
        config: this.config,
        sources: this.sources,
      });
    }
    on(event: string, handler: (event: string, data: unknown) => void) {
      (this.handlers.get(event) ?? this.handlers.set(event, []).get(event)!).push(handler);
    }
    loadSource(url: string) {
      this.sources.push(url);
    }
    attachMedia() {}
    destroy() {
      this.isDestroyed = true;
    }
  }
  return { default: FakeHls };
});

const messages = {
  couldNotReachServer: "server unreachable",
  playbackUnavailable: "playback unavailable",
  transcodedPlaybackUnsupported: "transcoded unsupported",
  playbackUnsupportedCodec: "unsupported codec",
  playbackFailed: "playback failed",
  terminalUnavailable: "terminal unavailable",
  mediaNotAvailable: "media not available",
  streamLimitReached: "stream limit reached",
  transcodeDisallowed: "transcode disallowed",
  sourceEndsEarly: "source ends early",
};

function Harness({
  mediaID = "media-alpha",
  startAtSeconds,
  nextMediaID,
  selectedVersion,
  startTracks,
  startID,
}: {
  mediaID?: string;
  startAtSeconds?: number;
  nextMediaID?: string;
  selectedVersion?: string;
  startTracks?: StartTracks;
  startID?: string;
}) {
  const { videoRef, state, retry, restart, switchToHls, adjust } = usePlayer(
    mediaID,
    messages,
    startAtSeconds,
    nextMediaID,
    selectedVersion,
    startTracks,
    startID,
  );
  return (
    <>
      <video data-testid="player" ref={videoRef} />
      <output data-testid="stream-kinds">
        {(state.streams ?? []).map((s) => s.kind).join(",")}
      </output>
      <output data-testid="active-kind">{state.stream?.kind ?? ""}</output>
      <output data-testid="hls-active">{state.hlsActive ? "yes" : "no"}</output>
      <output data-testid="switch-result" />
      <button
        type="button"
        onClick={() => {
          const out = screen.getByTestId("switch-result");
          out.textContent = String(switchToHls("720"));
        }}
      >
        switch-hls
      </button>
      <output data-testid="phase">{state.phase}</output>
      <output data-testid="message">{state.message}</output>
      <output data-testid="recovering">{state.recovering ? "yes" : "no"}</output>
      <output data-testid="waiting-for-network">{state.waitingForNetwork ? "yes" : "no"}</output>
      <output data-testid="action">{state.action ?? ""}</output>
      <output data-testid="detail">{state.detail ?? ""}</output>
      <button type="button" onClick={() => retry()}>
        retry
      </button>
      <button
        type="button"
        onClick={() => {
          const video = screen.getByTestId("player") as HTMLVideoElement;
          retry({ paused: video.paused });
        }}
      >
        renegotiate
      </button>
      <button type="button" onClick={() => adjust({ pitchShiftSemitones: 1, tempo: 0.8 })}>
        adjust
      </button>
      <button type="button" onClick={() => restart(0)}>
        restart
      </button>
      {/* The revealed playback document, surfaced so a test can assert it stops
          being addressable once the reveal goes terminal. */}
      <output data-testid="stream-url">{state.stream?.url ?? ""}</output>
      <output data-testid="terminal-flag">{state.terminal ? "yes" : "no"}</output>
      <output data-testid="info">{state.info ? "present" : "none"}</output>
      <output data-testid="decision">{state.decision}</output>
      <output data-testid="info-decision">{state.info?.decision ?? ""}</output>
      <output data-testid="markers">{state.markers.length}</output>
      <output data-testid="up-next">{state.info?.next?.mediaID ?? ""}</output>
    </>
  );
}

function capturePendingProgress() {
  const requests: Array<{
    variables: Record<string, unknown>;
    signal: AbortSignal;
    deadline: () => void;
    resolve: () => void;
  }> = [];
  let active = 0;
  let maxActive = 0;
  vi.mocked(graphql).mockImplementation((_query, variables, signal) => {
    return new Promise((resolve, reject) => {
      const requestVariables = variables ?? {};
      const requestSignal = signal as AbortSignal;
      let settled = false;
      active += 1;
      maxActive = Math.max(maxActive, active);
      const finish = (settle: () => void) => {
        if (settled) return;
        settled = true;
        active -= 1;
        settle();
      };
      requestSignal.addEventListener("abort", () => finish(() => reject(requestSignal.reason)), {
        once: true,
      });
      requests.push({
        variables: requestVariables,
        signal: requestSignal,
        deadline: () => reject(new ApiDeadlineError("/api/graphql", 30_000)),
        resolve: () =>
          finish(() => resolve({ reportPlayback: { mediaID: requestVariables.mediaID } } as never)),
      });
    });
  });
  return {
    requests,
    active: () => active,
    maxActive: () => maxActive,
  };
}

describe("usePlayer", () => {
  beforeEach(() => {
    vi.mocked(fetchPlaybackMarkers).mockResolvedValue([]);
    vi.mocked(graphql).mockResolvedValue({ reportPlayback: { mediaID: "media-alpha" } });
    resetIdentityEpoch.mockReset();
    setIdentityEpochReset(resetIdentityEpoch);
    fakeHlsInstances.length = 0;
    // Drop any once-queued answer a test left unconsumed.
    vi.mocked(fetchPlaybackInfo).mockReset();
    vi.mocked(fetchPlaybackInfo).mockResolvedValue({
      decision: "direct-play",
      streams: [{ kind: "direct-play", url: "/api/media/direct/token" }],
    });
    vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue(undefined);
    vi.spyOn(HTMLMediaElement.prototype, "load").mockImplementation(() => {});
    // jsdom never loads media, so readyState stays HAVE_NOTHING; progress only
    // counts presented frames, so default to a loaded element.
    vi.spyOn(HTMLMediaElement.prototype, "readyState", "get").mockReturnValue(
      HTMLMediaElement.HAVE_ENOUGH_DATA,
    );
  });

  afterEach(() => {
    setIdentityEpochReset();
    setIdentityEpochSignal();
    vi.restoreAllMocks();
  });

  it("sends eventTimeMS and the paused state with playback progress reports", async () => {
    vi.spyOn(Date, "now").mockReturnValue(1783432800123);
    vi.spyOn(performance, "now").mockReturnValue(1000);
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));

    const video = screen.getByTestId("player") as HTMLVideoElement;
    Object.defineProperty(video, "duration", { configurable: true, value: 120 });
    Object.defineProperty(video, "currentTime", {
      configurable: true,
      value: 45.9,
    });
    fireEvent.pause(video);

    await waitFor(() => expect(graphql).toHaveBeenCalled());
    expect(vi.mocked(graphql).mock.calls.at(-1)?.[1]).toEqual({
      mediaID: "media-alpha",
      positionSeconds: 45,
      durationSeconds: 120,
      finished: false,
      eventTimeMS: 1783432800123,
      paused: true,
      playbackID: expect.stringMatching(/^[0-9a-f]{32}$/),
    });
    const playbackID = vi.mocked(graphql).mock.calls.at(-1)?.[1]?.playbackID;

    // Resuming reports at once and says the stream is playing again, so the
    // server credits the time from here on and none from the pause. The play
    // keeps its playback ID.
    const calls = vi.mocked(graphql).mock.calls.length;
    Object.defineProperty(video, "paused", { configurable: true, value: false });
    fireEvent.play(video);
    await waitFor(() => expect(vi.mocked(graphql).mock.calls.length).toBeGreaterThan(calls));
    expect(vi.mocked(graphql).mock.calls.at(-1)?.[1]).toMatchObject({ paused: false, playbackID });
  });

  it("never lets a later report replace a pause queued behind a stalled one (CORE-1703)", async () => {
    // The event clock is monotonic across the file; keep it where its tests expect.
    vi.spyOn(Date, "now").mockReturnValue(1783432800123);
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    const progress = capturePendingProgress();
    const video = screen.getByTestId("player") as HTMLVideoElement;
    Object.defineProperty(video, "duration", { configurable: true, value: 3600 });
    Object.defineProperty(video, "seeking", { configurable: true, value: false });
    Object.defineProperty(video, "paused", { configurable: true, value: false });
    Object.defineProperty(video, "currentTime", { configurable: true, value: 300 });
    fireEvent.play(video);
    await waitFor(() => expect(progress.requests).toHaveLength(1));

    // The playing report stalls; the viewer pauses, then seeks while paused.
    Object.defineProperty(video, "paused", { configurable: true, value: true });
    fireEvent.pause(video);
    Object.defineProperty(video, "currentTime", { configurable: true, value: 1500 });
    fireEvent.seeked(video);
    const sent = () =>
      progress.requests.map(({ variables }) => [variables.positionSeconds, variables.paused]);

    progress.requests[0].resolve();
    await waitFor(() => expect(progress.requests).toHaveLength(2));
    progress.requests[1].resolve();
    await waitFor(() => expect(progress.requests).toHaveLength(3));
    expect(sent()).toEqual([
      [300, false],
      [300, true],
      [1500, true],
    ]);
  });

  // stallPlayingReport opens the player, plays from 300 and leaves that report
  // stalled, returning the captured requests, the video, a way to fire the
  // periodic heartbeat at a position, and what was sent so far.
  async function stallPlayingReport() {
    vi.spyOn(Date, "now").mockReturnValue(1783432800123);
    const intervals = vi.spyOn(window, "setInterval");
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    const progress = capturePendingProgress();
    const video = screen.getByTestId("player") as HTMLVideoElement;
    const set = (currentTime: number, paused: boolean) => {
      Object.defineProperty(video, "currentTime", { configurable: true, value: currentTime });
      Object.defineProperty(video, "paused", { configurable: true, value: paused });
    };
    Object.defineProperty(video, "duration", { configurable: true, value: 3600 });
    Object.defineProperty(video, "seeking", { configurable: true, value: false });
    set(300, false);
    fireEvent.play(video);
    await waitFor(() => expect(progress.requests).toHaveLength(1));
    const heartbeat = (currentTime: number) => {
      set(currentTime, false);
      const beacon = intervals.mock.calls.find(([, delay]) => delay === 15000)?.[0];
      (beacon as () => void)();
    };
    // Resolves each request as it goes out until `count` have been sent.
    const drain = async (count: number) => {
      for (let i = 0; i < count; i++) {
        await waitFor(() => expect(progress.requests.length).toBeGreaterThan(i));
        progress.requests[i].resolve();
      }
    };
    const sent = () =>
      progress.requests.map(({ variables }) => [
        variables.positionSeconds,
        variables.paused,
        variables.finished,
      ]);
    return { progress, video, set, heartbeat, drain, sent };
  }

  it("sends a pause, resume and heartbeat made during a stalled report in order (CORE-1703)", async () => {
    const { progress, video, set, heartbeat, drain, sent } = await stallPlayingReport();
    set(300, true);
    fireEvent.pause(video);
    set(300, false);
    fireEvent.play(video);
    heartbeat(314);
    await drain(4);
    expect(sent()).toEqual([
      [300, false, false],
      [300, true, false],
      [300, false, false],
      [314, false, false],
    ]);
    expect(new Set(progress.requests.map(({ variables }) => variables.playbackID)).size).toBe(1);
    expect(progress.maxActive()).toBe(1);
  });

  it("sends a paused seek, play and finish made during a stalled report in order (CORE-1703)", async () => {
    const { video, set, drain, sent } = await stallPlayingReport();
    set(300, true);
    fireEvent.pause(video);
    set(3599, true);
    fireEvent.seeked(video);
    set(3599, false);
    fireEvent.play(video);
    set(3600, false);
    fireEvent.ended(video);
    await drain(5);
    expect(sent()).toEqual([
      [300, false, false],
      [300, true, false],
      [3599, true, false],
      [3599, false, false],
      [3600, false, true],
    ]);
  });

  it("merges only a heartbeat into the heartbeat queued just before it (CORE-1703)", async () => {
    const { video, set, heartbeat, drain, sent } = await stallPlayingReport();
    heartbeat(315);
    heartbeat(330);
    set(330, true);
    fireEvent.pause(video);
    set(330, false);
    fireEvent.play(video);
    heartbeat(345);
    heartbeat(360);
    await drain(5);
    expect(sent()).toEqual([
      [300, false, false],
      [330, false, false],
      [330, true, false],
      [330, false, false],
      [360, false, false],
    ]);
  });

  it("sends a same-item reload's queued reports in order before the reload's own (CORE-1703)", async () => {
    // Codex CORE-1703 r4: a pause and a paused seek queued behind a stalled
    // report must survive a quality change, which keeps the playback ID, and
    // go out before anything the reloaded stream reports.
    const { progress, video, set, drain, sent } = await stallPlayingReport();
    set(300, true);
    fireEvent.pause(video);
    set(900, true);
    fireEvent.seeked(video);
    fireEvent.click(screen.getByRole("button", { name: "renegotiate" }));
    await waitFor(() => expect(fetchPlaybackInfo).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    set(900, false);
    fireEvent.play(video);
    await drain(4);
    expect(sent()).toEqual([
      [300, false, false],
      [300, true, false],
      [900, true, false],
      [900, false, false],
    ]);
    expect(progress.requests.every(({ signal }) => !signal?.aborted)).toBe(true);
    expect(new Set(progress.requests.map(({ variables }) => variables.playbackID)).size).toBe(1);
    expect(progress.maxActive()).toBe(1);
  });

  it("starts a new play when an ended item is played again in place (CORE-1703)", async () => {
    // Codex CORE-1703 r4: Play on a finished item calls video.play() on the
    // same element. That is a second play: a new playback ID, reports
    // accepted again after the finish, and the beacon running again.
    const intervals = vi.spyOn(window, "setInterval");
    const progress = capturePendingProgress();
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    const video = screen.getByTestId("player") as HTMLVideoElement;
    const set = (currentTime: number, paused: boolean) => {
      Object.defineProperty(video, "currentTime", { configurable: true, value: currentTime });
      Object.defineProperty(video, "paused", { configurable: true, value: paused });
    };
    Object.defineProperty(video, "duration", { configurable: true, value: 120 });
    Object.defineProperty(video, "seeking", { configurable: true, value: false });
    set(120, true);
    fireEvent.ended(video);
    await waitFor(() => expect(progress.requests).toHaveLength(1));
    progress.requests[0].resolve();
    const beacons = () => intervals.mock.calls.filter(([, delay]) => delay === 15000);
    const beaconsBefore = beacons().length;

    set(0, false);
    fireEvent.play(video);
    await waitFor(() => expect(progress.requests).toHaveLength(2));
    progress.requests[1].resolve();
    expect(beacons().length).toBe(beaconsBefore + 1);
    set(15, false);
    (beacons().at(-1)?.[0] as () => void)();
    await waitFor(() => expect(progress.requests).toHaveLength(3));
    const [finish, replay, beat] = progress.requests.map(({ variables }) => variables);
    expect(finish).toMatchObject({ positionSeconds: 120, finished: true });
    expect(replay).toMatchObject({ positionSeconds: 0, finished: false, paused: false });
    expect(beat).toMatchObject({ positionSeconds: 15, finished: false });
    expect(replay.playbackID).not.toBe(finish.playbackID);
    expect(beat.playbackID).toBe(replay.playbackID);
  });

  it("reports the probed duration, not a live transcode's growing video.duration (CORE-2314)", async () => {
    vi.mocked(fetchPlaybackInfo).mockResolvedValue({
      decision: "direct-play",
      streams: [{ kind: "direct-play", url: "/api/media/direct/token" }],
      timeline: { durationSeconds: 7260 },
    });
    // The event clock is monotonic across the file; keep it where its tests expect.
    vi.spyOn(Date, "now").mockReturnValue(1783432800123);
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));

    const video = screen.getByTestId("player") as HTMLVideoElement;
    // Only 268 s encoded so far: the stream's own duration is short.
    Object.defineProperty(video, "duration", { configurable: true, value: 268 });
    Object.defineProperty(video, "currentTime", { configurable: true, value: 180 });
    fireEvent.pause(video);

    await waitFor(() => expect(graphql).toHaveBeenCalled());
    expect(vi.mocked(graphql).mock.calls.at(-1)?.[1]).toMatchObject({
      positionSeconds: 180,
      durationSeconds: 7260,
    });
  });

  it("ends the playback session on unmount and on pagehide (CORE-2305)", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("{}"));
    const endCalls = () =>
      fetchSpy.mock.calls
        .filter(
          ([url, init]) => url === "/api/graphql" && String(init?.body).includes("endPlayback"),
        )
        .map(([, init]) => init);
    const { unmount } = render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));

    window.dispatchEvent(new Event("pagehide"));
    expect(endCalls()).toHaveLength(1);
    expect(endCalls()[0]).toMatchObject({ method: "POST", keepalive: true });
    expect(JSON.parse(String(endCalls()[0]?.body))).toEqual({
      query: expect.stringContaining("endPlayback(mediaId: $mediaId)"),
      variables: { mediaId: "media-alpha" },
    });

    // pagehide already ended it, so unmounting does not send a second end.
    unmount();
    expect(endCalls()).toHaveLength(1);
  });

  it("ends the session again after a bfcache restore reloads the player", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("{}"));
    const ends = () =>
      fetchSpy.mock.calls.filter(([, init]) => String(init?.body).includes("endPlayback")).length;
    const { unmount } = render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    const loads = vi.mocked(fetchPlaybackInfo).mock.calls.length;
    window.dispatchEvent(new Event("pagehide"));
    const restored = new Event("pageshow") as PageTransitionEvent;
    Object.defineProperty(restored, "persisted", { value: true });
    window.dispatchEvent(restored);
    await waitFor(() => expect(vi.mocked(fetchPlaybackInfo).mock.calls.length).toBe(loads + 1));
    unmount();
    expect(ends()).toBe(2);
  });

  it("ends the previous Media's session when the player moves to another Media", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("{}"));
    const { rerender } = render(<Harness mediaID="media-alpha" />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    rerender(<Harness mediaID="media-beta" />);
    const ended = fetchSpy.mock.calls
      .filter(([, init]) => String(init?.body).includes("endPlayback"))
      .map(([, init]) => JSON.parse(String(init?.body)).variables);
    expect(ended).toEqual([{ mediaId: "media-alpha" }]);
  });

  it("keeps playback progress eventTimeMS on the play's own clock when the wall clock moves (CORE-1703)", async () => {
    // Codex CORE-1703 r4: a device clock set back mid-play must neither
    // stall nor rewind the play's event times, or play history loses the
    // time played after it. The play's clock is the wall clock at its start
    // carried forward by performance.now().
    let now = 1783432800123;
    let perf = 1000;
    vi.spyOn(Date, "now").mockImplementation(() => now);
    vi.spyOn(performance, "now").mockImplementation(() => perf);
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));

    const video = screen.getByTestId("player") as HTMLVideoElement;
    Object.defineProperty(video, "duration", { configurable: true, value: 120 });
    Object.defineProperty(video, "currentTime", {
      configurable: true,
      value: 45.9,
    });
    vi.mocked(graphql).mockClear();
    fireEvent.pause(video);
    await waitFor(() => expect(graphql).toHaveBeenCalledTimes(1));
    const first = (vi.mocked(graphql).mock.calls[0]?.[1] as { eventTimeMS: number }).eventTimeMS;

    // Two minutes back on the wall clock, three seconds on the monotonic one.
    now -= 120_000;
    perf += 3000;
    fireEvent.pause(video);
    await waitFor(() => expect(graphql).toHaveBeenCalledTimes(2));
    const second = (vi.mocked(graphql).mock.calls[1]?.[1] as { eventTimeMS: number }).eventTimeMS;
    expect(second - first).toBe(3000);
  });

  it("starts the next play's clock no earlier than the last event time after a remount", async () => {
    let now = 1783432900000;
    vi.spyOn(Date, "now").mockImplementation(() => now);
    const first = render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));

    let video = screen.getByTestId("player") as HTMLVideoElement;
    Object.defineProperty(video, "duration", { configurable: true, value: 120 });
    Object.defineProperty(video, "currentTime", {
      configurable: true,
      value: 45.9,
    });
    vi.mocked(graphql).mockClear();
    fireEvent.pause(video);
    await waitFor(() => expect(graphql).toHaveBeenCalled());
    const before = Math.max(
      ...vi
        .mocked(graphql)
        .mock.calls.map((call) => (call[1] as { eventTimeMS: number }).eventTimeMS),
    );

    first.unmount();
    vi.mocked(graphql).mockClear();
    now -= 60_000;
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));

    video = screen.getByTestId("player") as HTMLVideoElement;
    Object.defineProperty(video, "duration", { configurable: true, value: 120 });
    Object.defineProperty(video, "currentTime", {
      configurable: true,
      value: 51.1,
    });
    fireEvent.pause(video);

    // The high-water mark survives the remount, so the new play's clock
    // starts no earlier than the last report even though the wall clock is.
    await waitFor(() => expect(graphql).toHaveBeenCalled());
    const times = vi
      .mocked(graphql)
      .mock.calls.map((call) => (call[1] as { eventTimeMS: number }).eventTimeMS);
    expect(times.every((t) => t >= before)).toBe(true);
  });

  it("reports the last presented position, not a pending seek target, while a seek stalls (CORE-0044)", async () => {
    vi.mocked(graphql).mockClear();
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));

    const video = screen.getByTestId("player") as HTMLVideoElement;
    Object.defineProperty(video, "duration", { configurable: true, value: 2700 });
    Object.defineProperty(video, "seeking", { configurable: true, value: false });
    Object.defineProperty(video, "currentTime", { configurable: true, value: 45.9 });
    fireEvent.timeUpdate(video);

    // A seek far ahead that the stream has not produced yet: currentTime is the
    // target, but no frame there has been presented.
    Object.defineProperty(video, "seeking", { configurable: true, value: true });
    Object.defineProperty(video, "currentTime", { configurable: true, value: 1800 });
    fireEvent.timeUpdate(video);
    fireEvent.pause(video);
    await waitFor(() => expect(graphql).toHaveBeenCalled());
    const positions = () =>
      vi
        .mocked(graphql)
        .mock.calls.map((call) => (call[1] as { positionSeconds: number }).positionSeconds);
    expect(positions()).not.toContain(1800);
    expect(positions().at(-1)).toBe(45);

    // The seek completes and its frame is presented: now it is progress.
    Object.defineProperty(video, "seeking", { configurable: true, value: false });
    Object.defineProperty(video, "currentTime", { configurable: true, value: 1800.4 });
    fireEvent.seeked(video);
    await waitFor(() => expect(positions().at(-1)).toBe(1800));
  });

  it("reports no progress before the load has presented a frame (CORE-0044)", async () => {
    vi.mocked(graphql).mockClear();
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));

    const video = screen.getByTestId("player") as HTMLVideoElement;
    Object.defineProperty(video, "duration", { configurable: true, value: 2700 });
    Object.defineProperty(video, "seeking", { configurable: true, value: true });
    Object.defineProperty(video, "currentTime", { configurable: true, value: 1800 });
    fireEvent.pause(video);

    // The seek settled but no frame is decoded yet: still nothing presented.
    Object.defineProperty(video, "seeking", { configurable: true, value: false });
    Object.defineProperty(video, "readyState", {
      configurable: true,
      value: HTMLMediaElement.HAVE_METADATA,
    });
    fireEvent.timeUpdate(video);
    fireEvent.pause(video);
    expect(graphql).not.toHaveBeenCalled();

    Object.defineProperty(video, "readyState", {
      configurable: true,
      value: HTMLMediaElement.HAVE_ENOUGH_DATA,
    });
    Object.defineProperty(video, "currentTime", { configurable: true, value: 30.2 });
    fireEvent.pause(video);
    await waitFor(() => expect(graphql).toHaveBeenCalled());
    const positions = vi
      .mocked(graphql)
      .mock.calls.map((call) => (call[1] as { positionSeconds: number }).positionSeconds);
    expect(positions).not.toContain(1800);
    expect(positions.at(-1)).toBe(30);
  });

  it("sends a progress storm one request at a time, in order, the finish last (CORE-1703)", async () => {
    const pending = capturePendingProgress();
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));

    const video = screen.getByTestId("player") as HTMLVideoElement;
    Object.defineProperty(video, "duration", { configurable: true, value: 120 });
    Object.defineProperty(video, "currentTime", { configurable: true, value: 10 });
    fireEvent.pause(video);
    Object.defineProperty(video, "currentTime", { configurable: true, value: 20 });
    fireEvent.seeked(video);
    Object.defineProperty(video, "currentTime", { configurable: true, value: 30 });
    fireEvent.pause(video);
    Object.defineProperty(video, "currentTime", { configurable: true, value: 40 });
    fireEvent.ended(video);

    expect(pending.requests).toHaveLength(1);
    for (let i = 0; i < 4; i++) {
      await waitFor(() => expect(pending.requests).toHaveLength(i + 1));
      expect(pending.maxActive()).toBe(1);
      pending.requests[i].resolve();
    }
    await waitFor(() => expect(pending.active()).toBe(0));
    expect(pending.requests.map(({ variables }) => variables.positionSeconds)).toEqual([
      10, 20, 30, 40,
    ]);
    expect(pending.requests[3].variables).toMatchObject({
      mediaID: "media-alpha",
      positionSeconds: 40,
      durationSeconds: 120,
      finished: true,
    });
  });

  it("never overlaps live transports when progress arrives during and after a deadline", async () => {
    const pending = capturePendingProgress();
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));

    const video = screen.getByTestId("player") as HTMLVideoElement;
    Object.defineProperty(video, "duration", { configurable: true, value: 120 });
    Object.defineProperty(video, "currentTime", { configurable: true, value: 10 });
    fireEvent.pause(video);
    Object.defineProperty(video, "currentTime", { configurable: true, value: 20 });
    fireEvent.seeked(video);

    expect(pending.requests).toHaveLength(1);
    pending.requests[0].deadline();
    await waitFor(() => expect(pending.requests).toHaveLength(2));
    expect(pending.requests[0].signal.aborted).toBe(true);
    expect(pending.active()).toBe(1);
    expect(pending.maxActive()).toBe(1);

    Object.defineProperty(video, "currentTime", { configurable: true, value: 30 });
    fireEvent.pause(video);
    Object.defineProperty(video, "currentTime", { configurable: true, value: 40 });
    fireEvent.ended(video);

    expect(pending.requests).toHaveLength(2);
    expect(pending.active()).toBe(1);
    expect(pending.maxActive()).toBe(1);
    pending.requests[1].resolve();
    await waitFor(() => expect(pending.requests).toHaveLength(3));
    expect(pending.requests[2].variables).toMatchObject({ positionSeconds: 30, finished: false });
    pending.requests[2].resolve();
    await waitFor(() => expect(pending.requests).toHaveLength(4));
    expect(pending.requests[3].variables).toMatchObject({
      mediaID: "media-alpha",
      positionSeconds: 40,
      durationSeconds: 120,
      finished: true,
    });
    expect(pending.maxActive()).toBe(1);
    pending.requests[3].resolve();
    await waitFor(() => expect(pending.active()).toBe(0));
  });

  it.each(["unmount", "media change", "identity change"])(
    "%s after a deadline leaves zero live progress requests",
    async (teardown) => {
      const identity = new AbortController();
      if (teardown === "identity change") setIdentityEpochSignal(identity.signal);
      const pending = capturePendingProgress();
      const view = render(<Harness mediaID="media-alpha" />);
      await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));

      const video = screen.getByTestId("player") as HTMLVideoElement;
      Object.defineProperty(video, "duration", { configurable: true, value: 120 });
      Object.defineProperty(video, "currentTime", { configurable: true, value: 10 });
      fireEvent.pause(video);
      Object.defineProperty(video, "currentTime", { configurable: true, value: 20 });
      fireEvent.seeked(video);
      pending.requests[0].deadline();
      await waitFor(() => expect(pending.requests).toHaveLength(2));

      if (teardown === "unmount") view.unmount();
      if (teardown === "media change") view.rerender(<Harness mediaID="media-beta" />);
      if (teardown === "identity change") identity.abort();

      await waitFor(() => expect(pending.active()).toBe(0));
      expect(pending.requests).toHaveLength(2);
      expect(pending.requests.every((request) => request.signal.aborted)).toBe(true);
      expect(pending.maxActive()).toBe(1);
    },
  );

  it("aborts old-media progress without flushing its queued replacement", async () => {
    const pending = capturePendingProgress();
    const view = render(<Harness mediaID="media-alpha" />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));

    const video = screen.getByTestId("player") as HTMLVideoElement;
    Object.defineProperty(video, "duration", { configurable: true, value: 120 });
    Object.defineProperty(video, "currentTime", { configurable: true, value: 10 });
    fireEvent.pause(video);
    Object.defineProperty(video, "currentTime", { configurable: true, value: 20 });
    fireEvent.seeked(video);

    view.rerender(<Harness mediaID="media-beta" />);
    await waitFor(() => expect(pending.requests[0].signal.aborted).toBe(true));
    expect(pending.active()).toBe(0);
    expect(pending.requests).toHaveLength(1);
  });

  it.each(["media change", "unmount"])(
    "delivers the finish report queued at ended across a %s (CORE-1143)",
    async (teardown) => {
      const pending = capturePendingProgress();
      const view = render(<Harness mediaID="media-alpha" />);
      await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));

      const video = screen.getByTestId("player") as HTMLVideoElement;
      Object.defineProperty(video, "duration", { configurable: true, value: 120 });
      Object.defineProperty(video, "currentTime", { configurable: true, value: 100 });
      fireEvent.pause(video);
      Object.defineProperty(video, "currentTime", { configurable: true, value: 120 });
      fireEvent.ended(video);
      expect(pending.requests).toHaveLength(1);

      // Auto-advance navigates while the pause report is still in flight: it
      // is delivered, then the finish (CORE-1703).
      if (teardown === "media change") view.rerender(<Harness mediaID="media-beta" />);
      if (teardown === "unmount") view.unmount();

      expect(pending.requests[0].signal.aborted).toBe(false);
      pending.requests[0].resolve();
      await waitFor(() => expect(pending.requests).toHaveLength(2));
      expect(pending.requests[1].variables).toMatchObject({
        mediaID: "media-alpha",
        positionSeconds: 120,
        finished: true,
      });
      expect(pending.requests[1].signal.aborted).toBe(false);
      expect(pending.maxActive()).toBe(1);
      pending.requests[1].resolve();
      await waitFor(() => expect(pending.active()).toBe(0));
      expect(pending.requests).toHaveLength(2);
    },
  );

  it("still aborts a retiring finish report when the identity epoch ends", async () => {
    const identity = new AbortController();
    setIdentityEpochSignal(identity.signal);
    const pending = capturePendingProgress();
    const view = render(<Harness mediaID="media-alpha" />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));

    const video = screen.getByTestId("player") as HTMLVideoElement;
    Object.defineProperty(video, "duration", { configurable: true, value: 120 });
    Object.defineProperty(video, "currentTime", { configurable: true, value: 120 });
    fireEvent.ended(video);
    view.unmount();
    expect(pending.requests).toHaveLength(1);
    expect(pending.requests[0].signal.aborted).toBe(false);

    identity.abort();
    await waitFor(() => expect(pending.requests[0].signal.aborted).toBe(true));
    expect(pending.active()).toBe(0);
  });

  it("aborts Profile-scoped progress when the identity epoch ends", async () => {
    const identity = new AbortController();
    setIdentityEpochSignal(identity.signal);
    const pending = capturePendingProgress();
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));

    const video = screen.getByTestId("player") as HTMLVideoElement;
    Object.defineProperty(video, "duration", { configurable: true, value: 120 });
    Object.defineProperty(video, "currentTime", { configurable: true, value: 10 });
    fireEvent.pause(video);
    identity.abort();

    await waitFor(() => expect(pending.requests[0].signal.aborted).toBe(true));
    expect(pending.active()).toBe(0);
    expect(pending.requests).toHaveLength(1);
  });

  it("aborts its only progress request and clears queued work on unmount", async () => {
    const pending = capturePendingProgress();
    const view = render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));

    const video = screen.getByTestId("player") as HTMLVideoElement;
    Object.defineProperty(video, "duration", { configurable: true, value: 120 });
    Object.defineProperty(video, "currentTime", { configurable: true, value: 10 });
    fireEvent.pause(video);
    Object.defineProperty(video, "currentTime", { configurable: true, value: 20 });
    fireEvent.seeked(video);
    view.unmount();

    expect(pending.requests[0].signal.aborted).toBe(true);
    expect(pending.active()).toBe(0);
    expect(pending.requests).toHaveLength(1);
  });

  it("fails closed with an unsupported-codec message on native decode errors", async () => {
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));

    const video = screen.getByTestId("player") as HTMLVideoElement;
    Object.defineProperty(video, "error", {
      configurable: true,
      value: { code: 3 },
    });
    fireEvent.error(video);

    await waitFor(() =>
      expect(screen.getByTestId("message")).toHaveTextContent("unsupported codec"),
    );
    expect(HTMLMediaElement.prototype.play).not.toHaveBeenCalled();
  });

  it("asks playback-info for the chosen Version (CORE-1676)", async () => {
    render(<Harness selectedVersion="v-uhd" />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    expect(fetchPlaybackInfo).toHaveBeenCalledWith(
      "media-alpha",
      undefined,
      DEFAULT_ADJUSTMENT,
      "v-uhd",
      undefined,
    );
  });

  it("plays only the server-minted stream-token URL from playback-info", async () => {
    render(<Harness />);

    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));

    expect(fetchPlaybackInfo).toHaveBeenCalledWith(
      "media-alpha",
      undefined,
      DEFAULT_ADJUSTMENT,
      "",
      undefined,
    );
    const video = screen.getByTestId("player");
    expect(video).toHaveAttribute("src", "/api/media/direct/token");
    fireEvent.canPlay(video);
    expect(HTMLMediaElement.prototype.play).toHaveBeenCalledOnce();
  });

  // CORE-1659: a key or tempo change restarts the stream at the same song
  // position; stream seconds are song seconds divided by the applied tempo.
  it("restarts with the requested adjustment at the same song position", async () => {
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    const video = screen.getByTestId("player") as HTMLVideoElement;
    fireEvent.loadedMetadata(video);
    video.currentTime = 30;
    fireEvent.timeUpdate(video);

    const adjustment = { pitchShiftSemitones: 1, tempo: 0.8 };
    vi.mocked(fetchPlaybackInfo).mockResolvedValueOnce({
      decision: "transcode",
      reason: "composed-picture",
      adjustment,
      timeline: { positionSeconds: 12 },
      streams: [{ kind: "direct-play", url: "/api/media/direct/adjusted" }],
    });
    fireEvent.click(screen.getByRole("button", { name: "adjust" }));

    await waitFor(() =>
      expect(screen.getByTestId("stream-url")).toHaveTextContent("/api/media/direct/adjusted"),
    );
    expect(fetchPlaybackInfo).toHaveBeenLastCalledWith(
      "media-alpha",
      undefined,
      adjustment,
      "",
      undefined,
    );
    const restarted = screen.getByTestId("player") as HTMLVideoElement;
    fireEvent.loadedMetadata(restarted);
    expect(restarted.currentTime).toBe(37.5);

    // Progress is reported in song seconds.
    vi.mocked(graphql).mockClear();
    Object.defineProperty(restarted, "duration", { configurable: true, value: 100 });
    restarted.currentTime = 40;
    fireEvent.timeUpdate(restarted);
    fireEvent.pause(restarted);
    await waitFor(() => expect(graphql).toHaveBeenCalled());
    expect(vi.mocked(graphql).mock.calls.at(-1)?.[1]).toMatchObject({
      positionSeconds: 32,
      durationSeconds: 80,
    });
  });

  // CORE-1971: a quality change re-requests playback info at the playhead and
  // leaves the play state as the viewer had it.
  it.each([
    ["paused", true, 0],
    ["playing", false, 1],
  ])(
    "keeps a %s player that way at the same position after a quality change",
    async (_name, paused, plays) => {
      render(<Harness />);
      await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
      const video = screen.getByTestId("player") as HTMLVideoElement;
      fireEvent.loadedMetadata(video);
      video.currentTime = 30;
      fireEvent.timeUpdate(video);
      Object.defineProperty(video, "paused", { configurable: true, value: paused });
      vi.mocked(HTMLMediaElement.prototype.play).mockClear();
      const loads = vi.mocked(fetchPlaybackInfo).mock.calls.length;

      fireEvent.click(screen.getByRole("button", { name: "renegotiate" }));

      await waitFor(() => expect(fetchPlaybackInfo).toHaveBeenCalledTimes(loads + 1));
      await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
      const reloaded = screen.getByTestId("player") as HTMLVideoElement;
      fireEvent.loadedMetadata(reloaded);
      fireEvent.canPlay(reloaded);
      expect(reloaded.currentTime).toBe(30);
      expect(HTMLMediaElement.prototype.play).toHaveBeenCalledTimes(plays);
    },
  );

  it("does not start itself after another device paused it while it set up", async () => {
    resetAppliedControlEvents();
    render(<Harness />);
    deliverPlayerCommand({ command: "pause", playingMediaIDs: ["media-alpha"] });
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    const video = screen.getByTestId("player");
    fireEvent.canPlay(video);
    expect(HTMLMediaElement.prototype.play).not.toHaveBeenCalled();
    resetAppliedControlEvents();
  });

  it("lets a reload start again once playback was started after a remote pause", async () => {
    resetAppliedControlEvents();
    vi.mocked(Hls.isSupported).mockReturnValue(true);
    vi.mocked(fetchPlaybackInfo).mockResolvedValue({
      decision: "direct-play",
      streams: [
        { kind: "direct-play", url: "/api/media/direct/token" },
        { kind: "hls", url: "/api/media/hls/token/master.m3u8" },
      ],
    });
    render(<Harness />);
    deliverPlayerCommand({ command: "pause", playingMediaIDs: ["media-alpha"] });
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    const video = screen.getByTestId("player") as HTMLVideoElement;
    fireEvent.canPlay(video);
    expect(HTMLMediaElement.prototype.play).not.toHaveBeenCalled();

    Object.defineProperty(video, "paused", { configurable: true, value: false });
    fireEvent.play(video); // started here after the remote pause
    Object.defineProperty(video, "error", { configurable: true, value: { code: 3 } });
    fireEvent.error(video); // decode failure fails over to HLS
    await waitFor(() => expect(HTMLMediaElement.prototype.play).toHaveBeenCalledOnce());
    resetAppliedControlEvents();
  });

  it("keeps a remote pause sent after playback started through a reload", async () => {
    resetAppliedControlEvents();
    vi.mocked(Hls.isSupported).mockReturnValue(true);
    vi.mocked(fetchPlaybackInfo).mockResolvedValue({
      decision: "direct-play",
      streams: [
        { kind: "direct-play", url: "/api/media/direct/token" },
        { kind: "hls", url: "/api/media/hls/token/master.m3u8" },
      ],
    });
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    const video = screen.getByTestId("player") as HTMLVideoElement;
    fireEvent.canPlay(video);
    expect(HTMLMediaElement.prototype.play).toHaveBeenCalledOnce();
    Object.defineProperty(video, "paused", { configurable: true, value: false });
    fireEvent.play(video);

    Object.defineProperty(video, "paused", { configurable: true, value: true });
    deliverPlayerCommand({ command: "pause", playingMediaIDs: ["media-alpha"] });
    Object.defineProperty(video, "error", { configurable: true, value: { code: 3 } });
    fireEvent.error(video); // decode failure fails over to HLS
    await waitFor(() => expect(fakeHlsInstances).toHaveLength(1));
    expect(HTMLMediaElement.prototype.play).toHaveBeenCalledOnce();
    resetAppliedControlEvents();
  });

  it("keeps a remote pause that lands before its autoplay's play event", async () => {
    resetAppliedControlEvents();
    vi.mocked(Hls.isSupported).mockReturnValue(true);
    vi.mocked(fetchPlaybackInfo).mockResolvedValue({
      decision: "direct-play",
      streams: [
        { kind: "direct-play", url: "/api/media/direct/token" },
        { kind: "hls", url: "/api/media/hls/token/master.m3u8" },
      ],
    });
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    const video = screen.getByTestId("player") as HTMLVideoElement;
    fireEvent.canPlay(video);
    expect(HTMLMediaElement.prototype.play).toHaveBeenCalledOnce();

    deliverPlayerCommand({ command: "pause", playingMediaIDs: ["media-alpha"] });
    fireEvent.play(video); // the autoplay's queued event, after the pause applied
    Object.defineProperty(video, "error", { configurable: true, value: { code: 3 } });
    fireEvent.error(video);
    await waitFor(() => expect(fakeHlsInstances).toHaveLength(1));
    expect(HTMLMediaElement.prototype.play).toHaveBeenCalledOnce();
    resetAppliedControlEvents();
  });

  it("honors a timestamped playback deep link over resume", async () => {
    vi.mocked(fetchPlaybackInfo).mockResolvedValueOnce({
      decision: "direct-play",
      timeline: { positionSeconds: 12 },
      streams: [{ kind: "direct-play", url: "/api/media/direct/token" }],
    });
    render(<Harness startAtSeconds={37} />);

    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    const video = screen.getByTestId("player") as HTMLVideoElement;
    fireEvent.loadedMetadata(video);

    expect(video.currentTime).toBe(37);
  });

  it("restarts at the requested position over a resume seek still waiting on metadata", async () => {
    vi.mocked(fetchPlaybackInfo).mockResolvedValue({
      decision: "direct-play",
      timeline: { positionSeconds: 91 },
      streams: [{ kind: "direct-play", url: "/api/media/direct/token" }],
    });
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));

    fireEvent.click(screen.getByRole("button", { name: "restart" }));
    await waitFor(() => expect(fetchPlaybackInfo).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    const video = screen.getByTestId("player") as HTMLVideoElement;
    fireEvent.loadedMetadata(video);

    expect(video.currentTime).toBe(0);
  });

  it("reports progress again after a restart of finished Media", async () => {
    const pending = capturePendingProgress();
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    let video = screen.getByTestId("player") as HTMLVideoElement;
    Object.defineProperty(video, "duration", { configurable: true, value: 120 });
    Object.defineProperty(video, "currentTime", { configurable: true, value: 120 });
    fireEvent.ended(video);
    await waitFor(() => expect(pending.requests).toHaveLength(1));
    expect(pending.requests[0].variables).toMatchObject({ finished: true });
    pending.requests[0].resolve();

    fireEvent.click(screen.getByRole("button", { name: "restart" }));
    await waitFor(() => expect(fetchPlaybackInfo).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    video = screen.getByTestId("player") as HTMLVideoElement;
    Object.defineProperty(video, "currentTime", { configurable: true, value: 5 });
    fireEvent.pause(video);

    await waitFor(() => expect(pending.requests).toHaveLength(2));
    expect(pending.requests[1].variables).toMatchObject({
      mediaID: "media-alpha",
      positionSeconds: 5,
      finished: false,
    });
    // A restart is a new play (CORE-1703).
    expect(pending.requests[1].variables.playbackID).not.toBe(
      pending.requests[0].variables.playbackID,
    );
    pending.requests[1].resolve();
  });

  it("keeps the playback ID across Try again, which carries on the same play (CORE-1703)", async () => {
    const pending = capturePendingProgress();
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    let video = screen.getByTestId("player") as HTMLVideoElement;
    Object.defineProperty(video, "duration", { configurable: true, value: 120 });
    Object.defineProperty(video, "currentTime", { configurable: true, value: 30 });
    fireEvent.pause(video);
    await waitFor(() => expect(pending.requests).toHaveLength(1));
    pending.requests[0].resolve();

    fireEvent.click(screen.getByRole("button", { name: "retry" }));
    await waitFor(() => expect(fetchPlaybackInfo).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    video = screen.getByTestId("player") as HTMLVideoElement;
    Object.defineProperty(video, "duration", { configurable: true, value: 120 });
    Object.defineProperty(video, "currentTime", { configurable: true, value: 31 });
    fireEvent.pause(video);
    await waitFor(() => expect(pending.requests).toHaveLength(2));
    expect(pending.requests[1].variables.playbackID).toBe(pending.requests[0].variables.playbackID);
    pending.requests[1].resolve();
  });

  it("removes the previous media resume listener before the next media loads metadata", async () => {
    vi.mocked(fetchPlaybackInfo)
      .mockResolvedValueOnce({
        decision: "direct-play",
        timeline: { positionSeconds: 91 },
        streams: [{ kind: "direct-play", url: "/api/media/direct/alpha" }],
      })
      .mockResolvedValueOnce({
        decision: "direct-play",
        streams: [{ kind: "direct-play", url: "/api/media/direct/beta" }],
      });
    const view = render(<Harness mediaID="media-alpha" />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));

    view.rerender(<Harness mediaID="media-beta" />);
    await waitFor(() =>
      expect(screen.getByTestId("player")).toHaveAttribute("src", "/api/media/direct/beta"),
    );
    const video = screen.getByTestId("player") as HTMLVideoElement;
    fireEvent.loadedMetadata(video);

    expect(video.currentTime).toBe(0);
  });

  it("attaches the stream without waiting for a markers request that never resolves", async () => {
    vi.mocked(fetchPlaybackMarkers).mockReturnValue(new Promise(() => {}));
    render(<Harness />);

    await waitFor(() =>
      expect(screen.getByTestId("player")).toHaveAttribute("src", "/api/media/direct/token"),
    );
    expect(screen.getByTestId("phase")).toHaveTextContent("playing");
    // Requested alongside playback info, not after it.
    expect(fetchPlaybackMarkers).toHaveBeenCalledWith("media-alpha");
  });

  it("loads the HLS source without waiting for a markers request that never resolves", async () => {
    vi.mocked(Hls.isSupported).mockReturnValue(true);
    vi.mocked(fetchPlaybackMarkers).mockReturnValue(new Promise(() => {}));
    vi.mocked(fetchPlaybackInfo).mockResolvedValueOnce({
      decision: "transcode",
      streams: [{ kind: "hls", url: "/api/media/hls/token.m3u8" }],
    });
    render(<Harness />);

    await waitFor(() => expect(fakeHlsInstances).toHaveLength(1));
    expect(fakeHlsInstances[0].sources).toEqual(["/api/media/hls/token.m3u8"]);
    expect(screen.getByTestId("phase")).toHaveTextContent("playing");
  });

  it("applies markers that arrive after playback has started", async () => {
    let resolveMarkers: (markers: PlaybackMarker[]) => void = () => {};
    vi.mocked(fetchPlaybackMarkers).mockReturnValue(
      new Promise((resolve) => {
        resolveMarkers = resolve;
      }),
    );
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    expect(screen.getByTestId("markers")).toHaveTextContent("0");

    await act(async () => {
      resolveMarkers([{ segmentKind: "intro", startSeconds: 1, endSeconds: 2, title: "Intro" }]);
    });
    expect(screen.getByTestId("markers")).toHaveTextContent("1");
  });

  it("starts a resumed hls.js session at the resume position instead of seeking after metadata", async () => {
    vi.mocked(Hls.isSupported).mockReturnValue(true);
    vi.mocked(fetchPlaybackInfo).mockResolvedValueOnce({
      decision: "transcode",
      timeline: { positionSeconds: 91 },
      streams: [{ kind: "hls", url: "/api/media/hls/token.m3u8" }],
    });
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    expect(fakeHlsInstances[0].config.startPosition).toBe(91);

    // hls.js owns the seek to startPosition; the player must not seek again.
    const video = screen.getByTestId("player") as HTMLVideoElement;
    fireEvent.loadedMetadata(video);
    expect(video.currentTime).toBe(0);
  });

  it("leaves hls.js on its default start position when there is nothing to resume", async () => {
    vi.mocked(Hls.isSupported).mockReturnValue(true);
    vi.mocked(fetchPlaybackInfo).mockResolvedValueOnce({
      decision: "transcode",
      streams: [{ kind: "hls", url: "/api/media/hls/token.m3u8" }],
    });
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    expect(fakeHlsInstances[0].config.startPosition).toBe(-1);
  });

  it("applies server track preferences once and keeps a manual subtitle-off across track updates", async () => {
    vi.mocked(Hls.isSupported).mockReturnValue(true);
    vi.mocked(fetchPlaybackInfo).mockResolvedValueOnce({
      decision: "transcode",
      streams: [{ kind: "hls", url: "/api/media/hls/token.m3u8" }],
      trackPreferences: {
        audioLanguages: [],
        subtitleLanguages: ["eng"],
        subtitleMode: "always",
        preferSDH: false,
      },
    });
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    expect(fakeHlsInstances).toHaveLength(1);
    const [hls] = fakeHlsInstances;
    const video = screen.getByTestId("player") as HTMLVideoElement;
    const textTracks = [
      { language: "en", label: "English", mode: "disabled" },
      { language: "fr", label: "French", mode: "disabled" },
    ];
    Object.defineProperty(video, "textTracks", { configurable: true, value: textTracks });
    hls.tracks.subtitleTracks = [{ lang: "en", name: "English" }];

    act(() => hls.emit("manifestParsed"));
    expect(hls.tracks.subtitleTrack).toBe(0);
    expect(textTracks.map((track) => track.mode)).toEqual(["showing", "disabled"]);

    // The viewer turns subtitles off by hand...
    hls.tracks.subtitleTrack = -1;
    textTracks[0].mode = "disabled";
    // ...and a later track-list update must not revert that.
    act(() => hls.emit("subtitleTracksUpdated"));
    act(() => hls.emit("audioTracksUpdated"));
    expect(hls.tracks.subtitleTrack).toBe(-1);
    expect(textTracks.map((track) => track.mode)).toEqual(["disabled", "disabled"]);
  });

  it("applies the preferred audio language to hls.js audio renditions (CORE-1680)", async () => {
    vi.mocked(Hls.isSupported).mockReturnValue(true);
    vi.mocked(fetchPlaybackInfo).mockResolvedValueOnce({
      decision: "transcode",
      streams: [{ kind: "hls", url: "/api/media/hls/token.m3u8" }],
      trackPreferences: { audioLanguages: ["fr"], subtitleLanguages: [], subtitleMode: "off" },
    });
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    const [hls] = fakeHlsInstances;
    hls.tracks.audioTracks = [
      { lang: "eng", name: "English" },
      { lang: "fra", name: "French" },
    ];
    act(() => hls.emit("manifestParsed"));
    expect(hls.tracks.audioTrack).toBe(1);
  });

  describe("server subtitle pick at start (CORE-1680)", () => {
    // jsdom has no TextTrack on <track>: give each element a mode to toggle.
    const modes = new WeakMap<HTMLTrackElement, { mode: string }>();
    beforeEach(() => {
      Object.defineProperty(HTMLTrackElement.prototype, "track", {
        configurable: true,
        get(this: HTMLTrackElement) {
          let track = modes.get(this);
          if (!track) {
            track = { mode: "disabled" };
            modes.set(this, track);
          }
          return track;
        },
      });
    });
    afterEach(() => {
      Reflect.deleteProperty(HTMLTrackElement.prototype, "track");
    });

    const shown = (video: HTMLVideoElement) =>
      Array.from(video.querySelectorAll("track"))
        .filter((element) => element.track.mode === "showing")
        .map((element) => element.getAttribute("data-bf-caption"));

    it("shows the forced text track the server picked on direct play", async () => {
      vi.mocked(fetchPlaybackInfo).mockResolvedValueOnce({
        decision: "direct-play",
        streams: [{ kind: "direct-play", url: "/api/media/direct/alpha" }],
        captionTracks: [
          {
            id: "muxed-main-3",
            format: "subrip",
            streamIndex: 3,
            language: "eng",
            url: "/s/3.vtt",
          },
          {
            id: "muxed-main-4",
            format: "subrip",
            streamIndex: 4,
            language: "fra",
            url: "/s/4.vtt",
          },
          {
            id: "muxed-main-5",
            format: "subrip",
            streamIndex: 5,
            language: "fra",
            forced: true,
            url: "/s/5.vtt",
          },
        ],
        selectedSubtitleTrack: {
          track: { kind: "muxed", language: "fr", roles: { forced: true } },
          reason: "forced subtitles",
        },
      });
      render(<Harness />);
      const video = screen.getByTestId("player") as HTMLVideoElement;
      await waitFor(() => expect(shown(video)).toEqual(["muxed-main-5"]));
      expect(fetchPlaybackInfo).toHaveBeenCalledTimes(1);
    });

    it("asks once more with a picture-based pick burned in", async () => {
      const picture = {
        decision: "direct-stream",
        streams: [{ kind: "hls", url: "/api/media/hls/token.m3u8" }],
        captionTracks: [
          { id: "muxed-main-6", format: "hdmv_pgs_subtitle", streamIndex: 6, language: "fra" },
        ],
        selectedSubtitleTrack: {
          track: { kind: "muxed", language: "fra" },
          reason: "preferred-language subtitles",
        },
      };
      vi.mocked(fetchPlaybackInfo).mockResolvedValueOnce(picture).mockResolvedValueOnce(picture);
      render(<Harness />);
      await waitFor(() => expect(fetchPlaybackInfo).toHaveBeenCalledTimes(2));
      expect(vi.mocked(fetchPlaybackInfo).mock.calls[0][4]).toBeUndefined();
      expect(vi.mocked(fetchPlaybackInfo).mock.calls[1][4]).toEqual({
        selectedAudioIndex: undefined,
        selectedSubtitleIndex: 6,
      });
      await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
      expect(fetchPlaybackInfo).toHaveBeenCalledTimes(2);
    });

    // CORE-2442: picks made on the detail page before playback.
    const textTracks = {
      decision: "direct-play",
      streams: [{ kind: "direct-play", url: "/api/media/direct/alpha" }],
      captionTracks: [
        { id: "muxed-main-3", format: "subrip", streamIndex: 3, language: "eng", url: "/s/3.vtt" },
        { id: "sidecar-main-0", format: "srt", language: "deu", sdh: true, url: "/s/side.vtt" },
      ],
      selectedSubtitleTrack: {
        track: { kind: "muxed", language: "eng" },
        reason: "preferred-language subtitles",
      },
    };

    it("starts with the audio and caption picked before playback, over the server's pick", async () => {
      vi.mocked(fetchPlaybackInfo).mockResolvedValueOnce(textTracks);
      render(
        <Harness
          startTracks={{ mediaID: "media-alpha", audioIndex: 2, captionID: "sidecar-main-0" }}
        />,
      );
      const video = screen.getByTestId("player") as HTMLVideoElement;
      await waitFor(() => expect(shown(video)).toEqual(["sidecar-main-0"]));
      expect(fetchPlaybackInfo).toHaveBeenCalledTimes(1);
      expect(vi.mocked(fetchPlaybackInfo).mock.calls[0][4]).toEqual({
        selectedAudioIndex: 2,
        selectedSubtitleIndex: undefined,
      });
    });

    // Review finding P2-2: Start over while docked keeps the player for the
    // same Media; the picks it carries still apply, and a reload keeps them.
    it("applies new picks on a fresh start of the same Media, and a reload keeps the picks in force", async () => {
      vi.mocked(fetchPlaybackInfo).mockResolvedValue(textTracks);
      const view = render(<Harness startTracks={{ mediaID: "media-alpha", audioIndex: 2 }} />);
      await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
      expect(vi.mocked(fetchPlaybackInfo).mock.calls[0][4]).toEqual({
        selectedAudioIndex: 2,
        selectedSubtitleIndex: undefined,
      });

      view.rerender(
        <Harness startAtSeconds={0} startTracks={{ mediaID: "media-alpha", audioIndex: 4 }} />,
      );
      await waitFor(() => expect(fetchPlaybackInfo).toHaveBeenCalledTimes(2));
      expect(vi.mocked(fetchPlaybackInfo).mock.calls[1][4]).toEqual({
        selectedAudioIndex: 4,
        selectedSubtitleIndex: undefined,
      });
      await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));

      // A reload with other picks handed in is not a new start.
      view.rerender(
        <Harness startAtSeconds={0} startTracks={{ mediaID: "media-alpha", audioIndex: 7 }} />,
      );
      fireEvent.click(screen.getByRole("button", { name: "retry" }));
      await waitFor(() => expect(fetchPlaybackInfo).toHaveBeenCalledTimes(3));
      expect(vi.mocked(fetchPlaybackInfo).mock.calls[2][4]).toEqual({
        selectedAudioIndex: 4,
        selectedSubtitleIndex: undefined,
      });
    });

    // Codex round 2, P2-1: Start over pressed again (same ?t=0) with new picks
    // is a new start, told apart by its startID; the same startID is not.
    it("starts afresh with new picks on a repeated Start over, keyed by its startID", async () => {
      vi.mocked(fetchPlaybackInfo).mockResolvedValue(textTracks);
      const view = render(
        <Harness
          startAtSeconds={0}
          startID="s1"
          startTracks={{ mediaID: "media-alpha", audioIndex: 2 }}
        />,
      );
      await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
      expect(fetchPlaybackInfo).toHaveBeenCalledTimes(1);

      view.rerender(
        <Harness
          startAtSeconds={0}
          startID="s2"
          startTracks={{ mediaID: "media-alpha", audioIndex: 4 }}
        />,
      );
      await waitFor(() => expect(fetchPlaybackInfo).toHaveBeenCalledTimes(2));
      expect(vi.mocked(fetchPlaybackInfo).mock.calls[1][4]).toEqual({
        selectedAudioIndex: 4,
        selectedSubtitleIndex: undefined,
      });
      await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));

      // The same start (the mini player expanded) does not reload.
      view.rerender(
        <Harness
          startAtSeconds={0}
          startID="s2"
          startTracks={{ mediaID: "media-alpha", audioIndex: 4 }}
        />,
      );
      await act(async () => {});
      expect(fetchPlaybackInfo).toHaveBeenCalledTimes(2);
    });

    it("starts with subtitles off when Off was picked, and ignores picks for another Media", async () => {
      vi.mocked(fetchPlaybackInfo).mockResolvedValueOnce(textTracks);
      const view = render(<Harness startTracks={{ mediaID: "media-alpha", captionID: null }} />);
      const video = screen.getByTestId("player") as HTMLVideoElement;
      await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
      expect(shown(video)).toEqual([]);
      view.unmount();

      vi.mocked(fetchPlaybackInfo).mockResolvedValueOnce(textTracks);
      render(<Harness startTracks={{ mediaID: "media-beta", audioIndex: 2, captionID: null }} />);
      const next = screen.getByTestId("player") as HTMLVideoElement;
      await waitFor(() => expect(shown(next)).toEqual(["muxed-main-3"]));
      expect(vi.mocked(fetchPlaybackInfo).mock.calls[1][4]).toBeUndefined();
    });

    it("burns in a picked picture-based track, and falls back when the file lacks the pick", async () => {
      const picture = {
        decision: "direct-stream",
        streams: [{ kind: "hls", url: "/api/media/hls/token.m3u8" }],
        captionTracks: [
          { id: "muxed-main-6", format: "hdmv_pgs_subtitle", streamIndex: 6, language: "fra" },
        ],
      };
      vi.mocked(fetchPlaybackInfo).mockResolvedValueOnce(picture).mockResolvedValueOnce(picture);
      const view = render(
        <Harness startTracks={{ mediaID: "media-alpha", captionID: "muxed-main-6" }} />,
      );
      await waitFor(() => expect(fetchPlaybackInfo).toHaveBeenCalledTimes(2));
      expect(vi.mocked(fetchPlaybackInfo).mock.calls[1][4]).toEqual({
        selectedAudioIndex: undefined,
        selectedSubtitleIndex: 6,
      });
      view.unmount();

      vi.mocked(fetchPlaybackInfo).mockResolvedValueOnce(textTracks);
      render(<Harness startTracks={{ mediaID: "media-alpha", captionID: "muxed-main-9" }} />);
      const video = screen.getByTestId("player") as HTMLVideoElement;
      await waitFor(() => expect(shown(video)).toEqual(["muxed-main-3"]));
      expect(fetchPlaybackInfo).toHaveBeenCalledTimes(3);
    });
  });

  it("leaves no loadedmetadata listener on the video after cleanup", async () => {
    vi.mocked(fetchPlaybackInfo).mockResolvedValueOnce({
      decision: "direct-play",
      timeline: { positionSeconds: 91 },
      streams: [{ kind: "direct-play", url: "/api/media/direct/alpha" }],
    });
    const live = new Set<EventListenerOrEventListenerObject>();
    const add = HTMLMediaElement.prototype.addEventListener;
    const remove = HTMLMediaElement.prototype.removeEventListener;
    vi.spyOn(HTMLMediaElement.prototype, "addEventListener").mockImplementation(function (
      this: HTMLMediaElement,
      type,
      listener,
      options,
    ) {
      if (type === "loadedmetadata" && listener) live.add(listener);
      return add.call(this, type, listener, options);
    });
    vi.spyOn(HTMLMediaElement.prototype, "removeEventListener").mockImplementation(function (
      this: HTMLMediaElement,
      type,
      listener,
      options,
    ) {
      if (type === "loadedmetadata" && listener) live.delete(listener);
      return remove.call(this, type, listener, options);
    });
    const view = render(<Harness />);
    // jsdom registers its own native listener at element creation; only the
    // player's listeners (added after the async info fetch) are under test.
    const baseline = new Set(live);
    const playerListeners = () => [...live].filter((listener) => !baseline.has(listener));
    await waitFor(() =>
      expect(screen.getByTestId("player")).toHaveAttribute("src", "/api/media/direct/alpha"),
    );
    // Resume seek + track-preference application both wait on metadata.
    expect(playerListeners()).toHaveLength(2);

    view.unmount();
    expect(playerListeners()).toHaveLength(0);
  });

  it.each([401, 410])(
    "turns a %i playback-info response into the terminal player state and requests an epoch reset",
    async (status) => {
      vi.mocked(fetchPlaybackInfo).mockRejectedValueOnce(new ApiError("reveal gone", status));

      await expectEpochReset(() => {
        render(<Harness />);
      });

      await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("terminal"));
      expect(screen.getByTestId("message")).toHaveTextContent("terminal unavailable");
      expect(HTMLMediaElement.prototype.play).not.toHaveBeenCalled();
      expect(screen.getByTestId("stream-url")).toBeEmptyDOMElement();
      expect(screen.getByTestId("info")).toHaveTextContent("none");
    },
  );

  it("offers Back when playback-info answers 404", async () => {
    vi.mocked(fetchPlaybackInfo).mockRejectedValueOnce(
      new ApiError("not found", 404, { error: "media unavailable" }),
    );
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("message"));
    expect(screen.getByTestId("message")).toHaveTextContent("media not available");
    expect(screen.getByTestId("action")).toHaveTextContent("back");
    expect(resetIdentityEpoch).not.toHaveBeenCalled();
  });

  it("shows the server's reason with Try again for other playback-info HTTP errors (CORE-1146)", async () => {
    vi.mocked(fetchPlaybackInfo).mockRejectedValueOnce(
      new ApiError("server error", 500, { error: "playback info unavailable" }),
    );
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("message"));
    expect(screen.getByTestId("message")).toHaveTextContent("playback unavailable");
    expect(screen.getByTestId("detail")).toHaveTextContent("playback info unavailable");
    expect(screen.getByTestId("action")).toHaveTextContent("retry");
  });

  it("explains an incomplete source and offers Back without retrying (CORE-2380)", async () => {
    vi.mocked(fetchPlaybackInfo).mockResolvedValueOnce({
      decision: "unavailable",
      reason: "media-source-incomplete",
      streams: [],
    });
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("message"));
    expect(screen.getByTestId("message")).toHaveTextContent("source ends early");
    expect(screen.getByTestId("action")).toHaveTextContent("back");
    expect(fetchPlaybackInfo).toHaveBeenCalledTimes(1);
  });

  it("maps an unavailable decision's reason to its copy and action (CORE-1146)", async () => {
    vi.mocked(fetchPlaybackInfo).mockResolvedValueOnce({
      decision: "unavailable",
      reason: "media-unavailable",
    });
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("message"));
    expect(screen.getByTestId("message")).toHaveTextContent("media not available");
    expect(screen.getByTestId("action")).toHaveTextContent("back");
  });

  it("waits for the network when playback-info cannot be reached offline, then starts on 'online' (CORE-1362)", async () => {
    const onLine = vi.spyOn(navigator, "onLine", "get").mockReturnValue(false);
    vi.mocked(fetchPlaybackInfo).mockRejectedValueOnce(new TypeError("Failed to fetch"));
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("waiting-for-network")).toHaveTextContent("yes"));
    expect(screen.getByTestId("phase")).not.toHaveTextContent("message");
    expect(fetchPlaybackInfo).toHaveBeenCalledTimes(1);

    onLine.mockReturnValue(true);
    act(() => {
      window.dispatchEvent(new Event("online"));
    });
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    expect(fetchPlaybackInfo).toHaveBeenCalledTimes(2);
    expect(screen.getByTestId("waiting-for-network")).toHaveTextContent("no");
  });

  it("reports an unreachable server with Try again while online", async () => {
    vi.mocked(fetchPlaybackInfo).mockRejectedValueOnce(new TypeError("Failed to fetch"));
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("message"));
    expect(screen.getByTestId("message")).toHaveTextContent("server unreachable");
    expect(screen.getByTestId("action")).toHaveTextContent("retry");
  });

  it.each([403, 410])(
    "renegotiates a %i stream refusal through playback-info instead of going terminal (CORE-1359)",
    async (status) => {
      vi.mocked(Hls.isSupported).mockReturnValue(true);
      vi.mocked(fetchPlaybackInfo)
        .mockResolvedValueOnce({
          decision: "transcode",
          streams: [{ kind: "hls", url: "/api/media/hls/token/master.m3u8" }],
        })
        .mockResolvedValueOnce({
          decision: "transcode",
          streams: [{ kind: "hls", url: "/api/media/hls/token2/master.m3u8" }],
        });

      render(<Harness />);
      await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
      const video = screen.getByTestId("player") as HTMLVideoElement;
      video.currentTime = 42;
      const callsBeforeError = vi.mocked(fetchPlaybackInfo).mock.calls.length;

      // A 410 on stream bytes is an evicted stream session, not an ended reveal.
      fakeHlsInstances[0].emitError({ fatal: true, response: { code: status } });

      await waitFor(() =>
        expect(screen.getByTestId("stream-url")).toHaveTextContent(
          "/api/media/hls/token2/master.m3u8",
        ),
      );
      expect(vi.mocked(fetchPlaybackInfo).mock.calls.length).toBe(callsBeforeError + 1);
      expect(fakeHlsInstances[0].sources).toContain("/api/media/hls/token2/master.m3u8");
      expect(fakeHlsInstances[0].config.startPosition).toBe(42);
      expect(fakeHlsInstances[0].destroyed()).toBe(false);
      expect(screen.getByTestId("phase")).toHaveTextContent("playing");
      expect(resetIdentityEpoch).not.toHaveBeenCalled();
    },
  );

  it("shares one playback-info refresh between concurrent stream refusals", async () => {
    vi.mocked(Hls.isSupported).mockReturnValue(true);
    let resolveRefresh: (info: Awaited<ReturnType<typeof fetchPlaybackInfo>>) => void = () => {};
    vi.mocked(fetchPlaybackInfo)
      .mockResolvedValueOnce({
        decision: "transcode",
        streams: [{ kind: "hls", url: "/api/media/hls/token/master.m3u8" }],
      })
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveRefresh = resolve;
          }),
      );

    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    const callsBeforeError = vi.mocked(fetchPlaybackInfo).mock.calls.length;

    // hls.js reports each refused segment and playlist request on its own.
    act(() => {
      fakeHlsInstances[0].emitError({ fatal: false, response: { code: 401 } });
      fakeHlsInstances[0].emitError({ fatal: true, response: { code: 401 } });
      fakeHlsInstances[0].emitError({ fatal: true, networkDetails: { status: 401 } });
    });
    expect(vi.mocked(fetchPlaybackInfo).mock.calls.length).toBe(callsBeforeError + 1);
    expect(screen.getByTestId("recovering")).toHaveTextContent("yes");

    await act(async () => {
      resolveRefresh({
        decision: "transcode",
        streams: [{ kind: "hls", url: "/api/media/hls/token2/master.m3u8" }],
      });
    });
    expect(fakeHlsInstances[0].sources).toEqual([
      "/api/media/hls/token/master.m3u8",
      "/api/media/hls/token2/master.m3u8",
    ]);
    expect(screen.getByTestId("phase")).toHaveTextContent("playing");
    expect(resetIdentityEpoch).not.toHaveBeenCalled();
  });

  it("treats a first mid-playback 401 as a stale stream token: refreshes playback info and resumes at the same position instead of going terminal", async () => {
    vi.mocked(Hls.isSupported).mockReturnValue(true);
    vi.mocked(fetchPlaybackInfo)
      .mockResolvedValueOnce({
        decision: "transcode",
        streams: [{ kind: "hls", url: "/api/media/hls/token/master.m3u8" }],
      })
      .mockResolvedValueOnce({
        decision: "transcode",
        streams: [{ kind: "hls", url: "/api/media/hls/token2/master.m3u8" }],
      });

    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    const video = screen.getByTestId("player") as HTMLVideoElement;
    video.currentTime = 87;

    // hls.js's XHR loader can report a refused segment fetch with no
    // data.response at all — only the raw XMLHttpRequest on networkDetails.
    const callsBeforeError = vi.mocked(fetchPlaybackInfo).mock.calls.length;
    fakeHlsInstances[0].emitError({ fatal: true, networkDetails: { status: 401 } });

    await waitFor(() =>
      expect(vi.mocked(fetchPlaybackInfo).mock.calls.length).toBe(callsBeforeError + 1),
    );
    await waitFor(() =>
      expect(screen.getByTestId("stream-url")).toHaveTextContent(
        "/api/media/hls/token2/master.m3u8",
      ),
    );
    expect(screen.getByTestId("phase")).toHaveTextContent("playing");
    expect(resetIdentityEpoch).not.toHaveBeenCalled();

    // The reloaded source resumes at the position playback was at, not from zero.
    expect(fakeHlsInstances[0].config.startPosition).toBe(87);
    video.currentTime = 0;
    fireEvent.loadedMetadata(video);
    expect(video.currentTime).toBe(87);
  });

  it("resumes a renewal at the same song second when the fresh stream drops the tempo", async () => {
    vi.mocked(Hls.isSupported).mockReturnValue(true);
    vi.mocked(fetchPlaybackInfo)
      .mockResolvedValueOnce({
        decision: "transcode",
        reason: "composed-picture",
        adjustment: { pitchShiftSemitones: 0, tempo: 0.8 },
        streams: [{ kind: "hls", url: "/api/media/hls/slow/master.m3u8" }],
      })
      // The renewal comes back unadjusted: no adjustment echo.
      .mockResolvedValueOnce({
        decision: "transcode",
        reason: "composed-picture",
        streams: [{ kind: "hls", url: "/api/media/hls/plain/master.m3u8" }],
      });

    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    const video = screen.getByTestId("player") as HTMLVideoElement;
    // Stream second 100 at tempo 0.8 is song second 80.
    video.currentTime = 100;
    fakeHlsInstances[0].emitError({ fatal: true, networkDetails: { status: 401 } });

    await waitFor(() =>
      expect(screen.getByTestId("stream-url")).toHaveTextContent(
        "/api/media/hls/plain/master.m3u8",
      ),
    );
    // The unadjusted stream runs in song seconds, so it resumes at 80.
    expect(fakeHlsInstances[0].config.startPosition).toBe(80);

    // Progress is no longer scaled by the dropped tempo.
    vi.mocked(graphql).mockClear();
    Object.defineProperty(video, "duration", { configurable: true, value: 200 });
    video.currentTime = 90;
    fireEvent.timeUpdate(video);
    fireEvent.pause(video);
    await waitFor(() => expect(graphql).toHaveBeenCalled());
    expect(vi.mocked(graphql).mock.calls.at(-1)?.[1]).toMatchObject({
      positionSeconds: 90,
      durationSeconds: 200,
    });
  });

  it("keeps the declared next item, so Up Next survives a stream-token refresh (CORE-1143)", async () => {
    vi.mocked(Hls.isSupported).mockReturnValue(true);
    let call = 0;
    vi.mocked(fetchPlaybackInfo).mockImplementation(async (_mediaID, nextMediaID) => {
      call += 1;
      return {
        decision: "transcode",
        streams: [{ kind: "hls", url: `/api/media/hls/token${call}/master.m3u8` }],
        // The server names a next item only when the request declares one.
        ...(nextMediaID ? { next: { mediaID: nextMediaID } } : {}),
      };
    });

    render(<Harness nextMediaID="media-next" />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    expect(screen.getByTestId("up-next")).toHaveTextContent("media-next");

    fakeHlsInstances[0].emitError({ fatal: true, networkDetails: { status: 401 } });

    await waitFor(() =>
      expect(screen.getByTestId("stream-url")).toHaveTextContent(
        "/api/media/hls/token2/master.m3u8",
      ),
    );
    expect(fetchPlaybackInfo).toHaveBeenLastCalledWith(
      "media-alpha",
      "media-next",
      DEFAULT_ADJUSTMENT,
      "",
      undefined,
    );
    expect(screen.getByTestId("up-next")).toHaveTextContent("media-next");
  });

  it("offers Try again, not the terminal card, on a second refusal before playback progresses", async () => {
    vi.mocked(Hls.isSupported).mockReturnValue(true);
    vi.mocked(fetchPlaybackInfo)
      .mockResolvedValueOnce({
        decision: "transcode",
        streams: [{ kind: "hls", url: "/api/media/hls/token/master.m3u8" }],
      })
      .mockResolvedValueOnce({
        decision: "transcode",
        streams: [{ kind: "hls", url: "/api/media/hls/token2/master.m3u8" }],
      });

    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));

    const callsBeforeFirstError = vi.mocked(fetchPlaybackInfo).mock.calls.length;
    fakeHlsInstances[0].emitError({ fatal: true, response: { code: 410 } });
    await waitFor(() =>
      expect(screen.getByTestId("stream-url")).toHaveTextContent(
        "/api/media/hls/token2/master.m3u8",
      ),
    );
    expect(vi.mocked(fetchPlaybackInfo).mock.calls.length).toBe(callsBeforeFirstError + 1);

    act(() => fakeHlsInstances[0].emitError({ fatal: true, response: { code: 410 } }));

    expect(screen.getByTestId("phase")).toHaveTextContent("message");
    expect(screen.getByTestId("message")).toHaveTextContent("playback failed");
    expect(screen.getByTestId("action")).toHaveTextContent("retry");
    expect(fakeHlsInstances[0].destroyed()).toBe(true);
    expect(resetIdentityEpoch).not.toHaveBeenCalled();
    // One renegotiation per stretch without progress.
    expect(vi.mocked(fetchPlaybackInfo).mock.calls.length).toBe(callsBeforeFirstError + 1);
  });

  it("renegotiates again after buffered progress", async () => {
    vi.mocked(Hls.isSupported).mockReturnValue(true);
    let call = 0;
    vi.mocked(fetchPlaybackInfo).mockImplementation(async () => {
      call += 1;
      return {
        decision: "transcode",
        streams: [{ kind: "hls", url: `/api/media/hls/token${call}/master.m3u8` }],
      };
    });

    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    fakeHlsInstances[0].emitError({ fatal: true, response: { code: 410 } });
    await waitFor(() => expect(screen.getByTestId("stream-url")).toHaveTextContent("token2"));
    act(() => fakeHlsInstances[0].emitFragBuffered());
    fakeHlsInstances[0].emitError({ fatal: true, response: { code: 410 } });
    await waitFor(() => expect(screen.getByTestId("stream-url")).toHaveTextContent("token3"));
    expect(screen.getByTestId("phase")).toHaveTextContent("playing");
  });

  it("goes reveal-terminal when the playback-info refresh itself fails", async () => {
    vi.mocked(Hls.isSupported).mockReturnValue(true);
    vi.mocked(fetchPlaybackInfo)
      .mockResolvedValueOnce({
        decision: "transcode",
        streams: [{ kind: "hls", url: "/api/media/hls/token/master.m3u8" }],
      })
      .mockRejectedValueOnce(new ApiError("reveal gone", 401));

    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));

    await expectEpochReset(() => {
      fakeHlsInstances[0].emitError({ fatal: true, response: { code: 401 } });
    });

    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("terminal"));
    expect(screen.getByTestId("message")).toHaveTextContent("terminal unavailable");
    expect(screen.getByTestId("stream-url")).toBeEmptyDOMElement();
    expect(screen.getByTestId("info")).toHaveTextContent("none");
  });

  it("offers Try again when the playback-info refresh cannot reach the server", async () => {
    vi.mocked(Hls.isSupported).mockReturnValue(true);
    vi.mocked(fetchPlaybackInfo)
      .mockResolvedValueOnce({
        decision: "transcode",
        streams: [{ kind: "hls", url: "/api/media/hls/token/master.m3u8" }],
      })
      .mockRejectedValueOnce(new TypeError("Failed to fetch"));

    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    fakeHlsInstances[0].emitError({ fatal: true, response: { code: 401 } });

    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("message"));
    expect(screen.getByTestId("message")).toHaveTextContent("server unreachable");
    expect(screen.getByTestId("action")).toHaveTextContent("retry");
    expect(screen.getByTestId("terminal-flag")).toHaveTextContent("no");
    expect(resetIdentityEpoch).not.toHaveBeenCalled();
  });

  it("shows the server's reason when the playback-info refresh fails with a server error", async () => {
    vi.mocked(Hls.isSupported).mockReturnValue(true);
    vi.mocked(fetchPlaybackInfo)
      .mockResolvedValueOnce({
        decision: "transcode",
        streams: [{ kind: "hls", url: "/api/media/hls/token/master.m3u8" }],
      })
      .mockRejectedValueOnce(new ApiError("failed", 500, { error: "playback info unavailable" }));

    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    fakeHlsInstances[0].emitError({ fatal: true, response: { code: 410 } });

    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("message"));
    expect(screen.getByTestId("message")).toHaveTextContent("playback unavailable");
    expect(screen.getByTestId("detail")).toHaveTextContent("playback info unavailable");
    expect(resetIdentityEpoch).not.toHaveBeenCalled();
  });

  it("never loads a direct-play file into hls.js when the refreshed info lost its HLS stream", async () => {
    vi.mocked(Hls.isSupported).mockReturnValue(true);
    vi.mocked(fetchPlaybackInfo)
      .mockResolvedValueOnce({
        decision: "transcode",
        streams: [{ kind: "hls", url: "/api/media/hls/token/master.m3u8" }],
      })
      .mockResolvedValueOnce({
        decision: "direct-play",
        streams: [{ kind: "direct-play", url: "/api/media/direct/token2" }],
      });

    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));

    fakeHlsInstances[0].emitError({ fatal: true, response: { code: 401 } });

    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("message"));
    expect(screen.getByTestId("message")).toHaveTextContent("playback failed");
    expect(screen.getByTestId("action")).toHaveTextContent("retry");
    expect(fakeHlsInstances[0].sources).not.toContain("/api/media/direct/token2");
    expect(fakeHlsInstances[0].destroyed()).toBe(true);
    expect(resetIdentityEpoch).not.toHaveBeenCalled();
  });

  it("maps an unavailable refresh to the reason's copy and action", async () => {
    vi.mocked(Hls.isSupported).mockReturnValue(true);
    vi.mocked(fetchPlaybackInfo)
      .mockResolvedValueOnce({
        decision: "transcode",
        streams: [{ kind: "hls", url: "/api/media/hls/token/master.m3u8" }],
      })
      .mockResolvedValueOnce({ decision: "unavailable", reason: "stream-limit-reached" });

    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
    fakeHlsInstances[0].emitError({ fatal: true, response: { code: 410 } });

    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("message"));
    expect(screen.getByTestId("message")).toHaveTextContent("stream limit reached");
    expect(screen.getByTestId("action")).toHaveTextContent("retry");
  });

  // feature:hls-error-recovery
  describe("stream inventory and failover (CORE-0048)", () => {
    const inventory = {
      decision: "direct-play",
      streams: [
        { kind: "direct-play", url: "/api/media/direct/token" },
        {
          kind: "hls",
          url: "/api/media/hls/token/master.m3u8",
          renditions: [{ id: "720", height: 720 }],
        },
      ],
    };

    it("keeps the whole stream inventory while playing streams[0]", async () => {
      vi.mocked(fetchPlaybackInfo).mockResolvedValue(inventory);
      render(<Harness />);
      await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
      expect(screen.getByTestId("stream-kinds")).toHaveTextContent("direct-play,hls");
      expect(screen.getByTestId("active-kind")).toHaveTextContent("direct-play");
      expect(screen.getByTestId("player")).toHaveAttribute("src", "/api/media/direct/token");
    });

    it("fails a native decode error over to the HLS stream at the same position", async () => {
      vi.mocked(Hls.isSupported).mockReturnValue(true);
      vi.mocked(fetchPlaybackInfo).mockResolvedValue(inventory);
      render(<Harness />);
      await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
      const video = screen.getByTestId("player") as HTMLVideoElement;
      video.currentTime = 42;
      Object.defineProperty(video, "error", { configurable: true, value: { code: 3 } });
      fireEvent.error(video);

      await waitFor(() => expect(fakeHlsInstances).toHaveLength(1));
      expect(fakeHlsInstances[0].sources).toEqual(["/api/media/hls/token/master.m3u8"]);
      expect(fakeHlsInstances[0].config.startPosition).toBe(42);
      expect(screen.getByTestId("phase")).toHaveTextContent("playing");
      expect(screen.getByTestId("active-kind")).toHaveTextContent("hls");
      expect(screen.getByTestId("decision")).toHaveTextContent("transcode");
      expect(screen.getByTestId("info-decision")).toHaveTextContent("transcode");
      expect(screen.getByTestId("hls-active")).toHaveTextContent("yes");
      expect(screen.getByTestId("message")).toHaveTextContent("");
    });

    it("keeps the requested start position when the failover beats loadedmetadata", async () => {
      vi.mocked(Hls.isSupported).mockReturnValue(true);
      vi.mocked(fetchPlaybackInfo).mockResolvedValue(inventory);
      render(<Harness startAtSeconds={37} />);
      await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
      const video = screen.getByTestId("player") as HTMLVideoElement;
      // The element has not applied the resume seek yet: currentTime is still 0.
      Object.defineProperty(video, "error", { configurable: true, value: { code: 3 } });
      fireEvent.error(video);
      await waitFor(() => expect(fakeHlsInstances).toHaveLength(1));
      expect(fakeHlsInstances[0].config.startPosition).toBe(37);
    });

    it("resumes playback after a failover that fires before playback started", async () => {
      vi.mocked(Hls.isSupported).mockReturnValue(true);
      vi.mocked(fetchPlaybackInfo).mockResolvedValue(inventory);
      render(<Harness />);
      await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
      const video = screen.getByTestId("player") as HTMLVideoElement;
      // Startup: the element is paused and has never played.
      Object.defineProperty(video, "paused", { configurable: true, value: true });
      Object.defineProperty(video, "error", { configurable: true, value: { code: 3 } });
      vi.mocked(HTMLMediaElement.prototype.play).mockClear();
      fireEvent.error(video);
      await waitFor(() => expect(fakeHlsInstances).toHaveLength(1));
      expect(HTMLMediaElement.prototype.play).toHaveBeenCalledOnce();
    });

    it("keeps a viewer's pause across the failover", async () => {
      vi.mocked(Hls.isSupported).mockReturnValue(true);
      vi.mocked(fetchPlaybackInfo).mockResolvedValue(inventory);
      render(<Harness />);
      await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
      const video = screen.getByTestId("player") as HTMLVideoElement;
      // Playback had begun and the viewer then paused.
      Object.defineProperty(video, "paused", { configurable: true, value: true });
      Object.defineProperty(video, "played", {
        configurable: true,
        value: { length: 1, start: () => 0, end: () => 5 },
      });
      Object.defineProperty(video, "error", { configurable: true, value: { code: 3 } });
      vi.mocked(HTMLMediaElement.prototype.play).mockClear();
      fireEvent.error(video);
      await waitFor(() => expect(fakeHlsInstances).toHaveLength(1));
      expect(HTMLMediaElement.prototype.play).not.toHaveBeenCalled();
    });

    it("keeps the unsupported-codec message when no HLS stream exists", async () => {
      vi.mocked(Hls.isSupported).mockReturnValue(true);
      render(<Harness />);
      await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
      const video = screen.getByTestId("player") as HTMLVideoElement;
      Object.defineProperty(video, "error", { configurable: true, value: { code: 4 } });
      fireEvent.error(video);
      await waitFor(() =>
        expect(screen.getByTestId("message")).toHaveTextContent("unsupported codec"),
      );
      expect(fakeHlsInstances).toHaveLength(0);
      expect(screen.getByTestId("message")).not.toHaveTextContent("transcode disallowed");
    });

    it("says converting is off when the Profile withheld the HLS stream (CORE-2181)", async () => {
      vi.mocked(Hls.isSupported).mockReturnValue(true);
      vi.mocked(fetchPlaybackInfo).mockResolvedValue({
        decision: "direct-play",
        reason: "transcode-disallowed",
        streams: [{ kind: "direct-play", url: "/api/media/direct/token" }],
      });
      render(<Harness />);
      await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
      const video = screen.getByTestId("player") as HTMLVideoElement;
      Object.defineProperty(video, "error", { configurable: true, value: { code: 4 } });
      fireEvent.error(video);
      await waitFor(() =>
        expect(screen.getByTestId("message")).toHaveTextContent("transcode disallowed"),
      );
      expect(screen.getByTestId("action")).toHaveTextContent("back");
      expect(fakeHlsInstances).toHaveLength(0);
    });

    it("keeps the unsupported-codec message when this browser cannot play HLS", async () => {
      vi.mocked(Hls.isSupported).mockReturnValue(false);
      vi.mocked(fetchPlaybackInfo).mockResolvedValue(inventory);
      render(<Harness />);
      await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
      const video = screen.getByTestId("player") as HTMLVideoElement;
      Object.defineProperty(video, "error", { configurable: true, value: { code: 3 } });
      fireEvent.error(video);
      await waitFor(() =>
        expect(screen.getByTestId("message")).toHaveTextContent("unsupported codec"),
      );
    });

    it("switches direct play to the pinned HLS rendition on request", async () => {
      vi.mocked(Hls.isSupported).mockReturnValue(true);
      vi.mocked(fetchPlaybackInfo).mockResolvedValue(inventory);
      render(<Harness />);
      await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
      const video = screen.getByTestId("player") as HTMLVideoElement;
      video.currentTime = 9;
      fireEvent.click(screen.getByText("switch-hls"));
      expect(screen.getByTestId("switch-result")).toHaveTextContent("hlsjs");
      await waitFor(() => expect(screen.getByTestId("active-kind")).toHaveTextContent("hls"));
      expect(fakeHlsInstances).toHaveLength(1);
      expect(fakeHlsInstances[0].config.startPosition).toBe(9);
      // A second request while HLS is active is not a switch.
      fireEvent.click(screen.getByText("switch-hls"));
      expect(screen.getByTestId("switch-result")).toHaveTextContent("false");
    });

    describe("rendition pin across a stream-token refresh", () => {
      const twoRenditions = {
        ...inventory,
        streams: [
          inventory.streams[0],
          {
            ...inventory.streams[1],
            renditions: [
              { id: "720", height: 720 },
              { id: "480", height: 480 },
            ],
          },
        ],
      };

      // The viewer picks 720 on direct play, then changes the choice (what the quality menu
      // does: assign hls.currentLevel), then a 401 refreshes the token and reloads the source.
      async function pickThenChooseThenRefresh(choice: number) {
        vi.mocked(Hls.isSupported).mockReturnValue(true);
        vi.mocked(fetchPlaybackInfo).mockResolvedValue(twoRenditions);
        render(<Harness />);
        await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
        fireEvent.click(screen.getByText("switch-hls"));
        await waitFor(() => expect(fakeHlsInstances).toHaveLength(1));
        const hls = fakeHlsInstances[0];
        // hls.js orders levels ascending: 480p is level 0, 720p is level 1.
        hls.tracks.levels = [{ height: 480 }, { height: 720 }];
        hls.emit("manifestParsed");
        expect(hls.tracks.currentLevel).toBe(1);
        hls.tracks.currentLevel = choice;
        hls.emitError({ fatal: true, response: { code: 401 } });
        await waitFor(() => expect(hls.sources).toHaveLength(2));
        hls.emit("manifestParsed");
        return hls;
      }

      it("keeps a later manual rendition instead of restoring the first pick", async () => {
        // The first pick (720p) is level 1; the later choice is level 0.
        const hls = await pickThenChooseThenRefresh(0);
        expect(hls.tracks.currentLevel).toBe(0);
      });

      it("pins the picked rendition, not its list position, in hls.js's ascending levels", async () => {
        vi.mocked(Hls.isSupported).mockReturnValue(true);
        vi.mocked(fetchPlaybackInfo).mockResolvedValue({
          ...inventory,
          streams: [
            inventory.streams[0],
            {
              ...inventory.streams[1],
              renditions: [
                { id: "1080", height: 1080 },
                { id: "720", height: 720 },
              ],
            },
          ],
        });
        render(<Harness />);
        await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
        fireEvent.click(screen.getByText("switch-hls"));
        await waitFor(() => expect(fakeHlsInstances).toHaveLength(1));
        const hls = fakeHlsInstances[0];
        hls.tracks.levels = [{ height: 720 }, { height: 1080 }];
        hls.emit("manifestParsed");
        // The harness picks 720p: level 0 here, though it is list position 1.
        expect(hls.tracks.currentLevel).toBe(0);
      });

      it("keeps Auto instead of restoring the first pick", async () => {
        const hls = await pickThenChooseThenRefresh(-1);
        expect(hls.tracks.currentLevel).toBe(-1);
      });
    });

    describe("a quality choice made while the manifest loads", () => {
      // The viewer picks 720 on direct play, then changes the choice through the quality
      // menu before hls.js has parsed the manifest.
      async function pickThenChooseBeforeParse(choice: number) {
        vi.mocked(Hls.isSupported).mockReturnValue(true);
        vi.mocked(fetchPlaybackInfo).mockResolvedValue({
          ...inventory,
          streams: [
            inventory.streams[0],
            {
              ...inventory.streams[1],
              renditions: [
                { id: "720", height: 720 },
                { id: "480", height: 480 },
              ],
            },
          ],
        });
        render(<Harness />);
        await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
        fireEvent.click(screen.getByText("switch-hls"));
        await waitFor(() => expect(fakeHlsInstances).toHaveLength(1));
        const hls = fakeHlsInstances[0];
        selectHlsLevel(hls.tracks as unknown as Hls, choice);
        hls.emit("manifestParsed");
        return hls;
      }

      it("applies the latest rendition picked before the manifest parsed", async () => {
        const hls = await pickThenChooseBeforeParse(-1);
        // Back on a fresh load: picks made with no levels yet are held for the parse.
        hls.tracks.currentLevel = -1;
        selectHlsRendition(hls.tracks as unknown as Hls, { id: "1080", height: 1080 });
        selectHlsRendition(hls.tracks as unknown as Hls, { id: "480", height: 480 });
        expect(hls.tracks.currentLevel).toBe(-1);
        hls.tracks.levels = [{ height: 480 }, { height: 1080 }];
        hls.emit("manifestParsed");
        expect(hls.tracks.currentLevel).toBe(0);
      });

      it("keeps Auto instead of restoring the pending pick", async () => {
        const hls = await pickThenChooseBeforeParse(-1);
        expect(hls.tracks.currentLevel).toBe(-1);
      });

      it("keeps the later rendition instead of restoring the pending pick", async () => {
        const hls = await pickThenChooseBeforeParse(1);
        expect(hls.tracks.currentLevel).toBe(1);
      });
    });

    it("keeps the HLS decision when a token refresh after the switch still says direct play", async () => {
      vi.mocked(Hls.isSupported).mockReturnValue(true);
      vi.mocked(fetchPlaybackInfo)
        .mockResolvedValueOnce(inventory)
        .mockResolvedValueOnce({
          ...inventory,
          streams: [
            inventory.streams[0],
            { ...inventory.streams[1], url: "/api/media/hls/token2/master.m3u8" },
          ],
        });
      render(<Harness />);
      await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
      fireEvent.click(screen.getByText("switch-hls"));
      await waitFor(() => expect(fakeHlsInstances).toHaveLength(1));
      const hls = fakeHlsInstances[0];
      hls.emitError({ fatal: true, response: { code: 401 } });
      await waitFor(() =>
        expect(screen.getByTestId("stream-url")).toHaveTextContent(
          "/api/media/hls/token2/master.m3u8",
        ),
      );
      expect(screen.getByTestId("info-decision")).toHaveTextContent("transcode");
      expect(screen.getByTestId("decision")).toHaveTextContent("transcode");
      expect(screen.getByTestId("active-kind")).toHaveTextContent("hls");
    });

    it("refuses a rendition switch the HLS stream does not advertise", async () => {
      vi.mocked(Hls.isSupported).mockReturnValue(true);
      vi.mocked(fetchPlaybackInfo).mockResolvedValue({
        ...inventory,
        streams: [
          inventory.streams[0],
          { ...inventory.streams[1], renditions: [{ id: "480", height: 480 }] },
        ],
      });
      render(<Harness />);
      await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
      fireEvent.click(screen.getByText("switch-hls"));
      expect(screen.getByTestId("switch-result")).toHaveTextContent("false");
      expect(screen.getByTestId("active-kind")).toHaveTextContent("direct-play");
      expect(fakeHlsInstances).toHaveLength(0);
    });

    describe("native MEDIA_ERR_NETWORK", () => {
      // Fake only the backoff clock, and never let it follow wall time
      // (CORE-1788): with shouldAdvanceTime a loaded host could let a backoff
      // step fire during a waitFor before the test advanced the clock. While
      // it is installed, settle the probe with flushProbe instead of waitFor
      // (testing-library's waitFor drains through a setTimeout).
      function useBackoffClock(): void {
        vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
      }

      async function flushProbe(): Promise<void> {
        await act(() => vi.advanceTimersByTimeAsync(0));
      }

      function failNetwork(): HTMLVideoElement {
        const video = screen.getByTestId("player") as HTMLVideoElement;
        Object.defineProperty(video, "error", { configurable: true, value: { code: 2 } });
        fireEvent.error(video);
        return video;
      }

      it.each([401, 410])(
        "renegotiates and reloads at the playhead when the probe shows a %i refusal",
        async (status) => {
          const fetchSpy = vi
            .spyOn(globalThis, "fetch")
            .mockResolvedValue(new Response("", { status }));
          vi.mocked(fetchPlaybackInfo)
            .mockResolvedValueOnce({
              decision: "direct-play",
              streams: [{ kind: "direct-play", url: "/api/media/direct/token" }],
            })
            .mockResolvedValueOnce({
              decision: "direct-play",
              streams: [{ kind: "direct-play", url: "/api/media/direct/token2" }],
            });
          render(<Harness />);
          await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
          const video = screen.getByTestId("player") as HTMLVideoElement;
          video.currentTime = 33;
          failNetwork();
          expect(fetchSpy).toHaveBeenCalledWith(
            "/api/media/direct/token",
            expect.objectContaining({ headers: { Range: "bytes=0-0" } }),
          );
          await waitFor(() =>
            expect(screen.getByTestId("stream-url")).toHaveTextContent("/api/media/direct/token2"),
          );
          expect(video.getAttribute("src")).toBe("/api/media/direct/token2");
          video.currentTime = 0;
          fireEvent.loadedMetadata(video);
          expect(video.currentTime).toBe(33);
          expect(screen.getByTestId("phase")).toHaveTextContent("playing");
          expect(resetIdentityEpoch).not.toHaveBeenCalled();
        },
      );

      it("goes reveal-terminal only when playback-info itself answers the refusal", async () => {
        vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("", { status: 403 }));
        vi.mocked(fetchPlaybackInfo)
          .mockResolvedValueOnce({
            decision: "direct-play",
            streams: [{ kind: "direct-play", url: "/api/media/direct/token" }],
          })
          .mockRejectedValueOnce(new ApiError("relocked", 403));
        render(<Harness />);
        await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
        await expectEpochReset(() => {
          failNetwork();
        });
        await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("terminal"));
        expect(screen.getByTestId("terminal-flag")).toHaveTextContent("yes");
      });

      it("reloads at the playhead on a 1/2/4 s backoff, then reports failure", async () => {
        vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("", { status: 500 }));
        render(<Harness />);
        await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
        const video = screen.getByTestId("player") as HTMLVideoElement;
        video.currentTime = 12;
        useBackoffClock();
        try {
          for (const delay of [1000, 2000, 4000]) {
            const loads = vi.mocked(HTMLMediaElement.prototype.load).mock.calls.length;
            await act(async () => {
              failNetwork();
            });
            await flushProbe();
            expect(screen.getByTestId("recovering")).toHaveTextContent("yes");
            await act(() => vi.advanceTimersByTimeAsync(delay - 50));
            expect(vi.mocked(HTMLMediaElement.prototype.load).mock.calls.length).toBe(loads);
            await act(() => vi.advanceTimersByTimeAsync(50));
            // The reload re-binds the same stream and seeks back to the playhead.
            expect(vi.mocked(HTMLMediaElement.prototype.load).mock.calls.length).toBe(loads + 1);
            expect(video.getAttribute("src")).toBe("/api/media/direct/token");
            video.currentTime = 0;
            fireEvent.loadedMetadata(video);
            expect(video.currentTime).toBe(12);
            expect(screen.getByTestId("phase")).toHaveTextContent("playing");
          }
          await act(async () => {
            failNetwork();
          });
          await flushProbe();
          expect(screen.getByTestId("message")).toHaveTextContent("playback failed");
          expect(screen.getByTestId("recovering")).toHaveTextContent("no");
          expect(resetIdentityEpoch).not.toHaveBeenCalled();
        } finally {
          vi.useRealTimers();
        }
      });

      // CORE-1971: a quality change made while paused must stay paused when the
      // new stream then faults; its empty `played` is not a pending autoplay.
      it("keeps a paused quality change paused through a fault reload", async () => {
        vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("", { status: 500 }));
        render(<Harness />);
        await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
        const first = screen.getByTestId("player") as HTMLVideoElement;
        fireEvent.loadedMetadata(first);
        first.currentTime = 30;
        fireEvent.timeUpdate(first);
        Object.defineProperty(first, "paused", { configurable: true, value: true });
        const loads = vi.mocked(fetchPlaybackInfo).mock.calls.length;
        fireEvent.click(screen.getByRole("button", { name: "renegotiate" }));
        await waitFor(() => expect(fetchPlaybackInfo).toHaveBeenCalledTimes(loads + 1));
        await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
        const video = screen.getByTestId("player") as HTMLVideoElement;
        Object.defineProperty(video, "paused", { configurable: true, value: true });
        Object.defineProperty(video, "played", { configurable: true, value: { length: 0 } });
        fireEvent.loadedMetadata(video);
        vi.mocked(HTMLMediaElement.prototype.play).mockClear();
        useBackoffClock();
        try {
          const reloads = vi.mocked(HTMLMediaElement.prototype.load).mock.calls.length;
          await act(async () => {
            failNetwork();
          });
          await flushProbe();
          await act(() => vi.advanceTimersByTimeAsync(1000));
          expect(vi.mocked(HTMLMediaElement.prototype.load).mock.calls.length).toBe(reloads + 1);
          fireEvent.loadedMetadata(video);
          fireEvent.canPlay(video);
          expect(HTMLMediaElement.prototype.play).not.toHaveBeenCalled();
        } finally {
          vi.useRealTimers();
        }
      });

      it("refills the backoff budget once playback resumes", async () => {
        vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("", { status: 500 }));
        render(<Harness />);
        await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
        useBackoffClock();
        try {
          for (let round = 0; round < 5; round += 1) {
            await act(async () => {
              failNetwork();
            });
            await flushProbe();
            expect(screen.getByTestId("recovering")).toHaveTextContent("yes");
            await act(() => vi.advanceTimersByTimeAsync(1000));
            fireEvent.playing(screen.getByTestId("player"));
            expect(screen.getByTestId("recovering")).toHaveTextContent("no");
          }
          expect(screen.getByTestId("phase")).toHaveTextContent("playing");
        } finally {
          vi.useRealTimers();
        }
      });

      it("retries rather than failing when the probe hangs past its timeout", async () => {
        vi.spyOn(globalThis, "fetch").mockImplementation(
          (_url, init) =>
            new Promise((_resolve, reject) => {
              init?.signal?.addEventListener("abort", () =>
                reject(new DOMException("aborted", "AbortError")),
              );
            }),
        );
        render(<Harness />);
        await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
        useBackoffClock();
        try {
          failNetwork();
          await act(() => vi.advanceTimersByTimeAsync(5001));
          expect(screen.getByTestId("recovering")).toHaveTextContent("yes");
          expect(screen.getByTestId("phase")).toHaveTextContent("playing");
          expect(resetIdentityEpoch).not.toHaveBeenCalled();
        } finally {
          vi.useRealTimers();
        }
      });

      it("waits for the network instead of probing while offline (CORE-1362)", async () => {
        const fetchSpy = vi.spyOn(globalThis, "fetch");
        render(<Harness />);
        await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
        const video = screen.getByTestId("player") as HTMLVideoElement;
        video.currentTime = 20;
        act(() => {
          window.dispatchEvent(new Event("offline"));
        });
        expect(screen.getByTestId("waiting-for-network")).toHaveTextContent("yes");
        const loads = vi.mocked(HTMLMediaElement.prototype.load).mock.calls.length;
        act(() => {
          failNetwork();
        });
        expect(fetchSpy).not.toHaveBeenCalled();
        expect(vi.mocked(HTMLMediaElement.prototype.load).mock.calls.length).toBe(loads);
        act(() => {
          window.dispatchEvent(new Event("online"));
        });
        expect(screen.getByTestId("waiting-for-network")).toHaveTextContent("no");
        expect(vi.mocked(HTMLMediaElement.prototype.load).mock.calls.length).toBe(loads + 1);
        video.currentTime = 0;
        fireEvent.loadedMetadata(video);
        expect(video.currentTime).toBe(20);
      });
    });
  });

  describe("hls.js fatal-error recovery", () => {
    async function renderHlsPlaying() {
      vi.mocked(Hls.isSupported).mockReturnValue(true);
      vi.mocked(fetchPlaybackInfo).mockResolvedValue({
        decision: "transcode",
        streams: [{ kind: "hls", url: "/api/media/hls/token/master.m3u8" }],
      });
      render(<Harness />);
      await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
      expect(fakeHlsInstances).toHaveLength(1);
      return fakeHlsInstances[0];
    }

    const networkError = { fatal: true, type: "networkError" };

    it("recovers a single fatal network error in place by re-arming startLoad", async () => {
      const hls = await renderHlsPlaying();
      vi.useFakeTimers();
      try {
        act(() => hls.emitError(networkError));
        expect(screen.getByTestId("phase")).toHaveTextContent("playing");
        expect(screen.getByTestId("recovering")).toHaveTextContent("yes");
        expect(hls.startLoad).not.toHaveBeenCalled();
        await act(() => vi.advanceTimersByTimeAsync(1000));
        expect(hls.startLoad).toHaveBeenCalledTimes(1);
        expect(hls.destroyed()).toBe(false);
        expect(screen.getByTestId("phase")).toHaveTextContent("playing");
        act(() => hls.emitFragBuffered());
        expect(screen.getByTestId("recovering")).toHaveTextContent("no");
      } finally {
        vi.useRealTimers();
      }
    });

    it("waits out a 503 at the playhead instead of moving it (CORE-0044)", async () => {
      const hls = await renderHlsPlaying();
      const video = screen.getByTestId("player") as HTMLVideoElement;
      const sets: number[] = [];
      Object.defineProperty(video, "currentTime", {
        configurable: true,
        get: () => 600,
        set: (value: number) => sets.push(value),
      });
      vi.useFakeTimers();
      try {
        act(() => hls.emitError({ ...networkError, response: { code: 503 } }));
        expect(screen.getByTestId("recovering")).toHaveTextContent("yes");
        await act(() => vi.advanceTimersByTimeAsync(1000));
        expect(hls.startLoad).toHaveBeenCalledTimes(1);
        // Reload from where hls.js stopped (no argument), never a later position.
        expect(hls.startLoad.mock.calls[0]).toEqual([]);
        expect(sets).toEqual([]);
        expect(hls.destroyed()).toBe(false);
        expect(screen.getByTestId("phase")).toHaveTextContent("playing");
      } finally {
        vi.useRealTimers();
      }
    });

    it("destroys hls.js and reports failure after four consecutive fatal network errors", async () => {
      const hls = await renderHlsPlaying();
      vi.useFakeTimers();
      try {
        for (const delay of [1000, 2000, 4000]) {
          act(() => hls.emitError(networkError));
          expect(hls.destroyed()).toBe(false);
          await act(() => vi.advanceTimersByTimeAsync(delay));
        }
        expect(hls.startLoad).toHaveBeenCalledTimes(3);
        act(() => hls.emitError(networkError));
        expect(hls.destroyed()).toBe(true);
        expect(screen.getByTestId("phase")).toHaveTextContent("message");
        expect(screen.getByTestId("message")).toHaveTextContent("playback failed");
        expect(screen.getByTestId("recovering")).toHaveTextContent("no");
      } finally {
        vi.useRealTimers();
      }
    });

    it("does not refill the budget when a recovery only re-buffers the same fragment", async () => {
      const hls = await renderHlsPlaying();
      const firstFragment = { start: 0, duration: 4 };
      act(() => hls.emitFragBuffered(firstFragment));
      vi.useFakeTimers();
      try {
        for (const delay of [1000, 2000, 4000]) {
          act(() => hls.emitError(networkError));
          await act(() => vi.advanceTimersByTimeAsync(delay));
          // The reload buffers the fragment already played, then fails on the next.
          act(() => hls.emitFragBuffered(firstFragment));
        }
        expect(screen.getByTestId("recovering")).toHaveTextContent("yes");
        act(() => hls.emitError(networkError));
        expect(hls.destroyed()).toBe(true);
        expect(screen.getByTestId("message")).toHaveTextContent("playback failed");
      } finally {
        vi.useRealTimers();
      }
    });

    describe("a source that ends early (fragment 404)", () => {
      const fragmentMissing = {
        fatal: false,
        type: "networkError",
        details: "fragLoadError",
        response: { code: 404 },
        frag: { start: 4, duration: 4 },
      };
      function bufferUntil(video: HTMLVideoElement, end: number) {
        Object.defineProperty(video, "buffered", {
          configurable: true,
          value: { length: 1, start: () => 0, end: () => end },
        });
      }

      it("stops loading, plays what is buffered, then says why at the cut", async () => {
        const hls = await renderHlsPlaying();
        const video = screen.getByTestId("player") as HTMLVideoElement;
        bufferUntil(video, 4);
        video.currentTime = 2;
        act(() => hls.emitError(fragmentMissing));
        expect(hls.stopLoad).toHaveBeenCalledTimes(1);
        expect(hls.startLoad).not.toHaveBeenCalled();
        expect(screen.getByTestId("phase")).toHaveTextContent("playing");
        // The level fallback's later errors change nothing.
        act(() => hls.emitError({ ...fragmentMissing, fatal: true }));
        expect(screen.getByTestId("phase")).toHaveTextContent("playing");
        video.currentTime = 3.9;
        act(() => {
          fireEvent.waiting(video);
        });
        expect(hls.destroyed()).toBe(true);
        expect(screen.getByTestId("phase")).toHaveTextContent("message");
        expect(screen.getByTestId("message")).toHaveTextContent("source ends early");
      });

      it("explains at once when the playhead is already at the cut", async () => {
        const hls = await renderHlsPlaying();
        const video = screen.getByTestId("player") as HTMLVideoElement;
        bufferUntil(video, 4);
        video.currentTime = 3.8;
        act(() => hls.emitError(fragmentMissing));
        expect(screen.getByTestId("message")).toHaveTextContent("source ends early");
      });

      it("loads again after a seek back before the cut, and explains after a seek past it", async () => {
        const hls = await renderHlsPlaying();
        const video = screen.getByTestId("player") as HTMLVideoElement;
        bufferUntil(video, 4);
        video.currentTime = 2;
        act(() => hls.emitError(fragmentMissing));
        video.currentTime = 1;
        act(() => {
          fireEvent.seeking(video);
        });
        expect(hls.startLoad).toHaveBeenCalledWith(1);
        // Loading again: an empty buffer is not the cut.
        bufferUntil(video, 0);
        act(() => {
          fireEvent.waiting(video);
        });
        expect(screen.getByTestId("phase")).toHaveTextContent("playing");
        video.currentTime = 90;
        act(() => {
          fireEvent.seeking(video);
        });
        expect(screen.getByTestId("message")).toHaveTextContent("source ends early");
      });
    });

    it("recovers a fatal media error through recoverMediaError, then with an audio codec swap", async () => {
      const hls = await renderHlsPlaying();
      const mediaError = { fatal: true, type: "mediaError" };
      act(() => hls.emitError(mediaError));
      expect(hls.recoverMediaError).toHaveBeenCalledTimes(1);
      expect(hls.swapAudioCodec).not.toHaveBeenCalled();
      expect(screen.getByTestId("phase")).toHaveTextContent("playing");
      act(() => hls.emitError(mediaError));
      expect(hls.swapAudioCodec).toHaveBeenCalledTimes(1);
      expect(hls.recoverMediaError).toHaveBeenCalledTimes(2);
      act(() => hls.emitError(mediaError));
      expect(hls.destroyed()).toBe(true);
      expect(screen.getByTestId("phase")).toHaveTextContent("message");
    });

    it("renegotiates a 410 refusal even when it is typed as a network error, without spending the budget", async () => {
      const hls = await renderHlsPlaying();
      vi.mocked(fetchPlaybackInfo).mockResolvedValueOnce({
        decision: "transcode",
        streams: [{ kind: "hls", url: "/api/media/hls/token2/master.m3u8" }],
      });
      hls.emitError({ ...networkError, response: { code: 410 } });
      await waitFor(() => expect(hls.sources).toContain("/api/media/hls/token2/master.m3u8"));
      expect(hls.startLoad).not.toHaveBeenCalled();
      expect(hls.destroyed()).toBe(false);
      expect(screen.getByTestId("phase")).toHaveTextContent("playing");
      expect(resetIdentityEpoch).not.toHaveBeenCalled();
    });

    it("routes a fatal 401 network error to the token refresh without spending the in-place budget", async () => {
      const hls = await renderHlsPlaying();
      vi.mocked(fetchPlaybackInfo).mockResolvedValueOnce({
        decision: "transcode",
        streams: [{ kind: "hls", url: "/api/media/hls/token2/master.m3u8" }],
      });
      const loadsBeforeError = vi.mocked(fetchPlaybackInfo).mock.calls.length;
      act(() => hls.emitError({ ...networkError, response: { code: 401 } }));
      await waitFor(() =>
        expect(screen.getByTestId("stream-url")).toHaveTextContent(
          "/api/media/hls/token2/master.m3u8",
        ),
      );
      expect(fetchPlaybackInfo).toHaveBeenCalledTimes(loadsBeforeError + 1);
      expect(screen.getByTestId("phase")).toHaveTextContent("playing");
      // Reconnecting until the fresh stream buffers.
      expect(screen.getByTestId("recovering")).toHaveTextContent("yes");
      act(() => hls.emitFragBuffered());
      expect(screen.getByTestId("recovering")).toHaveTextContent("no");
      expect(resetIdentityEpoch).not.toHaveBeenCalled();
      vi.useFakeTimers();
      try {
        await act(() => vi.advanceTimersByTimeAsync(1000));
        expect(hls.startLoad).not.toHaveBeenCalled();
        // The whole 1s/2s/4s budget is still there after the refresh.
        for (const delay of [1000, 2000, 4000]) {
          act(() => hls.emitError(networkError));
          expect(hls.destroyed()).toBe(false);
          await act(() => vi.advanceTimersByTimeAsync(delay));
        }
        expect(hls.startLoad).toHaveBeenCalledTimes(3);
        expect(screen.getByTestId("phase")).toHaveTextContent("playing");
      } finally {
        vi.useRealTimers();
      }
    });

    it("waits for the network without spending the budget while offline, then resumes on 'online' (CORE-1362)", async () => {
      const hls = await renderHlsPlaying();
      vi.useFakeTimers();
      try {
        act(() => {
          window.dispatchEvent(new Event("offline"));
        });
        expect(screen.getByTestId("waiting-for-network")).toHaveTextContent("yes");
        // Any number of offline faults never exhausts the budget.
        for (let i = 0; i < 6; i += 1) act(() => hls.emitError(networkError));
        await act(() => vi.advanceTimersByTimeAsync(10_000));
        expect(hls.startLoad).not.toHaveBeenCalled();
        expect(hls.destroyed()).toBe(false);
        expect(screen.getByTestId("phase")).toHaveTextContent("playing");
        expect(screen.getByTestId("recovering")).toHaveTextContent("no");

        act(() => {
          window.dispatchEvent(new Event("online"));
        });
        expect(screen.getByTestId("waiting-for-network")).toHaveTextContent("no");
        expect(hls.startLoad).toHaveBeenCalledTimes(1);
        expect(screen.getByTestId("recovering")).toHaveTextContent("yes");
        // The full budget is still there.
        for (const delay of [1000, 2000, 4000]) {
          act(() => hls.emitError(networkError));
          expect(hls.destroyed()).toBe(false);
          await act(() => vi.advanceTimersByTimeAsync(delay));
        }
        expect(hls.startLoad).toHaveBeenCalledTimes(4);
      } finally {
        vi.useRealTimers();
      }
    });

    it("restarts a starving load at the playhead on 'online' without waiting out hls.js's own retry backoff", async () => {
      const hls = await renderHlsPlaying();
      const video = screen.getByTestId("player") as HTMLVideoElement;
      video.currentTime = 30;
      act(() => {
        window.dispatchEvent(new Event("offline"));
      });
      // No fatal error: hls.js is retrying the fragment on its own timer.
      act(() => {
        window.dispatchEvent(new Event("online"));
      });
      expect(screen.getByTestId("waiting-for-network")).toHaveTextContent("no");
      expect(hls.startLoad).toHaveBeenCalledTimes(1);
      expect(hls.startLoad).toHaveBeenCalledWith(30);
    });

    it("parks a pending backoff retry when the network drops during the wait", async () => {
      const hls = await renderHlsPlaying();
      vi.useFakeTimers();
      try {
        act(() => hls.emitError(networkError));
        act(() => {
          window.dispatchEvent(new Event("offline"));
        });
        await act(() => vi.advanceTimersByTimeAsync(1000));
        expect(hls.startLoad).not.toHaveBeenCalled();
        act(() => {
          window.dispatchEvent(new Event("online"));
        });
        expect(hls.startLoad).toHaveBeenCalledTimes(1);
      } finally {
        vi.useRealTimers();
      }
    });

    it("re-runs the load for the same media when retry is invoked from the message state", async () => {
      const hls = await renderHlsPlaying();
      act(() => hls.emitError({ fatal: true, type: "otherError" }));
      expect(screen.getByTestId("phase")).toHaveTextContent("message");
      const loads = vi.mocked(fetchPlaybackInfo).mock.calls.length;
      fireEvent.click(screen.getByRole("button", { name: "retry" }));
      await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
      expect(fetchPlaybackInfo).toHaveBeenCalledTimes(loads + 1);
      expect(fakeHlsInstances).toHaveLength(2);
    });

    it("resumes Try again at the last presented position (CORE-1364)", async () => {
      const hls = await renderHlsPlaying();
      const video = screen.getByTestId("player") as HTMLVideoElement;
      video.currentTime = 64;
      fireEvent.timeUpdate(video);
      act(() => hls.emitError({ fatal: true, type: "otherError" }));
      expect(screen.getByTestId("phase")).toHaveTextContent("message");
      fireEvent.click(screen.getByRole("button", { name: "retry" }));
      await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
      expect(fakeHlsInstances[1].config.startPosition).toBe(64);
    });
  });
});
