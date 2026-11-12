import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it } from "vitest";
import type { LibrarySummary, NavigationNode } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { LibrarySidebar, libraryMonograms } from "./LibrarySidebar";

afterEach(cleanup);

describe("libraryMonograms", () => {
  it("uses a single initial when no other library shares it", () => {
    expect(libraryMonograms(["Music", "films"])).toEqual(["M", "F"]);
  });

  it("widens only the colliding libraries to two characters", () => {
    expect(libraryMonograms(["Podcasts", "Photos", "Music"])).toEqual(["Po", "Ph", "M"]);
  });

  it("uses word initials for a colliding multi-word title", () => {
    expect(libraryMonograms(["Home Videos", "Holidays"])).toEqual(["HV", "Ho"]);
  });

  it("survives blank and astral-plane titles", () => {
    expect(libraryMonograms(["", "  "])).toEqual(["•", "•"]);
    expect(libraryMonograms(["𝒜rchive"])).toEqual(["𝒜"]);
  });
});

describe("LibrarySidebar library tiles", () => {
  function library(id: string, title: string): [LibrarySummary, NavigationNode] {
    return [
      { id, title, pluginID: null, previewClips: false },
      { id: `library-${id}`, title, view: "layout", layoutID: "layout-home", libraryID: id },
    ];
  }

  it("tells same-initial libraries apart and keeps each link's accessible name", () => {
    const entries = [library("a", "Podcasts"), library("b", "Photos"), library("c", "Music")];
    render(
      <I18nProvider>
        <MemoryRouter>
          <LibrarySidebar
            id="side"
            navigation={[
              { id: "home", title: "Home", view: "layout", layoutID: "layout-home" },
              ...entries.map(([, node]) => node),
            ]}
            libraries={entries.map(([summary]) => summary)}
            activeNodeId="home"
            collapsed={false}
            drawer={false}
            open={false}
            onNavigate={() => {}}
          />
        </MemoryRouter>
      </I18nProvider>,
    );

    const tile = (name: string) =>
      screen.getByRole("link", { name }).querySelector(".bf-side-tile");
    expect(tile("Podcasts")).toHaveTextContent("Po");
    expect(tile("Photos")).toHaveTextContent("Ph");
    expect(tile("Music")).toHaveTextContent("M");
    expect(tile("Podcasts")).toHaveAttribute("aria-hidden", "true");
  });

  it("draws the library's icon, and a monogram for a name this client does not know", () => {
    const entries = [
      library("a", "Films"),
      library("b", "Family"),
      library("c", "Music"),
      library("d", "Mixtapes"),
    ];
    entries[0][0].icon = "film";
    entries[1][0].icon = "hologram";
    render(
      <I18nProvider>
        <MemoryRouter>
          <LibrarySidebar
            id="side"
            navigation={entries.map(([, node]) => node)}
            libraries={entries.map(([summary]) => summary)}
            activeNodeId=""
            collapsed={false}
            drawer={false}
            open={false}
            onNavigate={() => {}}
          />
        </MemoryRouter>
      </I18nProvider>,
    );

    const tile = (name: string) =>
      screen.getByRole("link", { name }).querySelector(".bf-side-tile");
    // A known icon replaces the letter; the link keeps its title as its name.
    expect(tile("Films")?.querySelector("svg.lucide-film")).not.toBeNull();
    expect(tile("Films")).toHaveTextContent("");
    expect(tile("Films")).toHaveAttribute("aria-hidden", "true");
    // Unknown and absent names keep the collision-aware monogram. Only
    // lettered tiles can collide: "Family" shares an initial with "Films",
    // which shows an icon, so it stays a single letter.
    expect(tile("Family")?.querySelector("svg")).toBeNull();
    expect(tile("Family")).toHaveTextContent(/^F$/);
    expect(tile("Music")).toHaveTextContent("Mu");
    expect(tile("Mixtapes")).toHaveTextContent("Mi");
  });
});
