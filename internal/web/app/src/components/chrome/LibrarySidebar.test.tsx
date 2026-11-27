import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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
    prefsReady = true,
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
            prefsReady={prefsReady}
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

  it("keeps Edit inert until the saved choice has loaded", () => {
    const save = vi.fn(async (_prefs: SidebarLibraryPrefs) => {});
    const view = renderSidebar(undefined, save, false, false);
    const edit = screen.getByRole("button", { name: "Edit libraries" });
    expect(edit).toHaveAttribute("aria-disabled", "true");
    fireEvent.click(edit);
    // No draft from server order, so no Done that could overwrite the choice.
    expect(screen.queryByRole("button", { name: "Done" })).toBeNull();
    view.rerender(
      <I18nProvider>
        <MemoryRouter>
          <LibrarySidebar
            id="side"
            navigation={navigation}
            libraries={libraries}
            activeNodeId="home"
            collapsed={false}
            drawer={false}
            onNavigate={() => {}}
            prefs={{ order: ["c", "b", "a"], hidden: [] }}
            prefsReady
            onSavePrefs={save}
          />
        </MemoryRouter>
      </I18nProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Edit libraries" }));
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    expect(save.mock.calls[0][0]).toEqual({ order: ["c", "b", "a"], hidden: [] });
  });

  it("ignores edits while a save is in flight", async () => {
    let finish: () => void = () => {};
    const save = vi.fn(
      (_prefs: SidebarLibraryPrefs) =>
        new Promise<void>((resolve) => {
          finish = resolve;
        }),
    );
    renderSidebar(undefined, save);
    fireEvent.click(screen.getByRole("button", { name: "Edit libraries" }));
    fireEvent.click(screen.getByRole("button", { name: "Move Photos up" }));
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    const filmsPin = screen.getByRole("button", { name: "Pin Films" });
    for (const name of ["Pin Films", "Move Films down", "Cancel", "Done"]) {
      expect(screen.getByRole("button", { name })).toHaveAttribute("aria-disabled", "true");
    }
    fireEvent.click(filmsPin);
    fireEvent.click(screen.getByRole("button", { name: "Move Films down" }));
    fireEvent.keyDown(filmsPin, { key: "ArrowDown", altKey: true });
    const row = (name: string) => screen.getByText(name).closest("li") as HTMLElement;
    expect(row("Films")).toHaveAttribute("draggable", "false");
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(filmsPin).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: "Done" })).toBeInTheDocument();
    expect(save).toHaveBeenCalledTimes(1);
    expect(save.mock.calls[0][0]).toEqual({ order: ["a", "c", "b"], hidden: [] });
    await act(async () => finish());
    expect(screen.queryByRole("button", { name: "Done" })).toBeNull();
  });

  it("shows rail navigation when collapsed mid-edit and resumes the draft on expand", async () => {
    const save = vi.fn(async (_prefs: SidebarLibraryPrefs) => {});
    const sidebar = (collapsed: boolean) => (
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
            onSavePrefs={save}
          />
        </MemoryRouter>
      </I18nProvider>
    );
    const view = render(sidebar(false));
    fireEvent.click(screen.getByRole("button", { name: "Edit libraries" }));
    fireEvent.click(screen.getByRole("button", { name: "Move Photos up" }));
    view.rerender(sidebar(true));
    // The rail carries library links, not the editor.
    expect(screen.queryByRole("button", { name: "Done" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Pin Films" })).toBeNull();
    const lists = within(screen.getByRole("navigation")).getAllByRole("list");
    expect(within(lists[1]).getAllByRole("link")).toHaveLength(3);
    view.rerender(sidebar(false));
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
    expect(save.mock.calls[0][0]).toEqual({ order: ["a", "c", "b"], hidden: [] });
  });

  it("moves focus into the editor and back to Edit on Cancel and Done", async () => {
    renderSidebar(undefined, async () => {});
    fireEvent.click(screen.getByRole("button", { name: "Edit libraries" }));
    expect(screen.getByRole("button", { name: "Pin Films" })).toHaveFocus();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.getByRole("button", { name: "Edit libraries" })).toHaveFocus();
    fireEvent.click(screen.getByRole("button", { name: "Edit libraries" }));
    expect(screen.getByRole("button", { name: "Pin Films" })).toHaveFocus();
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Edit libraries" })).toHaveFocus(),
    );
  });
});

describe("LibrarySidebar Your stuff group (CORE-2827)", () => {
  const navigation: NavigationNode[] = [
    { id: "home", title: "Home", view: "layout", layoutID: "layout-home" },
    { id: "library-a", title: "Films", view: "layout", layoutID: "layout-a", libraryID: "a" },
  ];
  const libraries: LibrarySummary[] = [
    { id: "a", title: "Films", pluginID: null, previewClips: false },
  ];

  function renderSidebar({
    prefs,
    onSavePrefs,
    collapsed = false,
    pathname = "/",
  }: {
    prefs?: SidebarLibraryPrefs;
    onSavePrefs?: (prefs: SidebarLibraryPrefs) => Promise<void>;
    collapsed?: boolean;
    pathname?: string;
  }) {
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
            prefsReady
            onSavePrefs={onSavePrefs}
            showPersonal
            pathname={pathname}
          />
        </MemoryRouter>
      </I18nProvider>,
    );
  }

  const groupNames = () =>
    within(document.getElementById("side-personal") as HTMLElement)
      .getAllByRole("link")
      .map((link) => link.querySelector(".bf-side-label")?.textContent);

  it("is hidden unless the shell asks for it", () => {
    render(
      <I18nProvider>
        <MemoryRouter>
          <LibrarySidebar
            id="side"
            navigation={navigation}
            libraries={libraries}
            activeNodeId="home"
            collapsed={false}
            drawer={false}
            onNavigate={() => {}}
          />
        </MemoryRouter>
      </I18nProvider>,
    );
    expect(screen.queryByRole("heading", { name: "Your stuff" })).toBeNull();
    expect(screen.queryByRole("link", { name: "Playlists" })).toBeNull();
  });

  it("puts Playlists one click away, marked current on its page, on the rail too", async () => {
    const { container } = renderSidebar({ pathname: "/playlists" });
    expect(screen.getByRole("heading", { name: "Your stuff" })).toBeInTheDocument();
    expect(groupNames()).toEqual(["Playlists", "Favorites", "History"]);
    const playlists = screen.getByRole("link", { name: "Playlists" });
    expect(playlists).toHaveAttribute("href", "/playlists");
    expect(playlists).toHaveAttribute("aria-current", "page");
    expect(screen.getByRole("link", { name: "Favorites" })).toHaveAttribute("href", "/favorites");
    expect(screen.getByRole("link", { name: "History" })).not.toHaveAttribute("aria-current");
    await expectNoAxeViolations(container);
    cleanup();
    renderSidebar({ collapsed: true, pathname: "/playlists/p1" });
    expect(screen.getByRole("link", { name: "Playlists" })).toHaveAttribute("aria-current", "page");
  });

  it("applies a stored choice and leaves the library choice alone", () => {
    renderSidebar({
      prefs: {
        order: [],
        hidden: [],
        personal: { order: ["history", "playlists"], hidden: ["favorites"] },
      },
    });
    expect(groupNames()).toEqual(["History", "Playlists"]);
    expect(screen.queryByRole("link", { name: "Favorites" })).toBeNull();
    expect(screen.getByRole("link", { name: "Films" })).toBeInTheDocument();
  });

  it("leaves an untouched group out of a legacy choice when the libraries are saved", async () => {
    const save = vi.fn(async (_prefs: SidebarLibraryPrefs) => {});
    renderSidebar({ prefs: { order: ["a"], hidden: [] }, onSavePrefs: save });
    fireEvent.click(screen.getByRole("button", { name: "Edit libraries" }));
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
    expect(save.mock.calls[0][0]).not.toHaveProperty("personal");
  });

  it("stores only the unpinned entries when the default order is untouched", async () => {
    const save = vi.fn(async (_prefs: SidebarLibraryPrefs) => {});
    renderSidebar({ prefs: { order: ["a"], hidden: [] }, onSavePrefs: save });
    fireEvent.click(screen.getByRole("button", { name: "Edit your stuff" }));
    fireEvent.click(screen.getByRole("button", { name: "Pin Favorites" }));
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
    expect(save.mock.calls[0][0]).toEqual({
      order: ["a"],
      hidden: [],
      personal: { order: [], hidden: ["favorites"] },
    });
  });

  it("re-pins an entry back to its default place when no order was ever chosen", async () => {
    const save = vi.fn(async (_prefs: SidebarLibraryPrefs) => {});
    renderSidebar({
      prefs: { order: [], hidden: [], personal: { order: [], hidden: ["favorites"] } },
      onSavePrefs: save,
    });
    fireEvent.click(screen.getByRole("button", { name: "Edit your stuff" }));
    fireEvent.click(screen.getByRole("button", { name: "Pin Favorites" }));
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
    expect(save.mock.calls[0][0]).toEqual({
      order: [],
      hidden: [],
      personal: { order: [], hidden: [] },
    });
  });

  it("keeps a stored order when a later save only unpins", async () => {
    const save = vi.fn(async (_prefs: SidebarLibraryPrefs) => {});
    const stored = { order: ["history", "playlists", "favorites"], hidden: [] };
    renderSidebar({ prefs: { order: [], hidden: [], personal: stored }, onSavePrefs: save });
    fireEvent.click(screen.getByRole("button", { name: "Edit your stuff" }));
    fireEvent.click(screen.getByRole("button", { name: "Pin History" }));
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
    expect(save.mock.calls[0][0].personal).toEqual({
      order: ["history", "playlists", "favorites"],
      hidden: ["history"],
    });
  });

  it("keeps a stored order when an entry it lists first is re-pinned", async () => {
    const save = vi.fn(async (_prefs: SidebarLibraryPrefs) => {});
    const stored = { order: ["history", "playlists", "favorites"], hidden: ["history"] };
    renderSidebar({ prefs: { order: [], hidden: [], personal: stored }, onSavePrefs: save });
    fireEvent.click(screen.getByRole("button", { name: "Edit your stuff" }));
    fireEvent.click(screen.getByRole("button", { name: "Pin History" }));
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
    expect(save.mock.calls[0][0].personal).toEqual({
      order: ["history", "playlists", "favorites"],
      hidden: [],
    });
  });

  it("reorders and unpins the group in its own editor and keeps the library choice", async () => {
    const save = vi.fn(async (_prefs: SidebarLibraryPrefs) => {});
    const { container } = renderSidebar({ prefs: { order: ["a"], hidden: [] }, onSavePrefs: save });
    fireEvent.click(screen.getByRole("button", { name: "Edit your stuff" }));
    // Only one group is edited at a time.
    expect(screen.queryByRole("button", { name: "Edit libraries" })).toBeNull();
    expect(screen.getByRole("button", { name: "Pin Playlists" })).toHaveFocus();
    await expectNoAxeViolations(container);

    fireEvent.click(screen.getByRole("button", { name: "Move History up" }));
    fireEvent.click(screen.getByRole("button", { name: "Pin Favorites" }));
    fireEvent.click(screen.getByRole("button", { name: "Done" }));

    await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
    expect(save.mock.calls[0][0]).toEqual({
      order: ["a"],
      hidden: [],
      personal: { order: ["playlists", "history", "favorites"], hidden: ["favorites"] },
    });
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Edit your stuff" })).toHaveFocus(),
    );
  });
});

describe("LibrarySidebar groups and dividers", () => {
  const titles: Record<string, string> = {
    a: "Films",
    b: "Music",
    c: "Podcasts",
    d: "Photos",
  };
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

  function renderSidebar({
    prefs,
    onSavePrefs,
    activeNodeId = "home",
    collapsed = false,
  }: {
    prefs?: SidebarLibraryPrefs;
    onSavePrefs?: (prefs: SidebarLibraryPrefs) => Promise<void>;
    activeNodeId?: string;
    collapsed?: boolean;
  }) {
    return render(
      <I18nProvider>
        <MemoryRouter>
          <LibrarySidebar
            id="side"
            navigation={navigation}
            libraries={libraries}
            activeNodeId={activeNodeId}
            collapsed={collapsed}
            drawer={false}
            onNavigate={() => {}}
            prefs={prefs}
            prefsReady
            onSavePrefs={onSavePrefs}
            showPersonal
          />
        </MemoryRouter>
      </I18nProvider>,
    );
  }

  it("renders collapsible groups and dividers in normal view and persists collapse state", async () => {
    const save = vi.fn(async (_prefs: SidebarLibraryPrefs) => {});
    const prefs: SidebarLibraryPrefs = {
      order: ["a", "b", "c", "d"],
      hidden: [],
      groups: [{ id: "grp-1", name: "Audio", libraries: ["b", "c"], collapsed: true }],
      layout: [
        { type: "library", id: "a" },
        { type: "divider", id: "div-1" },
        { type: "group", id: "grp-1" },
        { type: "library", id: "d" },
      ],
    };
    const { container } = renderSidebar({ prefs, onSavePrefs: save });

    expect(screen.getByRole("separator")).toBeInTheDocument();
    const toggle = screen.getByRole("button", { name: "Audio" });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("link", { name: "Music" })).toBeNull();
    expect(screen.getByRole("link", { name: "Films" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Photos" })).toBeInTheDocument();
    await expectNoAxeViolations(container);

    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByRole("link", { name: "Music" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Podcasts" })).toBeInTheDocument();
    await expectNoAxeViolations(container);
    await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
    expect(save.mock.calls[0][0].groups).toEqual([
      { id: "grp-1", name: "Audio", libraries: ["b", "c"] },
    ]);

    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("link", { name: "Music" })).toBeNull();
    await waitFor(() => expect(save).toHaveBeenCalledTimes(2));
    expect(save.mock.calls[1][0].groups).toEqual([
      { id: "grp-1", name: "Audio", libraries: ["b", "c"], collapsed: true },
    ]);
  });

  it("auto-expands a collapsed group when its Library is active, while allowing manual toggle", () => {
    const prefs: SidebarLibraryPrefs = {
      order: ["a", "b", "c", "d"],
      hidden: [],
      groups: [{ id: "grp-1", name: "Audio", libraries: ["b", "c"], collapsed: true }],
      layout: [
        { type: "library", id: "a" },
        { type: "group", id: "grp-1" },
        { type: "library", id: "d" },
      ],
    };
    const view = renderSidebar({ prefs, activeNodeId: "library-b" });
    const toggle = screen.getByRole("button", { name: "Audio" });
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByRole("link", { name: "Music" })).toHaveAttribute("aria-current", "page");

    // User can still manually collapse the group while viewing its active Library.
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("link", { name: "Music" })).toBeNull();

    // Navigating to another Library inside the collapsed group auto-expands it again.
    view.rerender(
      <I18nProvider>
        <MemoryRouter>
          <LibrarySidebar
            id="side"
            navigation={navigation}
            libraries={libraries}
            activeNodeId="library-c"
            collapsed={false}
            drawer={false}
            onNavigate={() => {}}
            prefs={prefs}
            prefsReady
          />
        </MemoryRouter>
      </I18nProvider>,
    );
    expect(screen.getByRole("button", { name: "Audio" })).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByRole("link", { name: "Podcasts" })).toHaveAttribute("aria-current", "page");

    // Leaving back to Home restores the saved collapsed state.
    view.rerender(
      <I18nProvider>
        <MemoryRouter>
          <LibrarySidebar
            id="side"
            navigation={navigation}
            libraries={libraries}
            activeNodeId="home"
            collapsed={false}
            drawer={false}
            onNavigate={() => {}}
            prefs={prefs}
            prefsReady
          />
        </MemoryRouter>
      </I18nProvider>,
    );
    expect(screen.getByRole("button", { name: "Audio" })).toHaveAttribute("aria-expanded", "false");
  });

  it("flattens grouped libraries onto the desktop icon rail while keeping dividers", () => {
    const prefs: SidebarLibraryPrefs = {
      order: ["a", "b", "c", "d"],
      hidden: [],
      groups: [{ id: "grp-1", name: "Audio", libraries: ["b", "c"], collapsed: true }],
      layout: [
        { type: "library", id: "a" },
        { type: "divider", id: "div-1" },
        { type: "group", id: "grp-1" },
        { type: "library", id: "d" },
      ],
    };
    renderSidebar({ prefs, collapsed: true });
    expect(screen.getByRole("separator")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Audio" })).toBeNull();
    expect(screen.getByRole("link", { name: "Music" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Podcasts" })).toBeInTheDocument();
  });

  it("creates, renames, populates, reorders, and deletes groups and dividers in Edit mode", async () => {
    const save = vi.fn(async (_prefs: SidebarLibraryPrefs) => {});
    const { container } = renderSidebar({ onSavePrefs: save });

    fireEvent.click(screen.getByRole("button", { name: "Edit libraries" }));

    // Add a group and rename it directly via the auto-focused input.
    fireEvent.click(screen.getByRole("button", { name: "New group" }));
    const nameInput = screen.getByRole("textbox", { name: "Group name" });
    expect(nameInput).toHaveFocus();
    fireEvent.change(nameInput, { target: { value: "Audio" } });
    fireEvent.keyDown(nameInput, { key: "Enter" });
    expect(screen.getByRole("button", { name: "Rename Audio" })).toBeInTheDocument();

    // Add a divider.
    fireEvent.click(screen.getByRole("button", { name: "Divider" }));
    expect(screen.getByRole("button", { name: "Remove Divider" })).toBeInTheDocument();

    // Assign Music and Podcasts into Audio using the group selector.
    fireEvent.change(screen.getByRole("combobox", { name: "Group for Music" }), {
      target: { value: "grp-1" },
    });
    fireEvent.change(screen.getByRole("combobox", { name: "Group for Podcasts" }), {
      target: { value: "grp-1" },
    });

    // Reorder inside the group (move Podcasts above Music).
    fireEvent.click(screen.getByRole("button", { name: "Move Podcasts up" }));
    expect(screen.getByText("Podcasts moved to position 1 of 2")).toBeInTheDocument();

    // Reorder the divider above Audio at top level.
    fireEvent.click(screen.getByRole("button", { name: "Move Divider up" }));

    await expectNoAxeViolations(container);

    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
    expect(save.mock.calls[0][0]).toEqual({
      order: ["a", "d", "c", "b"],
      hidden: [],
      groups: [{ id: "grp-1", name: "Audio", libraries: ["c", "b"] }],
      layout: [
        { type: "library", id: "a" },
        { type: "library", id: "d" },
        { type: "divider", id: "div-1" },
        { type: "group", id: "grp-1" },
      ],
    });
  });

  it("supports drag-and-drop into, within, and out of groups, plus group deletion and divider removal", async () => {
    const save = vi.fn(async (_prefs: SidebarLibraryPrefs) => {});
    const prefs: SidebarLibraryPrefs = {
      order: ["a", "b", "c", "d"],
      hidden: [],
      groups: [{ id: "grp-1", name: "Audio", libraries: ["b", "c"], collapsed: true }],
      layout: [
        { type: "library", id: "a" },
        { type: "divider", id: "div-1" },
        { type: "group", id: "grp-1" },
        { type: "library", id: "d" },
      ],
    };
    renderSidebar({ prefs, onSavePrefs: save });

    fireEvent.click(screen.getByRole("button", { name: "Edit libraries" }));

    // Drag Photos into the Audio group (dropping on Music places it at index 0 inside Audio).
    const row = (name: string) => screen.getByText(name).closest("li") as HTMLElement;
    fireEvent.dragStart(row("Photos"));
    fireEvent.dragOver(row("Music"));
    fireEvent.drop(row("Music"));

    // Rename Audio, verify Escape cancels, then commit via Done directly.
    fireEvent.click(screen.getByRole("button", { name: "Rename Audio" }));
    let input = screen.getByRole("textbox", { name: "Group name" });
    fireEvent.change(input, { target: { value: "Temp" } });
    fireEvent.keyDown(input, { key: "Escape" });
    expect(screen.getByRole("button", { name: "Rename Audio" })).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Rename Audio" }));
    input = screen.getByRole("textbox", { name: "Group name" });
    fireEvent.change(input, { target: { value: "Sound" } });
    fireEvent.click(screen.getByRole("button", { name: "Done" }));

    await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
    expect(save.mock.calls[0][0]).toEqual({
      order: ["a", "d", "b", "c"],
      hidden: [],
      groups: [{ id: "grp-1", name: "Sound", libraries: ["d", "b", "c"], collapsed: true }],
      layout: [
        { type: "library", id: "a" },
        { type: "divider", id: "div-1" },
        { type: "group", id: "grp-1" },
      ],
    });

    // Re-enter edit mode, drag Music out to top level (onto Films), move Podcasts via select,
    // test Alt+ArrowUp on a grouped row, and test numbered dividers + deletion.
    const savedPrefs = save.mock.calls[0][0];
    cleanup();
    const save2 = vi.fn(async (_prefs: SidebarLibraryPrefs) => {});
    renderSidebar({ prefs: savedPrefs, onSavePrefs: save2 });
    fireEvent.click(screen.getByRole("button", { name: "Edit libraries" }));

    // Drag Music out of Sound onto Films (top-level index 0).
    const row2 = (name: string) => screen.getByText(name).closest("li") as HTMLElement;
    fireEvent.dragStart(row2("Music"));
    fireEvent.dragOver(row2("Films"));
    fireEvent.drop(row2("Films"));

    // Alt+ArrowUp inside Sound moves Podcasts above Photos.
    fireEvent.keyDown(screen.getByRole("button", { name: "Pin Podcasts" }), {
      key: "ArrowUp",
      altKey: true,
    });
    expect(screen.getByText("Podcasts moved to position 1 of 2")).toBeInTheDocument();

    // Add a second divider -> labels disambiguate Divider 1 / Divider 2.
    fireEvent.click(screen.getByRole("button", { name: "Divider" }));
    expect(screen.getByRole("button", { name: "Remove Divider 1" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Remove Divider 2" })).toBeInTheDocument();

    // Delete Sound (returns Podcasts, Photos to top level in place), remove both dividers.
    fireEvent.click(screen.getByRole("button", { name: "Delete group Sound" }));
    fireEvent.click(screen.getByRole("button", { name: "Remove Divider 2" }));
    fireEvent.click(screen.getByRole("button", { name: "Remove Divider" }));
    fireEvent.click(screen.getByRole("button", { name: "Done" }));

    await waitFor(() => expect(save2).toHaveBeenCalledTimes(1));
    expect(save2.mock.calls[0][0]).toEqual({
      order: ["b", "a", "c", "d"],
      hidden: [],
    });
  });

  it("hides group and divider creation controls when editing Your stuff", () => {
    renderSidebar({ onSavePrefs: async () => {} });
    fireEvent.click(screen.getByRole("button", { name: "Edit your stuff" }));
    expect(screen.queryByRole("button", { name: "New group" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Divider" })).toBeNull();
  });
});
