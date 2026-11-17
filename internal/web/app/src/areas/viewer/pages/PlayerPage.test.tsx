import "@testing-library/jest-dom/vitest";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, type NavigateFunction, Route, Routes, useNavigate } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setIdentityEpochSignal } from "@/lib/api";
import {
  fetchClientPrefs,
  fetchMedia,
  fetchPlaybackAdjustmentRanges,
  fetchPrePlayQueue,
  setClientPref,
} from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import type { PlaybackAdjustment, PlaybackInfo } from "../lib/playback";
import { readVersionChoice, writeVersionChoice } from "../lib/versionChoice";
import { PlayerHost } from "../player/PlayerHost";
import { PlayerTransport } from "../player/PlayerTransport";
import type { PlayerState } from "../player/usePlayer";
import { DEFAULT_ADJUSTMENT, usePlayer } from "../player/usePlayer";
import { watchPartyDefersUpNext } from "../player/watchPartyStore";
import { deliverPlayerCommand } from "../remote/remoteControl";
import { buildTimestampedLink, PlayerPage } from "./PlayerPage";

vi.mock("@/lib/session", () => ({
  useSession: () => ({ revealed: true, session: { profileID: "profile-a" } }),
  useViewerIdentity: () => "viewer-1",
}));
vi.mock("@/lib/graphql", () => ({
  fetchClientPrefs: vi.fn().mockResolvedValue([]),
  setClientPref: vi.fn().mockResolvedValue(undefined),
  fetchMedia: vi.fn().mockResolvedValue(null),
  fetchPlaybackAdjustmentRanges: vi.fn().mockResolvedValue(null),
  fetchPrePlayQueue: vi.fn().mockResolvedValue([]),
  // Off the home network: the player does not listen for a lead party.
  calibrateWatchPartyClock: vi.fn().mockRejectedValue(new Error("not on the home network")),
}));
vi.mock("../player/PrePlayStage", () => ({
  PrePlayStage: vi.fn(({ items, onDone }: { items: { title: string }[]; onDone: () => void }) => (
    <section aria-label="pre-play stage">
      <span>{items.map((item) => item.title).join(", ")}</span>
      <button type="button" onClick={onDone}>
        Skip all
      </button>
    </section>
  )),
}));
vi.mock("../player/usePlayer", () => ({
  usePlayer: vi.fn(),
  DEFAULT_ADJUSTMENT: { pitchShiftSemitones: 0, tempo: 1 },
}));
vi.mock("../player/PlayerTransport", () => ({ PlayerTransport: vi.fn(() => null) }));
vi.mock("../player/watchPartyStore", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../player/watchPartyStore")>()),
  watchPartyDefersUpNext: vi.fn(() => false),
}));

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
    restart: vi.fn(),
    switchToHls: vi.fn(() => false as const),
    adjustment,
    adjust,
  });
  const tree = () => (
    <I18nProvider>
      <MemoryRouter
        initialEntries={[
          {
            pathname: "/watch/media-alpha",
            // These cases test the title's player, so they arrive as an
            // advance does: past any pre-play queue (CORE-1701).
            state: { skipPrePlay: true, ...((navState as object | undefined) ?? {}) },
          },
        ]}
      >
        <Routes>
          <Route path="/watch/*" element={<PlayerPage />} />
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

  it("keeps the server's reason behind a Details disclosure (CORE-1146, CORE-2011)", () => {
    renderPlayer(
      playerState({
        phase: "message",
        message: "Playback unavailable.",
        detail: "playback info unavailable",
      }),
    );
    const reason = screen.getByText("Message from the server: playback info unavailable");
    expect(reason.closest("details")).not.toHaveAttribute("open");
    expect(screen.getByText("Details").tagName).toBe("SUMMARY");
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
      hdrToneMapDisabled: expect.any(String),
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

  it("never auto-advances into an issued next the learned class removed (CORE-1732)", async () => {
    // An unclassified queue of a track then a video: playback-info issues the
    // video as next (with streams) before its class is known.
    renderPlayer(
      playerState({
        phase: "playing",
        info: {
          mediaClass: "audio",
          next: {
            mediaID: "media-v1",
            mediaClass: "video",
            streams: [{ kind: "direct-play", url: "/api/media/v1" }],
          },
        } as PlayerState["info"],
      }),
      {
        title: "Alpha",
        queue: [
          { id: "media-alpha", title: "Alpha" },
          { id: "media-v1", title: "Film" },
        ],
        queueIdentity: "viewer-1",
      },
    );
    // Learning the video's class leaves the track alone in its queue.
    await waitFor(() => expect(vi.mocked(usePlayer).mock.calls.at(-1)?.[3]).toBe(""));
    expect(lastTransportProps()?.onNextInQueue).toBeUndefined();

    fireEvent.ended(stageVideo());
    await act(async () => {});
    expect(lastPlayedID()).toBe("media-alpha");
    expect(vi.mocked(usePlayer).mock.calls.some((call) => call[0] === "media-v1")).toBe(false);
  });

  it("still advances into an issued next of the playing class without a card queue", async () => {
    renderPlayer(
      playerState({
        phase: "playing",
        info: {
          mediaClass: "audio",
          next: {
            mediaID: "media-t2",
            mediaClass: "audio",
            streams: [{ kind: "direct-play", url: "/api/media/t2" }],
          },
        } as PlayerState["info"],
      }),
      { title: "Alpha" },
    );
    fireEvent.ended(stageVideo());
    await waitFor(() => expect(lastPlayedID()).toBe("media-t2"));
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

// An advance resets the engine for the next item, but the audio stage, its
// words and the transport stay up until that item's data arrives (CORE-2363).
describe("PlayerPage audio advance (CORE-2363)", () => {
  const queue = [
    { id: "media-prev", title: "Before" },
    { id: "media-alpha", title: "Alpha" },
    { id: "media-next", title: "After" },
  ];
  const audio = { mediaClass: "audio" } as PlayerState["info"];
  const videoRef: { current: HTMLVideoElement | null } = { current: null };
  let states: Record<string, PlayerState> = {};
  let releaseNext: (info: never) => void = () => undefined;

  function track(id: string, title: string, artist: string) {
    return {
      id,
      title,
      artworkSlots: { poster: `/art/${id}.jpg` },
      credits: [{ entityID: `p-${id}`, title: artist, kind: "artist", archetype: "person" }],
    } as never;
  }

  beforeEach(() => {
    vi.mocked(usePlayer).mockReset();
    vi.mocked(PlayerTransport).mockClear();
    states = {
      "media-alpha": playerState({ phase: "playing", mediaID: "media-alpha", info: audio }),
    };
    // The engine for an item it has not loaded yet: playback-info in flight.
    vi.mocked(usePlayer).mockImplementation((mediaID) => ({
      videoRef,
      hlsRef: { current: null },
      state: states[mediaID] ?? playerState({ phase: "loading", mediaID: undefined }),
      retry: vi.fn(),
      restart: vi.fn(),
      switchToHls: vi.fn(() => false as const),
      adjustment: DEFAULT_ADJUSTMENT,
      adjust: vi.fn(),
    }));
    // The next item's media read waits until the test releases it.
    vi.mocked(fetchMedia).mockImplementation((id) =>
      id === "media-next"
        ? new Promise((resolve) => {
            releaseNext = resolve;
          })
        : Promise.resolve(track("media-alpha", "Alpha", "Ann")),
    );
  });
  afterEach(() => vi.mocked(fetchMedia).mockResolvedValue(null));

  // The queue reports no classes, so only the playing item's info names it a track.
  function renderAdvance() {
    const tree = () => (
      <I18nProvider>
        <MemoryRouter
          initialEntries={[
            {
              pathname: "/watch/media-alpha",
              state: { skipPrePlay: true, title: "Alpha", queue, queueIdentity: "viewer-1" },
            },
          ]}
        >
          <Routes>
            <Route path="/watch/*" element={<PlayerPage />} />
          </Routes>
        </MemoryRouter>
      </I18nProvider>
    );
    const view = render(tree());
    return () => view.rerender(tree());
  }

  async function advanceFromFirstTrack(): Promise<HTMLElement> {
    const stage = document.querySelector<HTMLElement>(".bf-audio-stage");
    if (!stage) throw new Error("no audio stage");
    expect(await within(stage).findByText("Ann")).toBeInTheDocument();
    presentFirstFrame();
    expect(screen.queryByRole("status")).toBeNull();
    act(() => vi.mocked(PlayerTransport).mock.calls.at(-1)?.[0]?.onNextInQueue?.());
    await waitFor(() => expect(vi.mocked(usePlayer).mock.calls.at(-1)?.[0]).toBe("media-next"));
    return stage;
  }

  it("keeps the stage and the item on it until the next item's data arrives, then swaps", async () => {
    renderAdvance();
    const stage = await advanceFromFirstTrack();

    // Loading the next item: the same stage, the previous words and picture,
    // no spinner, and the transport still mounted.
    expect(document.querySelector(".bf-audio-stage")).toBe(stage);
    expect(within(stage).getByText("Ann")).toBeInTheDocument();
    expect(within(stage).getByText("2 of 3")).toBeInTheDocument();
    expect(stage.querySelector(".bf-audio-artwork img")).toHaveAttribute(
      "src",
      "/art/media-alpha.jpg",
    );
    expect(screen.queryByRole("status")).toBeNull();
    expect(vi.mocked(PlayerTransport).mock.calls.at(-1)?.[0]?.state.phase).toBe("loading");

    await act(async () => releaseNext(track("media-next", "After", "Bo")));
    expect(await within(stage).findByText("Bo")).toBeInTheDocument();
    expect(within(stage).getByText("3 of 3")).toBeInTheDocument();
    expect(document.querySelector(".bf-audio-stage")).toBe(stage);
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("swaps when the next item starts playing before its media read answers", async () => {
    const rerender = renderAdvance();
    const stage = await advanceFromFirstTrack();
    expect(within(stage).getByText("Ann")).toBeInTheDocument();

    states["media-next"] = playerState({ phase: "playing", mediaID: "media-next", info: audio });
    rerender();
    expect(document.querySelector(".bf-audio-stage")).toBe(stage);
    expect(within(stage).getByText("3 of 3")).toBeInTheDocument();
  });

  it("drops the stage once the next item's playback-info names a video", async () => {
    const rerender = renderAdvance();
    await advanceFromFirstTrack();
    expect(document.querySelector(".bf-audio-stage")).not.toBeNull();

    states["media-next"] = playerState({
      phase: "playing",
      mediaID: "media-next",
      info: { mediaClass: "video" } as PlayerState["info"],
    });
    rerender();
    expect(document.querySelector(".bf-audio-stage")).toBeNull();
  });

  it("shows a small cue, not a cover, while an audio item loads", () => {
    renderPlayer(playerState({ phase: "loading" }), {
      title: "Alpha",
      queue: [{ id: "media-alpha", title: "Alpha", mediaClass: "audio" }],
      queueIdentity: "viewer-1",
    });
    expect(document.querySelector(".bf-audio-stage")).not.toBeNull();
    const cue = screen.getByRole("status");
    expect(cue).toHaveTextContent("Loading…");
    expect(cue).not.toHaveClass("inset-0");
  });
});

describe("PlayerPage Back", () => {
  function renderAt(entries: string[]) {
    vi.mocked(usePlayer).mockReturnValue({
      videoRef: { current: null },
      hlsRef: { current: null },
      state: playerState({ phase: "playing" }),
      retry: vi.fn(),
      restart: vi.fn(),
      switchToHls: vi.fn(() => false as const),
      adjustment: DEFAULT_ADJUSTMENT,
      adjust: vi.fn(),
    });
    render(
      <I18nProvider>
        <MemoryRouter
          initialEntries={entries.map((pathname) => ({ pathname, state: { skipPrePlay: true } }))}
          initialIndex={entries.length - 1}
        >
          <Routes>
            <Route path="/" element={<p>home route</p>} />
            <Route path="/entity/:entityId" element={<p>entity route</p>} />
            <Route path="/watch/*" element={<PlayerPage />} />
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

describe("PlayerPage Version choice (CORE-1676)", () => {
  beforeEach(() => {
    vi.mocked(usePlayer).mockReset();
    vi.mocked(PlayerTransport).mockClear();
    window.localStorage.clear();
  });

  it("asks for the profile's remembered Version, and a new pick is remembered and reloads", async () => {
    writeVersionChoice("profile-a", "media-alpha", "v-remembered");
    const { retry } = renderPlayer(
      playerState({ phase: "playing", info: { mediaClass: "video" } as PlayerState["info"] }),
    );
    expect(vi.mocked(usePlayer).mock.calls.at(-1)?.[4]).toBe("v-remembered");

    const props = vi.mocked(PlayerTransport).mock.calls.at(-1)?.[0];
    act(() => props?.onSelectVersion?.("v-uhd"));

    expect(readVersionChoice("profile-a", "media-alpha")).toBe("v-uhd");
    expect(retry).toHaveBeenCalledOnce();
    await waitFor(() => expect(vi.mocked(usePlayer).mock.calls.at(-1)?.[4]).toBe("v-uhd"));

    // Choosing the Version already playing does nothing.
    act(() => vi.mocked(PlayerTransport).mock.calls.at(-1)?.[0]?.onSelectVersion?.("v-uhd"));
    expect(retry).toHaveBeenCalledOnce();
  });
});

// CORE-1701: the pre-play queue plays before the title unless the viewer
// arrived by a timestamped link or an advance.
describe("PlayerPage pre-play", () => {
  beforeEach(() => {
    vi.mocked(usePlayer).mockReset();
    vi.mocked(usePlayer).mockReturnValue({
      videoRef: { current: null },
      hlsRef: { current: null },
      state: playerState({ phase: "playing" }),
      retry: vi.fn(),
      restart: vi.fn(),
      switchToHls: vi.fn(() => false as const),
      adjustment: DEFAULT_ADJUSTMENT,
      adjust: vi.fn(),
    });
    vi.mocked(fetchPrePlayQueue).mockReset();
  });

  function renderEntry(entry: { pathname: string; search?: string; state?: unknown }) {
    render(
      <I18nProvider>
        <MemoryRouter initialEntries={[entry]}>
          <Routes>
            <Route path="/watch/*" element={<PlayerPage />} />
          </Routes>
        </MemoryRouter>
      </I18nProvider>,
    );
  }

  it("plays the pre-play queue first, then the title on Skip all", async () => {
    vi.mocked(fetchPrePlayQueue).mockResolvedValue([
      { mediaID: "clip-1", title: "Intro clip", durationSeconds: 10, role: "clip", label: "" },
      {
        mediaID: "s-1",
        title: "Suggested one",
        durationSeconds: 90,
        role: "suggested",
        label: "Preview",
      },
    ]);
    renderEntry({ pathname: "/watch/media-alpha" });
    const stage = await screen.findByRole("region", { name: "pre-play stage" });
    expect(stage).toHaveTextContent("Intro clip, Suggested one");
    expect(vi.mocked(fetchPrePlayQueue).mock.calls[0]?.[0]).toBe("media-alpha");
    expect(usePlayer).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Skip all" }));
    expect(screen.queryByRole("region", { name: "pre-play stage" })).not.toBeInTheDocument();
    expect(usePlayer).toHaveBeenCalled();
  });

  it("starts the title when the queue is empty or cannot load", async () => {
    vi.mocked(fetchPrePlayQueue).mockRejectedValue(new Error("offline"));
    renderEntry({ pathname: "/watch/media-alpha" });
    await waitFor(() => expect(usePlayer).toHaveBeenCalled());
    expect(screen.queryByRole("region", { name: "pre-play stage" })).not.toBeInTheDocument();
  });

  it("skips pre-play for a timestamped link and for an advance", () => {
    renderEntry({ pathname: "/watch/media-alpha", search: "?t=42" });
    expect(fetchPrePlayQueue).not.toHaveBeenCalled();
    expect(usePlayer).toHaveBeenCalled();
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
      restart: vi.fn(),
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
            <Route path="/watch/*" element={<PlayerPage />} />
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

// feature:persistent-mini-player
describe("PlayerPage in the player host (CORE-1633)", () => {
  beforeEach(() => {
    vi.mocked(usePlayer).mockReset();
    vi.mocked(PlayerTransport).mockClear();
  });

  const queue = [
    { id: "media-alpha", title: "Track One" },
    { id: "media-next", title: "Track Two" },
  ];
  let go: NavigateFunction = () => undefined;
  function Navigator() {
    go = useNavigate();
    return null;
  }

  function renderHosted(info: Partial<PlaybackInfo>) {
    vi.mocked(usePlayer).mockImplementation((mediaID) => ({
      videoRef: { current: null },
      hlsRef: { current: null },
      state: playerState({ phase: "playing", mediaID, info: info as PlaybackInfo }),
      retry: vi.fn(),
      restart: vi.fn(),
      switchToHls: vi.fn(() => false as const),
      adjustment: DEFAULT_ADJUSTMENT,
      adjust: vi.fn(),
    }));
    render(
      <I18nProvider>
        <MemoryRouter
          initialEntries={[
            { pathname: "/" },
            {
              pathname: "/watch/media-alpha",
              state: { skipPrePlay: true, title: "Track One", queue, queueIdentity: "viewer-1" },
            },
          ]}
          initialIndex={1}
        >
          <Navigator />
          <PlayerHost>
            <Routes>
              <Route path="/" element={<p>home route</p>} />
              <Route path="/search" element={<p>search route</p>} />
              <Route path="/media/*" element={<p>media route</p>} />
              <Route path="/watch/*" element={<PlayerPage />} />
            </Routes>
          </PlayerHost>
        </MemoryRouter>
      </I18nProvider>,
    );
  }

  const miniPlayer = () => screen.queryByRole("region", { name: "Now playing" });

  it("keeps audio playing on the same media element in a docked mini player across routes", async () => {
    renderHosted({ mediaClass: "audio" });
    const video = await waitFor(() => stageVideo());
    expect(miniPlayer()).toBeNull();

    act(() => {
      void go("/search");
    });
    expect(screen.getByText("search route")).toBeInTheDocument();
    const region = await waitFor(() => {
      const found = miniPlayer();
      if (!found) throw new Error("mini player not docked");
      return found;
    });
    expect(stageVideo()).toBe(video);
    expect(document.querySelector("main")).not.toBeVisible();
    expect(within(region).getByText("Track One")).toBeInTheDocument();
    expect(within(region).getByRole("progressbar", { name: "Playback position" })).toBeVisible();
    for (const name of ["Play", "Next", "Open player", "Stop and close player"]) {
      expect(within(region).getByRole("button", { name })).toBeEnabled();
    }

    fireEvent.click(within(region).getByRole("button", { name: "Open player" }));
    await waitFor(() => expect(miniPlayer()).toBeNull());
    expect(screen.queryByText("search route")).toBeNull();
    expect(stageVideo()).toBe(video);
    expect(document.querySelector("main")).toBeVisible();
  });

  it("advances to the next track in place while docked", async () => {
    renderHosted({ mediaClass: "audio" });
    await waitFor(() => stageVideo());
    act(() => {
      void go("/search");
    });
    const region = await waitFor(() => {
      const found = miniPlayer();
      if (!found) throw new Error("mini player not docked");
      return found;
    });

    fireEvent.click(within(region).getByRole("button", { name: "Next" }));
    await waitFor(() =>
      expect(vi.mocked(usePlayer).mock.calls.some(([id]) => id === "media-next")).toBe(true),
    );
    expect(screen.getByText("search route")).toBeInTheDocument();
    expect(within(miniPlayer() as HTMLElement).getByText("Track Two")).toBeInTheDocument();
  });

  it("stops and closes on the mini player's Stop", async () => {
    renderHosted({ mediaClass: "audio" });
    await waitFor(() => stageVideo());
    act(() => {
      void go("/search");
    });
    const region = await waitFor(() => {
      const found = miniPlayer();
      if (!found) throw new Error("mini player not docked");
      return found;
    });
    fireEvent.click(within(region).getByRole("button", { name: "Stop and close player" }));
    await waitFor(() => expect(miniPlayer()).toBeNull());
    expect(document.querySelector("video")).toBeNull();
  });

  it("stops and closes on a remote Stop while the audio is docked (CORE-1875)", async () => {
    renderHosted({ mediaClass: "audio" });
    await waitFor(() => stageVideo());
    act(() => {
      void go("/search");
    });
    await waitFor(() => {
      if (!miniPlayer()) throw new Error("mini player not docked");
    });
    act(() => deliverPlayerCommand({ command: "stop", mediaID: "media-alpha" }));
    await waitFor(() => expect(miniPlayer()).toBeNull());
    expect(document.querySelector("video")).toBeNull();
    expect(screen.getByText("search route")).toBeInTheDocument();
  });

  it("stops audio on a remote Stop in the full player instead of docking it (CORE-1875)", async () => {
    renderHosted({ mediaClass: "audio" });
    await waitFor(() => stageVideo());
    const props = vi.mocked(PlayerTransport).mock.calls.at(-1)?.[0];
    act(() => props?.onRemoteStop?.());
    await waitFor(() => expect(screen.getByText("media route")).toBeInTheDocument());
    expect(miniPlayer()).toBeNull();
    expect(document.querySelector("video")).toBeNull();
  });

  it("stops a video Media when its watch route closes, keeping the full-screen route", async () => {
    renderHosted({ mediaClass: "video" });
    await waitFor(() => stageVideo());
    act(() => {
      void go("/search");
    });
    expect(screen.getByText("search route")).toBeInTheDocument();
    expect(miniPlayer()).toBeNull();
    expect(document.querySelector("video")).toBeNull();
  });
});

// feature:watch-together-entry (CORE-1764)
describe("PlayerPage Watch together arrival", () => {
  it("opens the Watch Party panel when the detail page asks for the invite", () => {
    renderPlayer(playerState({ phase: "playing" }), { watchPartyIntent: "invite" });
    expect(screen.getByRole("region", { name: "Watch Party" })).toBeInTheDocument();
  });

  it("keeps the panel closed on an ordinary arrival", () => {
    renderPlayer(playerState({ phase: "playing" }));
    expect(screen.queryByRole("region", { name: "Watch Party" })).toBeNull();
  });
});

// feature:watch-party
describe("PlayerPage in a Watch Party (CORE-1766)", () => {
  beforeEach(() => {
    vi.mocked(usePlayer).mockReset();
    vi.mocked(PlayerTransport).mockClear();
  });
  afterEach(() => vi.mocked(watchPartyDefersUpNext).mockReturnValue(false));

  const queue = [
    { id: "media-prev", title: "Before" },
    { id: "media-alpha", title: "Alpha" },
    { id: "media-next", title: "After" },
  ];

  it("hands a member's next item and autoplay to the party", () => {
    vi.mocked(watchPartyDefersUpNext).mockReturnValue(true);
    renderPlayer(
      playerState({ phase: "playing", info: { mediaClass: "audio" } as PlayerState["info"] }),
      { title: "Alpha", queue, queueIdentity: "viewer-1" },
    );
    const props = vi.mocked(PlayerTransport).mock.calls.at(-1)?.[0];
    expect(props?.onNextInQueue).toBeUndefined();
    // Only the forward step defers.
    expect(props?.onPreviousInQueue).toBeTypeOf("function");
  });

  it("keeps the host's next item, whose advance the party follows", () => {
    renderPlayer(
      playerState({ phase: "playing", info: { mediaClass: "audio" } as PlayerState["info"] }),
      { title: "Alpha", queue, queueIdentity: "viewer-1" },
    );
    expect(vi.mocked(PlayerTransport).mock.calls.at(-1)?.[0]?.onNextInQueue).toBeTypeOf("function");
  });
});
