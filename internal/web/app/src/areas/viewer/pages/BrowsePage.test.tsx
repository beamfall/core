import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { readPosterSize } from "@/components/chrome/posterSize";
import type { Layout, NavigationManifest, Section } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import type { HomeLibrarySeeAllState, SeeAllContext } from "../lib/seeall";
import { BrowsePage } from "./BrowsePage";

// The manifest the server really sends: Home, the layout-less "ambient" entry,
// then one node per Library.
const manifest = {
  libraries: [{ id: "lib-a", title: "Space Archive", pluginID: null, previewClips: false }],
  navigation: [
    { id: "home", title: "Home", view: "layout", layoutID: "layout-home" },
    { id: "ambient", title: "Ambient", view: "ambient", layoutID: "" },
    {
      id: "library-lib-a",
      title: "Space Archive",
      view: "layout",
      layoutID: "layout-home",
      libraryID: "lib-a",
    },
  ],
  layouts: [{ id: "layout-home", sections: [] }],
} as unknown as NavigationManifest;

const mockSession = vi.fn();
vi.mock("@/lib/session", () => ({
  useSession: () => mockSession(),
}));
vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
}));
vi.mock("../components/LayoutView", () => ({
  LayoutView: ({
    layout,
    navigationID,
    libraryID,
    home,
    buildSeeAll,
    onEmptyChange,
  }: {
    layout: Layout | null;
    navigationID: string;
    libraryID: string;
    home?: boolean;
    buildSeeAll?: (
      section: Section,
      homeShelfLibraryID?: string,
    ) => { state: SeeAllContext | HomeLibrarySeeAllState } | undefined;
    onEmptyChange?: (empty: boolean) => void;
  }) => {
    const shelf: Section = {
      id: "recent",
      kind: "shelf",
      title: "Recently Added",
      artworkSlot: "poster",
      source: { kind: "recently-added" },
    };
    const state = buildSeeAll?.(shelf)?.state;
    return (
      <div
        data-testid="layout"
        data-navigation={navigationID}
        data-library={libraryID}
        data-home={String(!!home)}
        data-seeall-home={String(!!(state && "home" in state && state.home))}
        data-seeall-library={state?.libraryID}
        data-seeall-scoped-state={JSON.stringify(buildSeeAll?.(shelf, "scoped-lib")?.state)}
      >
        {layout?.sections.map((section) => (
          <span key={section.id} data-testid={section.id}>
            {JSON.stringify(buildSeeAll?.(section)?.state)}
          </span>
        ))}
        <button type="button" onClick={() => onEmptyChange?.(true)}>
          settle empty
        </button>
      </div>
    );
  },
}));
const requestManifestRefresh = vi.hoisted(() => vi.fn());
vi.mock("@/lib/manifestRefresh", () => ({ requestManifestRefresh }));
vi.mock("../components/HomeEditor", () => ({
  HomeEditor: ({ onSaved, onClose }: { onSaved: () => void; onClose: () => void }) => (
    <button
      type="button"
      onClick={() => {
        onSaved();
        onClose();
      }}
    >
      home editor
    </button>
  ),
}));
vi.mock("../components/RevealControls", () => ({ RevealControls: () => null }));
vi.mock("../components/ViewerActions", () => ({ ViewerActions: () => null }));

function Path() {
  return <span data-testid="path">{useLocation().pathname}</span>;
}

function renderAt(path: string, session: object = { manifest, loading: false }) {
  mockSession.mockReturnValue(session);
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[path]}>
        <Path />
        <Routes>
          <Route path="/" element={<BrowsePage />} />
          <Route path="/n/:nodeId" element={<BrowsePage />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

afterEach(() => {
  cleanup();
  mockSession.mockReset();
});

describe("BrowsePage node resolution", () => {
  it("offers Edit home on Home only and swaps the layout for the editor (CORE-1972)", () => {
    const refresh = vi.fn();
    renderAt("/", { manifest, loading: false, refresh });
    fireEvent.click(screen.getByRole("button", { name: "Edit home" }));
    expect(screen.queryByTestId("layout")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "home editor" }));
    expect(screen.getByTestId("layout")).toBeInTheDocument();
    // A save re-reads the manifest in place; a session reload would stop playback.
    expect(requestManifestRefresh).toHaveBeenCalledTimes(1);
    expect(refresh).not.toHaveBeenCalled();
    cleanup();

    renderAt("/n/library-lib-a");
    expect(screen.queryByRole("button", { name: "Edit home" })).toBeNull();
  });

  it("renders a library node under its own heading and scope", () => {
    renderAt("/n/library-lib-a");

    expect(screen.getByTestId("path")).toHaveTextContent("/n/library-lib-a");
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Space Archive");
    expect(screen.getByTestId("layout")).toHaveAttribute("data-library", "lib-a");
  });

  // Home hydrates aggregate Sections without the Libraries a manager hid from
  // it; a Library's own page shows that Library whatever its Home setting
  // (CORE-2360).
  it("asks for Home rows on Home only, and carries the flag into See All", () => {
    renderAt("/");
    expect(screen.getByTestId("layout")).toHaveAttribute("data-home", "true");
    expect(screen.getByTestId("layout")).toHaveAttribute("data-seeall-home", "true");
    expect(screen.getByTestId("layout")).toHaveAttribute("data-seeall-library", "");
    expect(screen.getByTestId("layout")).toHaveAttribute(
      "data-seeall-scoped-state",
      JSON.stringify({ libraryID: "scoped-lib" }),
    );
    cleanup();

    renderAt("/n/library-lib-a");
    expect(screen.getByTestId("layout")).toHaveAttribute("data-home", "false");
    expect(screen.getByTestId("layout")).toHaveAttribute("data-seeall-home", "false");
    expect(screen.getByTestId("layout")).toHaveAttribute("data-seeall-library", "lib-a");
  });

  it("keeps the selected sources when the first layout belongs to a library", () => {
    const sections: Section[] = [
      {
        id: "continue",
        kind: "shelf",
        title: "Continue Watching",
        artworkSlot: "poster",
        source: { kind: "continue-watching" },
      },
      {
        id: "watchlist",
        kind: "shelf",
        title: "Watchlist",
        artworkSlot: "poster",
        source: { kind: "watchlist" },
      },
    ];
    renderAt("/", {
      loading: false,
      manifest: {
        ...manifest,
        navigation: [...manifest.navigation].reverse(),
        layouts: [{ id: "layout-home", sections }],
      },
    });

    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Space Archive");
    for (const section of sections) {
      expect(JSON.parse(screen.getByTestId(section.id).textContent ?? "")).toEqual({
        section,
        libraryID: "lib-a",
        entityID: "",
        returnTo: "/",
        home: false,
      });
    }
  });

  it("sends the layout-less ambient node to Home instead of drawing Home at its address", () => {
    renderAt("/n/ambient");

    expect(screen.getByTestId("path")).toHaveTextContent(/^\/$/);
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Home");
    expect(screen.getByTestId("layout")).toHaveAttribute("data-navigation", "home");
  });

  it("sends an unknown or no-longer-visible node to Home", () => {
    renderAt("/n/library-gone");

    expect(screen.getByTestId("path")).toHaveTextContent(/^\/$/);
  });

  it("stays put while there is no navigation to resolve against", () => {
    renderAt("/n/library-lib-a", { manifest: null, loading: false });

    expect(screen.getByTestId("path")).toHaveTextContent("/n/library-lib-a");
  });
});

// The server names a Library's node "library-" + the Library's ID, and real
// Library IDs already start "library-" (CORE-2322).
describe("BrowsePage readable node address", () => {
  const doubled = {
    ...manifest,
    navigation: [
      { id: "home", title: "Home", view: "layout", layoutID: "layout-home" },
      {
        id: "library-library-films",
        title: "Films",
        view: "layout",
        layoutID: "layout-home",
        libraryID: "library-films",
      },
    ],
  } as unknown as NavigationManifest;

  it("serves the node at its collapsed address with the Library pivots (CORE-2437)", () => {
    renderAt("/n/library-films", { manifest: doubled, loading: false });

    expect(screen.getByTestId("path")).toHaveTextContent("/n/library-films");
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Films");
    const pivots = within(screen.getByRole("navigation", { name: "Library views" }));
    const hrefs = pivots.getAllByRole("link").map((link) => link.getAttribute("href"));
    expect(hrefs).toEqual([
      "/n/library-films",
      "/n/library-films/all",
      "/n/library-films/collections",
      "/n/library-films/categories",
    ]);
    expect(pivots.getByRole("link", { name: "Recommended" })).toHaveAttribute(
      "aria-current",
      "page",
    );
    expect(pivots.getByRole("link", { name: "Library" })).not.toHaveAttribute("aria-current");
  });

  it("drops the pivots once the Library's Layout settles empty (CORE-2322)", () => {
    renderAt("/n/library-films", { manifest: doubled, loading: false });

    fireEvent.click(screen.getByRole("button", { name: "settle empty" }));

    expect(screen.queryByRole("navigation", { name: "Library views" })).toBeNull();
    expect(screen.queryByRole("group", { name: "Poster size" })).toBeNull();
  });

  it("offers the poster size on the Recommended pivot, shared with the Library pivot (CORE-2893)", () => {
    localStorage.clear();
    renderAt("/n/library-films", { manifest: doubled, loading: false });

    const control = within(screen.getByRole("group", { name: "Poster size" }));
    expect(control.getByRole("button", { name: "Large posters" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    fireEvent.click(control.getByRole("button", { name: "Small posters" }));
    expect(control.getByRole("button", { name: "Small posters" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    // The same per-browser choice the Library pivot's toolbar reads.
    expect(readPosterSize()).toBe("small");
    localStorage.clear();
  });

  it("keeps the poster size off Home, which has no pivots (CORE-2893)", () => {
    renderAt("/", { manifest: doubled, loading: false });

    expect(screen.queryByRole("group", { name: "Poster size" })).toBeNull();
  });

  it("moves an old raw-ID link to the readable address", () => {
    renderAt("/n/library-library-films", { manifest: doubled, loading: false });

    expect(screen.getByTestId("path")).toHaveTextContent(/^\/n\/library-films$/);
    expect(screen.getByTestId("layout")).toHaveAttribute("data-library", "library-films");
  });

  it("keeps the address of a real Library whose ID repeats the prefix", () => {
    const current = {
      ...doubled,
      navigation: [
        doubled.navigation[0],
        { ...doubled.navigation[1], id: "library-films" },
        {
          ...doubled.navigation[1],
          title: "Library Films",
          libraryID: "library-library-films",
        },
      ],
    };
    renderAt("/n/library-library-films", { manifest: current, loading: false });

    expect(screen.getByTestId("path")).toHaveTextContent(/^\/n\/library-library-films$/);
    expect(screen.getByTestId("layout")).toHaveAttribute("data-library", "library-library-films");
    expect(screen.getByRole("link", { name: "Library" })).toHaveAttribute(
      "href",
      "/n/library-library-films/all",
    );
  });
});
