import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchMutes } from "@/lib/graphql";
import type { SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { notifyEngagementChanged } from "../actions/engagementSync";
import { BROWSE_SEARCH_DEBOUNCE_MS } from "../components/LibraryBrowseToolbar";
import { letterRailOwner } from "../components/LibraryLetterRail";
import { fetchLibraryItems, type LibraryItemsRequest } from "../lib/libraryBrowse";
import { LibraryBrowsePage } from "./LibraryBrowsePage";

vi.mock("../lib/libraryBrowse", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/libraryBrowse")>();
  return { ...actual, fetchLibraryItems: vi.fn() };
});
vi.mock("@/lib/graphql", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/graphql")>();
  return {
    ...actual,
    fetchClientPrefs: vi.fn(async () => []),
    fetchMutes: vi.fn(async () => []),
    setClientPref: vi.fn(async () => undefined),
  };
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
  useViewerIdentity: () => "viewer-p",
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

// Opens the toolbar's Filters panel, unless it is already open, and returns it.
async function openFilters(user: ReturnType<typeof userEvent.setup>) {
  const button = screen.getByRole("button", { name: /^Filters/ });
  if (button.getAttribute("aria-expanded") !== "true") await user.click(button);
  return screen.findByRole("dialog", { name: "Filters" });
}

afterEach(() => {
  cleanup();
  vi.mocked(fetchLibraryItems).mockReset();
  window.localStorage.clear();
});

// Each case drives many user events; a loaded gate host can take well past the
// default 5s per case, so these get a wider budget (CORE-1884).
describe("Library browse", { timeout: 20_000 }, () => {
  it("carries the filtered Play all listing into the watch entry", async () => {
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => pageFor(request));
    function PlayAllEntry() {
      return <output data-testid="play-all-entry">{JSON.stringify(useLocation().state)}</output>;
    }
    render(
      <I18nProvider>
        <MemoryRouter initialEntries={["/n/films/all?sort=year&dir=desc"]}>
          <Routes>
            <Route path="/n/:nodeId/all" element={<LibraryBrowsePage />} />
            <Route path="/watch/*" element={<PlayAllEntry />} />
          </Routes>
        </MemoryRouter>
      </I18nProvider>,
    );
    await userEvent.click(await screen.findByRole("button", { name: "Play all" }));
    const entry = JSON.parse((await screen.findByTestId("play-all-entry")).textContent ?? "");
    expect(entry).toMatchObject({
      queueIdentity: "viewer-p",
      playAllSource: { libraryID: "lib", filters: "sort=year&dir=desc" },
    });
    expect(entry).not.toHaveProperty("queue");
  });

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
    const first = await screen.findByRole("link", { name: "Item 0" });
    expect(first.closest("li")).toHaveAttribute("aria-setsize", "1200");
    expect(document.querySelectorAll(".bf-card").length).toBeLessThan(TOTAL);
    expect(screen.getByRole("button", { name: "Play all" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Shuffle" })).toBeEnabled();

    await user.click(screen.getByRole("button", { name: "Sort by Year, Descending" }));
    await user.click(await screen.findByRole("menuitemradio", { name: "Ascending" }));
    expect(screen.getByTestId("where")).toHaveTextContent("/n/films/all?sort=year");
    expect(screen.getByRole("button", { name: "Sort by Year, Ascending" })).toBeInTheDocument();

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

  it("searches the whole library from the toolbar, debounced, and keeps it in the URL (CORE-2568)", async () => {
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => {
      const page = pageFor(request);
      if (!request.state.query) return page;
      const total = request.state.query === "nothing" ? 0 : 2;
      return { ...page, total, items: page.items.slice(0, total), letters: [] };
    });
    const user = userEvent.setup();
    renderBrowse("/n/films/all?watch=unwatched");

    expect(await screen.findByText("3 items")).toBeInTheDocument();
    const search = screen.getByRole("searchbox", { name: "Search Films" });
    await user.type(search, "  blue moon ");
    // The draft keeps what is typed; the URL and the read take it trimmed.
    expect(search).toHaveValue("  blue moon ");
    await waitFor(() =>
      expect(screen.getByTestId("where")).toHaveTextContent(
        /^\/n\/films\/all\?q=blue\+moon&watch=unwatched$/,
      ),
    );
    expect(await screen.findByText("2 items")).toBeInTheDocument();
    expect(vi.mocked(fetchLibraryItems).mock.calls.at(-1)?.[0].state).toMatchObject({
      query: "blue moon",
      watch: "unwatched",
    });
    // A search and a filter together offer Clear all, which clears both.
    await user.click(screen.getByRole("button", { name: "Clear all" }));
    await waitFor(() => expect(screen.getByTestId("where")).toHaveTextContent(/^\/n\/films\/all$/));
    expect(search).toHaveValue("");

    await user.type(search, "nothing{Enter}");
    expect(screen.getByTestId("where")).toHaveTextContent(/^\/n\/films\/all\?q=nothing$/);
    expect(
      await screen.findByText("Nothing in this library matches “nothing”."),
    ).toBeInTheDocument();
    await user.click(screen.getAllByRole("button", { name: "Clear search" }).at(-1) as HTMLElement);
    await waitFor(() => expect(screen.getByTestId("where")).toHaveTextContent(/^\/n\/films\/all$/));
    expect(search).toHaveValue("");
  });

  it("lets a Clear all clicked mid-typing stay cleared (CORE-2568)", async () => {
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => pageFor(request));
    const user = userEvent.setup();
    renderBrowse("/n/films/all?watch=unwatched&decade=1990");

    await screen.findByRole("link", { name: "Item 0" });
    const search = screen.getByRole("searchbox", { name: "Search Films" });
    await user.type(search, "moon");
    await user.click(screen.getByRole("button", { name: "Clear all" }));
    expect(screen.getByTestId("where")).toHaveTextContent(/^\/n\/films\/all$/);
    expect(search).toHaveValue("");
    // No draft is left pending to land after the debounce.
    await new Promise((resolve) => setTimeout(resolve, BROWSE_SEARCH_DEBOUNCE_MS + 100));
    expect(screen.getByTestId("where")).toHaveTextContent(/^\/n\/films\/all$/);
  });

  it("accepts up to 200 Unicode code points in the browse search (CORE-2582)", async () => {
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => pageFor(request));
    const user = userEvent.setup();
    renderBrowse("/n/films/all");

    await screen.findByRole("link", { name: "Item 0" });
    const search = screen.getByRole("searchbox", { name: "Search Films" });
    const character = "\u{20000}";
    await user.click(search);
    await user.paste(character.repeat(150));
    expect(search).toHaveValue(character.repeat(150));

    await user.paste(character.repeat(51));
    expect(search).toHaveValue(character.repeat(200));
    await user.keyboard("{Enter}");
    expect(vi.mocked(fetchLibraryItems).mock.calls.at(-1)?.[0].state.query).toBe(
      character.repeat(200),
    );
  });

  it.each([
    { name: "middle insertion", selected: 0, pasted: "y", kept: "", rest: "x" },
    {
      name: "selected-text replacement",
      selected: 1,
      pasted: "\u{20000}y",
      kept: "\u{20000}",
      rest: "",
    },
  ])(
    "preserves the search suffix after $name at capacity",
    async ({ selected, pasted, kept, rest }) => {
      vi.mocked(fetchLibraryItems).mockImplementation(async (request) => pageFor(request));
      const user = userEvent.setup();
      const prefix = "a".repeat(99);
      const suffix = "z".repeat(100);
      renderBrowse(`/n/films/all?q=${prefix}x${suffix}`);

      const search = await screen.findByRole<HTMLInputElement>("searchbox", {
        name: "Search Films",
      });
      await user.click(search);
      search.setSelectionRange(prefix.length, prefix.length + selected);
      await user.paste(pasted);
      expect(search).toHaveValue(`${prefix}${kept}${rest}${suffix}`);
      expect(search.selectionStart).toBe(prefix.length + kept.length);
    },
  );

  it("opens the filters from one button that counts them and returns focus on Escape (CORE-2568)", async () => {
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => pageFor(request));
    const user = userEvent.setup();
    renderBrowse("/n/films/all?decade=1990&quality=hd1080");

    await screen.findByRole("link", { name: "Item 0" });
    const button = screen.getByRole("button", { name: "Filters, 2 active" });
    expect(button).toHaveAttribute("aria-expanded", "false");
    const panel = await openFilters(user);
    expect(button).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByRole("combobox", { name: "Decade" })).toHaveTextContent("1990s");

    await user.click(within(panel).getByRole("button", { name: "Clear all" }));
    await waitFor(() => expect(screen.getByTestId("where")).toHaveTextContent(/^\/n\/films\/all$/));
    expect(screen.getByRole("button", { name: "Filters" })).toBeInTheDocument();
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Filters" })).toBeNull());
    expect(button).toHaveFocus();
  });

  it("drops the watching controls from an all-audio library (CORE-2327)", async () => {
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => {
      const page = pageFor(request);
      return {
        ...page,
        items: page.items.map((item) => ({ ...item, mediaClass: "audio" })),
      };
    });

    render(
      <I18nProvider>
        <MemoryRouter initialEntries={["/n/films/all"]}>
          <Routes>
            <Route path="/n/:nodeId/all" element={<LibraryBrowsePage />} />
          </Routes>
        </MemoryRouter>
      </I18nProvider>,
    );

    expect(await screen.findByText("1,200 items")).toBeInTheDocument();
    const user = userEvent.setup();
    await openFilters(user);
    await waitFor(() => expect(screen.queryByLabelText("Watched")).toBeNull());
    expect(screen.queryByLabelText("Resolution")).toBeNull();
    await user.keyboard("{Escape}");
    await user.click(screen.getByRole("button", { name: /^Sort by/ }));
    expect(await screen.findByRole("menuitemradio", { name: "Title" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitemradio", { name: "Last watched" })).toBeNull();
  });

  it("keeps the watching controls for a video library", async () => {
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => {
      const page = pageFor(request);
      return {
        ...page,
        items: page.items.map((item) => ({ ...item, mediaClass: "video" })),
      };
    });

    render(
      <I18nProvider>
        <MemoryRouter initialEntries={["/n/films/all"]}>
          <Routes>
            <Route path="/n/:nodeId/all" element={<LibraryBrowsePage />} />
          </Routes>
        </MemoryRouter>
      </I18nProvider>,
    );

    expect(await screen.findByText("1,200 items")).toBeInTheDocument();
    const user = userEvent.setup();
    await openFilters(user);
    expect(screen.getByLabelText("Watched")).toBeInTheDocument();
    expect(screen.getByLabelText("Resolution")).toBeInTheDocument();
    await user.keyboard("{Escape}");
    await user.click(screen.getByRole("button", { name: /^Sort by/ }));
    expect(await screen.findByRole("menuitemradio", { name: "Last watched" })).toBeInTheDocument();
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
    expect(screen.getByRole("button", { name: "Filters, 1 active" })).toBeInTheDocument();
    await openFilters(user);
    expect(screen.getByRole("combobox", { name: "Sector" })).toHaveTextContent("Outer");
    await user.keyboard("{Escape}");

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

    await user.click(await screen.findByRole("link", { name: "Item 1" }));
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

  it("starts an unfiltered read over when Hide watched flips, so the count follows (CORE-2836)", async () => {
    const user = userEvent.setup();
    let hiding = false;
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) =>
      hiding
        ? { ...pageFor(request), total: 3, items: pageFor(request).items.slice(0, 3) }
        : pageFor(request),
    );
    render(
      <I18nProvider>
        <MemoryRouter initialEntries={["/n/films/all"]}>
          <Routes>
            <Route path="/n/:nodeId/all" element={<LibraryBrowsePage />} />
          </Routes>
        </MemoryRouter>
      </I18nProvider>,
    );
    expect(await screen.findByText("1,200 items")).toBeInTheDocument();
    const toggle = screen.getByRole("button", { name: "Hide watched" });
    await waitFor(() => expect(toggle).toBeEnabled());

    hiding = true;
    await user.click(toggle);
    expect(await screen.findByText("3 items")).toBeInTheDocument();
  });

  it("starts the read over when the Profile's hidden items change, so the count follows (CORE-2836)", async () => {
    let hidden = false;
    vi.mocked(fetchMutes).mockImplementation(async () =>
      hidden ? ([{ kind: "media", targetID: "media-0" }] as never) : [],
    );
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) =>
      hidden ? { ...pageFor(request), total: TOTAL - 1 } : pageFor(request),
    );
    render(
      <I18nProvider>
        <MemoryRouter initialEntries={["/n/films/all"]}>
          <Routes>
            <Route path="/n/:nodeId/all" element={<LibraryBrowsePage />} />
          </Routes>
        </MemoryRouter>
      </I18nProvider>,
    );
    expect(await screen.findByText("1,200 items")).toBeInTheDocument();
    await waitFor(() => expect(fetchMutes).toHaveBeenCalled());

    hidden = true;
    act(() => notifyEngagementChanged());
    expect(await screen.findByText("1,199 items")).toBeInTheDocument();
    vi.mocked(fetchMutes).mockImplementation(async () => []);
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
    await waitFor(() => expect(screen.queryByRole("link", { name: "Item 0" })).toBeNull());
    await waitFor(() => expect(vi.mocked(fetchLibraryItems)).toHaveBeenCalled());
    matches = 1;
    act(() => notifyEngagementChanged());
    expect(await screen.findByRole("link", { name: "Item 0" })).toBeInTheDocument();
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => pageFor(request));

    // A query that does not depend on engagement keeps its pages and count, and
    // re-reads the loaded ones so each card's watched mark follows the change.
    cleanup();
    mount("/n/films/all?sort=year");
    expect(await screen.findByText("1,200 items")).toBeInTheDocument();
    await screen.findByRole("link", { name: "Item 0" });
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => {
      const page = pageFor(request);
      return {
        ...page,
        items: page.items.map((item) => ({ ...item, engagement: { completed: true } })),
      };
    });
    act(() => notifyEngagementChanged());
    expect(await screen.findByRole("link", { name: "Item 0, Finished" })).toBeInTheDocument();
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

  it("offers poster, thumbnail and list layouts and remembers the choice per library (CORE-2386)", async () => {
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => pageFor(request));
    // A choice stored before CORE-2386 ("grid") opens as posters.
    window.localStorage.setItem("beamfall.libraryView.lib", "grid");
    const user = userEvent.setup();
    renderBrowse("/n/films/all");

    await screen.findByRole("link", { name: "Item 0" });
    const group = screen.getByRole("group", { name: "View" });
    expect(group).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Posters" })).toHaveAttribute("aria-pressed", "true");
    const list = screen.getByRole("list", { name: "Films" });
    expect(list).toHaveClass("bf-grid");
    expect(list).not.toHaveClass("bf-grid-wide");

    await user.click(screen.getByRole("button", { name: "Thumbnails" }));
    expect(screen.getByRole("list", { name: "Films" })).toHaveClass("bf-grid", "bf-grid-wide");
    expect(document.querySelector(".bf-poster-wide")).not.toBeNull();
    expect(screen.getByRole("button", { name: "Thumbnails" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(window.localStorage.getItem("beamfall.libraryView.lib")).toBe("thumbnail");

    // A fresh visit to the same library opens on the remembered layout.
    cleanup();
    renderBrowse("/n/films/all");
    await screen.findByRole("link", { name: "Item 0" });
    expect(screen.getByRole("list", { name: "Films" })).toHaveClass("bf-grid-wide");
    expect(screen.getByRole("button", { name: "Thumbnails" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
  });

  it("reads a year range from the URL, names it in a chip, and clears it (CORE-2443)", async () => {
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => pageFor(request));
    const user = userEvent.setup();
    renderBrowse("/n/films/all?from=1990&to=1999&sort=plays");

    await screen.findByRole("link", { name: "Item 0" });
    expect(vi.mocked(fetchLibraryItems).mock.calls[0][0].state).toMatchObject({
      sort: "plays",
      yearFrom: 1990,
      yearTo: 1999,
    });
    await openFilters(user);
    expect(screen.getByLabelText("From year")).toHaveValue(1990);
    await user.keyboard("{Escape}");
    await user.click(screen.getByRole("button", { name: "Remove filter: 1990–1999" }));
    await waitFor(() =>
      expect(screen.getByTestId("where")).toHaveTextContent(/^\/n\/films\/all\?sort=plays$/),
    );
    await openFilters(user);
    expect(screen.getByLabelText("From year")).toHaveValue(null);
  });

  it("commits a typed year range in order, replacing the decade (CORE-2443)", async () => {
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => pageFor(request));
    const user = userEvent.setup();
    renderBrowse("/n/films/all?decade=1990");

    await screen.findByRole("link", { name: "Item 0" });
    expect(screen.getByRole("button", { name: "Remove filter: 1990s" })).toBeInTheDocument();
    // Typing alone changes nothing; a year out of range reverts on commit.
    await openFilters(user);
    await user.type(screen.getByLabelText("To year"), "19{Enter}");
    expect(screen.getByTestId("where")).toHaveTextContent("/n/films/all?decade=1990");
    expect(screen.getByLabelText("To year")).toHaveValue(null);

    await user.type(screen.getByLabelText("To year"), "1985{Enter}");
    await waitFor(() =>
      expect(screen.getByTestId("where")).toHaveTextContent("/n/films/all?to=1985"),
    );
    await user.type(screen.getByLabelText("From year"), "2001");
    await user.tab();
    await waitFor(() =>
      expect(screen.getByTestId("where")).toHaveTextContent("/n/films/all?from=1985&to=2001"),
    );
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("button", { name: "Remove filter: 1990s" })).toBeNull();
    expect(screen.getByRole("button", { name: "Remove filter: 1985–2001" })).toBeInTheDocument();
  });

  it("keeps a cleared year cleared when its field loses focus again (CORE-2443)", async () => {
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => pageFor(request));
    const user = userEvent.setup();
    renderBrowse("/n/films/all");

    await screen.findByRole("link", { name: "Item 0" });
    await openFilters(user);
    await user.type(screen.getByLabelText("From year"), "1990");
    await user.tab();
    await waitFor(() =>
      expect(screen.getByTestId("where")).toHaveTextContent(/^\/n\/films\/all\?from=1990$/),
    );
    await user.keyboard("{Escape}");
    await user.click(screen.getByRole("button", { name: "Remove filter: From 1990" }));
    await waitFor(() => expect(screen.getByTestId("where")).toHaveTextContent(/^\/n\/films\/all$/));
    await openFilters(user);
    expect(screen.getByLabelText("From year")).toHaveValue(null);

    await user.click(screen.getByLabelText("From year"));
    await user.tab();
    expect(screen.getByLabelText("From year")).toHaveValue(null);
    expect(screen.getByTestId("where")).toHaveTextContent(/^\/n\/films\/all$/);
  });

  it("shows the swapped range after a commit leaves this side's year unchanged (CORE-2443)", async () => {
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => pageFor(request));
    const user = userEvent.setup();
    renderBrowse("/n/films/all?from=1990&to=1990");

    await screen.findByRole("link", { name: "Item 0" });
    await openFilters(user);
    await user.clear(screen.getByLabelText("From year"));
    await user.type(screen.getByLabelText("From year"), "2000");
    await user.tab();
    await waitFor(() =>
      expect(screen.getByTestId("where")).toHaveTextContent(/^\/n\/films\/all\?from=1990&to=2000$/),
    );
    expect(screen.getByLabelText("From year")).toHaveValue(1990);
    expect(screen.getByLabelText("To year")).toHaveValue(2000);

    await user.click(screen.getByLabelText("From year"));
    await user.tab();
    expect(screen.getByTestId("where")).toHaveTextContent(/^\/n\/films\/all\?from=1990&to=2000$/);
  });

  it("draws the table view as dense rows without artwork, with year, runtime and score (CORE-2443)", async () => {
    vi.mocked(fetchLibraryItems).mockImplementation(async (request) => {
      const page = pageFor(request);
      return {
        ...page,
        items: page.items.map((item) => ({
          ...item,
          year: 1994,
          durationSeconds: 5400,
          audienceScore: 8.2,
        })),
      };
    });
    const user = userEvent.setup();
    renderBrowse("/n/films/all");

    await screen.findByRole("link", { name: /^Item 0/ });
    await user.click(screen.getByRole("button", { name: "Table" }));
    const list = screen.getByRole("list", { name: "Films" });
    expect(list).toHaveClass("bf-stack", "bf-table-rows");
    expect(list.querySelector(".bf-poster")).toBeNull();
    const row = screen.getByRole("link", { name: /^Item 0/ });
    expect(row).toHaveTextContent("1994");
    expect(row.querySelector(".bf-card-runtime")).not.toBeNull();
    expect(row).toHaveTextContent("8.2");
    expect(window.localStorage.getItem("beamfall.libraryView.lib")).toBe("table");
  });

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
    await waitFor(() => expect(screen.getByRole("link", { name: "Item 5" })).toHaveFocus());
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
      await waitFor(() => expect(screen.getByRole("link", { name: "Item 5" })).toHaveFocus());
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

  it("finds the rail letter that holds a title, under the server's rune casing (CORE-1685)", () => {
    const letters = [
      { letter: "#", offset: 0, count: 2 },
      { letter: "A", offset: 2, count: 1 },
      { letter: "ß", offset: 3, count: 1 },
      { letter: "ẞ", offset: 4, count: 1 },
      { letter: "ᾈ", offset: 5, count: 1 },
      { letter: "I", offset: 6, count: 2 },
    ];
    const owner = (title: string, index: number) => letterRailOwner(letters, title, index)?.letter;
    expect(owner("3 days", 0)).toBe("#");
    expect(owner("~tilde", 1)).toBe("#");
    expect(owner("apple", 2)).toBe("A");
    // Go keeps "ß" and "ẞ" apart and maps "ᾀ" to "ᾈ"; JavaScript's full
    // upper case would give "SS" and "ἈΙ".
    expect(owner("ßalpha", 3)).toBe("ß");
    expect(owner("ẞbeta", 4)).toBe("ẞ");
    expect(owner("ᾀgamma", 5)).toBe("ᾈ");
    // Go upper-cases the dotless "ı" to "I", whose lower case is "i".
    expect(owner("India", 6)).toBe("I");
    expect(owner("ıdelta", 7)).toBe("I");
    // A letter that starts below the index does not hold it yet.
    expect(owner("apple", 1)).toBeUndefined();
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
