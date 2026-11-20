import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Layout, SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
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
});

describe("Viewer Home", () => {
  it("scopes new shelves and omits empty and hidden libraries while keeping Continue Watching", async () => {
    const one = newItem("Square artwork", { mediaClass: "audio", artwork: { poster: "/square" } });
    const two = newItem("Landscape artwork", { artworkSlots: { backdrop: "/landscape" } });
    vi.mocked(fetchSectionItems).mockImplementation(async (section, context) => {
      if (section.id === "continue") return [newItem("In progress")];
      switch (context.libraryID) {
        case "one":
          return [one];
        case "two":
          return [two];
        case "hidden":
          return [newItem("Hidden item")];
        case "":
          return [one, two];
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
    expect(screen.queryByText("Hidden item")).toBeNull();
    expect(screen.queryByRole("list", { name: /empty|hidden/i })).toBeNull();
    expect(screen.queryByRole("list", { name: "Ajouts récents" })).toBeNull();
    expect(vi.mocked(fetchSectionItems).mock.calls.every(([, context]) => context.home)).toBe(true);
    expect(fetchSectionItems).not.toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({ libraryID: "no-node" }),
      expect.anything(),
    );
  });

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
