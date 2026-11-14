import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { notifyEngagementChanged } from "../actions/engagementSync";
import { fetchLibraryItems, type LibraryItemsRequest } from "../lib/libraryBrowse";
import { LibraryBrowsePage } from "./LibraryBrowsePage";

vi.mock("../lib/libraryBrowse", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/libraryBrowse")>();
  return { ...actual, fetchLibraryItems: vi.fn() };
});
vi.mock("@/lib/session", () => ({
  useSession: () => ({
    loading: false,
    manifest: {
      navigation: [
        { id: "films", view: "layout", layoutID: "l", libraryID: "lib", title: { en: "Films" } },
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

const TOTAL = 1200;

function pageFor(request: LibraryItemsRequest) {
  const total = request.state.watch ? 3 : TOTAL;
  const count = Math.max(0, Math.min(request.limit, total - request.offset));
  const items = Array.from<unknown, SourceItem>({ length: count }, (_, i) => ({
    id: `media-${request.offset + i}`,
    kind: "media",
    title: `Item ${request.offset + i}`,
  }));
  return {
    total,
    items,
    facets: {
      groupings: [{ kind: "sector", title: { en: "Sector" }, values: ["Inner", "Outer"] }],
      decades: [1990],
      qualities: ["hd1080"],
    },
  };
}

function WatchState() {
  const location = useLocation();
  const queue = (location.state as { queue?: { id: string }[] } | null)?.queue ?? [];
  return <output data-testid="watch">{queue.map((entry) => entry.id).join(",")}</output>;
}

function Where() {
  const location = useLocation();
  return <output data-testid="where">{location.pathname + location.search}</output>;
}

afterEach(() => {
  cleanup();
  vi.mocked(fetchLibraryItems).mockReset();
  window.localStorage.clear();
});

describe("Library browse", () => {
  it("counts the whole library, windows it, and keeps sort, filters and view in the URL and storage", async () => {
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => pageFor(request));
    const user = userEvent.setup();

    render(
      <I18nProvider>
        <MemoryRouter initialEntries={["/n/films/all?sort=year&dir=desc"]}>
          <Routes>
            <Route path="/n/:nodeId/all" element={<LibraryBrowsePage />} />
          </Routes>
          <Where />
        </MemoryRouter>
      </I18nProvider>,
    );

    expect(await screen.findByText("1,200 items")).toBeInTheDocument();
    expect(vi.mocked(fetchLibraryItems).mock.calls[0][0]).toMatchObject({
      libraryID: "lib",
      state: { sort: "year", direction: "desc" },
    });
    const first = await screen.findByRole("button", { name: "Item 0" });
    expect(first.closest("li")).toHaveAttribute("aria-setsize", "1200");
    expect(document.querySelectorAll(".bf-card").length).toBeLessThan(TOTAL);
    expect(screen.getByRole("button", { name: "Play all" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Shuffle" })).toBeEnabled();

    await user.click(screen.getByRole("button", { name: "Sort direction: Descending" }));
    expect(screen.getByTestId("where")).toHaveTextContent("/n/films/all?sort=year");

    await user.click(screen.getByRole("button", { name: "List" }));
    expect(screen.getByRole("list", { name: "Films" })).toHaveClass("bf-stack");
    expect(window.localStorage.getItem("beamfall.libraryView.lib")).toBe("list");
  });

  it("shows an active filter from the URL as a removable chip", async () => {
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => pageFor(request));
    const user = userEvent.setup();

    render(
      <I18nProvider>
        <MemoryRouter initialEntries={["/n/films/all?watch=unwatched"]}>
          <Routes>
            <Route path="/n/:nodeId/all" element={<LibraryBrowsePage />} />
          </Routes>
          <Where />
        </MemoryRouter>
      </I18nProvider>,
    );

    expect(await screen.findByText("3 items")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Remove filter: Unwatched" }));
    await waitFor(() => expect(screen.getByTestId("where")).toHaveTextContent("/n/films/all"));
    expect(await screen.findByText("1,200 items")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Remove filter/ })).toBeNull();
  });

  it("offers the plugin's groupings by their own names and keeps the choice in the URL", async () => {
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => pageFor(request));
    const user = userEvent.setup();

    render(
      <I18nProvider>
        <MemoryRouter initialEntries={["/n/films/all?group.sector=Outer"]}>
          <Routes>
            <Route path="/n/:nodeId/all" element={<LibraryBrowsePage />} />
          </Routes>
          <Where />
        </MemoryRouter>
      </I18nProvider>,
    );

    expect(await screen.findByText("1,200 items")).toBeInTheDocument();
    expect(vi.mocked(fetchLibraryItems).mock.calls[0][0]).toMatchObject({
      state: { groups: { sector: "Outer" } },
    });
    expect(screen.getByRole("combobox", { name: "Sector" })).toHaveTextContent("Outer");

    await user.click(screen.getByRole("button", { name: "Remove filter: Sector: Outer" }));
    await waitFor(() => expect(screen.getByTestId("where")).toHaveTextContent(/\/n\/films\/all$/));
    expect(screen.queryByRole("button", { name: /Remove filter/ })).toBeNull();
  });

  it("hands a played card's loaded neighbours to the player as its queue (CORE-1143)", async () => {
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => pageFor(request));
    const user = userEvent.setup();

    render(
      <I18nProvider>
        <MemoryRouter initialEntries={["/n/films/all"]}>
          <Routes>
            <Route path="/n/:nodeId/all" element={<LibraryBrowsePage />} />
            <Route path="/watch/:id" element={<WatchState />} />
          </Routes>
        </MemoryRouter>
      </I18nProvider>,
    );

    await user.click(await screen.findByRole("button", { name: "Item 1" }));
    const queue = (await screen.findByTestId("watch")).textContent?.split(",") ?? [];
    expect(queue.slice(0, 3)).toEqual(["media-0", "media-1", "media-2"]);
  });

  it("sorts and filters by the profile's own rating from the URL", async () => {
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => pageFor(request));
    const user = userEvent.setup();

    render(
      <I18nProvider>
        <MemoryRouter initialEntries={["/n/films/all?sort=rating&dir=desc&rated=8"]}>
          <Routes>
            <Route path="/n/:nodeId/all" element={<LibraryBrowsePage />} />
          </Routes>
          <Where />
        </MemoryRouter>
      </I18nProvider>,
    );

    expect(await screen.findByText("1,200 items")).toBeInTheDocument();
    expect(vi.mocked(fetchLibraryItems).mock.calls[0][0]).toMatchObject({
      state: { sort: "rating", direction: "desc", minRating: 8 },
    });
    await user.click(screen.getByRole("button", { name: "Remove filter: 4 stars or more" }));
    await waitFor(() =>
      expect(vi.mocked(fetchLibraryItems).mock.calls.at(-1)?.[0]).toMatchObject({
        state: { sort: "rating", minRating: 0 },
      }),
    );
    expect(screen.getByTestId("where")).not.toHaveTextContent("rated=");
  });

  it("reloads when engagement changes under a query that depends on it, and refreshes cards in place otherwise", async () => {
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => pageFor(request));
    const mount = (url: string) =>
      render(
        <I18nProvider>
          <MemoryRouter initialEntries={[url]}>
            <Routes>
              <Route path="/n/:nodeId/all" element={<LibraryBrowsePage />} />
            </Routes>
          </MemoryRouter>
        </I18nProvider>,
      );

    mount("/n/films/all?sort=rating");
    expect(await screen.findByText("1,200 items")).toBeInTheDocument();
    const before = vi.mocked(fetchLibraryItems).mock.calls.length;
    act(() => notifyEngagementChanged());
    await waitFor(() =>
      expect(vi.mocked(fetchLibraryItems).mock.calls.length).toBeGreaterThan(before),
    );
    expect(vi.mocked(fetchLibraryItems).mock.calls.at(-1)?.[0]).toMatchObject({ offset: 0 });
    expect(screen.getByText("1,200 items")).toBeInTheDocument();

    // An empty result has no window to reload, yet a change can add its first item.
    cleanup();
    let matches = 0;
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => ({
      ...pageFor(request),
      total: matches,
      items: pageFor(request).items.slice(0, matches),
    }));
    mount("/n/films/all?rated=8");
    await waitFor(() => expect(screen.queryByRole("button", { name: "Item 0" })).toBeNull());
    await waitFor(() => expect(vi.mocked(fetchLibraryItems)).toHaveBeenCalled());
    matches = 1;
    act(() => notifyEngagementChanged());
    expect(await screen.findByRole("button", { name: "Item 0" })).toBeInTheDocument();
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => pageFor(request));

    // A query that does not depend on engagement keeps its pages and count, and
    // re-reads the loaded ones so each card's watched mark follows the change.
    cleanup();
    mount("/n/films/all?sort=year");
    expect(await screen.findByText("1,200 items")).toBeInTheDocument();
    await screen.findByRole("button", { name: "Item 0" });
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => {
      const page = pageFor(request);
      return {
        ...page,
        items: page.items.map((item) => ({ ...item, engagement: { completed: true } })),
      };
    });
    act(() => notifyEngagementChanged());
    expect(await screen.findByRole("button", { name: "Item 0, Finished" })).toBeInTheDocument();
    expect(screen.getByText("1,200 items")).toBeInTheDocument();
  });
});
