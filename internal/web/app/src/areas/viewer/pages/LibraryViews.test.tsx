import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { fetchLibraryItems } from "../lib/libraryBrowse";
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
    expect(within(grid).getByText("Maker A")).toBeInTheDocument();
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

    // The first read already holds the opening grid; the Media view is not read.
    expect(fetchLibraryEntities).toHaveBeenCalledTimes(1);
    expect(vi.mocked(fetchLibraryEntities).mock.calls[0][0]).toEqual({
      libraryID: "lib",
      kind: undefined,
      limit: 100,
      offset: 0,
    });
    expect(fetchLibraryItems).not.toHaveBeenCalled();
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

    await user.click(screen.getByRole("button", { name: "Sort direction: Descending" }));
    expect(screen.getByTestId("where")).toHaveTextContent("/n/tunes/all?sort=year&view=media");

    await user.click(screen.getByRole("button", { name: "Makers" }));
    expect(await screen.findByRole("list", { name: "Makers" })).toBeInTheDocument();
  });

  it("opens on the Media view, with Folders beside it, when the Library has no browse kinds", async () => {
    vi.mocked(fetchLibraryEntities).mockResolvedValue({ kinds: [], kind: "", total: 0, items: [] });
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
    expect(within(switcher).getAllByRole("button").map((chip) => chip.textContent)).toEqual([
      "All items",
      "Folders",
    ]);
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
