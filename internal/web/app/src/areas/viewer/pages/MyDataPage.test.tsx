import type { ReactNode } from "react";
import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  deleteMyEngagement,
  fetchMyDataSummary,
  fetchMyEngagement,
  fetchMyEngagementLog,
  fetchMyPlayHistory,
  fetchMyPlayStats,
  fetchMySessions,
} from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { groupStreamTokens, MyDataPage } from "./MyDataPage";

vi.mock("@/lib/session", () => ({
  useSession: () => ({
    manifest: {
      libraries: [
        { id: "library-films", title: "Films", pluginID: null, previewClips: false },
        { id: "library-music", title: "Music", pluginID: null, previewClips: false },
      ],
    },
  }),
}));

vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: ReactNode }) => <>{children}</>,
}));
vi.mock("../components/ViewerActions", () => ({ ViewerActions: () => null }));
vi.mock("@/lib/graphql", () => ({
  fetchMySessions: vi.fn(),
  fetchMyEngagement: vi.fn(),
  deleteMyEngagement: vi.fn(),
  fetchMyDataSummary: vi.fn(),
  fetchMyEngagementLog: vi.fn(),
  fetchMyPlayHistory: vi.fn(),
  fetchMyPlayStats: vi.fn(),
}));

const NO_PLAYS = {
  windowDays: 30,
  since: "",
  until: "",
  totalPlays: 0,
  totalWatchedSeconds: 0,
  profiles: [],
  items: [],
  devices: [],
  methods: [],
  days: [],
};

beforeEach(() => {
  vi.mocked(fetchMySessions).mockReset().mockResolvedValue({
    sessions: [],
    streamTokens: [],
  });
  vi.mocked(fetchMyEngagement).mockReset().mockResolvedValue({
    schema: "v1",
    exportedAt: "2026-06-28T00:00:00Z",
    playbackProgress: [],
    favorites: [],
    queue: [],
    watchlist: [],
    ratings: [],
  });
  vi.mocked(fetchMyDataSummary).mockReset().mockResolvedValue({
    plays: 0,
    watched: 0,
    favorites: 0,
    queueEntries: 0,
    libraryCount: 0,
    libraryBreakdown: [],
  });
  vi.mocked(fetchMyEngagementLog).mockReset().mockResolvedValue({
    total: 0,
    events: [],
  });
  vi.mocked(fetchMyPlayStats).mockReset().mockResolvedValue(NO_PLAYS);
  vi.mocked(fetchMyPlayHistory).mockReset().mockResolvedValue([]);
  vi.mocked(deleteMyEngagement).mockReset().mockResolvedValue({ scope: "all" });
  vi.stubGlobal(
    "confirm",
    vi.fn(() => true),
  );
  vi.stubGlobal(
    "prompt",
    vi.fn(() => "1234"),
  );
});

describe("MyDataPage delete", () => {
  it("retries deleteMyEngagement with the Reveal PIN when required", async () => {
    vi.mocked(deleteMyEngagement)
      .mockRejectedValueOnce(new Error("reveal pin required"))
      .mockResolvedValueOnce({ scope: "all" });

    renderPage();

    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Delete all my engagement" }));

    await waitFor(() => expect(deleteMyEngagement).toHaveBeenNthCalledWith(1, "all"));
    expect(prompt).toHaveBeenCalledWith("Enter your Reveal PIN to confirm deletion.");
    expect(deleteMyEngagement).toHaveBeenNthCalledWith(2, "all", "1234");
    expect(await screen.findByText("Your engagement data was deleted.")).toBeInTheDocument();
  });
});

function renderPage() {
  render(
    <MemoryRouter>
      <I18nProvider>
        <MyDataPage />
      </I18nProvider>
    </MemoryRouter>,
  );
}

describe("MyDataPage internal identifiers (CORE-2319)", () => {
  it("names libraries by title and never prints library or media ids", async () => {
    vi.mocked(fetchMyDataSummary).mockResolvedValue({
      plays: 5,
      watched: 1,
      favorites: 0,
      queueEntries: 0,
      libraryCount: 3,
      libraryBreakdown: [
        { libraryID: "library-films", plays: 3 },
        { libraryID: "library-music", plays: 1 },
        { libraryID: "library-gone", plays: 1 },
      ],
    });
    vi.mocked(fetchMyEngagementLog).mockResolvedValue({
      total: 1,
      events: [
        {
          kind: "play",
          mediaID: "media-123",
          libraryID: "library-films",
          occurredAt: "2026-11-01T10:00:00Z",
        },
      ],
    });
    renderPage();

    expect(await screen.findByText("Removed library")).toBeInTheDocument();
    expect((await screen.findAllByText("Films")).length).toBeGreaterThan(0);
    expect(screen.getByText("Music")).toBeInTheDocument();
    expect(await screen.findByRole("link", { name: "Played", hidden: true })).toHaveAttribute(
      "href",
      "/media/media-123",
    );
    expect(document.body.textContent).not.toMatch(/library-films|library-music|media-123/);
  });

  it("collapses outstanding stream tokens into one counted row per kind", async () => {
    vi.mocked(fetchMySessions).mockResolvedValue({
      sessions: [],
      streamTokens: [
        { targetKind: "media-assets", expiresAt: "2026-11-01T10:00:00Z" },
        { targetKind: "media-assets", expiresAt: "2026-11-01T12:00:00Z" },
        { targetKind: "media-hls", expiresAt: "2026-11-01T11:00:00Z" },
        { targetKind: "media-direct", expiresAt: "2026-11-01T09:00:00Z" },
        { targetKind: "media-subtitle", expiresAt: "2026-11-01T09:00:00Z" },
      ],
    });
    renderPage();

    expect(await screen.findByText("Previews and thumbnails")).toBeInTheDocument();
    expect(screen.getByText("Playback")).toBeInTheDocument();
    expect(screen.getByText("Subtitles")).toBeInTheDocument();
    expect(screen.getAllByText("2 tokens")).toHaveLength(2);
    expect(screen.getByText("1 token")).toBeInTheDocument();
    expect(document.body.textContent).not.toMatch(/media-assets|media-hls/);
  });

  it("keeps the latest expiry per kind and folds unknown kinds into Other", () => {
    expect(
      groupStreamTokens([
        { targetKind: "media-assets", expiresAt: "2026-11-01T10:00:00Z" },
        { targetKind: "future-kind", expiresAt: "2026-11-01T09:00:00Z" },
        { targetKind: "media-assets", expiresAt: "2026-11-01T12:00:00Z" },
      ]),
    ).toEqual([
      { label: "myDataTokenKindPreviews", count: 2, expiresAt: "2026-11-01T12:00:00Z" },
      { label: "myDataTokenKindOther", count: 1, expiresAt: "2026-11-01T09:00:00Z" },
    ]);
  });
});

describe("MyDataPage watch history", () => {
  it("shows the viewer's own totals, breakdowns and recent plays over a chosen window", async () => {
    const tally = { plays: 2, watchedSeconds: 5400 };
    vi.mocked(fetchMyPlayStats).mockResolvedValue({
      ...NO_PLAYS,
      totalPlays: 2,
      totalWatchedSeconds: 5400,
      items: [{ mediaID: "m-1", title: "Night Train", libraryID: "library-films", ...tally }],
      devices: [{ deviceID: "d-1", deviceName: "Living room TV", deviceClient: "web", ...tally }],
      methods: [{ playMethod: "direct", ...tally }],
    });
    vi.mocked(fetchMyPlayHistory).mockResolvedValue([
      {
        profileID: "p-1",
        profileName: "Ada",
        mediaID: "m-1",
        title: "Night Train",
        libraryID: "library-films",
        libraryTitle: "Films",
        deviceID: "",
        playMethod: "unknown",
        startedAt: "2026-11-10T20:00:00Z",
        watchedSeconds: 840,
        finished: false,
      },
    ]);
    renderPage();

    expect(await screen.findByText("Total: 2 plays · 1 h 30 min")).toBeInTheDocument();
    expect(screen.getAllByText("2 plays · 1 h 30 min")).toHaveLength(3);
    expect(screen.getByText("Living room TV")).toBeInTheDocument();
    expect(screen.getByText("Direct play")).toBeInTheDocument();
    expect(screen.getByText(/Unknown device · Not recorded · 14 min/)).toBeInTheDocument();
    expect(fetchMyPlayStats).toHaveBeenLastCalledWith(30, expect.anything());
    expect(fetchMyPlayHistory).toHaveBeenLastCalledWith(10, expect.anything());

    vi.mocked(fetchMyPlayStats).mockResolvedValue(NO_PLAYS);
    await userEvent.click(screen.getByRole("button", { name: "7 days" }));
    expect(
      await screen.findByText("You have not played anything in this period."),
    ).toBeInTheDocument();
    expect(fetchMyPlayStats).toHaveBeenLastCalledWith(7, expect.anything());
  });
  it("clears the watch card after deleting all engagement", async () => {
    vi.mocked(fetchMyPlayStats).mockResolvedValue({
      ...NO_PLAYS,
      totalPlays: 1,
      totalWatchedSeconds: 600,
      items: [
        {
          mediaID: "m-1",
          title: "Night Train",
          libraryID: "library-films",
          plays: 1,
          watchedSeconds: 600,
        },
      ],
    });
    renderPage();
    expect(await screen.findByText("Night Train")).toBeInTheDocument();
    const statsCalls = vi.mocked(fetchMyPlayStats).mock.calls.length;
    const historyCalls = vi.mocked(fetchMyPlayHistory).mock.calls.length;

    vi.mocked(fetchMyPlayStats).mockResolvedValue(NO_PLAYS);
    await userEvent.click(screen.getByRole("button", { name: "Delete all my engagement" }));

    expect(
      await screen.findByText("You have not played anything in this period."),
    ).toBeInTheDocument();
    expect(screen.queryByText("Night Train")).not.toBeInTheDocument();
    expect(fetchMyPlayStats).toHaveBeenCalledTimes(statsCalls + 1);
    expect(fetchMyPlayHistory).toHaveBeenCalledTimes(historyCalls + 1);
  });
});
