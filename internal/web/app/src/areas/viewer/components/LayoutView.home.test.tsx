import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api";
import type { Layout, SourceItem } from "@/lib/graphql/types";
import { fetchHomeLibraries } from "@/lib/graphql/viewer";
import { I18nProvider } from "@/lib/i18n";
import { clearViewerCache } from "@/lib/viewerCache";
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
  useViewerIdentity: () => null,
  useSession: () => session,
}));
vi.mock("../lib/sections", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/sections")>()),
  fetchSectionItems: vi.fn(),
}));

vi.mock("@/lib/viewerCache", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/viewerCache")>()),
  clearViewerCache: vi.fn(),
}));

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

beforeEach(() => vi.mocked(fetchHomeLibraries).mockResolvedValue(libraries));

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
