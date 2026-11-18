import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { LibrarySummary, NavigationNode } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { expectNoAxeViolations } from "@/test/axe";
import { LibrarySidebar, libraryMonograms, moveEntry } from "./LibrarySidebar";
import type { SidebarLibraryPrefs } from "./sidebarPrefs";

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

describe("moveEntry", () => {
  it("moves one entry and ignores out-of-range moves", () => {
    expect(moveEntry(["a", "b", "c"], 2, 0)).toEqual(["c", "a", "b"]);
    const list = ["a", "b"];
    expect(moveEntry(list, 0, -1)).toBe(list);
    expect(moveEntry(list, 1, 2)).toBe(list);
  });
});

describe("LibrarySidebar per-Profile pin and order (CORE-2435)", () => {
  const titles: Record<string, string> = { a: "Films", b: "Music", c: "Photos" };
  const navigation: NavigationNode[] = [
    { id: "home", title: "Home", view: "layout", layoutID: "layout-home" },
    ...Object.entries(titles).map(([id, title]) => ({
      id: `library-${id}`,
      title,
      view: "layout",
      layoutID: "layout-home",
      libraryID: id,
    })),
  ];
  const libraries: LibrarySummary[] = Object.entries(titles).map(([id, title]) => ({
    id,
    title,
    pluginID: null,
    previewClips: false,
  }));

  function renderSidebar(
    prefs: SidebarLibraryPrefs | undefined,
    onSavePrefs?: (prefs: SidebarLibraryPrefs) => Promise<void>,
    collapsed = false,
  ) {
    return render(
      <I18nProvider>
        <MemoryRouter>
          <LibrarySidebar
            id="side"
            navigation={navigation}
            libraries={libraries}
            activeNodeId="home"
            collapsed={collapsed}
            drawer={false}
            onNavigate={() => {}}
            prefs={prefs}
            onSavePrefs={onSavePrefs}
          />
        </MemoryRouter>
      </I18nProvider>,
    );
  }

  const linkNames = (list: HTMLElement) =>
    within(list)
      .getAllByRole("link")
      .map((link) => link.querySelector(".bf-side-label")?.textContent);

  it("lists libraries in the Profile's order with unpinned ones under More", () => {
    renderSidebar({ order: ["b", "a"], hidden: ["c"] });
    const nav = screen.getByRole("navigation");
    const lists = within(nav).getAllByRole("list");
    expect(linkNames(lists[1])).toEqual(["Music", "Films"]);
    expect(screen.queryByRole("link", { name: "Photos" })).toBeNull();
    const more = screen.getByRole("button", { name: "More" });
    expect(more).toHaveAttribute("aria-expanded", "false");
    fireEvent.click(more);
    expect(more).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByRole("link", { name: "Photos" })).toBeInTheDocument();
  });

  it("offers no edit button without a saver or on the icon rail", () => {
    renderSidebar(undefined);
    expect(screen.queryByRole("button", { name: "Edit libraries" })).toBeNull();
    cleanup();
    renderSidebar(undefined, async () => {}, true);
    expect(screen.queryByRole("button", { name: "Edit libraries" })).toBeNull();
  });

  it("reorders with buttons and the keyboard, unpins, and saves on Done", async () => {
    const save = vi.fn(async (_prefs: SidebarLibraryPrefs) => {});
    const { container } = renderSidebar(undefined, save);
    fireEvent.click(screen.getByRole("button", { name: "Edit libraries" }));
    await expectNoAxeViolations(container);

    fireEvent.click(screen.getByRole("button", { name: "Move Music up" }));
    expect(screen.getByText("Music moved to position 1 of 3")).toBeInTheDocument();
    // Alt+Up on a focused row moves it too.
    const photosPin = screen.getByRole("button", { name: "Pin Photos" });
    fireEvent.keyDown(photosPin, { key: "ArrowUp", altKey: true });
    expect(screen.getByText("Photos moved to position 2 of 3")).toBeInTheDocument();
    // The first row cannot move up, but its button keeps focusability.
    const musicUp = screen.getByRole("button", { name: "Move Music up" });
    expect(musicUp).toHaveAttribute("aria-disabled", "true");
    fireEvent.click(musicUp);

    expect(photosPin).toHaveAttribute("aria-pressed", "true");
    fireEvent.click(photosPin);
    expect(photosPin).toHaveAttribute("aria-pressed", "false");

    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
    expect(save.mock.calls[0][0]).toEqual({ order: ["b", "c", "a"], hidden: ["c"] });
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Edit libraries" })).toBeVisible(),
    );
  });

  it("reorders by drag", async () => {
    const save = vi.fn(async (_prefs: SidebarLibraryPrefs) => {});
    renderSidebar(undefined, save);
    fireEvent.click(screen.getByRole("button", { name: "Edit libraries" }));
    const row = (name: string) => screen.getByText(name).closest("li") as HTMLElement;
    fireEvent.dragStart(row("Photos"));
    fireEvent.dragOver(row("Films"));
    fireEvent.drop(row("Films"));
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
    expect(save.mock.calls[0][0]).toEqual({ order: ["c", "a", "b"], hidden: [] });
  });

  it("stays in edit mode and says so when saving fails; Cancel discards", async () => {
    const save = vi.fn(async () => {
      throw new Error("offline");
    });
    renderSidebar(undefined, save);
    fireEvent.click(screen.getByRole("button", { name: "Edit libraries" }));
    fireEvent.click(screen.getByRole("button", { name: "Move Photos up" }));
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("could not be saved");
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    const lists = within(screen.getByRole("navigation")).getAllByRole("list");
    expect(linkNames(lists[1])).toEqual(["Films", "Music", "Photos"]);
  });
});
