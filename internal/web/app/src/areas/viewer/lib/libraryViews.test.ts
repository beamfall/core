import { describe, expect, it } from "vitest";
import { DEFAULT_BROWSE_STATE, parseBrowseState } from "./libraryBrowse";
import { FOLDER_VIEW } from "./libraryFolders";
import {
  entityCardLine,
  type LibraryEntityItem,
  libraryCategorySearch,
  libraryEntitiesVariables,
  libraryViews,
  MEDIA_VIEW,
  selectedView,
  viewSearchParams,
} from "./libraryViews";

const kinds = [
  { kind: "maker", label: { en: "Makers", fr: "Créateurs" }, total: 2 },
  { kind: "record", kindDisplayName: { en: "Record" }, total: 5 },
  { kind: "bare", total: 1 },
];

function entity(patch: Partial<LibraryEntityItem>): LibraryEntityItem {
  return {
    id: "e1",
    kind: "entity",
    title: "One",
    childCount: 0,
    mediaCount: 0,
    parents: [],
    ...patch,
  };
}

describe("library views (CORE-2362)", () => {
  it("lists one view per kind in server order, then the Media and Folders views", () => {
    expect(libraryViews(kinds, { en: "Library" }, "en", "All items", "Folders")).toEqual([
      { id: "maker", kind: "maker", label: "Makers" },
      { id: "record", kind: "record", label: "Record" },
      { id: "bare", kind: "bare", label: "bare" },
      { id: MEDIA_VIEW, label: "Library" },
      { id: FOLDER_VIEW, label: "Folders" },
    ]);
  });

  it("labels views in the display locale and falls back for the Media view", () => {
    const views = libraryViews(kinds, undefined, "fr", "Tous les éléments", "Dossiers");
    expect(views[0].label).toBe("Créateurs");
    expect(views.at(-2)).toEqual({ id: MEDIA_VIEW, label: "Tous les éléments" });
    expect(views.at(-1)).toEqual({ id: FOLDER_VIEW, label: "Dossiers" });
  });

  it("offers the Media and Folders views when the Library has no browse kinds", () => {
    expect(libraryViews([], { en: "Library" }, "en", "All items", "Folders")).toEqual([
      { id: MEDIA_VIEW, label: "Library" },
      { id: FOLDER_VIEW, label: "Folders" },
    ]);
  });

  it("selects the named view, else the first", () => {
    const views = libraryViews(kinds, undefined, "en", "All items", "Folders");
    expect(selectedView(views, "record")?.id).toBe("record");
    expect(selectedView(views, MEDIA_VIEW)?.id).toBe(MEDIA_VIEW);
    expect(selectedView(views, "gone")?.id).toBe("maker");
    expect(selectedView(views, null)?.id).toBe("maker");
    expect(selectedView([], "maker")).toBeUndefined();
  });

  it("sets the view and keeps the other search parameters", () => {
    const current = new URLSearchParams("sort=year&view=maker");
    const next = viewSearchParams(current, MEDIA_VIEW);
    expect(next.toString()).toBe("sort=year&view=media");
    expect(current.get("view")).toBe("maker");
  });

  it("names a card's parents, else counts its Media, else says nothing", () => {
    const count = (n: number) => `${n} items`;
    expect(
      entityCardLine(
        entity({
          mediaCount: 4,
          parents: [
            { id: "p1", title: "First" },
            { id: "p2", title: "" },
            { id: "p3", title: "Second" },
          ],
        }),
        count,
      ),
    ).toBe("First, Second");
    expect(entityCardLine(entity({ mediaCount: 4 }), count)).toBe("4 items");
    expect(entityCardLine(entity({}), count)).toBe("");
  });
});

describe("library category search (CORE-2437)", () => {
  it("opens the Media view filtered to one grouping value and nothing else", () => {
    const search = libraryCategorySearch("genre", "Sci-Fi & Fantasy");
    const params = new URLSearchParams(search);
    expect(params.get("view")).toBe("media");
    const state = parseBrowseState(params);
    expect(state.groups).toEqual({ genre: "Sci-Fi & Fantasy" });
    expect(state.watch).toBeFalsy();
  });
});

describe("library entities variables (CORE-2440)", () => {
  it("sends the kind with title order when no browse state is given", () => {
    expect(
      libraryEntitiesVariables({ libraryID: "lib", kind: "maker", limit: 10, offset: 20 }),
    ).toEqual({
      libraryID: "lib",
      kind: "maker",
      sort: "title",
      direction: "asc",
      limit: 10,
      offset: 20,
    });
  });

  it("sends the Media view's sort and filters under the same names", () => {
    const state = parseBrowseState(
      new URLSearchParams(
        "q=space+rock&sort=year&dir=desc&watch=unwatched&group.genre=Rock&decade=1990&quality=uhd&rated=8",
      ),
    );
    expect(libraryEntitiesVariables({ libraryID: "lib", state, limit: 100, offset: 0 })).toEqual({
      libraryID: "lib",
      query: "space rock",
      sort: "year",
      direction: "desc",
      watch: "unwatched",
      groupFilters: { genre: "Rock" },
      yearFrom: 1990,
      yearTo: 1999,
      quality: "uhd",
      minRating: 8,
      limit: 100,
      offset: 0,
    });
    expect(
      libraryEntitiesVariables({
        libraryID: "lib",
        state: DEFAULT_BROWSE_STATE,
        limit: 1,
        offset: 0,
      }),
    ).not.toHaveProperty("kind");
  });
});
