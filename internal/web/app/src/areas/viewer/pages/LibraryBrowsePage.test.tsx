import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { notifyEngagementChanged } from "../actions/engagementSync";
import { letterRailInitial } from "../components/LibraryLetterRail";
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
    // A title sort reports where each initial starts across the whole result.
    letters:
      request.state.sort === "title"
        ? [
            { letter: "#", offset: 0, count: 5 },
            { letter: "I", offset: 5, count: 895 },
            { letter: "M", offset: 900, count: total - 900 },
          ]
        : [],
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

    // Posters open the detail page; only list rows play on activation.
    expect(document.querySelector(".bf-card-play")).toBeNull();
    await user.click(screen.getByRole("button", { name: "List" }));
    expect(screen.getByRole("list", { name: "Films" })).toHaveClass("bf-stack");
    expect(document.querySelector(".bf-card-play")).not.toBeNull();
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

  // A poster opens the detail page (CORE-1144), which forwards the queue to its Play.
  it("hands an opened card's loaded neighbours on as the player's queue (CORE-1143)", async () => {
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => pageFor(request));
    const user = userEvent.setup();

    render(
      <I18nProvider>
        <MemoryRouter initialEntries={["/n/films/all"]}>
          <Routes>
            <Route path="/n/:nodeId/all" element={<LibraryBrowsePage />} />
            <Route path="/media/:id" element={<WatchState />} />
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

  function renderBrowse(url: string) {
    render(
      <I18nProvider>
        <MemoryRouter initialEntries={[url]}>
          <Routes>
            <Route path="/n/:nodeId/all" element={<LibraryBrowsePage />} />
          </Routes>
          <Where />
        </MemoryRouter>
      </I18nProvider>,
    );
  }

  it("jumps to a title initial from the letter rail and focuses its first item (CORE-1685)", async () => {
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => pageFor(request));
    // jsdom lays nothing out; the jump's scroll is observed, not performed.
    const scrollTo = vi.spyOn(window, "scrollTo").mockImplementation(() => {});
    const user = userEvent.setup();
    renderBrowse("/n/films/all");

    const rail = await screen.findByRole("navigation", { name: "Jump to letter" });
    // Every letter keeps its place; an empty one says so and is skipped.
    const letterB = screen.getByRole("button", { name: "B, no titles" });
    expect(letterB).toHaveAttribute("aria-disabled", "true");
    expect(letterB).toHaveAttribute("tabindex", "-1");
    // The letter at the top of the grid is current, and is the one tab stop.
    const other = screen.getByRole("button", { name: "Numbers and symbols, 5 titles" });
    expect(other).toHaveAttribute("aria-current", "true");
    expect(other).toHaveAttribute("tabindex", "0");
    expect(rail.querySelectorAll('[tabindex="0"]')).toHaveLength(1);

    // The arrows skip empty letters and jump the grid without leaving the rail.
    other.focus();
    scrollTo.mockClear();
    await user.keyboard("{ArrowDown}");
    const letterI = screen.getByRole("button", { name: "I, 895 titles" });
    expect(letterI).toHaveFocus();
    expect(scrollTo).toHaveBeenCalled();
    await user.keyboard("{End}");
    expect(screen.getByRole("button", { name: "M, 300 titles" })).toHaveFocus();

    // Typing an empty letter goes on to the next letter with titles, and says so.
    await user.keyboard("j");
    expect(screen.getByRole("button", { name: "M, 300 titles" })).toHaveFocus();
    expect(rail.querySelector('[role="status"]')).toHaveTextContent(
      "No titles start with J. Showing M.",
    );
    await user.keyboard("i");
    expect(letterI).toHaveFocus();
    expect(rail.querySelector('[role="status"]')).toHaveTextContent("Showing I.");
    expect(document.activeElement).toBe(letterI);

    // A click also lands focus on the letter's first title.
    await user.click(letterI);
    await waitFor(() => expect(screen.getByRole("button", { name: "Item 5" })).toHaveFocus());
    // Clicking an empty letter does nothing.
    scrollTo.mockClear();
    await user.click(letterB);
    expect(scrollTo).not.toHaveBeenCalled();
    scrollTo.mockRestore();

    await user.click(screen.getByRole("button", { name: "List" }));
    expect(screen.getByRole("navigation", { name: "Jump to letter" })).toBeInTheDocument();
  });

  it("swaps the rail for a Jump to sheet where the rail is too cramped (CORE-1685)", async () => {
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => pageFor(request));
    const scrollTo = vi.spyOn(window, "scrollTo").mockImplementation(() => {});
    const matchMedia = window.matchMedia;
    window.matchMedia = ((query: string) => ({
      matches: true,
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    })) as typeof window.matchMedia;
    const user = userEvent.setup();
    try {
      renderBrowse("/n/films/all");

      const trigger = await screen.findByRole("button", { name: "Jump to letter" });
      expect(screen.queryByRole("navigation", { name: "Jump to letter" })).toBeNull();
      await user.click(trigger);
      const sheet = await screen.findByRole("dialog", { name: "Jump to letter" });
      expect(sheet).toBeInTheDocument();
      const empty = screen.getByRole("button", { name: "B, no titles" });
      expect(empty).toHaveAttribute("aria-disabled", "true");
      expect(empty).toHaveAttribute("tabindex", "-1");
      await user.click(screen.getByRole("button", { name: "I, 895 titles" }));
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
      await waitFor(() => expect(screen.getByRole("button", { name: "Item 5" })).toHaveFocus());
    } finally {
      window.matchMedia = matchMedia;
      scrollTo.mockRestore();
    }
  });

  it("uses the sheet where the viewport is too short for the rail, and returns focus on cancel (CORE-1685)", async () => {
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => pageFor(request));
    const innerHeight = window.innerHeight;
    window.innerHeight = 400;
    const user = userEvent.setup();
    try {
      renderBrowse("/n/films/all");

      const trigger = await screen.findByRole("button", { name: "Jump to letter" });
      await user.click(trigger);
      await screen.findByRole("dialog", { name: "Jump to letter" });
      await user.keyboard("{Escape}");
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
      await waitFor(() => expect(trigger).toHaveFocus());
    } finally {
      window.innerHeight = innerHeight;
    }
  });

  it("derives a title's rail initial as the server does (CORE-1685)", () => {
    expect(letterRailInitial("apple")).toBe("A");
    expect(letterRailInitial("été")).toBe("É");
    expect(letterRailInitial("3 days")).toBe("#");
    expect(letterRailInitial("~tilde")).toBe("#");
    expect(letterRailInitial("")).toBe("#");
  });

  it("offers no letter rail on a short list (CORE-1685)", async () => {
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => ({
      ...pageFor(request),
      total: 40,
      items: pageFor(request).items.slice(0, 40),
      letters: [{ letter: "I", offset: 0, count: 40 }],
    }));
    renderBrowse("/n/films/all");

    expect(await screen.findByText("40 items")).toBeInTheDocument();
    expect(screen.queryByRole("navigation", { name: "Jump to letter" })).toBeNull();
  });

  it("offers no letter rail on a sort other than title", async () => {
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => pageFor(request));

    render(
      <I18nProvider>
        <MemoryRouter initialEntries={["/n/films/all?sort=year"]}>
          <Routes>
            <Route path="/n/:nodeId/all" element={<LibraryBrowsePage />} />
          </Routes>
        </MemoryRouter>
      </I18nProvider>,
    );

    expect(await screen.findByText("1,200 items")).toBeInTheDocument();
    expect(screen.queryByRole("navigation", { name: "Jump to letter" })).toBeNull();
  });
});
