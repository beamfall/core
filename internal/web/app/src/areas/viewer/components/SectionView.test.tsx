import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it } from "vitest";
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

afterEach(cleanup);

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
    expect(screen.getByRole("button", { name: /^Dune \(1984\)/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^Dune \(2021\)/ })).toBeInTheDocument();
    expect(screen.getByText("2021")).toBeInTheDocument();
    expect(screen.queryByText("2016")).toBeNull();
  });
});
