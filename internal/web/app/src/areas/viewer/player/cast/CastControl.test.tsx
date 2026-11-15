import "@testing-library/jest-dom/vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { createRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { CastControl } from "./CastControl";
import { resetCastSdkForTests } from "./castSdk";
import { resetCastContextForTests } from "./useCast";

// No Cast device is available to the test suite: these tests drive a mocked
// Cast framework that follows the Web Sender SDK's shape.

const postJSON = vi.fn();
vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  postJSON: (...args: unknown[]) => postJSON(...args),
}));

const CHROME_UA =
  "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36";

type Listener = (event: { castState?: string }) => void;

function fakeCast(initialState: string) {
  const contextListeners = new Map<string, Set<Listener>>();
  const playerListeners = new Set<() => void>();
  const loadMedia = vi.fn(async (_request: unknown) => undefined);
  const session = { loadMedia, getCastDevice: () => ({ friendlyName: "Lounge screen" }) };
  let state = initialState;
  let current: typeof session | null = null;
  const player = {
    isConnected: false,
    isMediaLoaded: false,
    isPaused: true,
    currentTime: 0,
    duration: 0,
  };
  const controller = {
    playOrPause: vi.fn(),
    seek: vi.fn(),
    stop: vi.fn(),
    addEventListener: (_type: string, fn: () => void) => playerListeners.add(fn),
    removeEventListener: (_type: string, fn: () => void) => playerListeners.delete(fn),
  };
  const context = {
    setOptions: vi.fn(),
    getCastState: () => state,
    requestSession: vi.fn(async () => {
      current = session;
      setState("CONNECTED");
      return undefined;
    }),
    getCurrentSession: () => current,
    endCurrentSession: vi.fn(() => {
      current = null;
      setState("NOT_CONNECTED");
    }),
    addEventListener: (type: string, fn: Listener) => {
      const set = contextListeners.get(type) ?? new Set();
      set.add(fn);
      contextListeners.set(type, set);
    },
    removeEventListener: (type: string, fn: Listener) => contextListeners.get(type)?.delete(fn),
  };
  function setState(next: string) {
    state = next;
    for (const fn of contextListeners.get("caststatechanged") ?? []) fn({ castState: next });
  }
  function remoteChange(next: Partial<typeof player>) {
    Object.assign(player, next);
    for (const fn of playerListeners) fn();
  }
  class MediaInfo {
    [key: string]: unknown;
    constructor(
      public contentId: string,
      public contentType: string,
    ) {}
  }
  class LoadRequest {
    [key: string]: unknown;
    constructor(public media: MediaInfo) {}
  }
  class Track {
    [key: string]: unknown;
    constructor(
      public trackId: number,
      public type: string,
    ) {}
  }
  class GenericMediaMetadata {
    [key: string]: unknown;
  }
  const framework = {
    CastContext: { getInstance: () => context },
    CastContextEventType: {
      CAST_STATE_CHANGED: "caststatechanged",
      SESSION_STATE_CHANGED: "sessionstatechanged",
    },
    CastState: {
      NO_DEVICES_AVAILABLE: "NO_DEVICES_AVAILABLE",
      NOT_CONNECTED: "NOT_CONNECTED",
      CONNECTING: "CONNECTING",
      CONNECTED: "CONNECTED",
    },
    SessionState: {},
    RemotePlayer: function RemotePlayer() {
      return player;
    },
    RemotePlayerController: function RemotePlayerController() {
      return controller;
    },
    RemotePlayerEventType: { ANY_CHANGE: "anyChanged" },
  };
  const chromeCast = {
    AutoJoinPolicy: { ORIGIN_SCOPED: "origin_scoped" },
    media: {
      DEFAULT_MEDIA_RECEIVER_APP_ID: "CC1AD845",
      MediaInfo,
      LoadRequest,
      Track,
      GenericMediaMetadata,
      TrackType: { TEXT: "TEXT" },
      TextTrackType: { SUBTITLES: "SUBTITLES", CAPTIONS: "CAPTIONS" },
      StreamType: { BUFFERED: "BUFFERED" },
      HlsSegmentFormat: { FMP4: "fmp4" },
      HlsVideoSegmentFormat: { FMP4: "fmp4" },
    },
  };
  return { framework, chromeCast, context, controller, loadMedia, setState, remoteChange };
}

const win = window as unknown as Record<string, unknown>;
const originalUA = Object.getOwnPropertyDescriptor(window.navigator, "userAgent");

function installCast(fake: ReturnType<typeof fakeCast> | null, userAgent = CHROME_UA) {
  Object.defineProperty(window, "isSecureContext", { value: true, configurable: true });
  Object.defineProperty(window.navigator, "userAgent", { value: userAgent, configurable: true });
  win.chrome = fake ? { cast: fake.chromeCast } : {};
  win.cast = fake ? { framework: fake.framework } : undefined;
}

function renderControl() {
  const videoRef = createRef<HTMLVideoElement>();
  const video = document.createElement("video");
  Object.defineProperty(video, "currentTime", { value: 0, writable: true, configurable: true });
  video.pause = vi.fn();
  (videoRef as { current: HTMLVideoElement | null }).current = video;
  render(
    <I18nProvider>
      <CastControl videoRef={videoRef} mediaID="media-1" mediaTitle="Sample video" />
    </I18nProvider>,
  );
  return video;
}

beforeEach(() => {
  postJSON.mockReset();
  resetCastSdkForTests();
  resetCastContextForTests();
});

afterEach(() => {
  delete win.chrome;
  delete win.cast;
  if (originalUA) Object.defineProperty(window.navigator, "userAgent", originalUA);
  else delete (window.navigator as unknown as Record<string, unknown>).userAgent;
  document.head.querySelectorAll("script").forEach((node) => node.remove());
});

describe("CastControl", () => {
  it("renders nothing and fetches nothing in a browser that cannot cast", async () => {
    installCast(null, "Mozilla/5.0 (X11; Linux x86_64; rv:128.0) Gecko/20100101 Firefox/128.0");
    renderControl();
    await act(async () => {});
    expect(screen.queryByRole("button", { name: "Cast to a device" })).not.toBeInTheDocument();
    expect(document.head.querySelector("script")).toBeNull();
  });

  it("stays hidden while no receiver is on the network", async () => {
    installCast(fakeCast("NO_DEVICES_AVAILABLE"));
    renderControl();
    await act(async () => {});
    expect(screen.queryByRole("button", { name: "Cast to a device" })).not.toBeInTheDocument();
  });

  it("casts with the resume point, the chosen subtitle and the server's receiver route", async () => {
    const fake = fakeCast("NOT_CONNECTED");
    installCast(fake);
    postJSON.mockResolvedValue({
      mediaClass: "video",
      streams: [{ kind: "hls", url: "/api/media/hls/tok/master.m3u8" }],
      timeline: { positionSeconds: 312 },
      captionTracks: [
        { id: "sidecar-a-0", url: "/api/media/sidecar/tok/0.vtt", format: "srt", language: "en" },
        { id: "sidecar-a-1", url: "/api/media/sidecar/tok/1.vtt", format: "srt", language: "fr" },
      ],
      selectedSubtitleTrack: { track: { language: "fr", kind: "sidecar", roles: {} } },
    });
    const video = renderControl();
    const button = await screen.findByRole("button", { name: "Cast to a device" });
    expect(fake.context.setOptions).toHaveBeenCalledWith({
      receiverApplicationId: "CC1AD845",
      autoJoinPolicy: "origin_scoped",
    });

    fireEvent.click(button);
    expect(fake.context.requestSession).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(fake.loadMedia).toHaveBeenCalledTimes(1));

    expect(video.pause).toHaveBeenCalled();
    const [path, body] = postJSON.mock.calls[0] as [
      string,
      { mediaID: string; capabilityProfile: { client: string } },
    ];
    expect(path).toBe("/api/playback-info");
    expect(body.mediaID).toBe("media-1");
    expect(body.capabilityProfile.client).toBe("chromecast-default-receiver");

    const request = fake.loadMedia.mock.calls[0]?.[0] as {
      autoplay: boolean;
      currentTime: number;
      activeTrackIds: number[];
      media: {
        contentId: string;
        contentType: string;
        hlsSegmentFormat: string;
        tracks: { trackContentId: string; trackContentType: string }[];
      };
    };
    expect(request.autoplay).toBe(true);
    expect(request.currentTime).toBe(312);
    expect(request.activeTrackIds).toEqual([2]);
    expect(request.media.contentId).toBe(`${window.location.origin}/api/media/hls/tok/master.m3u8`);
    expect(request.media.contentType).toBe("application/x-mpegurl");
    expect(request.media.hlsSegmentFormat).toBe("fmp4");
    expect(request.media.tracks.map((track) => track.trackContentType)).toEqual([
      "text/vtt",
      "text/vtt",
    ]);
    expect(await screen.findByText("Casting to Lounge screen")).toBeInTheDocument();
  });

  it("drives the receiver from the cast controls and hands position back on stop", async () => {
    const fake = fakeCast("NOT_CONNECTED");
    installCast(fake);
    postJSON.mockResolvedValue({
      streams: [{ kind: "direct-play", url: "/api/media/direct/tok" }],
    });
    const video = renderControl();
    fireEvent.click(await screen.findByRole("button", { name: "Cast to a device" }));
    await waitFor(() => expect(fake.loadMedia).toHaveBeenCalled());
    act(() =>
      fake.remoteChange({ isMediaLoaded: true, isPaused: false, currentTime: 40, duration: 600 }),
    );

    const controls = await screen.findByRole("group", { name: "Cast controls" });
    expect(controls).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Pause on the cast device" }));
    expect(fake.controller.playOrPause).toHaveBeenCalledTimes(1);

    fireEvent.change(screen.getByRole("slider", { name: "Cast position" }), {
      target: { value: "120" },
    });
    expect(fake.controller.seek).toHaveBeenCalledTimes(1);
    act(() => fake.remoteChange({ currentTime: 120 }));

    fireEvent.click(screen.getByRole("button", { name: "Stop casting" }));
    expect(fake.controller.stop).toHaveBeenCalledTimes(1);
    expect(fake.context.endCurrentSession).toHaveBeenCalledWith(true);
    await waitFor(() =>
      expect(screen.queryByRole("group", { name: "Cast controls" })).not.toBeInTheDocument(),
    );
    expect(video.currentTime).toBe(120);
  });

  it("says so when the receiver cannot play the video", async () => {
    const fake = fakeCast("NOT_CONNECTED");
    installCast(fake);
    postJSON.mockResolvedValue({
      streams: [{ kind: "direct-play", url: "/api/media/direct/tok" }],
    });
    fake.loadMedia.mockRejectedValueOnce("LOAD_FAILED");
    renderControl();
    fireEvent.click(await screen.findByRole("button", { name: "Cast to a device" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "The cast device could not play this video.",
    );
  });

  it("returns to idle when the receiver picker is closed", async () => {
    const fake = fakeCast("NOT_CONNECTED");
    installCast(fake);
    fake.context.requestSession.mockRejectedValueOnce("cancel");
    renderControl();
    fireEvent.click(await screen.findByRole("button", { name: "Cast to a device" }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Cast to a device" })).toBeEnabled(),
    );
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(postJSON).not.toHaveBeenCalled();
  });
});
