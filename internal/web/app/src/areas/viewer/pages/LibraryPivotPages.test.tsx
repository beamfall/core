import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { type CollectionInfo, fetchCollections } from "@/lib/graphql";
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

describe("Library Categories pivot (CORE-2437)", () => {
  it("tiles each grouping value and opens the filtered Media view", async () => {
    vi.mocked(fetchLibraryItems).mockResolvedValue({
      total: 3,
      items: [],
      facets: {
        groupings: [
          { kind: "genre", title: { en: "Genre" }, values: ["Drama", "Sci-Fi"] },
          { kind: "studio", title: { en: "Studio" }, values: [] },
        ],
        decades: [],
        qualities: [],
      },
      letters: [],
    });
    renderAt("/n/library-films/categories");

    const drama = await screen.findByRole("link", { name: "Drama" });
    expect(vi.mocked(fetchLibraryItems).mock.calls[0]?.[0]).toMatchObject({
      libraryID: "library-films",
    });
    expect(drama).toHaveAttribute("href", "/n/library-films/all?group.genre=Drama&view=media");
    expect(screen.getByRole("link", { name: "Sci-Fi" })).toHaveAttribute(
      "href",
      "/n/library-films/all?group.genre=Sci-Fi&view=media",
    );
    // A grouping with no values gets no section; a lone grouping needs no heading.
    expect(screen.queryByRole("region", { name: "Studio" })).toBeNull();
    expect(screen.queryByRole("heading", { level: 2 })).toBeNull();

    await expectNoAxeViolations(document.body);
    await userEvent.click(drama);
    expect(screen.getByTestId("where")).toHaveTextContent(
      "/n/library-films/all?group.genre=Drama&view=media",
    );
  });

  it("says when the Library has no categories", async () => {
    vi.mocked(fetchLibraryItems).mockResolvedValue({
      total: 0,
      items: [],
      facets: { groupings: [], decades: [], qualities: [] },
      letters: [],
    });
    renderAt("/n/library-films/categories");

    expect(await screen.findByText("This library has no categories.")).toBeInTheDocument();
  });
});
