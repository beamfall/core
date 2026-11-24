import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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
  partyScreenReport,
} from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import type { Locale } from "@/strings";
import { expectNoAxeViolations } from "@/test/axe";
import { usePlayer } from "../player/usePlayer";
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
  usePlayer: vi.fn(() => ({
    videoRef: createRef<HTMLVideoElement>(),
    state: { phase: "loading", message: "", info: null, mediaID: "" },
    retry: vi.fn(),
    adjustment: { pitchShiftSemitones: 0, tempo: 1 },
    adjust: vi.fn(),
  })),
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
    qrCorner: "off",
    muted: false,
    lead: true,
    joinCode: "QZXW",
    joinPath: "/web/join/QZXW",
    ...over,
  };
}

function renderStage(path = "/party/stage/s1", locale: Locale = "en") {
  return render(
    <I18nProvider initialLocale={locale}>
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
  vi.mocked(partyScreenReport).mockResolvedValue(true);
  vi.mocked(partyScreenRegister).mockResolvedValue({
    id: "scr-1",
    name: "Joined screen",
    kind: "joined",
    carrier: "local",
    lead: false,
    ticker: true,
    qrCorner: "off",
    muted: false,
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
    expect(screen.getByText("Juniper")).toHaveClass("bf-party-next-singer");
    expect(screen.getByText("Paper Lanterns")).toHaveClass("bf-party-next-song");
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

  it("names the singer, song and expected time of each entry on the ticker", async () => {
    vi.mocked(fetchPartyStage).mockResolvedValue(
      stage({
        current: song,
        upNext: [
          {
            ...song,
            id: "e2",
            title: "Caught Up",
            subtitle: "July",
            participantName: "Steve",
            state: "waiting",
            position: 1,
            etaSeconds: 30,
          },
          {
            ...song,
            id: "e3",
            title: "Electric Co",
            participantName: "Mara",
            state: "waiting",
            position: 2,
            etaSeconds: 400,
          },
        ],
      }),
    );
    renderStage();

    const ticker = await screen.findByRole("region", { name: "Up next" });
    const items = within(ticker).getAllByRole("listitem");
    expect(items.map((item) => item.textContent)).toEqual([
      "Steve: Caught Up · , July, next",
      "Mara: Electric Co, in about 7 min",
    ]);
    expect(within(items[0]).getByText("Steve")).toHaveClass("bf-party-next-singer");
  });

  it("reads the stage only for its own registered screen (ADR-0240 D10)", async () => {
    let finish: (value: Awaited<ReturnType<typeof partyScreenRegister>>) => void = () => {};
    vi.mocked(partyScreenRegister).mockReturnValue(
      new Promise((resolve) => {
        finish = resolve;
      }),
    );
    vi.mocked(fetchPartyStage).mockResolvedValue(stage());
    renderStage();

    await waitFor(() => expect(partyScreenRegister).toHaveBeenCalled());
    expect(fetchPartyStage).not.toHaveBeenCalled();
    finish({
      id: "scr-9",
      name: "Pop-out screen",
      kind: "popout",
      carrier: "local",
      lead: false,
      ticker: true,
      qrCorner: "off",
      muted: false,
      offsetMS: 0,
      rttMS: 0,
      reportedAt: "",
    });
    await waitFor(() =>
      expect(fetchPartyStage).toHaveBeenCalledWith("s1", "scr-9", expect.anything()),
    );
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

  it("plays silently on a screen that is not the lead", async () => {
    vi.mocked(fetchPartyStage).mockResolvedValue(stage({ current: song, lead: false }));
    const { container } = renderStage();

    expect(await screen.findByText("Sung by Juniper")).toBeInTheDocument();
    expect((container.querySelector("video") as HTMLVideoElement).muted).toBe(true);
  });

  it("plays aloud on the lead screen", async () => {
    vi.mocked(fetchPartyStage).mockResolvedValue(stage({ current: song }));
    const { container } = renderStage();

    expect(await screen.findByText("Sung by Juniper")).toBeInTheDocument();
    expect((container.querySelector("video") as HTMLVideoElement).muted).toBe(false);
  });

  it("draws the join QR in the chosen corner during a song and plays muted", async () => {
    vi.mocked(fetchPartyStage).mockResolvedValue(
      stage({ current: song, qrCorner: "bottom-right", muted: true }),
    );
    const { container } = renderStage();

    expect(await screen.findByText("Sung by Juniper")).toBeInTheDocument();
    const corner = container.querySelector(".bf-party-stage-corner");
    expect(corner).toHaveAttribute("data-corner", "bottom-right");
    expect(within(corner as HTMLElement).getByText("QZXW")).toBeInTheDocument();
    expect(within(corner as HTMLElement).getByRole("img")).toBeInTheDocument();
    expect((container.querySelector("video") as HTMLVideoElement).muted).toBe(true);
    await expectNoAxeViolations(container);
  });

  it("leaves the corner empty and the sound on by default", async () => {
    vi.mocked(fetchPartyStage).mockResolvedValue(stage({ current: song }));
    const { container } = renderStage();

    expect(await screen.findByText("Sung by Juniper")).toBeInTheDocument();
    expect(container.querySelector(".bf-party-stage-corner")).toBeNull();
    expect((container.querySelector("video") as HTMLVideoElement).muted).toBe(false);
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

  it.each([401, 403])("clears the player and ticker on a %i refusal", async (status) => {
    vi.mocked(fetchPartyStage)
      .mockResolvedValueOnce(
        stage({ current: song, upNext: [{ ...song, id: "e2", state: "waiting" }] }),
      )
      .mockRejectedValue(new ApiError("access refused", status));
    const { container } = renderStage();

    expect(await screen.findByText("Sung by Juniper")).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Up next" })).toBeInTheDocument();
    expect(container.querySelector("video")).not.toBeNull();

    expect(await screen.findByRole("alert", {}, { timeout: 3000 })).toBeInTheDocument();
    expect(screen.queryByText("Sung by Juniper")).not.toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Up next" })).not.toBeInTheDocument();
    expect(container.querySelector("video")).toBeNull();
  });

  it("clears playback and replaces a refused registration even when the heartbeat rejects", async () => {
    const key = "beamfall.partyScreen.popout.s1";
    window.sessionStorage.setItem(key, "scr-stale");
    const refusal = new ApiError("screen not found", 200);
    vi.mocked(partyScreenReport).mockRejectedValue(refusal);
    vi.mocked(fetchPartyStage)
      .mockResolvedValueOnce(stage({ current: song }))
      .mockRejectedValueOnce(refusal)
      .mockResolvedValue(stage());
    let finish: (value: Awaited<ReturnType<typeof partyScreenRegister>>) => void = () => {};
    vi.mocked(partyScreenRegister).mockReturnValueOnce(
      new Promise((resolve) => {
        finish = resolve;
      }),
    );
    const { container } = renderStage();

    expect(await screen.findByText("Sung by Juniper")).toBeInTheDocument();
    expect(partyScreenRegister).not.toHaveBeenCalled();
    await waitFor(() => expect(partyScreenRegister).toHaveBeenCalledTimes(1), { timeout: 3000 });
    expect(container.querySelector("video")).toBeNull();
    expect(window.sessionStorage.getItem(key)).toBeNull();
    finish({
      id: "scr-fresh",
      name: "Pop-out stage",
      kind: "popout",
      carrier: "local",
      lead: true,
      ticker: false,
      qrCorner: "off",
      muted: false,
      offsetMS: 0,
      rttMS: 0,
      reportedAt: "",
    });

    expect(await screen.findByText("QZXW")).toBeInTheDocument();
    expect(fetchPartyStage).toHaveBeenLastCalledWith("s1", "scr-fresh", expect.anything());
    expect(window.sessionStorage.getItem(key)).toBe("scr-fresh");
  });

  it.each([
    ["en", "Up next: Juniper"],
    ["fr", "À suivre : Juniper"],
    ["de", "Als Nächstes: Juniper"],
    ["es", "A continuación: Juniper"],
    ["pt", "A seguir: Juniper"],
  ] as const)(
    "shows participant-only copy in %s without starting playback",
    async (locale, copy) => {
      vi.mocked(fetchPartyStage).mockResolvedValue(
        stage({
          current: {
            ...song,
            mediaID: "",
            title: "",
            ineligible: true,
            label: "Up next: Juniper",
          },
          upNext: [
            {
              ...song,
              id: "e2",
              mediaID: "",
              title: "",
              participantName: "Avery",
              state: "waiting",
              ineligible: true,
              label: "Avery",
            },
          ],
        }),
      );
      const { container } = renderStage(undefined, locale);

      expect(await screen.findByText(copy)).toBeInTheDocument();
      expect(screen.getByText("Avery")).toBeInTheDocument();
      expect(container.querySelector("video")).toBeNull();
      expect(usePlayer).not.toHaveBeenCalled();
      expect(partyAdvance).not.toHaveBeenCalled();
    },
  );
});
