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
import { ApiError, fetchPlaybackInfo } from "../lib/playback";
import { usePlayer } from "./usePlayer";

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
      subtitleTracks: Array<{ lang?: string; name?: string }>;
      audioTracks: Array<{ lang?: string; name?: string }>;
    };
    emitFragBuffered: () => void;
    startLoad: ReturnType<typeof vi.fn>;
    recoverMediaError: ReturnType<typeof vi.fn>;
    swapAudioCodec: ReturnType<typeof vi.fn>;
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
    subtitleDisplay = false;
    audioTrack = -1;
    subtitleTracks: Array<{ lang?: string; name?: string }> = [];
    audioTracks: Array<{ lang?: string; name?: string }> = [];
    startLoad = vi.fn();
    recoverMediaError = vi.fn();
    swapAudioCodec = vi.fn();

    constructor() {
      const emit = (event: string, data?: unknown) =>
        this.handlers.get(event)?.forEach((handler) => handler(event, data));
      fakeHlsInstances.push({
        emitError: (data: unknown) => emit("error", data),
        emit,
        destroyed: () => this.isDestroyed,
        tracks: this,
        emitFragBuffered: () => emit("fragBuffered", {}),
        startLoad: this.startLoad,
        recoverMediaError: this.recoverMediaError,
        swapAudioCodec: this.swapAudioCodec,
      });
    }
    on(event: string, handler: (event: string, data: unknown) => void) {
      (this.handlers.get(event) ?? this.handlers.set(event, []).get(event)!).push(handler);
    }
    loadSource() {}
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
};

function Harness({
  mediaID = "media-alpha",
  startAtSeconds = 0,
}: {
  mediaID?: string;
  startAtSeconds?: number;
}) {
  const { videoRef, state, retry } = usePlayer(mediaID, messages, startAtSeconds);
  return (
    <>
      <video data-testid="player" ref={videoRef} />
      <output data-testid="phase">{state.phase}</output>
      <output data-testid="message">{state.message}</output>
      <output data-testid="recovering">{state.recovering ? "yes" : "no"}</output>
      <button type="button" onClick={retry}>
        retry
      </button>
      {/* The revealed playback document, surfaced so a test can assert it stops
          being addressable once the reveal goes terminal. */}
      <output data-testid="stream-url">{state.stream?.url ?? ""}</output>
      <output data-testid="info">{state.info ? "present" : "none"}</output>
      <output data-testid="decision">{state.decision}</output>
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

    expect(fetchPlaybackInfo).toHaveBeenCalledWith("media-alpha");
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

  it.each([403, 410])(
    "turns a reveal-terminal %i HLS error into the terminal player state immediately, without a token refresh",
    async (status) => {
      vi.mocked(Hls.isSupported).mockReturnValue(true);
      vi.mocked(fetchPlaybackInfo).mockResolvedValueOnce({
        decision: "transcode",
        streams: [{ kind: "hls", url: "/api/media/hls/token/master.m3u8" }],
      });

      render(<Harness />);
      await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("playing"));
      // The revealed manifest is addressable while the reveal holds…
      expect(screen.getByTestId("stream-url")).toHaveTextContent(
        "/api/media/hls/token/master.m3u8",
      );
      expect(fakeHlsInstances).toHaveLength(1);
      const callsBeforeError = vi.mocked(fetchPlaybackInfo).mock.calls.length;

      await expectEpochReset(() => {
        fakeHlsInstances[0].emitError({ fatal: true, response: { code: status } });
      });

      await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("terminal"));
      expect(screen.getByTestId("message")).toHaveTextContent("terminal unavailable");
      // …and is gone once it goes terminal: the session that minted it is retired,
      // so the pre-terminal stream URL, its playback document and its live hls.js
      // session must not survive as replayable state.
      expect(screen.getByTestId("stream-url")).toBeEmptyDOMElement();
      expect(screen.getByTestId("info")).toHaveTextContent("none");
      expect(screen.getByTestId("decision")).toBeEmptyDOMElement();
      expect(fakeHlsInstances[0].destroyed()).toBe(true);
      // 403/410 mean relocked/hidden, not a stale token — never worth a refresh.
      expect(vi.mocked(fetchPlaybackInfo).mock.calls.length).toBe(callsBeforeError);
    },
  );

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
    video.currentTime = 0;
    fireEvent.loadedMetadata(video);
    expect(video.currentTime).toBe(87);
  });

  it("goes reveal-terminal on a second 401 after the refreshed stream token also expires", async () => {
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
    fakeHlsInstances[0].emitError({ fatal: true, response: { code: 401 } });
    await waitFor(() =>
      expect(vi.mocked(fetchPlaybackInfo).mock.calls.length).toBe(callsBeforeFirstError + 1),
    );
    await waitFor(() =>
      expect(screen.getByTestId("stream-url")).toHaveTextContent(
        "/api/media/hls/token2/master.m3u8",
      ),
    );

    const callsBeforeSecondError = vi.mocked(fetchPlaybackInfo).mock.calls.length;
    await expectEpochReset(() => {
      fakeHlsInstances[0].emitError({ fatal: true, response: { code: 401 } });
    });

    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("terminal"));
    expect(screen.getByTestId("message")).toHaveTextContent("terminal unavailable");
    expect(screen.getByTestId("stream-url")).toBeEmptyDOMElement();
    expect(screen.getByTestId("info")).toHaveTextContent("none");
    // Exactly one refresh attempt total — the second 401 does not retry again.
    expect(vi.mocked(fetchPlaybackInfo).mock.calls.length).toBe(callsBeforeSecondError);
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

  // feature:hls-error-recovery
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

    it("keeps a reveal-terminal refusal terminal even when it is typed as a network error", async () => {
      const hls = await renderHlsPlaying();
      await expectEpochReset(() => {
        hls.emitError({ ...networkError, response: { code: 410 } });
      });
      await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("terminal"));
      expect(hls.startLoad).not.toHaveBeenCalled();
      expect(hls.destroyed()).toBe(true);
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
  });
});
