import "@testing-library/jest-dom/vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchMedia } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { PlayerTransport } from "../player/PlayerTransport";
import type { PlayerState } from "../player/usePlayer";
import { usePlayer } from "../player/usePlayer";
import { buildTimestampedLink, PlayerPage } from "./PlayerPage";

vi.mock("@/lib/session", () => ({
  useSession: () => ({ revealed: true }),
  useViewerIdentity: () => "viewer-1",
}));
vi.mock("@/lib/graphql", () => ({
  fetchClientPrefs: vi.fn().mockResolvedValue([]),
  setClientPref: vi.fn().mockResolvedValue(undefined),
  fetchMedia: vi.fn().mockResolvedValue(null),
}));
vi.mock("../player/usePlayer", () => ({ usePlayer: vi.fn() }));
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
    ...overrides,
  };
}

function renderPlayer(state: PlayerState, navState?: unknown) {
  const retry = vi.fn();
  vi.mocked(usePlayer).mockReturnValue({
    videoRef: { current: null },
    hlsRef: { current: null },
    state,
    retry,
    switchToHls: vi.fn(() => false as const),
  });
  render(
    <I18nProvider>
      <MemoryRouter initialEntries={[{ pathname: "/watch/media-alpha", state: navState }]}>
        <Routes>
          <Route path="/watch/:mediaId" element={<PlayerPage />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
  return { retry };
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
