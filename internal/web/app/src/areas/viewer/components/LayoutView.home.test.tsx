import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { LibrarySidebar } from "@/components/chrome/LibrarySidebar";
import { ApiError } from "@/lib/api";
import type { Layout, LibrarySummary, SourceItem } from "@/lib/graphql/types";
import { fetchHomeLibraries } from "@/lib/graphql/viewer";
import { I18nProvider } from "@/lib/i18n";
import { clearViewerCache, viewerCacheEntries } from "@/lib/viewerCache";
import { notifyEngagementChanged } from "../actions/engagementSync";
import { fetchSectionItems, MAX_RENDERED_SECTIONS } from "../lib/sections";
import { BrowsePage } from "../pages/BrowsePage";
import { SeeAllPage } from "../pages/SeeAllPage";
import { LayoutView } from "./LayoutView";

const libraries = [
  { id: "one", title: "Library One" },
  { id: "two", title: "Library Two" },
  { id: "empty", title: "Empty Library" },
  { id: "hidden", title: "Hidden from Home", showOnHome: false },
  { id: "quiet", title: "Quiet Library", showOnHome: true },
];

vi.mock("@/lib/graphql/viewer", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/graphql/viewer")>()),
  fetchHomeLibraries: vi.fn(),
}));
vi.mock("@/lib/session", () => ({
  useViewerIdentity: () => identity,
  useSession: () => activeSession,
}));
vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: React.ReactNode }) => {
    const { pathname } = useLocation();
    const { navigation, libraries } = activeSession.manifest;
    return (
      <>
        <LibrarySidebar
          id="sidebar"
          navigation={navigation}
          libraries={libraries}
          activeNodeId={pathname.split("/").at(-1) ?? ""}
          collapsed={false}
          drawer={false}
          onNavigate={() => {}}
        />
        {children}
      </>
    );
  },
}));
vi.mock("../lib/sections", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/sections")>()),
  fetchSectionItems: vi.fn(),
}));

vi.mock("@/lib/viewerCache", async (importOriginal) => {
  const cache = await importOriginal<typeof import("@/lib/viewerCache")>();
  return { ...cache, clearViewerCache: vi.fn(cache.clearViewerCache) };
});

let identity = "viewer";
const session = {
  manifest: {
    libraries: libraries.map(
      (library): LibrarySummary => ({
        ...library,
        pluginID: null,
        previewClips: false,
      }),
    ),
    navigation: libraries
      .filter(({ id }) => id !== "quiet")
      .map(({ id, title }) => ({
        id: `library-${id}`,
        libraryID: id,
        title,
        view: "layout",
        layoutID: `layout-${id}`,
      })),
    layouts: [] as Layout[],
  },
  isManager: false,
};
let activeSession = session;
const layout: Layout = {
  id: "layout-home",
  sections: [
    {
      id: "continue",
      kind: "shelf",
      title: { en: "Continue Watching", fr: "Reprendre la lecture" },
      artworkSlot: "poster",
      source: { kind: "continue-watching" },
    },
    {
      id: "recent",
      kind: "shelf",
      title: { en: "Recently Added", fr: "Ajouts récents" },
      artworkSlot: "poster",
      source: { kind: "recently-added" },
    },
  ],
};
const newItem = (id: string, extra?: Partial<SourceItem>): SourceItem => ({
  id,
  title: id,
  kind: "media",
  ...extra,
});

const homeNode = { id: "home", libraryID: "", title: "Home", view: "layout", layoutID: layout.id };

function renderViewerRoutes(navigation: typeof session.manifest.navigation) {
  activeSession = {
    ...session,
    manifest: { ...session.manifest, navigation, layouts: [layout] },
  };
  return render(
    <I18nProvider initialLocale="en">
      <MemoryRouter basename="/web" initialEntries={["/web/"]}>
        <Routes>
          <Route path="/" element={<BrowsePage />} />
          <Route path="/n/:nodeId" element={<BrowsePage />} />
          <Route path="/seeall/:sourceId" element={<SeeAllPage />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

function renderHome(homeLayout = layout) {
  return render(
    <I18nProvider initialLocale="fr">
      <MemoryRouter>
        <LayoutView layout={homeLayout} libraryID="" home navigationID="home" />
      </MemoryRouter>
    </I18nProvider>,
  );
}

beforeEach(() => {
  identity = "viewer";
  activeSession = session;
  clearViewerCache();
  vi.mocked(clearViewerCache).mockClear();
  vi.mocked(fetchHomeLibraries).mockResolvedValue(libraries);
});

afterEach(() => {
  cleanup();
  vi.mocked(fetchHomeLibraries).mockReset();
  vi.mocked(fetchSectionItems).mockReset();
  vi.mocked(clearViewerCache).mockClear();
});

describe("Viewer Home", () => {
  it("keeps Home visibility, artwork and See All when a library is the landing node", async () => {
    const visible = newItem("Visible item", { artwork: { poster: "/visible" } });
    const hidden = newItem("Hidden item");
    vi.mocked(fetchSectionItems).mockImplementation(async (section, { libraryID, home }) => {
      if (section.id === "continue") return home ? [visible] : [visible, hidden];
      if (libraryID === "one") return [visible];
      if (libraryID === "hidden" || (!libraryID && !home)) return [hidden];
      return [];
    });
    const { container } = renderViewerRoutes([...session.manifest.navigation, homeNode]);

    expect(screen.getByRole("heading", { name: "Library One", level: 1 })).toBeVisible();
    expect(fetchHomeLibraries).not.toHaveBeenCalled();
    const homeLink = screen.getByRole("link", { name: "Home" });
    expect(homeLink).toHaveAttribute("href", "/web/n/home");
    fireEvent.click(homeLink);

    const shelf = await screen.findByRole("list", { name: "Library One" });
    expect(shelf.querySelector(".bf-poster-intrinsic img")).toBeVisible();
    expect(screen.getByRole("list", { name: "Continue Watching" })).toBeVisible();
    expect(screen.queryByRole("link", { name: hidden.title })).toBeNull();
    expect(fetchSectionItems).toHaveBeenCalledWith(
      layout.sections[0],
      expect.objectContaining({ libraryID: "", home: true }),
      expect.anything(),
    );
    // Continue Watching has an item, so it leads Home and no feature covers it.
    expect(container.querySelector(".bf-hero")).toBeNull();

    fireEvent.click(within(shelf.parentElement ?? shelf).getByRole("button", { name: "See all" }));
    expect(await screen.findByRole("heading", { name: "Library One" })).toBeVisible();
    expect(screen.getByRole("link", { name: visible.title })).toBeVisible();
    expect(screen.queryByRole("link", { name: hidden.title })).toBeNull();
    expect(fetchSectionItems).toHaveBeenCalledWith(
      layout.sections[1],
      expect.objectContaining({ libraryID: "one", home: true, paging: { limit: 60, offset: 0 } }),
      undefined,
    );

    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(screen.getByRole("heading", { name: "Home", level: 1 })).toBeInTheDocument();
    expect(await screen.findByRole("list", { name: "Library One" })).toBeVisible();
  });

  it("drops the whole Home snapshot when See All still shows a remaining library", async () => {
    const visible = libraries.slice(0, 2);
    vi.mocked(fetchHomeLibraries).mockResolvedValue(visible);
    vi.mocked(fetchSectionItems).mockImplementation(async (section, { libraryID }) =>
      section.id === "recent" ? [newItem(`Item in ${libraryID}`)] : [],
    );
    renderViewerRoutes([homeNode, ...session.manifest.navigation]);

    const shelf = await screen.findByRole("list", { name: "Library One" });
    expect(await screen.findByRole("list", { name: "Library Two" })).toBeVisible();
    expect(viewerCacheEntries(identity).map(([key]) => key)).toContain("home-recent");

    vi.mocked(fetchHomeLibraries).mockResolvedValue(visible.slice(0, 1));
    fireEvent.click(within(shelf.parentElement ?? shelf).getByRole("button", { name: "See all" }));

    expect(await screen.findByRole("heading", { name: "Library One" })).toBeVisible();
    expect(screen.getByRole("link", { name: "Item in one" })).toBeVisible();
    expect(viewerCacheEntries(identity).map(([key]) => key)).not.toContain("home-recent");
  });

  it.each(["Load more", "engagement refresh"] as const)(
    "drops the cached Home shelf when %s in See All observes a hidden library",
    async (trigger) => {
      const item = newItem("Cached item");
      vi.mocked(fetchSectionItems).mockImplementation(async (section, { libraryID, paging }) => {
        if (section.id !== "recent" || libraryID !== "one") return [];
        if (!paging) return [item];
        return [item, ...Array.from({ length: paging.limit - 1 }, (_, i) => newItem(`Item ${i}`))];
      });
      renderViewerRoutes([homeNode, ...session.manifest.navigation]);

      const shelf = await screen.findByRole("list", { name: "Library One" });
      expect(within(shelf).getByRole("link", { name: item.title })).toBeVisible();
      fireEvent.click(
        within(shelf.parentElement ?? shelf).getByRole("button", { name: "See all" }),
      );
      await screen.findByRole("button", { name: "Load more" });
      expect(screen.getByRole("link", { name: item.title })).toBeVisible();

      vi.mocked(fetchHomeLibraries).mockResolvedValue(
        libraries.map((library) =>
          library.id === "one" ? { ...library, showOnHome: false } : library,
        ),
      );
      if (trigger === "Load more") {
        fireEvent.click(screen.getByRole("button", { name: trigger }));
      } else {
        act(() => notifyEngagementChanged());
      }
      await screen.findByRole("status");
      expect(screen.queryByRole("link", { name: item.title })).toBeNull();

      vi.mocked(fetchHomeLibraries).mockRejectedValue(new Error("offline"));
      fireEvent.click(screen.getByRole("button", { name: "Back" }));
      expect(screen.getByRole("heading", { name: "Home", level: 1 })).toBeInTheDocument();
      expect(screen.queryByRole("list", { name: "Library One" })).toBeNull();
      expect(await screen.findByText(/connection.*lost/i)).toBeVisible();
      expect(screen.queryByRole("link", { name: item.title })).toBeNull();
    },
  );

  it("does not hydrate a Recent shelf beyond the layout's Section limit", () => {
    const [keepWatching, recent] = layout.sections;
    const sections = Array.from({ length: MAX_RENDERED_SECTIONS }, (_, index) => ({
      ...keepWatching,
      id: `section-${index}`,
    }));
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    vi.mocked(fetchSectionItems).mockReturnValue(new Promise(() => {}));

    try {
      renderHome({ ...layout, sections: [...sections, recent] });

      expect(fetchHomeLibraries).not.toHaveBeenCalled();
      expect(fetchSectionItems).toHaveBeenCalledTimes(MAX_RENDERED_SECTIONS);
      expect(warn).toHaveBeenCalledWith("Beamfall rendering diagnostic: SectionLimitExceeded");
    } finally {
      warn.mockRestore();
    }
  });

  it("pages populated libraries within the Section budget", async () => {
    const populated = Array.from({ length: 25 }, (_, i) => ({
      id: `library-${i + 1}`,
      title: `Library ${i + 1}`,
    }));
    vi.mocked(fetchHomeLibraries).mockResolvedValue([...libraries, ...populated]);
    vi.mocked(fetchSectionItems).mockImplementation(async (section, { libraryID }) => {
      if (section.id === "continue") return [newItem("In progress")];
      if (!libraryID.startsWith("library-")) return [];
      return [
        newItem(
          libraryID,
          libraryID === populated[0].id ? { artwork: { poster: "/featured" } } : undefined,
        ),
      ];
    });

    const { container } = renderHome();
    const next = await screen.findByRole("button", { name: "Bibliothèques suivantes" });
    await waitFor(() => expect(next).toHaveAttribute("aria-disabled", "false"));
    const previous = screen.getByRole("button", { name: "Bibliothèques précédentes" });
    expect(previous).toHaveAttribute("aria-disabled", "true");
    expect(screen.getByRole("list", { name: "Library 23" })).toBeVisible();
    expect(screen.queryByRole("list", { name: "Library 24" })).toBeNull();

    expect(container.querySelector(".bf-hero")).toBeNull();
    expect(container.querySelectorAll(".bf-section:not([hidden])")).toHaveLength(
      MAX_RENDERED_SECTIONS,
    );

    next.focus();
    fireEvent.click(next);
    expect(next).toHaveFocus();
    expect(next).toHaveAttribute("aria-disabled", "true");
    expect(screen.queryByRole("list", { name: "Library 1" })).toBeNull();
    expect(screen.getByRole("list", { name: "Library 24" })).toBeVisible();
    expect(screen.getByRole("list", { name: "Library 25" })).toBeVisible();
    expect(screen.getByRole("list", { name: "Reprendre la lecture" })).toBeVisible();
    expect(container.querySelectorAll(".bf-section:not([hidden])")).toHaveLength(3);

    fireEvent.click(previous);
    expect(screen.getByRole("list", { name: "Library 1" })).toBeVisible();
    expect(screen.queryByRole("list", { name: "Library 25" })).toBeNull();
  });

  it("shows a quiet library outside the aggregate window and excludes hidden and empty libraries", async () => {
    const one = newItem("Square artwork", { mediaClass: "audio", artwork: { poster: "/square" } });
    const two = newItem("Landscape artwork", { artworkSlots: { backdrop: "/landscape" } });
    const quiet = newItem("Quiet item");
    const hidden = newItem("Hidden artwork", { artwork: { poster: "/hidden" } });
    const aggregate = [
      ...Array.from({ length: 198 }, (_, index) => newItem(`busy-${index}`)),
      one,
      two,
    ];
    vi.mocked(fetchSectionItems).mockImplementation(async (section, context) => {
      if (section.id === "continue") return [newItem("In progress")];
      switch (context.libraryID) {
        case "one":
          return [one];
        case "two":
          return [two];
        case "quiet":
          return [quiet];
        case "hidden":
          // The scoped feed alone does not apply ShowOnHome on current Core.
          return [hidden];
        case "":
          return aggregate;
        default:
          return [];
      }
    });

    renderHome();

    const resume = await screen.findByRole("list", { name: "Reprendre la lecture" });
    expect(within(resume).getByRole("link", { name: "In progress" })).toBeVisible();
    const quietShelf = await screen.findByRole("list", { name: "Quiet Library" });
    expect(within(quietShelf).getByRole("link", { name: quiet.title })).toBeVisible();
    for (const [name, item] of [
      ["Library One", one],
      ["Library Two", two],
    ] as const) {
      const shelf = await screen.findByRole("list", { name });
      expect(within(shelf).getByRole("link", { name: item.title })).toBeVisible();
      expect(shelf.querySelector(".bf-poster-intrinsic img")).toBeVisible();
    }
    expect(screen.queryByRole("list", { name: /empty|hidden/i })).toBeNull();
    expect(screen.queryByRole("list", { name: "Ajouts récents" })).toBeNull();
    expect(vi.mocked(fetchSectionItems).mock.calls.every(([, context]) => context.home)).toBe(true);
    expect(screen.queryByRole("link", { name: hidden.title })).toBeNull();
    expect(
      vi
        .mocked(fetchSectionItems)
        .mock.calls.filter(([section]) => section.id === "recent")
        .map(([, context]) => context.libraryID),
    ).toEqual(["one", "two", "empty", "quiet"]);
  });

  it("keeps the server-filtered aggregate shelf when Core refuses the visibility query", async () => {
    vi.mocked(fetchHomeLibraries).mockResolvedValue(null);
    const available = newItem("Visible aggregate item", { artwork: { poster: "/visible" } });
    vi.mocked(fetchSectionItems).mockImplementation(async (section, context) =>
      section.id === "recent" && !context.libraryID ? [available] : [],
    );

    const { container, unmount } = renderHome();

    const shelf = await screen.findByRole("list", { name: "Ajouts récents" });
    expect(within(shelf).getByRole("link", { name: available.title })).toBeVisible();
    expect(container.querySelector(".bf-hero img")).toHaveAttribute("src", "/visible?bucket=w1280");
    expect(screen.queryByText(/connexion.*perdue/i)).toBeNull();
    expect(
      vi
        .mocked(fetchSectionItems)
        .mock.calls.filter(([section]) => section.id === "recent")
        .map(([, context]) => context),
    ).toEqual([{ libraryID: "", entityID: "", extraFilters: null, home: true }]);

    unmount();
    let resolveAggregate!: (items: SourceItem[]) => void;
    const pendingAggregate = new Promise<SourceItem[]>((resolve) => {
      resolveAggregate = resolve;
    });
    const loadAggregate = vi.fn(() => pendingAggregate);
    vi.mocked(fetchSectionItems).mockImplementation(async (section) =>
      section.id === "recent" ? loadAggregate() : [],
    );

    const returnedHome = renderHome();
    await waitFor(() => expect(loadAggregate).toHaveBeenCalledOnce());
    expect(screen.queryByRole("link", { name: available.title })).toBeNull();
    expect(returnedHome.container.querySelector(".bf-hero img")).toBeNull();
    await act(async () => resolveAggregate([]));
    expect(screen.queryByRole("list", { name: "Ajouts récents" })).toBeNull();
  });

  it("renders shelves in the order the server sends, even with Continue Watching after Recent (CORE-1972)", async () => {
    // One aggregate Recent shelf rather than a shelf per library.
    vi.mocked(fetchHomeLibraries).mockResolvedValue(null);
    const [keepWatching, recent] = layout.sections;
    vi.mocked(fetchSectionItems).mockImplementation(async (section) =>
      section.id === "continue"
        ? [newItem("Resume me")]
        : [newItem("Fresh item", { artworkSlots: { backdrop: "/fresh" } })],
    );

    const { container } = renderHome({ ...layout, sections: [recent, keepWatching] });
    await screen.findByRole("list", { name: "Reprendre la lecture" });
    await screen.findByRole("list", { name: "Ajouts récents" });
    expect(container.querySelector(".bf-hero")).toBeNull();
    // The server applies the default Continue-first order (CORE-2825) and the
    // Profile's saved order; the client never reorders.
    expect(screen.getAllByRole("list")[0]).toHaveAccessibleName("Ajouts récents");
  });

  it("holds the hero while a Continue shelf cached empty revalidates (CORE-2825)", async () => {
    // One aggregate Recent shelf rather than a shelf per library.
    vi.mocked(fetchHomeLibraries).mockResolvedValue(null);
    const feature = newItem("Featured", { artworkSlots: { backdrop: "/feature" } });
    vi.mocked(fetchSectionItems).mockImplementation(async (section) =>
      section.id === "continue" ? [] : [feature],
    );
    const first = renderHome();
    await screen.findByRole("list", { name: "Ajouts récents" });
    await waitFor(() => expect(first.container.querySelector(".bf-hero img")).not.toBeNull());
    first.unmount();

    let resolveContinue!: (items: SourceItem[]) => void;
    const pendingContinue = new Promise<SourceItem[]>((resolve) => {
      resolveContinue = resolve;
    });
    vi.mocked(fetchSectionItems).mockImplementation(async (section) =>
      section.id === "continue" ? pendingContinue : [feature],
    );
    const second = renderHome();
    await screen.findByRole("list", { name: "Ajouts récents" });
    expect(second.container.querySelector(".bf-hero")).toBeNull();

    await act(async () => resolveContinue([newItem("Resume me")]));
    await screen.findByRole("list", { name: "Reprendre la lecture" });
    expect(second.container.querySelector(".bf-hero")).toBeNull();
  });

  it("features artwork from a visible shelf when another shelf has none", async () => {
    const feature = newItem("Visible artwork", { artworkSlots: { backdrop: "/older" } });
    vi.mocked(fetchSectionItems).mockImplementation(async (section, context) => {
      if (section.id === "continue") return [];
      if (context.libraryID === "one") return [newItem("No artwork")];
      return context.libraryID === "two" ? [feature] : [];
    });

    const { container } = renderHome();
    const shelf = await screen.findByRole("list", { name: "Library Two" });
    expect(within(shelf).getByRole("link", { name: feature.title })).toBeVisible();
    const heroArt = container.querySelector(".bf-hero img");
    expect(heroArt).toHaveAttribute("src", "/older?bucket=w1280");
    fireEvent.load(heroArt as HTMLImageElement);
    expect(screen.getByRole("heading", { name: feature.title })).toBeVisible();
    expect(container.querySelector(".bf-hero-action")).toHaveFocus();
  });

  it("keeps the hero action focused when a higher-ranked shelf resolves later", async () => {
    let resolveFirst!: (items: SourceItem[]) => void;
    const first = new Promise<SourceItem[]>((resolve) => {
      resolveFirst = resolve;
    });
    vi.mocked(fetchSectionItems).mockImplementation(async (section, { libraryID }) => {
      if (section.id !== "recent") return [];
      if (libraryID === "one") return first;
      return libraryID === "two"
        ? [newItem("Second library hero", { artwork: { poster: "/second" } })]
        : [];
    });

    const { container } = renderHome();
    await screen.findByRole("list", { name: "Library Two" });
    const heroArt = container.querySelector(".bf-hero img") as HTMLImageElement;
    expect(heroArt).toHaveAttribute("src", "/second?bucket=w1280");
    fireEvent.load(heroArt);
    const action = container.querySelector(".bf-hero-action");
    expect(action).toHaveFocus();

    await act(async () =>
      resolveFirst([newItem("First library artwork", { artwork: { poster: "/first" } })]),
    );
    expect(screen.getByRole("list", { name: "Library One" })).toBeVisible();
    expect(container.querySelector(".bf-hero img")).toBe(heroArt);
    expect(screen.getByRole("heading", { name: "Second library hero" })).toBeVisible();
    expect(action).toHaveFocus();
  });

  it("features a visible container's poster when its new items have no art", async () => {
    const container = { id: "group", title: "Visible container", artwork: { poster: "/group" } };
    const lockedContainer = {
      id: "locked-group",
      title: "Locked container",
      artwork: { poster: "/locked-group" },
    };
    vi.mocked(fetchSectionItems).mockImplementation(async (section, context) =>
      section.id === "recent" && context.libraryID === "one"
        ? [
            newItem("Locked first", { container: lockedContainer, locked: true }),
            newItem("Locked second", { container: lockedContainer, locked: true }),
            newItem("First item", { container }),
            newItem("Second item", { container }),
          ]
        : [],
    );

    const home = renderHome();
    const shelf = await screen.findByRole("list", { name: "Library One" });
    expect(within(shelf).getByRole("link", { name: /visible container/i })).toBeVisible();
    const heroArt = home.container.querySelector(".bf-hero img");
    expect(heroArt).toHaveAttribute("src", "/group?bucket=w1280");
    fireEvent.load(heroArt as HTMLImageElement);
    expect(screen.getByRole("heading", { name: container.title })).toBeVisible();
  });

  it("focuses a shelf when the hero image never settles", async () => {
    vi.mocked(fetchSectionItems).mockImplementation(async (section, context) =>
      section.id === "recent" && context.libraryID === "one"
        ? [newItem("Available item", { artwork: { poster: "/stalled" } })]
        : [],
    );

    const { container } = renderHome();
    const card = await screen.findByRole("link", { name: "Available item" });
    const heroArt = container.querySelector(".bf-hero img");
    expect(heroArt).toHaveAttribute("src", "/stalled?bucket=w1280");
    expect(heroArt).not.toBeVisible();
    await waitFor(() => expect(card).toHaveFocus(), { timeout: 2000 });

    fireEvent.load(heroArt as HTMLImageElement);
    expect(screen.getByRole("heading", { name: "Available item" })).toBeVisible();
    expect(card).toHaveFocus();
  });

  it("advances past a stalled image and shows the next artwork without taking shelf focus", async () => {
    vi.useFakeTimers();
    vi.mocked(fetchSectionItems).mockImplementation(async (section, { libraryID }) => {
      if (section.id !== "recent") return [];
      if (libraryID === "one") return [newItem("Stalled", { artwork: { poster: "/stalled" } })];
      return libraryID === "two" ? [newItem("Working", { artwork: { poster: "/working" } })] : [];
    });

    try {
      const { container } = renderHome();
      await act(async () => {});
      const firstImage = container.querySelector(".bf-hero img") as HTMLImageElement;
      expect(firstImage).toHaveAttribute("src", "/stalled?bucket=w1280");
      const card = screen.getByRole("link", { name: "Stalled" });

      act(() => vi.advanceTimersByTime(3000));
      const secondImage = container.querySelector(".bf-hero img") as HTMLImageElement;
      expect(secondImage).toHaveAttribute("src", "/working?bucket=w1280");
      expect(card).toHaveFocus();
      fireEvent.load(secondImage);
      expect(screen.getByRole("heading", { name: "Working" })).toBeVisible();
      expect(card).toHaveFocus();
    } finally {
      vi.useRealTimers();
    }
  });

  it("leads with Continue Watching instead of a feature when a Profile has items to resume", async () => {
    const recent = newItem("Recent item", { artworkSlots: { backdrop: "/recent" } });
    vi.mocked(fetchSectionItems).mockImplementation(async (section, context) => {
      if (section.id === "continue") return [newItem("In progress")];
      return context.libraryID === "one" ? [recent] : [];
    });

    const { container } = renderHome();
    const keepWatching = await screen.findByRole("list", { name: "Reprendre la lecture" });
    await screen.findByRole("list", { name: "Library One" });
    expect(container.querySelector(".bf-hero")).toBeNull();
    expect(container.querySelector(".bf-section:not([hidden])")).toContainElement(keepWatching);
  });

  it("holds the feature until Continue Watching settles empty", async () => {
    const recent = newItem("Recent item", { artworkSlots: { backdrop: "/recent" } });
    let settleContinue!: (items: SourceItem[]) => void;
    const pendingContinue = new Promise<SourceItem[]>((resolve) => {
      settleContinue = resolve;
    });
    vi.mocked(fetchSectionItems).mockImplementation(async (section, context) => {
      if (section.id === "continue") return pendingContinue;
      return context.libraryID === "one" ? [recent] : [];
    });

    const { container } = renderHome();
    await screen.findByRole("list", { name: "Library One" });
    // Neither the feature nor its placeholder (also .bf-hero) shows yet.
    expect(container.querySelector(".bf-hero")).toBeNull();

    await act(async () => settleContinue([]));
    const heroArt = await waitFor(() => {
      const img = container.querySelector(".bf-hero img");
      expect(img).toHaveAttribute("src", "/recent?bucket=w1280");
      return img as HTMLImageElement;
    });
    fireEvent.load(heroArt);
    expect(screen.getByRole("heading", { name: recent.title })).toBeVisible();
  });

  it("keeps all 24 manifest sections when the hero loads from the final shelf", async () => {
    const [keepWatching, recent] = layout.sections;
    const sections = [
      ...Array.from({ length: MAX_RENDERED_SECTIONS - 1 }, (_, index) => ({
        ...keepWatching,
        id: `section-${index + 1}`,
        title: `Shelf ${index + 1}`,
        source: { kind: "section-items" },
      })),
      recent,
    ];
    vi.mocked(fetchSectionItems).mockImplementation(async (section, context) => {
      if (section.id !== "recent") return [newItem(section.id)];
      return section.id === "recent" && context.libraryID === "one"
        ? [newItem("Recent item", { artwork: { poster: "/broken" } })]
        : [];
    });

    const { container } = renderHome({ ...layout, sections });
    const shelf = await screen.findByRole("list", { name: "Library One" });
    const heroArt = container.querySelector(".bf-hero img") as HTMLImageElement;
    expect(heroArt).toBeInTheDocument();

    fireEvent.load(heroArt);
    expect(screen.getByRole("heading", { name: "Recent item" })).toBeVisible();
    expect(within(shelf).getByRole("link", { name: "Recent item" })).toBeVisible();
    expect(screen.getAllByRole("list")).toHaveLength(MAX_RENDERED_SECTIONS);

    fireEvent.error(heroArt);
    expect(container.querySelector(".bf-hero")).toBeNull();
    expect(screen.getAllByRole("list")).toHaveLength(MAX_RENDERED_SECTIONS);
  });

  it("waits for current visibility before showing cached shelves or hero artwork on return", async () => {
    const hidden = newItem("Previously visible item", { artwork: { poster: "/previous" } });
    const visible = newItem("Still visible item");
    vi.mocked(fetchSectionItems).mockImplementation(async (section, { libraryID }) => {
      if (section.id !== "recent") return [];
      if (libraryID === "one") return [hidden];
      return libraryID === "two" ? [visible] : [];
    });
    const { container } = renderViewerRoutes([homeNode, ...session.manifest.navigation]);

    expect(await screen.findByRole("list", { name: "Library One" })).toBeVisible();
    expect(await screen.findByRole("list", { name: "Library Two" })).toBeVisible();
    expect(container.querySelector(".bf-hero img")).toHaveAttribute(
      "src",
      "/previous?bucket=w1280",
    );
    fireEvent.click(screen.getByRole("link", { name: "Library Two" }));
    expect(screen.getByRole("heading", { name: "Library Two", level: 1 })).toBeVisible();

    const updatedLibraries = libraries.map((library) =>
      library.id === "one" ? { ...library, showOnHome: false } : library,
    );
    let resolveVisibility!: (value: typeof updatedLibraries) => void;
    const pendingVisibility = new Promise<typeof updatedLibraries>((resolve) => {
      resolveVisibility = resolve;
    });
    const expectNoHiddenContent = () => {
      expect(screen.queryByRole("list", { name: "Library One" })).toBeNull();
      expect(screen.queryByRole("link", { name: hidden.title })).toBeNull();
      expect(container.querySelector(".bf-hero img")).toBeNull();
    };
    vi.mocked(fetchHomeLibraries).mockImplementationOnce(() => {
      // The first render has committed when the visibility effect starts.
      expectNoHiddenContent();
      return pendingVisibility;
    });
    fireEvent.click(screen.getByRole("link", { name: "Home" }));
    expectNoHiddenContent();
    expect(screen.queryByRole("list", { name: "Library Two" })).toBeNull();

    await act(async () => resolveVisibility(updatedLibraries));
    const shelf = screen.getByRole("list", { name: "Library Two" });
    expect(within(shelf).getByRole("link", { name: visible.title })).toBeVisible();
    expectNoHiddenContent();
  });

  it("restores cached shelves after a failed visibility read and during failed engagement revalidation", async () => {
    vi.mocked(fetchSectionItems).mockImplementation(async (section, context) =>
      section.id === "recent" && context.libraryID === "one" ? [newItem("Cached item")] : [],
    );
    const home = renderHome();
    expect(await screen.findByRole("link", { name: "Cached item" })).toBeVisible();
    home.unmount();

    vi.mocked(fetchHomeLibraries).mockRejectedValue(new Error("offline"));
    renderHome();
    expect(screen.queryByRole("link", { name: "Cached item" })).toBeNull();
    expect(await screen.findByText(/connexion.*perdue/i)).toBeVisible();
    expect(screen.getByRole("link", { name: "Cached item" })).toBeVisible();

    let reject!: (error: Error) => void;
    vi.mocked(fetchHomeLibraries).mockResolvedValue(libraries);
    vi.mocked(fetchSectionItems).mockImplementation(async (section, context) => {
      if (section.id !== "recent" || context.libraryID !== "one") return [];
      return new Promise((_, onReject) => {
        reject = onReject;
      });
    });
    await act(async () => notifyEngagementChanged());
    expect(screen.getByRole("link", { name: "Cached item" })).toBeVisible();
    await act(async () => reject(new Error("offline")));
    expect(screen.getByRole("link", { name: "Cached item" })).toBeVisible();

    vi.mocked(fetchSectionItems).mockResolvedValue([]);
    await act(async () => notifyEngagementChanged());
    expect(screen.queryByRole("link", { name: "Cached item" })).toBeNull();
  });

  it.each(["hidden", "role", "unauthorized"] as const)(
    "discards cached scoped feeds on a %s visibility or authorization change",
    async (change) => {
      vi.mocked(fetchSectionItems).mockImplementation(async (section, context) =>
        section.id === "recent" && context.libraryID === "one" ? [newItem("Cached item")] : [],
      );
      const home = renderHome();
      expect(await screen.findByRole("link", { name: "Cached item" })).toBeVisible();

      if (change === "hidden") {
        vi.mocked(fetchHomeLibraries).mockResolvedValue(
          libraries.map((library) =>
            library.id === "one" ? { ...library, showOnHome: false } : library,
          ),
        );
      } else if (change === "role") {
        vi.mocked(fetchHomeLibraries).mockResolvedValue(null);
        vi.mocked(fetchSectionItems).mockRejectedValue(new Error("offline"));
      } else {
        vi.mocked(fetchHomeLibraries).mockRejectedValue(new ApiError("unauthorized", 401));
      }
      await act(async () => notifyEngagementChanged());
      expect(screen.queryByRole("link", { name: "Cached item" })).toBeNull();
      home.unmount();

      vi.mocked(fetchHomeLibraries).mockRejectedValue(new Error("offline"));
      renderHome();
      expect(await screen.findByText(/connexion.*perdue/i)).toBeVisible();
      expect(screen.queryByRole("link", { name: "Cached item" })).toBeNull();
    },
  );

  it("never renders a cached recent shelf for another identity", async () => {
    vi.mocked(fetchSectionItems).mockImplementation(async (section, context) =>
      section.id === "recent" && context.libraryID === "one" ? [newItem("Cached item")] : [],
    );
    const home = renderHome();
    expect(await screen.findByRole("link", { name: "Cached item" })).toBeVisible();
    home.unmount();

    identity = "another viewer";
    vi.mocked(fetchHomeLibraries).mockRejectedValue(new Error("offline"));
    renderHome();
    expect(await screen.findByText(/connexion.*perdue/i)).toBeVisible();
    expect(screen.queryByRole("link", { name: "Cached item" })).toBeNull();
  });

  it.each([false, true])(
    "keeps successful feeds independent when another fails (authorization: %s)",
    async (auth) => {
      const good = newItem("Available item");
      let reject!: (error: Error) => void;
      let failedSignal: AbortSignal | undefined;
      vi.mocked(fetchSectionItems).mockImplementation(async (section, context, signal) => {
        if (section.id === "continue") return [];
        if (context.libraryID === "two") {
          failedSignal = signal;
          return new Promise((_, onReject) => {
            reject = onReject;
          });
        }
        return context.libraryID === "one" ? [good] : [];
      });

      renderHome();
      expect(await screen.findByRole("list", { name: "Library One" })).toBeVisible();
      expect(screen.queryByRole("list", { name: "Library Two" })).toBeNull();
      await act(async () =>
        reject(auth ? new ApiError("unauthorized", 401) : new Error("offline")),
      );

      expect(screen.getByText(/connexion.*perdue/i)).toBeInTheDocument();
      expect(screen.queryByRole("list", { name: "Library Two" })).toBeNull();
      if (auth) {
        expect(clearViewerCache).toHaveBeenCalledOnce();
        expect(screen.queryByRole("list", { name: "Library One" })).toBeNull();
        expect(failedSignal?.aborted).toBe(true);
      } else {
        expect(clearViewerCache).not.toHaveBeenCalled();
        expect(screen.getByRole("list", { name: "Library One" })).toBeVisible();
      }
    },
  );

  it("tries available hero artwork and collapses the feature when all images fail", async () => {
    const bare = newItem("No artwork");
    const locked = newItem("Locked", { locked: true, artwork: { poster: "/locked" } });
    const feature = newItem("Featured item", {
      artworkSlots: { backdrop: "/backdrop", poster: "/poster" },
    });
    const rows = [bare, locked, feature];
    vi.mocked(fetchSectionItems).mockImplementation(async (section, context) =>
      section.id !== "continue" && context.libraryID === "one" ? rows : [],
    );

    const { container } = renderHome();
    const heroArt = () => container.querySelector(".bf-hero img");
    await waitFor(() => expect(heroArt()).toHaveAttribute("src", "/backdrop?bucket=w1280"));
    const backdrop = heroArt();
    if (!backdrop) throw new Error("Expected hero backdrop");
    fireEvent.error(backdrop);
    await waitFor(() => expect(heroArt()).toHaveAttribute("src", "/poster?bucket=w1280"));
    const poster = heroArt();
    if (!poster) throw new Error("Expected hero poster");
    fireEvent.load(poster);
    expect(screen.getByRole("heading", { name: "Featured item" })).toBeVisible();
    expect(screen.getByText("À la une")).toBeInTheDocument();

    fireEvent.error(poster);
    expect(heroArt()).toBeNull();
    expect(screen.queryByRole("heading", { name: "Featured item" })).toBeNull();
    expect(await screen.findByRole("list", { name: "Library One" })).toBeVisible();
  });

  it("leaves the library shelf in place when recent items have no artwork", async () => {
    vi.mocked(fetchSectionItems).mockImplementation(async (section, context) =>
      section.id !== "continue" && context.libraryID === "one" ? [newItem("No artwork")] : [],
    );
    const { container } = renderHome();

    expect(await screen.findByRole("list", { name: "Library One" })).toBeVisible();
    expect(container.querySelector(".bf-hero")).toBeNull();
    expect(container.querySelector(".bf-poster-glyph")).toBeVisible();
  });

  it("features a recent item whose only artwork is a banner", async () => {
    const feature = newItem("Banner item", { artworkSlots: { banner: "/banner" } });
    vi.mocked(fetchSectionItems).mockImplementation(async (section, { libraryID }) =>
      section.id === "recent" && libraryID === "one" ? [feature] : [],
    );
    const { container } = renderHome();
    await screen.findByRole("list", { name: "Library One" });

    const heroArt = container.querySelector(".bf-hero img");
    expect(heroArt).toHaveAttribute("src", "/banner?bucket=w1280");
    if (!heroArt) throw new Error("Expected hero banner");
    fireEvent.load(heroArt);
    expect(screen.getByRole("heading", { name: feature.title })).toBeVisible();
  });
});
