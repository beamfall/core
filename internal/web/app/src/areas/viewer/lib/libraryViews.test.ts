import { describe, expect, it } from "vitest";
import {
  entityCardLine,
  type LibraryEntityItem,
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
  it("lists one view per kind in server order, then the Media view", () => {
    expect(libraryViews(kinds, { en: "Library" }, "en", "All items")).toEqual([
      { id: "maker", kind: "maker", label: "Makers" },
      { id: "record", kind: "record", label: "Record" },
      { id: "bare", kind: "bare", label: "bare" },
      { id: MEDIA_VIEW, label: "Library" },
    ]);
  });

  it("labels views in the display locale and falls back for the Media view", () => {
    const views = libraryViews(kinds, undefined, "fr", "Tous les éléments");
    expect(views[0].label).toBe("Créateurs");
    expect(views.at(-1)).toEqual({ id: MEDIA_VIEW, label: "Tous les éléments" });
  });

  it("offers no views when the Library has no browse kinds", () => {
    expect(libraryViews([], { en: "Library" }, "en", "All items")).toEqual([]);
  });

  it("selects the named view, else the first", () => {
    const views = libraryViews(kinds, undefined, "en", "All items");
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
