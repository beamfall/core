import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PartySession } from "@/lib/graphql";
import type { SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import type { LibraryItemsPage, LibraryItemsRequest } from "../lib/libraryBrowse";
import { KaraokeSongbook } from "./KaraokeSongbook";

// CORE-3362: the karaoke Library's Songbook.

const fetchLibraryItems = vi.hoisted(() => vi.fn());
const partyAdd = vi.hoisted(() => vi.fn());
vi.mock("../lib/libraryBrowse", async (original) => ({
  ...(await original<typeof import("../lib/libraryBrowse")>()),
  fetchLibraryItems,
}));
vi.mock("@/lib/graphql", async (original) => ({
  ...(await original<typeof import("@/lib/graphql")>()),
  partyAdd,
}));

const LIBRARY = "library-karaoke";

function song(id: string, title: string, artist: string): SourceItem {
  return { id, kind: "song", title, subtitle: artist } as SourceItem;
}

const SONGS = [
  song("m1", "Dancing Queen", "ABBA"),
  song("m2", "Waterloo", "ABBA"),
  song("m3", "Total Eclipse of the Heart", "Bonnie Tyler"),
];

function page(items: SourceItem[]): LibraryItemsPage {
  return {
    total: items.length,
    items,
    facets: {} as LibraryItemsPage["facets"],
    letters: [
      { letter: "A", offset: 0, count: 2 },
      { letter: "B", offset: 2, count: 1 },
    ],
  } as LibraryItemsPage;
}

function party(overrides: Partial<PartySession> = {}): PartySession {
  return {
    id: "p1",
    libraryIDs: [LIBRARY],
    participants: [
      { id: "s1", name: "Robin", guest: true, waiting: 1, lastTurnAt: "2026-11-23T10:00:00Z" },
    ],
    entries: [],
    ...overrides,
  } as PartySession;
}

function Where() {
  return <output data-testid="where">{useLocation().pathname}</output>;
}

function renderSongbook(session: PartySession | null, run = vi.fn(async () => true)) {
  render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/n/karaoke"]}>
        <Where />
        <Routes>
          <Route
            path="*"
            element={<KaraokeSongbook libraryID={LIBRARY} session={session} run={run} />}
          />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
  return run;
}

const lastRequest = () => fetchLibraryItems.mock.lastCall?.[0] as LibraryItemsRequest;

beforeEach(() => {
  fetchLibraryItems.mockImplementation(async () => page(SONGS));
  partyAdd.mockResolvedValue(null);
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("KaraokeSongbook", () => {
  it("lists songs by artist under artist headings", async () => {
    renderSongbook(null);
    expect(await screen.findByText("Dancing Queen")).toBeInTheDocument();
    expect(lastRequest()).toMatchObject({ libraryID: LIBRARY, fieldSort: "artist", offset: 0 });
    expect(screen.getByRole("heading", { level: 3, name: "ABBA" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 3, name: "Bonnie Tyler" })).toBeInTheDocument();
    expect(screen.getByText("3 songs")).toBeInTheDocument();
  });

  it("searches artist and title with one query", async () => {
    const user = userEvent.setup();
    renderSongbook(null);
    await screen.findByText("Dancing Queen");
    fetchLibraryItems.mockImplementation(async () => page([SONGS[2]]));
    await user.type(screen.getByRole("searchbox", { name: "Search songs" }), "tyler");
    await waitFor(() => expect(lastRequest().state.query).toBe("tyler"));
    expect(await screen.findByText("1 match")).toBeInTheDocument();
  });

  it("orders by song title without the artist field", async () => {
    const user = userEvent.setup();
    renderSongbook(null);
    await screen.findByText("Dancing Queen");
    await user.click(screen.getByRole("button", { name: "Song" }));
    await waitFor(() => expect(lastRequest().fieldSort).toBeUndefined());
    expect(lastRequest().state).toMatchObject({ sort: "title", direction: "asc" });
    expect(screen.getByRole("button", { name: "Song" })).toHaveAttribute("aria-pressed", "true");
  });

  it("plays a song", async () => {
    const user = userEvent.setup();
    renderSongbook(null);
    await user.click(await screen.findByRole("button", { name: "Play Waterloo" }));
    expect(screen.getByTestId("where").textContent).toContain("m2");
  });

  it("offers Add for a singer only while a party runs in this Library", async () => {
    renderSongbook(party({ libraryIDs: ["other"] }));
    await screen.findByText("Dancing Queen");
    expect(screen.queryByRole("button", { name: "Add Waterloo for a singer" })).toBeNull();
  });

  it("adds a song for a recent singer", async () => {
    const user = userEvent.setup();
    const run = renderSongbook(party());
    await user.click(await screen.findByRole("button", { name: "Add Waterloo for a singer" }));
    await user.click(screen.getByRole("button", { name: /Robin/ }));
    expect(run).toHaveBeenCalledTimes(1);
    const [op] = run.mock.calls[0] as unknown as [() => Promise<unknown>];
    await op();
    expect(partyAdd).toHaveBeenCalledWith({ mediaID: "m2", participantID: "s1" });
  });
});
