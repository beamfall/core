import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  fetchPartySession,
  type PartyEntry,
  type PartyScreen,
  type PartySession,
  partyRemove,
  partyRestore,
} from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { expectNoAxeViolations } from "@/test/axe";
import { PartyHostPage } from "./PartyHostPage";

vi.mock("@/lib/graphql", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/graphql")>();
  return {
    ...actual,
    fetchPartySession: vi.fn(),
    fetchHomeLibraries: vi.fn(async () => []),
    fetchPlaybackAdjustmentRanges: vi.fn(async () => null),
    calibrateWatchPartyClock: vi.fn(async () => ({ offsetMS: 0, roundTripMS: 10 })),
    partyRemove: vi.fn(),
    partyRestore: vi.fn(),
  };
});
vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
vi.mock("../components/ViewerActions", () => ({ ViewerActions: () => null }));

function entry(id: string, title: string, position: number): PartyEntry {
  return {
    id,
    mediaID: `m-${id}`,
    title,
    subtitle: "",
    durationSeconds: 200,
    artworkURL: "",
    participantID: "p1",
    participantName: "Juniper",
    state: "waiting",
    pinned: false,
    position,
    etaSeconds: position * 200,
    addedAt: "2026-11-21T20:00:00Z",
    lastPlayedAt: "",
    pitchSemitones: 0,
  };
}

function screenOf(id: string, name: string, offsetMS: number, reportedAt: string): PartyScreen {
  return {
    id,
    name,
    kind: "popout",
    carrier: "local",
    lead: false,
    ticker: true,
    offsetMS,
    rttMS: 12,
    reportedAt,
  };
}

function session(over: Partial<PartySession> = {}): PartySession {
  return {
    id: "s1",
    joinCode: "QZXW",
    joinPath: "/web/join/QZXW",
    fairness: "round-robin",
    autoAdd: false,
    joinOpen: true,
    libraryIDs: ["lib-1"],
    startedAt: "2026-11-21T20:00:00Z",
    expiresAt: "2026-11-22T04:00:00Z",
    changeoverSeconds: 20,
    current: null,
    playback: null,
    entries: [],
    requests: [],
    recent: [],
    removed: [],
    participants: [{ id: "p1", name: "Juniper", guest: false, waiting: 1, lastTurnAt: "" }],
    screens: [],
    skipped: [],
    ...over,
  };
}

function renderPage() {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/party"]}>
        <PartyHostPage />
      </MemoryRouter>
    </I18nProvider>,
  );
}

beforeEach(() => {
  vi.mocked(fetchPartySession).mockReset();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("PartyHostPage", () => {
  it("shows a screen in sync only once it has reported within 40 ms", async () => {
    vi.mocked(fetchPartySession).mockResolvedValue(
      session({
        screens: [
          screenOf("a", "Living room", 32, "2026-11-21T20:01:00Z"),
          screenOf("b", "Patio", 180, "2026-11-21T20:01:00Z"),
          screenOf("c", "Garage", 0, ""),
        ],
      }),
    );
    const { container } = renderPage();

    const screens = await screen.findByRole("region", { name: "Screens" });
    expect(within(screens).getAllByText(/in sync/)).toHaveLength(1);
    expect(within(screens).getByText("180 ms off")).toBeInTheDocument();
    expect(within(screens).getByText(/waiting for its first report/)).toBeInTheDocument();
    await expectNoAxeViolations(container);
  });

  it("offers a new-tab link when the browser blocks the stage window", async () => {
    vi.mocked(fetchPartySession).mockResolvedValue(session());
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    renderPage();

    await userEvent.click(await screen.findByRole("button", { name: /Pop out stage/ }));

    expect(open).toHaveBeenCalledWith(
      "/party/stage/s1",
      "beamfall-party-stage-s1",
      expect.any(String),
    );
    expect(screen.getByRole("alert")).toHaveTextContent("The browser blocked the stage window.");
    const link = screen.getByRole("link", { name: /Open the stage in a new tab/ });
    expect(link).toHaveAttribute("target", "_blank");
    expect(link).toHaveAttribute("href", "/party/stage/s1");
  });

  it("removes a rotation entry and restores it with Undo", async () => {
    const song = entry("e1", "Paper Lanterns", 1);
    vi.mocked(fetchPartySession).mockResolvedValue(session({ entries: [song] }));
    vi.mocked(partyRemove).mockResolvedValue(session({ removed: [song] }));
    vi.mocked(partyRestore).mockResolvedValue(session({ entries: [song] }));
    renderPage();

    await userEvent.click(await screen.findByRole("button", { name: "Remove Paper Lanterns" }));
    expect(partyRemove).toHaveBeenCalledWith("e1");
    expect(await screen.findByText("Removed Paper Lanterns.")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Undo" }));
    expect(partyRestore).toHaveBeenCalledWith("e1");
    expect(
      await screen.findByRole("button", { name: "Remove Paper Lanterns" }),
    ).toBeInTheDocument();
  });
});
