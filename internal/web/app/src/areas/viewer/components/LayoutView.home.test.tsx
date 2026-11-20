import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api";
import type { Layout, SourceItem } from "@/lib/graphql/types";
import { fetchHomeLibraries } from "@/lib/graphql/viewer";
import { I18nProvider } from "@/lib/i18n";
import { clearViewerCache } from "@/lib/viewerCache";
import { notifyEngagementChanged } from "../actions/engagementSync";
import { fetchSectionItems, MAX_RENDERED_SECTIONS } from "../lib/sections";
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
  useSession: () => session,
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
    libraries,
    navigation: libraries
      .filter(({ id }) => id !== "quiet")
      .map(({ id }) => ({ libraryID: id, view: "layout" })),
  },
  isManager: false,
};
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

    const { container } = renderHome();

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

  it("keeps the last shelf in a full manifest when the hero artwork fails", async () => {
    const [keepWatching, recent] = layout.sections;
    const sections = [
      recent,
      ...Array.from({ length: MAX_RENDERED_SECTIONS - 1 }, (_, index) => ({
        ...keepWatching,
        id: `section-${index + 2}`,
        title: `Shelf ${index + 2}`,
      })),
    ];
    vi.mocked(fetchSectionItems).mockImplementation(async (section, context) => {
      if (section.id === `section-${MAX_RENDERED_SECTIONS}`) return [newItem("Last item")];
      return section.id === "recent" && context.libraryID === "one"
        ? [newItem("Recent item", { artwork: { poster: "/broken" } })]
        : [];
    });

    const { container } = renderHome({ ...layout, sections });
    const lastShelf = `Shelf ${MAX_RENDERED_SECTIONS}`;
    const shelf = await screen.findByRole("list", { name: lastShelf });
    expect(within(shelf).getByRole("link", { name: "Last item" })).toBeVisible();
    const heroArt = container.querySelector(".bf-hero img");
    expect(heroArt).toBeInTheDocument();

    fireEvent.load(heroArt as HTMLImageElement);
    expect(screen.getByRole("heading", { name: "Recent item" })).toBeVisible();
    expect(screen.queryByRole("list", { name: lastShelf })).toBeNull();

    fireEvent.error(heroArt as HTMLImageElement);
    expect(container.querySelector(".bf-hero")).toBeNull();
    const restored = await screen.findByRole("list", { name: lastShelf });
    expect(within(restored).getByRole("link", { name: "Last item" })).toBeVisible();
  });

  it("renders cached shelves on return and keeps them during failed engagement revalidation", async () => {
    vi.mocked(fetchSectionItems).mockImplementation(async (section, context) =>
      section.id === "recent" && context.libraryID === "one" ? [newItem("Cached item")] : [],
    );
    const home = renderHome();
    expect(await screen.findByRole("link", { name: "Cached item" })).toBeVisible();
    home.unmount();

    vi.mocked(fetchHomeLibraries).mockRejectedValue(new Error("offline"));
    renderHome();
    expect(screen.getByRole("link", { name: "Cached item" })).toBeVisible();
    expect(await screen.findByText(/connexion.*perdue/i)).toBeVisible();

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
});
