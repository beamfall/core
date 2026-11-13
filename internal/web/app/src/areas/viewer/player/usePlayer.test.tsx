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
import { selectHlsLevel, selectHlsRendition, usePlayer } from "./usePlayer";

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
  sourceEndsEarly: "source ends early",
};

function Harness({
  mediaID = "media-alpha",
  startAtSeconds = 0,
  nextMediaID,
}: {
  mediaID?: string;
  startAtSeconds?: number;
  nextMediaID?: string;
}) {
  const { videoRef, state, retry, switchToHls } = usePlayer(
    mediaID,
    messages,
    startAtSeconds,
    nextMediaID,
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
      <button type="button" onClick={retry}>
        retry
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
  });

  afterEach(() => {
    setIdentityEpochReset();
    setIdentityEpochSignal();
    vi.restoreAllMocks();
  });

  it("sends eventTimeMS with playback progress reports", async () => {
    vi.spyOn(Date, "now").mockReturnValue(1783432800123);
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
    });
  });

  it("keeps playback progress eventTimeMS monotonic when the wall clock moves backward", async () => {
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));

    const video = screen.getByTestId("player") as HTMLVideoElement;
    Object.defineProperty(video, "duration", { configurable: true, value: 120 });
    Object.defineProperty(video, "currentTime", {
      configurable: true,
      value: 45.9,
    });
    let now = 1783432800123;
    vi.spyOn(Date, "now").mockImplementation(() => now);

    fireEvent.pause(video);
    now = 1783432799000;
    fireEvent.pause(video);

    // Edge and cleanup paths can emit additional progress beacons, so assert
    // the monotonic contract across *every* report rather than an exact call
    // count: none regress below the earlier wall clock, even though it moved
    // backward between the two pauses.
    await waitFor(() => expect(vi.mocked(graphql).mock.calls.length).toBeGreaterThanOrEqual(2));
    const times = vi
      .mocked(graphql)
      .mock.calls.map((call) => (call[1] as { eventTimeMS: number }).eventTimeMS);
    expect(times.every((t) => t === 1783432800123)).toBe(true);
    expect([...times]).toEqual([...times].sort((a, b) => a - b));
  });

  it("keeps playback progress eventTimeMS monotonic after a remount", async () => {
    let now = 1783432800456;
    vi.spyOn(Date, "now").mockImplementation(() => now);
    const first = render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));

    let video = screen.getByTestId("player") as HTMLVideoElement;
    Object.defineProperty(video, "duration", { configurable: true, value: 120 });
    Object.defineProperty(video, "currentTime", {
      configurable: true,
      value: 45.9,
    });
    fireEvent.pause(video);
    await waitFor(() => expect(graphql).toHaveBeenCalled());

    first.unmount();
    vi.mocked(graphql).mockClear();
    now = 1783432790000;
    render(<Harness />);
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));

    video = screen.getByTestId("player") as HTMLVideoElement;
    Object.defineProperty(video, "duration", { configurable: true, value: 120 });
    Object.defineProperty(video, "currentTime", {
      configurable: true,
      value: 51.1,
    });
    fireEvent.pause(video);

    // The high-water mark survives the remount, so every report after the
    // remount clamps to the prior epoch's timestamp even though the new clock
    // is earlier — assert that invariant over all reports, not an exact count.
    await waitFor(() => expect(graphql).toHaveBeenCalled());
    const times = vi
      .mocked(graphql)
      .mock.calls.map((call) => (call[1] as { eventTimeMS: number }).eventTimeMS);
    expect(times.every((t) => t === 1783432800456)).toBe(true);
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

    Object.defineProperty(video, "seeking", { configurable: true, value: false });
    Object.defineProperty(video, "currentTime", { configurable: true, value: 30.2 });
    fireEvent.pause(video);
    await waitFor(() => expect(graphql).toHaveBeenCalled());
    const positions = vi
      .mocked(graphql)
      .mock.calls.map((call) => (call[1] as { positionSeconds: number }).positionSeconds);
    expect(positions).not.toContain(1800);
    expect(positions.at(-1)).toBe(30);
  });

  it("coalesces progress storms to one request and delivers the latest final progress", async () => {
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
    expect(pending.maxActive()).toBe(1);
    pending.requests[0].resolve();
    await waitFor(() => expect(pending.requests).toHaveLength(2));

    expect(pending.requests[1].variables).toMatchObject({
      mediaID: "media-alpha",
      positionSeconds: 40,
      durationSeconds: 120,
      finished: true,
    });
    expect(pending.maxActive()).toBe(1);
    pending.requests[1].resolve();
    await waitFor(() => expect(pending.active()).toBe(0));
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
    expect(pending.requests[2].variables).toMatchObject({
      mediaID: "media-alpha",
      positionSeconds: 40,
      durationSeconds: 120,
      finished: true,
    });
    expect(pending.maxActive()).toBe(1);
    pending.requests[2].resolve();
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

      // Auto-advance navigates while the periodic report is still in flight.
      if (teardown === "media change") view.rerender(<Harness mediaID="media-beta" />);
      if (teardown === "unmount") view.unmount();

      await waitFor(() => expect(pending.requests).toHaveLength(2));
      expect(pending.requests[0].signal.aborted).toBe(true);
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

  it("plays only the server-minted stream-token URL from playback-info", async () => {
    render(<Harness />);

    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));

    expect(fetchPlaybackInfo).toHaveBeenCalledWith("media-alpha", undefined);
    const video = screen.getByTestId("player");
    expect(video).toHaveAttribute("src", "/api/media/direct/token");
    fireEvent.canPlay(video);
    expect(HTMLMediaElement.prototype.play).toHaveBeenCalledOnce();
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
      trackPreferences: { subtitleLanguage: "en", subtitleMode: "on" },
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
    expect(fetchPlaybackInfo).toHaveBeenLastCalledWith("media-alpha", "media-next");
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
        vi.useFakeTimers({ shouldAdvanceTime: true });
        try {
          for (const delay of [1000, 2000, 4000]) {
            const loads = vi.mocked(HTMLMediaElement.prototype.load).mock.calls.length;
            await act(async () => {
              failNetwork();
            });
            await waitFor(() => expect(screen.getByTestId("recovering")).toHaveTextContent("yes"));
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
          await waitFor(() =>
            expect(screen.getByTestId("message")).toHaveTextContent("playback failed"),
          );
          expect(screen.getByTestId("recovering")).toHaveTextContent("no");
          expect(resetIdentityEpoch).not.toHaveBeenCalled();
        } finally {
          vi.useRealTimers();
        }
      });

      it("refills the backoff budget once playback resumes", async () => {
        vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("", { status: 500 }));
        render(<Harness />);
        await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
        vi.useFakeTimers({ shouldAdvanceTime: true });
        try {
          for (let round = 0; round < 5; round += 1) {
            await act(async () => {
              failNetwork();
            });
            await waitFor(() => expect(screen.getByTestId("recovering")).toHaveTextContent("yes"));
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
        vi.useFakeTimers({ shouldAdvanceTime: true });
        try {
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
