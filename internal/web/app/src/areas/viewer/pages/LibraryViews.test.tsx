import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { expectNoAxeViolations } from "@/test/axe";
import { notifyEngagementChanged } from "../actions/engagementSync";
import { DEFAULT_BROWSE_STATE, fetchLibraryItems } from "../lib/libraryBrowse";
import {
  fetchLibraryEntities,
  type LibraryEntitiesPage,
  type LibraryEntitiesRequest,
} from "../lib/libraryViews";
import { LibraryBrowsePage } from "./LibraryBrowsePage";

vi.mock("../lib/libraryViews", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/libraryViews")>();
  return { ...actual, fetchLibraryEntities: vi.fn() };
});
vi.mock("../lib/libraryBrowse", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/libraryBrowse")>();
  return { ...actual, fetchLibraryItems: vi.fn() };
});
vi.mock("@/lib/graphql", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/graphql")>();
  return {
    ...actual,
    fetchClientPrefs: vi.fn(async () => []),
    setClientPref: vi.fn(async () => undefined),
  };
});
vi.mock("@/lib/session", () => ({
  useSession: () => ({
    loading: false,
    manifest: {
      navigation: [
        { id: "tunes", view: "layout", layoutID: "l", libraryID: "lib", title: { en: "Tunes" } },
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

// Kinds a plugin browses the Library by, in the server's order (parents first).
const KINDS = [
  { kind: "maker", label: { en: "Makers" }, archetype: "person", total: 2 },
  { kind: "record", label: { en: "Records" }, archetype: "container", total: 1 },
];

function entitiesPage(request: LibraryEntitiesRequest): LibraryEntitiesPage {
  const kind = request.kind ?? "maker";
  const items =
    kind === "maker"
      ? [
          {
            id: "m1",
            kind: "entity",
            title: "Maker A",
            mediaClass: "audio",
            childCount: 1,
            mediaCount: 3,
            parents: [],
          },
          {
            id: "m2",
            kind: "entity",
            title: "Maker B",
            mediaClass: "audio",
            childCount: 0,
            mediaCount: 1,
            parents: [],
          },
        ]
      : [
          {
            id: "r1",
            kind: "entity",
            title: "Record One",
            mediaClass: "audio",
            childCount: 0,
            mediaCount: 3,
            parents: [{ id: "m1", title: "Maker A" }],
          },
        ];
  return {
    kinds: KINDS,
    allLabel: { en: "Library" },
    kind,
    total: items.length,
    items: items.slice(request.offset, request.offset + request.limit),
    letters: [],
  };
}

function Where() {
  const location = useLocation();
  return <output data-testid="where">{location.pathname + location.search}</output>;
}

function renderAt(path: string) {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/n/:nodeId/all" element={<LibraryBrowsePage />} />
        </Routes>
        <Where />
      </MemoryRouter>
    </I18nProvider>,
  );
}

beforeEach(() => {
  vi.mocked(fetchLibraryEntities).mockImplementation(async (request) => entitiesPage(request));
  vi.mocked(fetchLibraryItems).mockImplementation(async () => ({
    total: 4,
    items: [{ id: "t1", kind: "media", title: "Track One", mediaClass: "audio" }],
    facets: { groupings: [], decades: [], qualities: [] },
    letters: [],
  }));
});

afterEach(() => {
  cleanup();
  vi.mocked(fetchLibraryEntities).mockReset();
  vi.mocked(fetchLibraryItems).mockReset();
  window.localStorage.clear();
});

describe("Library views (CORE-2362)", () => {
  it("opens on the first browse kind with a switcher for every kind, the Media and Folders views", async () => {
    renderAt("/n/tunes/all");

    const grid = await screen.findByRole("list", { name: "Makers" });
    expect(await within(grid).findByText("Maker A")).toBeInTheDocument();
    expect(within(grid).getByText("3 items")).toBeInTheDocument();
    expect(within(grid).getByText("1 item")).toBeInTheDocument();
    expect(screen.getByText("2 items")).toHaveAttribute("role", "status");

    const switcher = screen.getByRole("group", { name: "Browse by" });
    const chips = within(switcher).getAllByRole("button");
    expect(chips.map((chip) => chip.textContent)).toEqual([
      "Makers",
      "Records",
      "Library",
      "Folders",
    ]);
    expect(chips[0]).toHaveAttribute("aria-pressed", "true");
    expect(chips[1]).toHaveAttribute("aria-pressed", "false");

    // The first read already holds the opening grid, read under the URL's
    // browse state; the Media view is read once, only for the filter values.
    expect(fetchLibraryEntities).toHaveBeenCalledTimes(1);
    expect(vi.mocked(fetchLibraryEntities).mock.calls[0][0]).toEqual({
      libraryID: "lib",
      kind: undefined,
      state: DEFAULT_BROWSE_STATE,
      limit: 100,
      offset: 0,
    });
    await waitFor(() => expect(fetchLibraryItems).toHaveBeenCalledTimes(1));
    expect(vi.mocked(fetchLibraryItems).mock.calls[0][0]).toMatchObject({
      state: DEFAULT_BROWSE_STATE,
      offset: 0,
    });
  });

  it("switches to another kind by keyboard and keeps the choice in the URL", async () => {
    const user = userEvent.setup();
    renderAt("/n/tunes/all");
    await screen.findByRole("list", { name: "Makers" });

    screen.getByRole("button", { name: "Records" }).focus();
    await user.keyboard("{Enter}");

    const grid = await screen.findByRole("list", { name: "Records" });
    // A card under a parent names it.
    expect(within(grid).getByText("Maker A")).toBeInTheDocument();
    expect(screen.getByTestId("where")).toHaveTextContent("/n/tunes/all?view=record");
    expect(screen.getByRole("button", { name: "Records" })).toHaveAttribute("aria-pressed", "true");
    expect(vi.mocked(fetchLibraryEntities).mock.calls.at(-1)?.[0]).toMatchObject({
      kind: "record",
      offset: 0,
    });
  });

  it("opens the kind a shared link names", async () => {
    renderAt("/n/tunes/all?view=record");

    expect(await screen.findByRole("list", { name: "Records" })).toBeInTheDocument();
    expect(vi.mocked(fetchLibraryEntities).mock.calls[0][0]).toMatchObject({ kind: "record" });
  });

  it("shows the Media view with the switcher, and keeps the view across browse changes", async () => {
    const user = userEvent.setup();
    renderAt("/n/tunes/all?view=media&sort=year&dir=desc");

    expect(await screen.findByText("4 items")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Library" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: "Shuffle" })).toBeInTheDocument();
    // Only the kinds are read for the Media view.
    expect(vi.mocked(fetchLibraryEntities).mock.calls[0][0]).toMatchObject({ limit: 1 });

    await user.click(screen.getByRole("button", { name: "Sort by Year, Descending" }));
    await user.click(await screen.findByRole("menuitemradio", { name: "Ascending" }));
    expect(screen.getByTestId("where")).toHaveTextContent("/n/tunes/all?sort=year&view=media");

    await user.click(screen.getByRole("button", { name: "Makers" }));
    expect(await screen.findByRole("list", { name: "Makers" })).toBeInTheDocument();
  });

  it("opens on the Media view, with Folders beside it, when the Library has no browse kinds", async () => {
    vi.mocked(fetchLibraryEntities).mockResolvedValue({
      kinds: [],
      kind: "",
      total: 0,
      items: [],
      letters: [],
    });
    renderAt("/n/tunes/all");

    expect(await screen.findByText("4 items")).toBeInTheDocument();
    const switcher = await screen.findByRole("group", { name: "Browse by" });
    const chips = within(switcher).getAllByRole("button");
    expect(chips.map((chip) => chip.textContent)).toEqual(["All items", "Folders"]);
    expect(chips[0]).toHaveAttribute("aria-pressed", "true");
  });

  it("falls back to the Media view, with Folders beside it, when the kinds cannot be read", async () => {
    vi.mocked(fetchLibraryEntities).mockRejectedValue(new Error("offline"));
    renderAt("/n/tunes/all");

    expect(await screen.findByText("4 items")).toBeInTheDocument();
    const switcher = await screen.findByRole("group", { name: "Browse by" });
    expect(
      within(switcher)
        .getAllByRole("button")
        .map((chip) => chip.textContent),
    ).toEqual(["All items", "Folders"]);
  });

  it("offers a retry when a kind's grid cannot be read", async () => {
    const user = userEvent.setup();
    renderAt("/n/tunes/all");
    await screen.findByRole("list", { name: "Makers" });

    vi.mocked(fetchLibraryEntities).mockRejectedValueOnce(new Error("offline"));
    await user.click(screen.getByRole("button", { name: "Records" }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Could not load this library.");

    await user.click(within(alert).getByRole("button"));
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
    expect(
      within(screen.getByRole("list", { name: "Records" })).getByText("Record One"),
    ).toBeVisible();
  });
});

// A kind long enough for the letter rail: "#" holds 5 titles, then 70 under
// "A" and 45 under "C".
function longKindPage(request: LibraryEntitiesRequest): LibraryEntitiesPage {
  const titles = Array.from({ length: 120 }, (_, i) =>
    i < 5 ? `${i} Count` : i < 75 ? `A Maker ${i}` : `C Maker ${i}`,
  );
  return {
    kinds: KINDS,
    allLabel: { en: "Library" },
    kind: request.kind ?? "maker",
    total: titles.length,
    items: titles.slice(request.offset, request.offset + request.limit).map((title, i) => ({
      id: `m${request.offset + i}`,
      kind: "entity",
      title,
      childCount: 0,
      mediaCount: 1,
      parents: [],
    })),
    letters:
      request.state?.sort === "title" || !request.state
        ? [
            { letter: "#", offset: 0, count: 5 },
            { letter: "A", offset: 5, count: 70 },
            { letter: "C", offset: 75, count: 45 },
          ]
        : [],
  };
}

describe("Entity kind sort, filters and letter rail (CORE-2440)", () => {
  it("sorts a kind from the Media view's toolbar and keeps the order in the URL", async () => {
    const user = userEvent.setup();
    renderAt("/n/tunes/all?view=maker");
    await screen.findByRole("list", { name: "Makers" });

    await user.click(screen.getByRole("button", { name: "Sort by Title, Ascending" }));
    await user.click(await screen.findByRole("menuitemradio", { name: "Descending" }));

    expect(screen.getByTestId("where")).toHaveTextContent("/n/tunes/all?dir=desc&view=maker");
    await waitFor(() =>
      expect(vi.mocked(fetchLibraryEntities).mock.calls.at(-1)?.[0]).toMatchObject({
        kind: "maker",
        state: { sort: "title", direction: "desc" },
        offset: 0,
      }),
    );
  });

  it("reads a shared link's filters into the first read, names them, and clears them", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchLibraryEntities).mockImplementation(async (request) =>
      request.state?.watch
        ? { ...entitiesPage(request), total: 0, items: [] }
        : entitiesPage(request),
    );
    renderAt("/n/tunes/all?view=maker&watch=unwatched&sort=year");

    expect(
      await screen.findByText("Nothing in this library matches these filters."),
    ).toBeInTheDocument();
    expect(vi.mocked(fetchLibraryEntities).mock.calls[0][0]).toMatchObject({
      kind: "maker",
      state: { sort: "year", watch: "unwatched" },
    });
    const chips = screen.getByRole("list", { name: "Active filters" });
    expect(within(chips).getByRole("button", { name: "Remove filter: Unwatched" })).toBeVisible();

    await user.click(within(chips).getByRole("button", { name: "Remove filter: Unwatched" }));
    expect(screen.getByTestId("where")).toHaveTextContent("/n/tunes/all?sort=year&view=maker");
    expect(
      await within(screen.getByRole("list", { name: "Makers" })).findByText("Maker A"),
    ).toBeVisible();
  });

  it("reads a plays-sorted kind afresh when watched state changes (CORE-2443)", async () => {
    renderAt("/n/tunes/all?view=maker&sort=plays");
    await within(await screen.findByRole("list", { name: "Makers" })).findByText("Maker A");
    const before = vi.mocked(fetchLibraryEntities).mock.calls.length;

    act(() => notifyEngagementChanged());
    await waitFor(() =>
      expect(vi.mocked(fetchLibraryEntities).mock.calls.length).toBeGreaterThan(before),
    );
    expect(vi.mocked(fetchLibraryEntities).mock.calls.at(-1)?.[0]).toMatchObject({
      kind: "maker",
      state: { sort: "plays" },
      offset: 0,
    });
  });

  it("reads an unfiltered kind again when Hide watched flips (CORE-2836)", async () => {
    const user = userEvent.setup();
    renderAt("/n/tunes/all?view=maker");
    await within(await screen.findByRole("list", { name: "Makers" })).findByText("Maker A");
    const toggle = screen.getByRole("button", { name: "Hide watched" });
    await waitFor(() => expect(toggle).toBeEnabled());
    const before = vi.mocked(fetchLibraryEntities).mock.calls.length;

    await user.click(toggle);
    await waitFor(() =>
      expect(vi.mocked(fetchLibraryEntities).mock.calls.length).toBeGreaterThan(before),
    );
    expect(vi.mocked(fetchLibraryEntities).mock.calls.at(-1)?.[0]).toMatchObject({
      kind: "maker",
      offset: 0,
    });
  });

  it("reads a filtered kind afresh when it opens again after a watched change (CORE-2443)", async () => {
    const user = userEvent.setup();
    let watched = false;
    vi.mocked(fetchLibraryEntities).mockImplementation(async (request) =>
      watched && request.state?.watch === "unwatched"
        ? { ...entitiesPage(request), total: 0, items: [] }
        : entitiesPage(request),
    );
    renderAt("/n/tunes/all?view=maker&watch=unwatched");
    await within(await screen.findByRole("list", { name: "Makers" })).findByText("Maker A");

    // Marking the kind's Media watched empties the unwatched filter.
    watched = true;
    act(() => notifyEngagementChanged());
    expect(
      await screen.findByText("Nothing in this library matches these filters."),
    ).toBeInTheDocument();

    // Leaving for the Media view and coming back must not repaint the
    // Library's opening read from before the change.
    await user.click(screen.getByRole("button", { name: "Library" }));
    await waitFor(() => expect(vi.mocked(fetchLibraryItems)).toHaveBeenCalled());
    const before = vi.mocked(fetchLibraryEntities).mock.calls.length;
    await user.click(screen.getByRole("button", { name: "Makers" }));
    expect(
      await screen.findByText("Nothing in this library matches these filters."),
    ).toBeInTheDocument();
    expect(screen.queryByText("Maker A")).toBeNull();
    expect(vi.mocked(fetchLibraryEntities).mock.calls.length).toBeGreaterThan(before);
  });

  it("carries the browse state across kinds and into the Media view", async () => {
    const user = userEvent.setup();
    renderAt("/n/tunes/all?view=maker&sort=added&dir=desc");
    await screen.findByRole("list", { name: "Makers" });

    await user.click(screen.getByRole("button", { name: "Records" }));
    await screen.findByRole("list", { name: "Records" });
    expect(vi.mocked(fetchLibraryEntities).mock.calls.at(-1)?.[0]).toMatchObject({
      kind: "record",
      state: { sort: "added", direction: "desc" },
    });

    await user.click(screen.getByRole("button", { name: "Library" }));
    await waitFor(() =>
      expect(vi.mocked(fetchLibraryItems).mock.calls.at(-1)?.[0]).toMatchObject({
        state: { sort: "added", direction: "desc" },
      }),
    );
  });

  it("offers the letter rail on a long title-sorted kind and jumps the grid", async () => {
    vi.mocked(fetchLibraryEntities).mockImplementation(async (request) => longKindPage(request));
    const scrollTo = vi.spyOn(window, "scrollTo").mockImplementation(() => {});
    const user = userEvent.setup();
    const { container } = renderAt("/n/tunes/all?view=maker");

    const rail = await screen.findByRole("navigation", { name: "Jump to letter" });
    expect(within(rail).getByRole("button", { name: "B, no titles" })).toHaveAttribute(
      "aria-disabled",
      "true",
    );
    scrollTo.mockClear();
    await user.click(within(rail).getByRole("button", { name: "C, 45 titles" }));
    expect(scrollTo).toHaveBeenCalled();
    await expectNoAxeViolations(container);
    scrollTo.mockRestore();
  });

  it("offers no letter rail on a sort other than title", async () => {
    vi.mocked(fetchLibraryEntities).mockImplementation(async (request) => longKindPage(request));
    renderAt("/n/tunes/all?view=maker&sort=year");

    await screen.findByRole("list", { name: "Makers" });
    expect(screen.queryByRole("navigation", { name: "Jump to letter" })).toBeNull();
  });
});
