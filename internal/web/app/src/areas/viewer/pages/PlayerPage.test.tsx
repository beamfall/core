import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
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
    expect(screen.getByRole("status")).toHaveTextContent("Reconnecting…");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("hides the reconnecting status once playback is healthy", () => {
    renderPlayer(playerState({ phase: "playing", hlsActive: true }));
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
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
