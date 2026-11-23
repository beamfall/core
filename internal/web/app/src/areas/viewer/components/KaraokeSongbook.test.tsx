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

  it("marks the letter of the first song showing, not the window start", async () => {
    // jsdom lays nothing out: put the ABBA rows above the header and Bonnie Tyler below it.
    const rect = vi
      .spyOn(HTMLElement.prototype, "getBoundingClientRect")
      .mockImplementation(function (this: HTMLElement) {
        const offset = Number(this.dataset.offset ?? -1);
        const bottom = offset < 0 ? 0 : offset < 2 ? 40 : 400;
        return { bottom, top: bottom - 60, left: 0, right: 0, width: 0, height: 60 } as DOMRect;
      });
    renderSongbook(null);
    await screen.findByText("Dancing Queen");
    await waitFor(() =>
      expect(document.querySelector('[aria-current="true"]')?.textContent).toContain("B"),
    );
    rect.mockRestore();
  });

  it("filters by the duet and format values the Library has (CORE-3369)", async () => {
    const user = userEvent.setup();
    const withFacets = (items: SourceItem[]): LibraryItemsPage => ({
      ...page(items),
      facets: {
        groupings: [],
        decades: [],
        qualities: [],
        fields: [
          { field: "class", values: ["cdg", "lrc"], counts: [2, 1] },
          { field: "singers", values: ["duet"], counts: [1] },
          { field: "vocals", values: [], counts: [] },
        ],
      },
    });
    fetchLibraryItems.mockImplementation(async () => withFacets(SONGS));
    renderSongbook(null);
    await screen.findByText("Dancing Queen");
    expect(lastRequest().facetFields).toEqual(["singers", "vocals", "class"]);
    expect(screen.getByRole("button", { name: "CDG · 2 songs" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Guide vocal/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /Video/ })).toBeNull();
    cleanup();

    // A value every song has filters nothing, so it gets no chip.
    fetchLibraryItems.mockImplementation(async () => ({
      ...withFacets(SONGS),
      facets: {
        ...withFacets(SONGS).facets,
        fields: [{ field: "class", values: ["lrc"], counts: [3] }],
      },
    }));
    renderSongbook(null);
    await screen.findByText("Dancing Queen");
    expect(screen.queryByRole("button", { name: /LRC/ })).toBeNull();
    cleanup();

    fetchLibraryItems.mockImplementation(async () => withFacets(SONGS));
    renderSongbook(null);
    await screen.findByText("Dancing Queen");
    const duet = screen.getByRole("button", { name: "Duet · 1 song" });

    fetchLibraryItems.mockImplementation(async () => withFacets([SONGS[2]]));
    await user.click(duet);
    await waitFor(() => expect(lastRequest().fieldFilters).toEqual({ singers: "duet" }));
    expect(await screen.findByText("1 match")).toBeInTheDocument();
    expect(duet).toHaveAttribute("aria-pressed", "true");
    expect(duet).toHaveFocus();

    fetchLibraryItems.mockImplementation(async () => withFacets([]));
    await user.click(screen.getByRole("button", { name: "LRC · 1 song" }));
    await waitFor(() =>
      expect(lastRequest().fieldFilters).toEqual({ singers: "duet", class: "lrc" }),
    );
    expect(await screen.findByText("No songs match these filters.")).toBeInTheDocument();
    fetchLibraryItems.mockImplementation(async () => withFacets(SONGS));
    await user.click(screen.getByRole("button", { name: "Clear filters" }));
    await waitFor(() => expect(lastRequest().fieldFilters).toEqual({}));
    expect(await screen.findByText("3 songs")).toBeInTheDocument();
  });

  it("compares chip counts with the whole Library, not a search", async () => {
    const user = userEvent.setup();
    // Facets count the Library: 2 of its 3 songs are LRC, which a search for
    // ABBA's 2 songs must not mistake for "every song".
    const facets = {
      groupings: [],
      decades: [],
      qualities: [],
      fields: [{ field: "class", values: ["lrc", "cdg"], counts: [2, 1] }],
    };
    fetchLibraryItems.mockImplementation(async () => ({ ...page(SONGS), facets }));
    renderSongbook(null);
    await screen.findByText("Dancing Queen");
    fetchLibraryItems.mockImplementation(async () => ({
      ...page(SONGS.slice(0, 2)),
      facets,
    }));
    await user.type(screen.getByRole("searchbox", { name: "Search songs" }), "abba");
    expect(await screen.findByText("2 matches")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "LRC · 2 songs" })).toBeInTheDocument();
  });
});
