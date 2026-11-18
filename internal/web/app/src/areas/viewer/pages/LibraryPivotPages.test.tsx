import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { type CollectionInfo, fetchCollections } from "@/lib/graphql";
import type { SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { expectNoAxeViolations, expectNoPositiveTabindex } from "@/test/axe";
import { fetchLibraryItems } from "../lib/libraryBrowse";
import { LibraryPivotPage } from "./LibraryPivotPages";

vi.mock("@/lib/graphql", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/graphql")>();
  return { ...actual, fetchCollections: vi.fn() };
});
vi.mock("../lib/libraryBrowse", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/libraryBrowse")>();
  return { ...actual, fetchLibraryItems: vi.fn() };
});
// The server names a Library's node "library-" + the Library's ID, and real
// Library IDs already start "library-", so the readable address drops one
// (CORE-2322).
vi.mock("@/lib/session", () => ({
  useSession: () => ({
    loading: false,
    manifest: {
      navigation: [
        {
          id: "library-library-films",
          view: "layout",
          layoutID: "l",
          libraryID: "library-films",
          title: { en: "Films" },
        },
        { id: "home", view: "layout", layoutID: "l", title: { en: "Home" } },
      ],
    },
  }),
}));
vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
vi.mock("../components/ViewerActions", () => ({ ViewerActions: () => null }));

function collection(id: string, name: string, sortOrder: number): CollectionInfo {
  return { id, libraryID: "library-films", name, sortOrder, memberCount: 2, members: [] };
}

function Where() {
  const location = useLocation();
  return <output data-testid="where">{location.pathname + location.search}</output>;
}

function renderAt(path: string) {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[path]}>
        <Where />
        <Routes>
          <Route path="/" element={<p>root</p>} />
          <Route path="/n/:nodeId" element={<p>node</p>} />
          <Route path="/n/:nodeId/collections" element={<LibraryPivotPage pivot="collections" />} />
          <Route path="/n/:nodeId/categories" element={<LibraryPivotPage pivot="categories" />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.mocked(fetchCollections).mockReset();
  vi.mocked(fetchLibraryItems).mockReset();
});

describe("Library Collections pivot (CORE-2437)", () => {
  it("lists only the Library's own Collections, in curated order, with the pivot selected", async () => {
    vi.mocked(fetchCollections).mockResolvedValue([
      collection("c2", "Noir", 2),
      collection("c1", "Classics", 1),
    ]);
    renderAt("/n/library-films/collections");

    const tiles = await screen.findAllByRole("link", { name: /items?$/ });
    expect(fetchCollections).toHaveBeenCalledWith("library-films");
    expect(tiles.map((tile) => tile.getAttribute("href"))).toEqual([
      "/collections/library-films/c1",
      "/collections/library-films/c2",
    ]);
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Films");
    const pivots = within(screen.getByRole("navigation", { name: "Library views" }));
    expect(pivots.getByRole("link", { name: "Collections" })).toHaveAttribute(
      "aria-current",
      "page",
    );
    expect(screen.getByRole("link", { name: "All collections" })).toHaveAttribute(
      "href",
      "/collections",
    );
  });

  it("passes axe and walks the pivots, then the tiles, by keyboard", async () => {
    vi.mocked(fetchCollections).mockResolvedValue([collection("c1", "Classics", 1)]);
    const { container } = renderAt("/n/library-films/collections");
    const tile = await screen.findByRole("link", { name: /Classics/ });

    await expectNoAxeViolations(container);
    expectNoPositiveTabindex(container);
    const pivots = within(screen.getByRole("navigation", { name: "Library views" }));
    const order = [...pivots.getAllByRole("link"), tile];
    for (const link of order) {
      await userEvent.tab();
      expect(link).toHaveFocus();
    }
  });

  it("says when the Library has none", async () => {
    vi.mocked(fetchCollections).mockResolvedValue([]);
    renderAt("/n/library-films/collections");

    expect(await screen.findByText("This library has no collections yet.")).toBeInTheDocument();
  });

  it("offers a retry when the read fails", async () => {
    vi.mocked(fetchCollections)
      .mockRejectedValueOnce(new Error("down"))
      .mockResolvedValue([collection("c1", "Classics", 1)]);
    renderAt("/n/library-films/collections");

    const alert = await screen.findByRole("alert");
    await userEvent.click(within(alert).getByRole("button"));

    expect(await screen.findByRole("link", { name: /Classics/ })).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("moves an old raw-ID address to the readable one", async () => {
    vi.mocked(fetchCollections).mockResolvedValue([]);
    renderAt("/n/library-library-films/collections");

    expect(screen.getByTestId("where")).toHaveTextContent(/^\/n\/library-films\/collections$/);
    expect(await screen.findByText("This library has no collections yet.")).toBeInTheDocument();
  });

  it("sends a node with no Library back to its own page", () => {
    renderAt("/n/home/collections");

    expect(screen.getByTestId("where")).toHaveTextContent(/^\/n\/home$/);
  });
});

type Grouping = { kind: string; title?: { en: string }; values: string[]; counts?: number[] };

const studioValues = Array.from({ length: 45 }, (_, i) => `Studio ${String(i).padStart(2, "0")}`);
const studioCounts = studioValues.map((_, i) => (i === 44 ? 1 : i % 5));

/** The facet read answers with the groupings; a per-value read with that value's items. */
function serveCategories(groupings: Grouping[], art: Record<string, SourceItem[]> = {}) {
  vi.mocked(fetchLibraryItems).mockImplementation(async ({ state }) => {
    const [kind, value] = Object.entries(state.groups)[0] ?? [];
    return {
      total: 0,
      items: kind ? (art[`${kind}=${value}`] ?? []) : [],
      facets: { groupings, decades: [], qualities: [] },
      letters: [],
    };
  });
}

function withArt(id: string, backdrop: string): SourceItem {
  return { id, kind: "media", title: id, artworkSlots: { backdrop } } as unknown as SourceItem;
}

describe("Library Categories pivot (CORE-2437, CORE-2505)", () => {
  it("tiles a featured grouping's values with counts and opens the filtered Media view", async () => {
    serveCategories([
      { kind: "genre", title: { en: "Genre" }, values: ["Drama", "Sci-Fi"], counts: [12, 1] },
      { kind: "studio", title: { en: "Studio" }, values: [] },
    ]);
    renderAt("/n/library-films/categories");

    const drama = await screen.findByRole("link", { name: "Drama 12 items" });
    expect(vi.mocked(fetchLibraryItems).mock.calls[0]?.[0]).toMatchObject({
      libraryID: "library-films",
    });
    expect(drama).toHaveAttribute("href", "/n/library-films/all?group.genre=Drama&view=media");
    // A featured grouping keeps a single-item value.
    expect(screen.getByRole("link", { name: "Sci-Fi 1 item" })).toHaveAttribute(
      "href",
      "/n/library-films/all?group.genre=Sci-Fi&view=media",
    );
    // An empty kind gets no section; a lone grouping needs no heading or jump links.
    expect(screen.queryByRole("region", { name: "Studio" })).toBeNull();
    expect(screen.queryByRole("heading", { level: 2 })).toBeNull();
    expect(screen.queryByRole("navigation", { name: "Jump to a category" })).toBeNull();

    await expectNoAxeViolations(document.body);
    await userEvent.click(drama);
    expect(screen.getByTestId("where")).toHaveTextContent(
      "/n/library-films/all?group.genre=Drama&view=media",
    );
  });

  it("shows each featured value's best rated backdrop, never the same picture twice", async () => {
    serveCategories([{ kind: "genre", values: ["Drama", "Noir", "Western"], counts: [3, 2, 2] }], {
      "genre=Drama": [withArt("a", "/art/a")],
      "genre=Noir": [withArt("a", "/art/a"), withArt("b", "/art/b")],
      "genre=Western": [withArt("c", "/art/a")],
    });
    const { container } = renderAt("/n/library-films/categories");

    await screen.findByRole("link", { name: "Drama 3 items" });
    await vi.waitFor(() => expect(container.querySelectorAll("img")).toHaveLength(2));
    const sources = [...container.querySelectorAll("img")].map((img) => img.getAttribute("src"));
    expect(sources).toEqual(["/art/a?bucket=w640", "/art/b?bucket=w640"]);
    const reads = vi.mocked(fetchLibraryItems).mock.calls.map(([request]) => request);
    expect(reads.find((request) => request.state.groups.genre === "Noir")?.state).toMatchObject({
      sort: "audience-score",
      direction: "desc",
    });
    // The Western tile keeps the accent surface: its only picture is Drama's.
    expect(
      within(screen.getByRole("link", { name: "Western 2 items" })).queryByRole("img"),
    ).toBeNull();
  });

  it("shortens a long-tail grouping to its most used values, with jump links and See all", async () => {
    serveCategories([
      { kind: "studio", title: { en: "Studio" }, values: studioValues, counts: studioCounts },
      { kind: "genre", title: { en: "Genre" }, values: ["Drama"], counts: [4] },
    ]);
    const { container } = renderAt("/n/library-films/categories");

    const studio = await screen.findByRole("region", { name: "Studio" });
    const headings = screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent);
    expect(headings).toEqual(["Genre", "Studio"]);
    const top = within(studio)
      .getAllByRole("link")
      .filter((link) => link.getAttribute("href")?.includes("group.studio="));
    expect(top).toHaveLength(12);
    expect(top[0]).toHaveAccessibleName("Studio 04 4 items");
    expect(within(studio).queryByRole("link", { name: /Studio 44/ })).toBeNull();
    expect(within(studio).queryByRole("link", { name: /^Studio 00/ })).toBeNull();

    const jump = within(screen.getByRole("navigation", { name: "Jump to a category" }));
    await userEvent.click(jump.getByRole("link", { name: "Studio" }));
    expect(screen.getByRole("heading", { name: "Studio" })).toHaveFocus();
    await expectNoAxeViolations(container);
    expectNoPositiveTabindex(container);

    await userEvent.click(within(studio).getByRole("link", { name: "See all 45: Studio" }));
    expect(screen.getByTestId("where")).toHaveTextContent(
      /^\/n\/library-films\/categories\?kind=studio$/,
    );
  });

  it("lists every value A-Z with a filter that ignores case and accents", async () => {
    serveCategories([
      {
        kind: "studio",
        title: { en: "Studio" },
        values: [...studioValues.slice(0, 40), "Pathé", "Warner Bros.", "warner home", "20th"],
        counts: [...studioCounts.slice(0, 40), 3, 9, 1, 2],
      },
    ]);
    const { container } = renderAt("/n/library-films/categories?kind=studio");

    expect(await screen.findByRole("heading", { level: 2, name: "Studio" })).toHaveFocus();
    const letters = within(screen.getByRole("navigation", { name: "Jump to a letter" }));
    expect(letters.getAllByRole("link").map((link) => link.textContent)).toEqual([
      "#",
      "P",
      "S",
      "W",
    ]);
    // Single-item values belong in the full list.
    expect(screen.getByRole("link", { name: "warner home 1 item" })).toBeInTheDocument();
    await expectNoAxeViolations(container);

    const filter = screen.getByRole("searchbox", { name: "Filter Studio" });
    await userEvent.type(filter, "WARNER");
    expect(
      screen
        .getAllByRole("link")
        .filter((link) => link.getAttribute("href")?.includes("group.studio="))
        .map((link) => link.textContent),
    ).toEqual(["Warner Bros.9 items", "warner home1 item"]);
    await userEvent.clear(filter);
    await userEvent.type(filter, "pathe");
    expect(screen.getByRole("link", { name: "Pathé 3 items" })).toHaveAttribute(
      "href",
      "/n/library-films/all?group.studio=Path%C3%A9&view=media",
    );
    await userEvent.type(filter, "zzz");
    expect(screen.getByText("Nothing matches “pathezzz”.")).toHaveAttribute("role", "status");

    await userEvent.click(screen.getByRole("link", { name: "All categories" }));
    expect(screen.getByTestId("where")).toHaveTextContent(/^\/n\/library-films\/categories$/);
  });

  it("degrades to See all alone when the server reports no counts, and ignores an unknown kind", async () => {
    serveCategories([{ kind: "studio", title: { en: "Studio" }, values: studioValues }]);
    renderAt("/n/library-films/categories?kind=nope");

    const studio = await screen.findByRole("region", { name: "Studio" });
    expect(within(studio).getAllByRole("link")).toHaveLength(1);
    expect(within(studio).getByRole("link", { name: "See all 45: Studio" })).toHaveTextContent(
      "See all 45",
    );
  });

  it("says when the Library has no categories", async () => {
    serveCategories([]);
    renderAt("/n/library-films/categories");

    expect(await screen.findByText("This library has no categories.")).toBeInTheDocument();
  });

  it("offers a retry when the facet read fails", async () => {
    vi.mocked(fetchLibraryItems).mockRejectedValueOnce(new Error("down"));
    renderAt("/n/library-films/categories");

    const alert = await screen.findByRole("alert");
    serveCategories([{ kind: "genre", values: ["Drama"], counts: [2] }]);
    await userEvent.click(within(alert).getByRole("button"));
    expect(await screen.findByRole("link", { name: "Drama 2 items" })).toBeInTheDocument();
  });
});
