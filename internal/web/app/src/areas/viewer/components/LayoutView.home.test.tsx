import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api";
import type { Layout, SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { clearViewerCache } from "@/lib/viewerCache";
import { fetchSectionItems } from "../lib/sections";
import { LayoutView } from "./LayoutView";

const libraries = [
  { id: "one", title: "Library One" },
  { id: "two", title: "Library Two" },
  { id: "empty", title: "Empty Library" },
  { id: "hidden", title: "Hidden from Home" },
  { id: "no-node", title: "Hidden from navigation" },
];

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
      .filter(({ id }) => id !== "no-node")
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

function renderHome() {
  return render(
    <I18nProvider initialLocale="fr">
      <MemoryRouter>
        <LayoutView layout={layout} libraryID="" home navigationID="home" />
      </MemoryRouter>
    </I18nProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.mocked(fetchSectionItems).mockReset();
  vi.mocked(clearViewerCache).mockClear();
});

describe("Viewer Home", () => {
  it("uses scoped shelves even outside the aggregate feed and keeps Continue Watching", async () => {
    const one = newItem("Square artwork", { mediaClass: "audio", artwork: { poster: "/square" } });
    const two = newItem("Landscape artwork", { artworkSlots: { backdrop: "/landscape" } });
    const shared = newItem("Shared item without artwork");
    const hidden = newItem("Hidden artwork", { artwork: { poster: "/hidden" } });
    const aggregate = [
      shared,
      ...Array.from({ length: 198 }, (_, index) => newItem(`bare-${index}`)),
      one,
    ];
    vi.mocked(fetchSectionItems).mockImplementation(async (section, context) => {
      if (section.id === "continue") return [newItem("In progress")];
      switch (context.libraryID) {
        case "one":
          return [one];
        case "two":
          return [shared, two];
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
    expect(fetchSectionItems).toHaveBeenCalledWith(
      expect.objectContaining({ id: "recent" }),
      expect.objectContaining({ libraryID: "", paging: { limit: 200, offset: 0 } }),
      expect.anything(),
    );
    expect(fetchSectionItems).not.toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({ libraryID: "no-node" }),
      expect.anything(),
    );
  });

  it("features artwork from a visible shelf when all 200 aggregate items lack artwork", async () => {
    const shared = newItem("Shared item without artwork");
    const feature = newItem("Artwork outside the aggregate window", {
      artworkSlots: { backdrop: "/older" },
    });
    const aggregate = [
      ...Array.from({ length: 199 }, (_, index) => newItem(`bare-${index}`)),
      shared,
    ];
    vi.mocked(fetchSectionItems).mockImplementation(async (section, context) => {
      if (section.id === "continue") return [];
      if (!context.libraryID) return aggregate;
      return context.libraryID === "two" ? [shared, feature] : [];
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
        return ["", "one"].includes(context.libraryID) ? [good] : [];
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
      section.id !== "continue" && ["", "one"].includes(context.libraryID) ? rows : [],
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
      section.id !== "continue" && ["", "one"].includes(context.libraryID)
        ? [newItem("No artwork")]
        : [],
    );
    const { container } = renderHome();

    expect(await screen.findByRole("list", { name: "Library One" })).toBeVisible();
    expect(container.querySelector(".bf-hero")).toBeNull();
    expect(container.querySelector(".bf-poster-glyph")).toBeVisible();
  });
});
