import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { createRef } from "react";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api";
import {
  fetchPartyStage,
  type PartyEntry,
  type PartyStage,
  partyAdvance,
  partyScreenRegister,
} from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { expectNoAxeViolations } from "@/test/axe";
import { PartyStagePage } from "./PartyStagePage";

vi.mock("@/lib/graphql", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/graphql")>();
  return {
    ...actual,
    fetchPartyStage: vi.fn(),
    partyScreenRegister: vi.fn(),
    partyScreenReport: vi.fn(async () => true),
    partyAdvance: vi.fn(),
    calibrateWatchPartyClock: vi.fn(async () => ({ offsetMS: 0, roundTripMS: 10 })),
  };
});
vi.mock("../player/usePlayer", () => ({
  usePlayer: () => ({
    videoRef: createRef<HTMLVideoElement>(),
    state: { phase: "loading", message: "", info: null, mediaID: "" },
    retry: vi.fn(),
    adjustment: { pitchShiftSemitones: 0, tempo: 1 },
    adjust: vi.fn(),
  }),
}));

const song: PartyEntry = {
  id: "e1",
  mediaID: "m1",
  title: "Paper Lanterns",
  subtitle: "",
  durationSeconds: 200,
  artworkURL: "",
  participantID: "p1",
  participantName: "Juniper",
  state: "current",
  pinned: false,
  position: 0,
  etaSeconds: 0,
  addedAt: "2026-11-21T20:00:00Z",
  lastPlayedAt: "",
  pitchSemitones: 0,
};

function stage(over: Partial<PartyStage> = {}): PartyStage {
  return {
    sessionID: "s1",
    serverNowMS: Date.now(),
    current: null,
    playback: null,
    upNext: [],
    ticker: true,
    joinCode: "QZXW",
    joinPath: "/web/join/QZXW",
    ...over,
  };
}

function renderStage(path = "/party/stage/s1") {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/party/stage/:sessionID" element={<PartyStagePage />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

beforeEach(() => {
  window.sessionStorage.clear();
  vi.mocked(partyScreenRegister).mockResolvedValue({
    id: "scr-1",
    name: "Joined screen",
    kind: "joined",
    carrier: "local",
    lead: false,
    ticker: true,
    offsetMS: 0,
    rttMS: 0,
    reportedAt: "",
  });
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("PartyStagePage", () => {
  it("registers a joined screen and shows the join code between songs", async () => {
    vi.mocked(fetchPartyStage).mockResolvedValue(
      stage({ upNext: [{ ...song, state: "waiting" }] }),
    );
    const { container } = renderStage("/party/stage/s1?joined=1");

    expect(await screen.findByText("QZXW")).toBeInTheDocument();
    expect(screen.getByText("Paper Lanterns · Juniper")).toBeInTheDocument();
    await waitFor(() =>
      expect(partyScreenRegister).toHaveBeenCalledWith({
        sessionID: "s1",
        name: "Joined screen",
        kind: "joined",
        carrier: "local",
      }),
    );
    await waitFor(() =>
      expect(window.sessionStorage.getItem("beamfall.partyScreen.joined.s1")).toBe("scr-1"),
    );
    await expectNoAxeViolations(container);
  });

  it("advances the rotation once when the song ends", async () => {
    vi.mocked(fetchPartyStage).mockResolvedValue(stage({ current: song }));
    vi.mocked(partyAdvance).mockResolvedValue({} as never);
    const { container } = renderStage();

    expect(await screen.findByText("Sung by Juniper")).toBeInTheDocument();
    const video = container.querySelector("video");
    expect(video).not.toBeNull();
    fireEvent.ended(video as HTMLVideoElement);
    fireEvent.ended(video as HTMLVideoElement);

    expect(partyAdvance).toHaveBeenCalledTimes(1);
    expect(partyAdvance).toHaveBeenCalledWith("e1");
  });

  it("keeps playing through a network blip", async () => {
    vi.mocked(fetchPartyStage)
      .mockResolvedValueOnce(stage({ current: song }))
      .mockRejectedValue(new TypeError("Failed to fetch"));
    const { container } = renderStage();

    expect(await screen.findByText("Sung by Juniper")).toBeInTheDocument();
    // The third poll starts only after the second one's failure settled.
    await waitFor(() => expect(fetchPartyStage).toHaveBeenCalledTimes(3), { timeout: 4000 });
    expect(container.querySelector("video")).not.toBeNull();
  });

  it("unmounts the player when the party ends", async () => {
    vi.mocked(fetchPartyStage)
      .mockResolvedValueOnce(stage({ current: song }))
      .mockRejectedValue(new ApiError("party session not found", 200));
    const { container } = renderStage();

    expect(await screen.findByText("Sung by Juniper")).toBeInTheDocument();
    expect(
      await screen.findByText(
        "The party is not reachable. It may have ended.",
        {},
        { timeout: 3000 },
      ),
    ).toBeInTheDocument();
    expect(container.querySelector("video")).toBeNull();
  });
});
