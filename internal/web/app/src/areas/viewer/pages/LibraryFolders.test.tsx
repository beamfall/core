import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { fetchLibraryItems } from "../lib/libraryBrowse";
import {
  fetchLibraryFolder,
  folderPath,
  folderSearch,
  isFolderView,
  type LibraryFolderPage,
  type LibraryFolderRequest,
} from "../lib/libraryFolders";
import { LibraryBrowsePage } from "./LibraryBrowsePage";

vi.mock("../lib/libraryBrowse", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/libraryBrowse")>();
  return { ...actual, fetchLibraryItems: vi.fn() };
});
// The Library has no browse kinds, so it opens on the Media view (CORE-2362).
vi.mock("../lib/libraryViews", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/libraryViews")>();
  return {
    ...actual,
    fetchLibraryEntities: vi.fn(async () => ({ kinds: [], kind: "", total: 0, items: [] })),
  };
});
vi.mock("../lib/libraryFolders", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/libraryFolders")>();
  return { ...actual, fetchLibraryFolder: vi.fn() };
});
vi.mock("@/lib/session", () => ({
  useSession: () => ({
    loading: false,
    manifest: {
      navigation: [
        {
          id: "archive",
          view: "layout",
          layoutID: "l",
          libraryID: "lib",
          title: { en: "Archive" },
        },
      ],
    },
  }),
  useViewerIdentity: () => ({ profileID: "p", revealed: false }),
}));
vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
vi.mock("../components/ViewerActions", () => ({ ViewerActions: () => null }));
vi.mock("../components/RevealControls", () => ({ RevealControls: () => null }));

function media(id: string): SourceItem {
  return { id, kind: "media", title: `Clip ${id}` };
}

// A Library with two roots; /a holds a nested folder and three Media, and /a/Trips one more.
function folderFor(request: LibraryFolderRequest): LibraryFolderPage {
  const base = { rootCount: 2 };
  switch (request.path) {
    case "":
      return {
        ...base,
        path: "",
        crumbs: [],
        folders: [
          { path: "/a", name: "a", mediaCount: 4 },
          { path: "/b", name: "b", mediaCount: 1 },
        ],
        total: 0,
        items: [],
      };
    case "/a": {
      const all = ["m1", "m2", "m3"].map(media);
      return {
        ...base,
        path: "/a",
        crumbs: [{ path: "/a", name: "a" }],
        folders: [{ path: "/a/Trips", name: "Trips", mediaCount: 1 }],
        total: all.length,
        items: all.slice(request.offset, request.offset + 2),
      };
    }
    case "/a/Trips":
      return {
        ...base,
        path: "/a/Trips",
        crumbs: [
          { path: "/a", name: "a" },
          { path: "/a/Trips", name: "Trips" },
        ],
        folders: [],
        total: 1,
        items: [media("m4")],
      };
    default:
      return { ...base, path: request.path, crumbs: [], folders: [], total: 0, items: [] };
  }
}

function Where() {
  const location = useLocation();
  return <output data-testid="where">{location.pathname + location.search}</output>;
}

function renderAt(entry: string) {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[entry]}>
        <Routes>
          <Route
            path="/n/:nodeId/all"
            element={
              <>
                <LibraryBrowsePage />
                <Where />
              </>
            }
          />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.mocked(fetchLibraryFolder).mockReset();
  vi.mocked(fetchLibraryItems).mockReset();
  window.localStorage.clear();
});

describe("library folder URL mapping (CORE-2359)", () => {
  it("round-trips the open folder through the search string", () => {
    expect(folderSearch()).toBe("?view=folders");
    const params = new URLSearchParams(folderSearch("/srv/Home Videos/2019"));
    expect(isFolderView(params)).toBe(true);
    expect(folderPath(params)).toBe("/srv/Home Videos/2019");
    expect(isFolderView(new URLSearchParams("view=grid"))).toBe(false);
  });
});

describe("Library folder view (CORE-2359)", () => {
  it("lists the roots, then opens nested folders one level at a time with breadcrumbs", async () => {
    vi.mocked(fetchLibraryFolder).mockImplementation(async (request) => folderFor(request));
    const user = userEvent.setup();
    renderAt("/n/archive/all?view=folders");

    const roots = await screen.findByRole("list", { name: "Folders" });
    expect(
      within(roots)
        .getAllByRole("link")
        .map((link) => link.textContent),
    ).toEqual(["a4 items", "b1 item"]);
    const path = screen.getByRole("navigation", { name: "Folder path" });
    expect(within(path).getByText("All folders")).toHaveAttribute("aria-current", "page");
    expect(await screen.findByRole("button", { name: "Folders" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(fetchLibraryFolder).toHaveBeenLastCalledWith(
      { libraryID: "lib", path: "", limit: 200, offset: 0 },
      expect.any(AbortSignal),
    );

    await user.click(within(roots).getByRole("link", { name: /^a/ }));
    expect(await screen.findByRole("list", { name: "a" })).toBeInTheDocument();
    expect(screen.getByTestId("where")).toHaveTextContent(
      "/n/archive/all?view=folders&folder=%2Fa",
    );
    // The heading counts the folder's own three Media and the one in Trips.
    expect(screen.getByText("4 items")).toHaveAttribute("role", "status");
    const cards = within(screen.getByRole("list", { name: "a" })).getAllByRole("listitem");
    expect(cards).toHaveLength(2);
    expect(cards[0]).toHaveAttribute("aria-setsize", "3");
    expect(within(path).getByRole("link", { name: "All folders" })).toBeInTheDocument();
    expect(within(path).getByText("a")).toHaveAttribute("aria-current", "page");

    // The rest of a folder's Media come a page at a time.
    await user.click(screen.getByRole("button", { name: "Load more" }));
    await waitFor(() =>
      expect(within(screen.getByRole("list", { name: "a" })).getAllByRole("listitem")).toHaveLength(
        3,
      ),
    );
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();

    await user.click(screen.getByRole("link", { name: /^Trips/ }));
    expect(await screen.findByRole("list", { name: "Trips" })).toBeInTheDocument();
    expect(within(path).getByText("Trips")).toHaveAttribute("aria-current", "page");

    // A breadcrumb walks back up.
    await user.click(within(path).getByRole("link", { name: "a" }));
    expect(await screen.findByRole("list", { name: "a" })).toBeInTheDocument();
    expect(screen.getByTestId("where")).toHaveTextContent("folder=%2Fa");
  });

  it("counts a folder of folders by the Media below it, never 0 items", async () => {
    // A single-root Library opens straight into its root, which holds only folders.
    vi.mocked(fetchLibraryFolder).mockResolvedValue({
      path: "/tv",
      rootCount: 1,
      crumbs: [{ path: "/tv", name: "tv" }],
      folders: [
        { path: "/tv/Shows", name: "Shows", mediaCount: 5 },
        { path: "/tv/Specials", name: "Specials", mediaCount: 2 },
      ],
      total: 0,
      items: [],
    });
    renderAt("/n/archive/all?view=folders");

    expect(await screen.findByRole("link", { name: /^Shows/ })).toBeInTheDocument();
    expect(screen.getByText("7 items")).toHaveAttribute("role", "status");
    expect(screen.queryByText("0 items")).toBeNull();
  });

  it("opens a folder by keyboard", async () => {
    vi.mocked(fetchLibraryFolder).mockImplementation(async (request) => folderFor(request));
    const user = userEvent.setup();
    renderAt("/n/archive/all?view=folders");

    const first = within(await screen.findByRole("list", { name: "Folders" })).getAllByRole(
      "link",
    )[0];
    first.focus();
    await user.keyboard("{Enter}");
    expect(await screen.findByRole("list", { name: "a" })).toBeInTheDocument();
  });

  it("says when a folder holds nothing to play", async () => {
    vi.mocked(fetchLibraryFolder).mockImplementation(async (request) => folderFor(request));
    renderAt("/n/archive/all?view=folders&folder=%2Fa%2Fempty");

    expect(await screen.findByText("Nothing to play in this folder.")).toBeInTheDocument();
  });

  it("offers a retry when a folder fails to load", async () => {
    vi.mocked(fetchLibraryFolder)
      .mockRejectedValueOnce(new Error("folder is outside the library roots"))
      .mockImplementation(async (request) => folderFor(request));
    const user = userEvent.setup();
    renderAt("/n/archive/all?view=folders&folder=%2Fa");

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Could not load this folder.");
    await user.click(within(alert).getByRole("button", { name: "Retry" }));
    expect(await screen.findByRole("list", { name: "a" })).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("switches between the Media and Folders views with the view switcher", async () => {
    vi.mocked(fetchLibraryFolder).mockImplementation(async (request) => folderFor(request));
    vi.mocked(fetchLibraryItems).mockImplementation(async () => ({
      total: 1,
      items: [media("m1")],
      facets: { groupings: [], decades: [], qualities: [] },
      letters: [],
    }));
    const user = userEvent.setup();
    renderAt("/n/archive/all");

    expect(await screen.findByText("1 item")).toBeInTheDocument();
    await user.click(await screen.findByRole("button", { name: "Folders" }));
    expect(await screen.findByRole("list", { name: "Folders" })).toBeInTheDocument();
    expect(screen.getByTestId("where")).toHaveTextContent("/n/archive/all?view=folders");

    await user.click(screen.getByRole("button", { name: "All items" }));
    expect(await screen.findByText("1 item")).toBeInTheDocument();
    expect(screen.getByTestId("where").textContent).toBe("/n/archive/all?view=media");
  });

  it("draws an open folder's items in the remembered layout with their subtitles (CORE-2386)", async () => {
    const episodes: SourceItem[] = [1, 2].map((index) => ({
      id: `e${index}`,
      kind: "media",
      title: "The Single Life",
      subtitle: { en: `S5 · E${index} · Part ${index}` },
      sequence: 5,
      index,
      durationSeconds: 2520,
    }));
    vi.mocked(fetchLibraryFolder).mockImplementation(async (request) =>
      request.path === "/s5"
        ? {
            rootCount: 1,
            path: "/s5",
            crumbs: [{ path: "/s5", name: "Season 5" }],
            folders: [],
            total: episodes.length,
            items: episodes,
          }
        : folderFor(request),
    );
    const user = userEvent.setup();
    renderAt("/n/archive/all?view=folders&folder=%2Fs5");

    const list = await screen.findByRole("list", { name: "Season 5" });
    expect(list).toHaveClass("bf-grid");
    expect(screen.getByRole("link", { name: "The Single Life, S5 · E1 · Part 1" })).toBeVisible();
    expect(screen.getByRole("link", { name: "The Single Life, S5 · E2 · Part 2" })).toBeVisible();

    await user.click(screen.getByRole("button", { name: "List" }));
    const rows = screen.getByRole("list", { name: "Season 5" });
    expect(rows).toHaveClass("bf-stack", "bf-rows");
    expect([...rows.querySelectorAll(".bf-card-ordinal")].map((n) => n.textContent)).toEqual([
      "1",
      "2",
    ]);
    expect(rows.querySelector(".bf-card-runtime")).toHaveTextContent("42:00");
    expect(window.localStorage.getItem("beamfall.libraryView.lib")).toBe("list");
  });

  it("hides the layout choice where a folder holds only folders", async () => {
    vi.mocked(fetchLibraryFolder).mockImplementation(async (request) => folderFor(request));
    renderAt("/n/archive/all?view=folders");

    expect(await screen.findByRole("list", { name: "Folders" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Posters" })).toBeNull();
  });
});
