import "@testing-library/jest-dom/vitest";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, type NavigateFunction, Route, Routes, useNavigate } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setIdentityEpochSignal } from "@/lib/api";
import {
  fetchClientPrefs,
  fetchMedia,
  fetchPlaybackAdjustmentRanges,
  setClientPref,
} from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import type { PlaybackAdjustment, PlaybackInfo } from "../lib/playback";
import { PlayerTransport } from "../player/PlayerTransport";
import type { PlayerState } from "../player/usePlayer";
import { DEFAULT_ADJUSTMENT, usePlayer } from "../player/usePlayer";
import { buildTimestampedLink, PlayerPage } from "./PlayerPage";

vi.mock("@/lib/session", () => ({
  useSession: () => ({ revealed: true }),
  useViewerIdentity: () => "viewer-1",
}));
vi.mock("@/lib/graphql", () => ({
  fetchClientPrefs: vi.fn().mockResolvedValue([]),
  setClientPref: vi.fn().mockResolvedValue(undefined),
  fetchMedia: vi.fn().mockResolvedValue(null),
  fetchPlaybackAdjustmentRanges: vi.fn().mockResolvedValue(null),
}));
vi.mock("../player/usePlayer", () => ({
  usePlayer: vi.fn(),
  DEFAULT_ADJUSTMENT: { pitchShiftSemitones: 0, tempo: 1 },
}));
vi.mock("../player/PlayerTransport", () => ({ PlayerTransport: vi.fn(() => null) }));

function playerState(overrides: Partial<PlayerState>): PlayerState {
  return {
    phase: "loading",
    message: "",
    terminal: false,
    info: null,
    stream: null,
    decision: "",
    markers: [],
    trickplay: null,
    trickplayBaseURL: "",
    hlsActive: false,
    recovering: false,
    tracksVersion: 0,
    mediaID: "media-alpha",
    ...overrides,
  };
}

function renderPlayer(
  state: PlayerState,
  navState?: unknown,
  adjustment: PlaybackAdjustment = DEFAULT_ADJUSTMENT,
) {
  const retry = vi.fn();
  const adjust = vi.fn();
  vi.mocked(usePlayer).mockReturnValue({
    videoRef: { current: null },
    hlsRef: { current: null },
    state,
    retry,
    switchToHls: vi.fn(() => false as const),
    adjustment,
    adjust,
  });
  const tree = () => (
    <I18nProvider>
      <MemoryRouter initialEntries={[{ pathname: "/watch/media-alpha", state: navState }]}>
        <Routes>
          <Route path="/watch/:mediaId" element={<PlayerPage />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>
  );
  const view = render(tree());
  return { retry, adjust, rerender: () => view.rerender(tree()) };
}

function stageVideo(): HTMLVideoElement {
  const video = document.querySelector("video");
  if (!video) throw new Error("no <video> on the stage");
  return video;
}

// The <video> presented its first frame, so the loading spinner clears.
function presentFirstFrame(): HTMLVideoElement {
  const video = stageVideo();
  fireEvent.loadedData(video);
  return video;
}

describe("PlayerPage timestamp links", () => {
  afterEach(() => vi.unstubAllEnvs());

  it("keeps shared playback links under the Vite base URL", () => {
    vi.stubEnv("BASE_URL", "/web/");
    expect(buildTimestampedLink("media/alpha", 42)).toBe(
      `${window.location.origin}/web/watch/media%2Falpha?t=42`,
    );
  });
});

// feature:hls-error-recovery
describe("PlayerPage failure and recovery states", () => {
  beforeEach(() => vi.mocked(usePlayer).mockReset());

  it("offers Try again on a non-terminal message and routes it to the engine retry", () => {
    const { retry } = renderPlayer(
      playerState({ phase: "message", message: "Playback failed. Please try again." }),
    );
    expect(screen.getByRole("alert")).toHaveTextContent("Playback failed. Please try again.");
    const tryAgain = screen.getByRole("button", { name: "Try again" });
    expect(retry).not.toHaveBeenCalled();
    fireEvent.click(tryAgain);
    expect(retry).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("button", { name: "Back to Browse" })).not.toBeInTheDocument();
  });

  it("renders exactly one h1 on the player route (CORE-0089)", () => {
    renderPlayer(playerState({ phase: "playing" }));
    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
  });

  it("keeps a reveal-terminal block terminal: Back to Browse, no retry", () => {
    const { retry } = renderPlayer(
      playerState({ phase: "terminal", terminal: true, message: "Unavailable" }),
    );
    expect(screen.getByRole("button", { name: "Back to Browse" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Try again" })).not.toBeInTheDocument();
    expect(retry).not.toHaveBeenCalled();
  });

  it("renders a non-modal reconnecting status while hls.js recovers", () => {
    renderPlayer(playerState({ phase: "playing", hlsActive: true, recovering: true }));
    presentFirstFrame();
    expect(screen.getByRole("status")).toHaveTextContent("Reconnecting…");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("hides the reconnecting status once playback is healthy", () => {
    renderPlayer(playerState({ phase: "playing", hlsActive: true }));
    presentFirstFrame();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("offers Back to Browse, not Try again, when another attempt cannot help (CORE-1146)", () => {
    const { retry } = renderPlayer(
      playerState({
        phase: "message",
        message: "This item is not available.",
        action: "back",
      }),
    );
    expect(screen.getByRole("alert")).toHaveTextContent("This item is not available.");
    expect(screen.queryByRole("button", { name: "Try again" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Back to Browse" }));
    expect(retry).not.toHaveBeenCalled();
  });

  it("shows the server's reason under the message (CORE-1146)", () => {
    renderPlayer(
      playerState({
        phase: "message",
        message: "Playback unavailable.",
        detail: "playback info unavailable",
      }),
    );
    expect(screen.getByText("Server said: playback info unavailable")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Try again" })).toBeInTheDocument();
  });

  it("passes copy for every unavailable reason to the engine", () => {
    renderPlayer(playerState({ phase: "loading" }));
    const messages = vi.mocked(usePlayer).mock.calls.at(-1)?.[1];
    expect(messages).toMatchObject({
      mediaFileMissing: expect.any(String),
      mediaNotAvailable: expect.any(String),
      transcodeDisallowed: expect.any(String),
      streamLimitReached:
        "This profile has reached its stream limit. Stop another stream, then try again.",
      transcodeCapacityReached: expect.any(String),
      dolbyVisionUnavailable: expect.any(String),
      hdrToneMapUnavailable: expect.any(String),
      sourceEndsEarly: expect.any(String),
    });
  });

  it("shows Waiting for network instead of Reconnecting while offline (CORE-1362)", () => {
    renderPlayer(
      playerState({ phase: "playing", hlsActive: true, recovering: true, waitingForNetwork: true }),
    );
    presentFirstFrame();
    expect(screen.getByRole("status")).toHaveTextContent("Waiting for network…");
    expect(screen.queryByText("Reconnecting…")).not.toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});

describe("PlayerPage loading spinner (CORE-1363)", () => {
  beforeEach(() => vi.mocked(usePlayer).mockReset());
  afterEach(() => vi.useRealTimers());

  it("shows while playback info loads", () => {
    renderPlayer(playerState({ phase: "loading" }));
    expect(screen.getByRole("status")).toHaveTextContent("Loading…");
  });

  it("shows from attach until the first frame", () => {
    renderPlayer(playerState({ phase: "playing" }));
    expect(screen.getByRole("status")).toHaveTextContent("Loading…");
    presentFirstFrame();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("shows when playback waits for data longer than 500 ms, and clears when it resumes", () => {
    vi.useFakeTimers();
    renderPlayer(playerState({ phase: "playing" }));
    const video = presentFirstFrame();
    Object.defineProperty(video, "paused", { configurable: true, value: false });
    fireEvent.waiting(video);
    act(() => {
      vi.advanceTimersByTime(499);
    });
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    act(() => {
      vi.advanceTimersByTime(1);
    });
    expect(screen.getByRole("status")).toHaveTextContent("Loading…");
    fireEvent.playing(video);
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("clears a stall once time moves again", () => {
    vi.useFakeTimers();
    renderPlayer(playerState({ phase: "playing" }));
    const video = presentFirstFrame();
    Object.defineProperty(video, "paused", { configurable: true, value: false });
    fireEvent.stalled(video);
    act(() => {
      vi.advanceTimersByTime(500);
    });
    expect(screen.getByRole("status")).toHaveTextContent("Loading…");
    video.currentTime = 5;
    fireEvent.timeUpdate(video);
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("stays hidden for a wait while paused", () => {
    vi.useFakeTimers();
    renderPlayer(playerState({ phase: "playing" }));
    const video = presentFirstFrame();
    fireEvent.waiting(video);
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("uses the backdrop as the video poster", async () => {
    vi.mocked(fetchMedia).mockResolvedValueOnce({
      id: "media-alpha",
      title: "Alpha",
      artworkSlots: { poster: "/art/poster.jpg", backdrop: "/art/backdrop.jpg" },
      credits: [],
    } as never);
    renderPlayer(
      playerState({ phase: "playing", info: { mediaClass: "video" } as PlayerState["info"] }),
    );
    await waitFor(() => expect(stageVideo()).toHaveAttribute("poster", "/art/backdrop.jpg"));
  });
});

describe("PlayerPage audio items (CORE-1143)", () => {
  beforeEach(() => {
    vi.mocked(usePlayer).mockReset();
    vi.mocked(PlayerTransport).mockClear();
  });

  const queue = [
    { id: "media-prev", title: "Before" },
    { id: "media-alpha", title: "Alpha" },
    { id: "media-next", title: "After" },
  ];

  it("shows the video surface, not the artwork stage, for a composed-picture audio item (CORE-1656)", () => {
    renderPlayer(
      playerState({
        phase: "playing",
        info: { mediaClass: "audio", reason: "composed-picture" } as PlayerState["info"],
      }),
      { title: "Alpha" },
    );
    expect(document.querySelector(".bf-audio-stage")).toBeNull();
    expect(document.querySelector("video")).not.toBeNull();
  });

  it("shows artwork, title and credits for an audio item, and declares the queue's next item", async () => {
    vi.mocked(fetchMedia).mockResolvedValueOnce({
      id: "media-alpha",
      title: "Alpha",
      artworkSlots: { poster: "/art/alpha.jpg" },
      credits: [
        { entityID: "p1", title: "Ann", kind: "artist", archetype: "person", relation: "by" },
        { entityID: "c1", title: "Record", kind: "album", archetype: "container", relation: "on" },
      ],
    } as never);
    renderPlayer(
      playerState({ phase: "playing", info: { mediaClass: "audio" } as PlayerState["info"] }),
      { title: "Alpha", queue, queueIdentity: "viewer-1" },
    );

    expect(await screen.findByText("Ann")).toBeInTheDocument();
    expect(screen.getByText("Record")).toBeInTheDocument();
    expect(screen.getByText("2 of 3")).toBeInTheDocument();
    expect(document.querySelector(".bf-audio-artwork img")).toHaveAttribute(
      "src",
      "/art/alpha.jpg",
    );
    expect(vi.mocked(usePlayer).mock.calls.at(-1)?.[3]).toBe("media-next");

    await waitFor(() => {
      const props = vi.mocked(PlayerTransport).mock.calls.at(-1)?.[0];
      expect(props?.onPreviousInQueue).toBeTypeOf("function");
      expect(props?.onNextInQueue).toBeTypeOf("function");
      expect(props?.showSkipIntervalCollection).toBe("Record");
    });
  });

  it("declares an empty next item on a card queue's last item, so Play all is not resumed", () => {
    renderPlayer(
      playerState({ phase: "playing", info: { mediaClass: "audio" } as PlayerState["info"] }),
      { title: "Alpha", queue: queue.slice(0, 2), queueIdentity: "viewer-1" },
    );
    expect(vi.mocked(usePlayer).mock.calls.at(-1)?.[3]).toBe("");
  });

  it("drops a queue listed for another viewer identity (relock or profile switch)", () => {
    renderPlayer(
      playerState({ phase: "playing", info: { mediaClass: "audio" } as PlayerState["info"] }),
      { title: "Alpha", queue, queueIdentity: "viewer-revealed" },
    );
    expect(screen.queryByText("2 of 3")).toBeNull();
    expect(vi.mocked(usePlayer).mock.calls.at(-1)?.[3]).toBeUndefined();
    const props = vi.mocked(PlayerTransport).mock.calls.at(-1)?.[0];
    expect(props?.onPreviousInQueue).toBeUndefined();
    expect(props?.onNextInQueue).toBeUndefined();
  });

  it("keeps the video stage, and no queue controls, for a video item without a queue", () => {
    renderPlayer(
      playerState({ phase: "playing", info: { mediaClass: "video" } as PlayerState["info"] }),
    );
    expect(document.querySelector(".bf-audio-stage")).toBeNull();
    const props = vi.mocked(PlayerTransport).mock.calls.at(-1)?.[0];
    expect(props?.onPreviousInQueue).toBeUndefined();
    expect(props?.onNextInQueue).toBeUndefined();
    expect(props?.showSkipIntervalLabel).toBe("");
  });
});

describe("PlayerPage class-narrowed queue (CORE-1490, CORE-1309)", () => {
  beforeEach(() => {
    vi.mocked(usePlayer).mockReset();
    vi.mocked(PlayerTransport).mockClear();
  });

  const audioInfo = (next?: { mediaID: string; mediaClass: string }) =>
    ({ mediaClass: "audio", ...(next ? { next } : {}) }) as PlayerState["info"];
  const lastTransportProps = () => vi.mocked(PlayerTransport).mock.calls.at(-1)?.[0];
  const lastPlayedID = () => vi.mocked(usePlayer).mock.calls.at(-1)?.[0];

  it("steps only through tracks of a mixed queue whose rows report their class", async () => {
    renderPlayer(playerState({ phase: "playing", info: audioInfo() }), {
      title: "Alpha",
      queue: [
        { id: "media-prev", title: "Before", mediaClass: "audio" },
        { id: "media-v1", title: "Film", mediaClass: "video" },
        { id: "media-alpha", title: "Alpha", mediaClass: "audio" },
        { id: "media-v2", title: "Clip", mediaClass: "video" },
        { id: "media-next", title: "After", mediaClass: "audio" },
      ],
      queueIdentity: "viewer-1",
    });
    expect(screen.getByText("2 of 3")).toBeInTheDocument();
    expect(vi.mocked(usePlayer).mock.calls.at(-1)?.[3]).toBe("media-next");

    act(() => lastTransportProps()?.onNextInQueue?.());
    await waitFor(() => expect(lastPlayedID()).toBe("media-next"));
    // The narrowed queue travels with it: the videos stay out.
    expect(screen.getByText("3 of 3")).toBeInTheDocument();
  });

  it("drops the declared next item once playback-info reports it is a video", async () => {
    renderPlayer(
      playerState({
        phase: "playing",
        info: audioInfo({ mediaID: "media-v1", mediaClass: "video" }),
      }),
      {
        title: "Alpha",
        queue: [
          { id: "media-alpha", title: "Alpha" },
          { id: "media-v1", title: "Film" },
          { id: "media-t2", title: "Second" },
        ],
        queueIdentity: "viewer-1",
      },
    );
    expect(await screen.findByText("1 of 2")).toBeInTheDocument();

    act(() => lastTransportProps()?.onNextInQueue?.());
    await waitFor(() => expect(lastPlayedID()).toBe("media-t2"));
    expect(screen.getByText("2 of 2")).toBeInTheDocument();
  });

  it("shows the audio stage while loading when the queue entry is a track", () => {
    renderPlayer(playerState({ phase: "loading" }), {
      title: "Alpha",
      queue: [
        { id: "media-alpha", title: "Alpha", mediaClass: "audio" },
        { id: "media-v1", title: "Film", mediaClass: "video" },
      ],
      queueIdentity: "viewer-1",
    });
    const stage = document.querySelector(".bf-audio-stage");
    expect(stage).not.toBeNull();
    expect(stage).toHaveTextContent("Alpha");
    expect(stage?.querySelector("[data-visualizer-slot]")).not.toBeNull();
    // Only the track remains, so there is no "1 of 2" and no next to declare.
    expect(vi.mocked(usePlayer).mock.calls.at(-1)?.[3]).toBe("");
  });

  it("keeps the video stage while loading a video or an item of unknown class", () => {
    renderPlayer(playerState({ phase: "loading" }), {
      title: "Alpha",
      queue: [
        { id: "media-alpha", title: "Alpha", mediaClass: "video" },
        { id: "media-t1", title: "Track" },
      ],
      queueIdentity: "viewer-1",
    });
    expect(document.querySelector(".bf-audio-stage")).toBeNull();
    // An entry of unknown class stays queued.
    expect(vi.mocked(usePlayer).mock.calls.at(-1)?.[3]).toBe("media-t1");
  });
});

describe("PlayerPage Back", () => {
  function renderAt(entries: string[]) {
    vi.mocked(usePlayer).mockReturnValue({
      videoRef: { current: null },
      hlsRef: { current: null },
      state: playerState({ phase: "playing" }),
      retry: vi.fn(),
      switchToHls: vi.fn(() => false as const),
      adjustment: DEFAULT_ADJUSTMENT,
      adjust: vi.fn(),
    });
    render(
      <I18nProvider>
        <MemoryRouter initialEntries={entries} initialIndex={entries.length - 1}>
          <Routes>
            <Route path="/" element={<p>home route</p>} />
            <Route path="/entity/:entityId" element={<p>entity route</p>} />
            <Route path="/watch/:mediaId" element={<PlayerPage />} />
          </Routes>
        </MemoryRouter>
      </I18nProvider>,
    );
  }

  it("returns to the page the player was opened from", () => {
    renderAt(["/", "/entity/e1", "/watch/media-alpha"]);
    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(screen.getByText("entity route")).toBeInTheDocument();
  });

  it("falls back to Browse on a deep link with no in-app history", () => {
    renderAt(["/watch/media-alpha"]);
    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(screen.getByText("home route")).toBeInTheDocument();
  });
});

describe("PlayerPage subtitle appearance prefs (CORE-1681)", () => {
  beforeEach(() => {
    vi.mocked(usePlayer).mockReset();
    vi.mocked(setClientPref).mockReset();
  });
  afterEach(() => vi.mocked(fetchClientPrefs).mockResolvedValue([]));

  function transportProps() {
    const props = vi.mocked(PlayerTransport).mock.lastCall?.[0];
    if (!props) throw new Error("PlayerTransport not rendered");
    return props;
  }

  it("keeps a choice made before the Profile read returns", async () => {
    let answer: (prefs: { key: string; value: string }[]) => void = () => {};
    vi.mocked(fetchClientPrefs).mockReturnValue(new Promise((resolve) => (answer = resolve)));
    vi.mocked(setClientPref).mockResolvedValue({ key: "" });
    renderPlayer(playerState({ phase: "playing" }));
    act(() => transportProps().onSubtitleAppearanceChange?.("color", "#ffff00"));
    await act(async () =>
      answer([
        { key: "subtitle.color", value: "#00ff00" },
        { key: "subtitle.font", value: "serif" },
      ]),
    );
    expect(transportProps().subtitleAppearance).toMatchObject({ color: "#ffff00", font: "serif" });
  });

  it("clears every key on Reset, one write at a time", async () => {
    let finishFirst: () => void = () => {};
    vi.mocked(setClientPref)
      .mockReturnValueOnce(new Promise((resolve) => (finishFirst = () => resolve({ key: "" }))))
      .mockResolvedValue({ key: "" });
    renderPlayer(playerState({ phase: "playing" }));
    act(() => transportProps().onSubtitleAppearanceReset?.());
    await waitFor(() => expect(setClientPref).toHaveBeenCalledTimes(1));
    await act(async () => finishFirst());
    await waitFor(() => expect(setClientPref).toHaveBeenCalledTimes(7));
    expect(vi.mocked(setClientPref).mock.calls.map(([key, value]) => `${key}=${value}`)).toEqual([
      "subtitle.size=",
      "subtitle.background=",
      "subtitle.font=",
      "subtitle.color=",
      "subtitle.opacity=",
      "subtitle.edge=",
      "subtitle.position=",
    ]);
  });

  it("drops writes still queued when the identity epoch ends", async () => {
    const epoch = new AbortController();
    setIdentityEpochSignal(epoch.signal);
    let finishFirst: () => void = () => {};
    vi.mocked(setClientPref)
      .mockReturnValueOnce(new Promise((resolve) => (finishFirst = () => resolve({ key: "" }))))
      .mockResolvedValue({ key: "" });
    try {
      renderPlayer(playerState({ phase: "playing" }));
      act(() => transportProps().onSubtitleAppearanceReset?.());
      await waitFor(() => expect(setClientPref).toHaveBeenCalledTimes(1));
      epoch.abort();
      await act(async () => finishFirst());
      expect(setClientPref).toHaveBeenCalledTimes(1);
    } finally {
      setIdentityEpochSignal();
    }
  });
});

// CORE-1659: key and tempo controls for a karaoke song.
describe("PlayerPage karaoke key and tempo controls", () => {
  const ranges = {
    pitchShiftSemitonesMin: -6,
    pitchShiftSemitonesMax: 6,
    tempoMin: 0.75,
    tempoMax: 1.25,
  };
  // A synthetic karaoke song: an audio item the server composes a picture for.
  const karaokeInfo = {
    mediaClass: "audio",
    reason: "composed-picture",
    streams: [],
  } as unknown as PlaybackInfo;

  beforeEach(() => {
    vi.mocked(usePlayer).mockReset();
    vi.mocked(fetchPlaybackAdjustmentRanges).mockResolvedValue(ranges);
  });
  afterEach(() => vi.mocked(fetchPlaybackAdjustmentRanges).mockResolvedValue(null));

  it("shows key and tempo controls for a karaoke song when the server advertises them", async () => {
    renderPlayer(playerState({ phase: "playing", info: karaokeInfo }));
    const group = await screen.findByRole("group", { name: "Key and tempo" });
    expect(group).toHaveTextContent("Key 0");
    expect(group).toHaveTextContent("Tempo 100%");
    for (const name of [
      "Lower the key by a semitone",
      "Raise the key by a semitone",
      "Reset the key",
      "Slow down by 5%",
      "Speed up by 5%",
      "Reset the tempo",
    ]) {
      expect(screen.getByRole("button", { name })).toBeInTheDocument();
    }
    expect(within(group).getByRole("status")).toHaveTextContent("Key 0, Tempo 100%");
  });

  it("hides the controls when the server does not advertise the capability", async () => {
    vi.mocked(fetchPlaybackAdjustmentRanges).mockResolvedValue(null);
    renderPlayer(playerState({ phase: "playing", info: karaokeInfo }));
    await waitFor(() => expect(fetchPlaybackAdjustmentRanges).toHaveBeenCalled());
    await act(async () => {});
    expect(screen.queryByRole("group", { name: "Key and tempo" })).not.toBeInTheDocument();
  });

  it("hides the controls for a Media that is not a karaoke song", async () => {
    renderPlayer(
      playerState({
        phase: "playing",
        info: {
          mediaClass: "video",
          reason: "direct-play",
          streams: [],
        } as unknown as PlaybackInfo,
      }),
    );
    await waitFor(() => expect(fetchPlaybackAdjustmentRanges).toHaveBeenCalled());
    await act(async () => {});
    expect(screen.queryByRole("group", { name: "Key and tempo" })).not.toBeInTheDocument();
  });

  it("applies a key and a tempo step through the player", async () => {
    const { adjust } = renderPlayer(playerState({ phase: "playing", info: karaokeInfo }));
    fireEvent.click(await screen.findByRole("button", { name: "Raise the key by a semitone" }));
    expect(adjust).toHaveBeenLastCalledWith({ pitchShiftSemitones: 1, tempo: 1 });
    fireEvent.click(screen.getByRole("button", { name: "Slow down by 5%" }));
    expect(adjust).toHaveBeenLastCalledWith({ pitchShiftSemitones: 0, tempo: 0.95 });
  });

  it("shows the applied key and tempo the server echoed", async () => {
    const adjustment = { pitchShiftSemitones: 2, tempo: 0.95 };
    renderPlayer(
      playerState({ phase: "playing", info: { ...karaokeInfo, adjustment }, adjustment }),
      undefined,
      adjustment,
    );
    const group = await screen.findByRole("group", { name: "Key and tempo" });
    expect(group).toHaveTextContent("Key +2");
    expect(group).toHaveTextContent("Tempo 95%");
    expect(group).not.toHaveTextContent("Not applied");
    expect(within(group).getByRole("status")).toHaveTextContent("Key +2, Tempo 95%");
  });

  it("says Not applied when the server did not echo the adjustment", async () => {
    const adjustment = { pitchShiftSemitones: 1, tempo: 1 };
    renderPlayer(
      playerState({ phase: "playing", info: karaokeInfo, adjustment }),
      undefined,
      adjustment,
    );
    const group = await screen.findByRole("group", { name: "Key and tempo" });
    expect(group).toHaveTextContent("Key +1");
    expect(group).toHaveTextContent("Not applied");
    expect(within(group).getByRole("status")).toHaveTextContent("Key +1, Tempo 100%: not applied");
  });

  it("keeps the controls mounted while a change restarts the stream", async () => {
    const adjustment = { pitchShiftSemitones: 1, tempo: 1 };
    const { adjust, rerender } = renderPlayer(playerState({ phase: "playing", info: karaokeInfo }));
    const up = await screen.findByRole("button", { name: "Raise the key by a semitone" });
    up.focus();
    fireEvent.click(up);
    expect(adjust).toHaveBeenCalledTimes(1);
    // The restart clears state.info; the song verdict stays for this Media.
    vi.mocked(usePlayer).mockReturnValue({
      ...vi.mocked(usePlayer).mock.results.at(-1)?.value,
      state: playerState({ phase: "loading" }),
      adjustment,
    });
    rerender();
    expect(up).toHaveFocus();
    expect(screen.getByRole("group", { name: "Key and tempo" })).toHaveTextContent("Key +1");
    expect(screen.getByRole("group", { name: "Key and tempo" })).not.toHaveTextContent(
      "Not applied",
    );
  });

  it("keeps the controls, focus and notice when a change is declined to bare audio", async () => {
    const adjustment = { pitchShiftSemitones: 1, tempo: 1 };
    const { adjust, rerender } = renderPlayer(playerState({ phase: "playing", info: karaokeInfo }));
    const up = await screen.findByRole("button", { name: "Raise the key by a semitone" });
    up.focus();
    fireEvent.click(up);
    expect(adjust).toHaveBeenCalledTimes(1);
    // The server declined: plain audio, no composed picture, no adjustment echo.
    vi.mocked(usePlayer).mockReturnValue({
      ...vi.mocked(usePlayer).mock.results.at(-1)?.value,
      state: playerState({
        phase: "playing",
        info: { mediaClass: "audio", reason: "direct-play", streams: [] },
        adjustment,
      }),
      adjustment,
    });
    rerender();
    const group = screen.getByRole("group", { name: "Key and tempo" });
    expect(up).toHaveFocus();
    expect(within(group).getByRole("status")).toHaveTextContent(/not applied/i);
  });

  it("does not carry a karaoke song's controls to the next Media", async () => {
    const karaokeState = playerState({ phase: "playing", info: karaokeInfo });
    let current = karaokeState;
    vi.mocked(usePlayer).mockImplementation(() => ({
      videoRef: { current: null },
      hlsRef: { current: null },
      state: current,
      retry: vi.fn(),
      switchToHls: vi.fn(() => false as const),
      adjustment: DEFAULT_ADJUSTMENT,
      adjust: vi.fn(),
    }));
    let navigate: NavigateFunction | undefined;
    function Navigator() {
      navigate = useNavigate();
      return null;
    }
    const tree = () => (
      <I18nProvider>
        <MemoryRouter initialEntries={["/watch/media-alpha"]}>
          <Navigator />
          <Routes>
            <Route path="/watch/:mediaId" element={<PlayerPage />} />
          </Routes>
        </MemoryRouter>
      </I18nProvider>
    );
    const view = render(tree());
    await screen.findByRole("group", { name: "Key and tempo" });

    // The new route renders first with the karaoke song's playback state.
    await act(async () => navigate?.("/watch/media-beta"));
    expect(screen.queryByRole("group", { name: "Key and tempo" })).not.toBeInTheDocument();

    // Then an ordinary track loads for it.
    current = playerState({
      phase: "playing",
      mediaID: "media-beta",
      info: { mediaClass: "audio", reason: "direct-play", streams: [] } as unknown as PlaybackInfo,
    });
    view.rerender(tree());
    expect(screen.queryByRole("group", { name: "Key and tempo" })).not.toBeInTheDocument();
  });

  it("clamps to the advertised range and keeps the control at its limit focusable", async () => {
    vi.mocked(fetchPlaybackAdjustmentRanges).mockResolvedValue({
      pitchShiftSemitonesMin: -2,
      pitchShiftSemitonesMax: 2,
      tempoMin: 0.9,
      tempoMax: 1.1,
    });
    const adjustment = { pitchShiftSemitones: 2, tempo: 0.9 };
    const { adjust } = renderPlayer(
      playerState({ phase: "playing", info: { ...karaokeInfo, adjustment }, adjustment }),
      undefined,
      adjustment,
    );
    const up = await screen.findByRole("button", { name: "Raise the key by a semitone" });
    const slower = screen.getByRole("button", { name: "Slow down by 5%" });
    expect(up).toHaveAttribute("aria-disabled", "true");
    expect(up).not.toBeDisabled();
    expect(slower).toHaveAttribute("aria-disabled", "true");
    fireEvent.click(up);
    fireEvent.click(slower);
    expect(adjust).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Speed up by 5%" }));
    expect(adjust).toHaveBeenLastCalledWith({ pitchShiftSemitones: 2, tempo: 0.95 });
    fireEvent.click(screen.getByRole("button", { name: "Reset the key" }));
    expect(adjust).toHaveBeenLastCalledWith({ pitchShiftSemitones: 0, tempo: 0.9 });
  });
});
