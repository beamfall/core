import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Section, SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { SECTION_GRID_CAP } from "../lib/sections";
import { SectionView } from "./SectionView";

const grid: Section = {
  id: "sec-all",
  kind: "grid",
  title: { en: "All" },
  artworkSlot: "poster",
  source: { kind: "library" } as Section["source"],
};

function items(count: number): SourceItem[] {
  return Array.from({ length: count }, (_, i) => ({
    id: `media-${i}`,
    kind: "media",
    title: `Item ${i}`,
    artwork: { poster: "" },
  }));
}

function renderGrid(count: number) {
  return render(
    <I18nProvider>
      <MemoryRouter>
        <SectionView
          section={grid}
          items={items(count)}
          seeAll={{ href: "/see-all", state: null as never }}
        />
      </MemoryRouter>
    </I18nProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

it("opens an audio card menu without a session and leaves radio unavailable", async () => {
  const requests = vi.fn<typeof fetch>(async () =>
    Response.json({ data: { itemState: null, mutes: [] } }),
  );
  vi.stubGlobal("fetch", requests);
  const track: SourceItem = { ...items(1)[0], mediaClass: "audio" };

  render(
    <I18nProvider initialLocale="en">
      <MemoryRouter>
        <SectionView section={grid} items={[track]} />
      </MemoryRouter>
    </I18nProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "More actions for Item 0" }));

  expect(await screen.findByRole("menuitem", { name: "Mark played" })).toBeInTheDocument();
  expect(screen.queryByRole("menuitem", { name: "Start radio" })).toBeNull();
  expect(requests).not.toHaveBeenCalledWith(
    expect.anything(),
    expect.objectContaining({ body: expect.stringContaining("stationGenerate(") }),
  );
});

// CORE-1213: a grid hydrates a bounded first page, so one that fills it must
// lead to the paged See All grid instead of silently stopping.
describe("SectionView grid See All", () => {
  it("offers See All when the grid fills its cap", () => {
    renderGrid(SECTION_GRID_CAP);
    expect(screen.getByRole("button", { name: "See all" })).toBeInTheDocument();
  });

  it("offers nothing when the grid holds the whole Source", () => {
    renderGrid(SECTION_GRID_CAP - 1);
    expect(screen.queryByRole("button", { name: "See all" })).toBeNull();
  });
});

it("keeps short rows in view when scrolling a mixed-art Home grid", async () => {
  const columns = 4;
  const gap = 20;
  const viewport = 240;
  const heights = Array.from({ length: 16 }, (_, row) => (row < 4 ? 240 : 80));
  const offsets = [0];
  for (const height of heights) offsets.push((offsets.at(-1) ?? 0) + height + gap);
  const mixed = items(heights.length * columns).map((item, index) => ({
    ...item,
    artwork: { poster: index < 4 * columns ? "/portrait.png" : "/landscape.png" },
  }));
  let scroll = 0;
  vi.stubGlobal("innerHeight", viewport);
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (
    this: HTMLElement,
  ) {
    if (this.classList.contains("bf-grid")) return { top: -scroll } as DOMRect;
    const index = Number(this.getAttribute("aria-posinset")) - 1;
    return { height: heights[Math.floor(index / columns)] ?? 0 } as DOMRect;
  });

  render(
    <I18nProvider>
      <MemoryRouter>
        <SectionView section={grid} items={mixed} intrinsicArtwork />
      </MemoryRouter>
    </I18nProvider>,
  );
  const list = screen.getByRole("list", { name: "All" });
  list.style.gridTemplateColumns = "150px ".repeat(columns).trim();
  list.style.rowGap = `${gap}px`;
  fireEvent.resize(window);

  for (const row of [3, 6, 9, 12]) {
    scroll = offsets[row] + 5;
    fireEvent.scroll(window);
    await waitFor(() => {
      const cards = within(list).getAllByRole("listitem");
      expect(cards[0]).toHaveAttribute("aria-posinset", String((row - 2) * columns + 1));
      expect(list.firstElementChild).toHaveStyle({ height: `${offsets[row - 2] - gap}px` });
      // A 240px viewport at these short rows also intersects the third row.
      if (row >= 6) {
        expect(
          within(list).getByRole("link", { name: `Item ${(row + 2) * columns}` }),
        ).toBeVisible();
      }
    });
  }
});

// CORE-2320: two cards with the same title read as one; each shows its year.
describe("SectionView repeated titles", () => {
  it("shows the year on cards whose title repeats in the row", () => {
    const row: SourceItem[] = [
      { id: "a", kind: "media", title: "Dune", year: 1984, artwork: { poster: "" } },
      { id: "b", kind: "media", title: "Dune", year: 2021, artwork: { poster: "" } },
      { id: "c", kind: "media", title: "Arrival", year: 2016, artwork: { poster: "" } },
    ];
    render(
      <I18nProvider>
        <MemoryRouter>
          <SectionView
            section={grid}
            items={row}
            seeAll={{ href: "/see-all", state: null as never }}
          />
        </MemoryRouter>
      </I18nProvider>,
    );
    expect(screen.getByRole("link", { name: /^Dune \(1984\)/ })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /^Dune \(2021\)/ })).toBeInTheDocument();
    expect(screen.getByText("2021")).toBeInTheDocument();
    expect(screen.queryByText("2016")).toBeNull();
  });
});

// CORE-2512: a catalog shelf is ranked, never A-Z; a grid keeps the server's order.
describe("SectionView shelf ranking", () => {
  const row: SourceItem[] = [
    { id: "a", kind: "entity", title: "Alpha", year: 2001, artwork: { poster: "" } },
    { id: "b", kind: "entity", title: "Bravo", year: 2020, artwork: { poster: "" } },
    {
      id: "c",
      kind: "entity",
      title: "Charlie",
      year: 1999,
      engagement: { totalCount: 4, remainingCount: 2 },
      artwork: { poster: "" },
    },
  ];
  function hrefs(section: Section) {
    render(
      <I18nProvider>
        <MemoryRouter>
          <SectionView section={section} items={row} />
        </MemoryRouter>
      </I18nProvider>,
    );
    return screen.getAllByRole("link").map((link) => link.getAttribute("href"));
  }

  it("puts in-progress, then newer items first on a catalog shelf", () => {
    const shelf = { ...grid, kind: "shelf", source: { kind: "entity-items" } } as Section;
    expect(hrefs(shelf)).toEqual(["/entity/c", "/entity/b", "/entity/a"]);
  });

  it("keeps the server's order in a grid", () => {
    expect(hrefs(grid)).toEqual(["/entity/a", "/entity/b", "/entity/c"]);
  });

  it.each([{ kind: "entity-items", entityKind: "container" }, { kind: "all-items" }])(
    "renders newer unstarted containers first on a Browse $kind shelf",
    (source) => {
      const section: Section = {
        id: "browse-containers",
        kind: "shelf",
        title: { en: "Containers" },
        artworkSlot: "poster",
        source,
      };
      const served: SourceItem[] = [
        {
          id: "entity-amber",
          kind: "entity",
          title: "Amber",
          year: 2005,
          mediaClass: "video",
          engagement: { totalCount: 6, remainingCount: 6 },
        },
        {
          id: "entity-cobalt",
          kind: "entity",
          title: "Cobalt",
          year: 2022,
          mediaClass: "video",
          engagement: { totalCount: 4, remainingCount: 4 },
        },
      ];
      render(
        <I18nProvider>
          <MemoryRouter>
            <SectionView section={section} items={served} navigationID="library-library-a" />
          </MemoryRouter>
        </I18nProvider>,
      );
      const shelf = within(screen.getByRole("list", { name: "Containers" }));
      expect(shelf.getAllByRole("link").map((link) => link.getAttribute("href"))).toEqual([
        "/entity/entity-cobalt",
        "/entity/entity-amber",
      ]);
    },
  );
});
