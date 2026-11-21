import { describe, expect, it } from "vitest";
import type { Section, SourceItem } from "@/lib/graphql/types";
import {
  foldNewShelf,
  groupByContainer,
  groupsNewByContainer,
  NEW_SHELF_CARDS,
  NEW_SHELF_FETCH,
} from "./containerGroups";

function episode(id: string, show?: string): SourceItem {
  return {
    id,
    kind: "media",
    title: id,
    mediaClass: "video",
    container: show
      ? { id: `show-${show}`, title: show, artwork: { poster: `/art/${show}` } }
      : undefined,
  };
}

const label = (count: number) => `${count} new`;

describe("groupByContainer (CORE-2524)", () => {
  it("folds siblings into one container card at the first sibling's place", () => {
    const grouped = groupByContainer(
      [
        episode("a1", "Alpha"),
        episode("film"),
        episode("a2", "Alpha"),
        episode("b1", "Beta"),
        episode("a3", "Alpha"),
      ],
      label,
    );
    expect(grouped.map((item) => item.id)).toEqual(["show-Alpha", "film", "b1"]);
    expect(grouped[0]).toMatchObject({
      kind: "entity",
      title: "Alpha",
      subtitle: "3 new",
      artwork: { poster: "/art/Alpha" },
    });
  });

  it("leaves a lone item and items without a container as they are", () => {
    const items = [episode("b1", "Beta"), episode("film")];
    expect(groupByContainer(items, label)).toEqual(items);
  });
});

describe("groupsNewByContainer", () => {
  const section = (kind: string, source: string) =>
    ({
      id: "s",
      kind,
      title: "",
      artworkSlot: "poster",
      source: { kind: source },
    }) as Section;

  it("applies to a recently-added shelf only", () => {
    expect(groupsNewByContainer(section("shelf", "recently-added"))).toBe(true);
    expect(groupsNewByContainer(section("grid", "recently-added"))).toBe(false);
    expect(groupsNewByContainer(section("shelf", "continue-watching"))).toBe(false);
  });
});

describe("foldNewShelf (CORE-2823)", () => {
  it("refills the shelf when one show dominates the newest items", () => {
    // A season of 40 episodes lands ahead of 12 films and 3 other shows: the
    // first 20 items alone would fold to one card.
    const items = [
      ...Array.from({ length: 40 }, (_, i) => episode(`s${i}`, "Saga")),
      ...Array.from({ length: 12 }, (_, i) => episode(`film${i}`)),
      ...["Alpha", "Beta", "Gamma"].flatMap((show) => [
        episode(`${show}-1`, show),
        episode(`${show}-2`, show),
      ]),
    ];
    expect(items.length).toBeLessThanOrEqual(NEW_SHELF_FETCH);
    expect(foldNewShelf(items.slice(0, NEW_SHELF_CARDS), label)).toHaveLength(1);

    const shelf = foldNewShelf(items, label);
    expect(shelf).toHaveLength(16);
    expect(shelf[0]).toMatchObject({ id: "show-Saga", subtitle: "40 new" });
    expect(shelf.filter((item) => item.kind === "media")).toHaveLength(12);
    expect(shelf.filter((item) => item.kind === "entity")).toHaveLength(4);
  });

  it("stops at the shelf's card count", () => {
    const films = Array.from({ length: 50 }, (_, i) => episode(`film${i}`));
    expect(foldNewShelf(films, label)).toHaveLength(NEW_SHELF_CARDS);
  });

  it("counts only unfinished items, and names no count when every one is finished", () => {
    const done = (item: SourceItem): SourceItem => ({ ...item, engagement: { completed: true } });
    const [partly, allDone] = foldNewShelf(
      [
        done(episode("a1", "Alpha")),
        episode("a2", "Alpha"),
        episode("a3", "Alpha"),
        done(episode("b1", "Beta")),
        done(episode("b2", "Beta")),
      ],
      label,
    );
    expect(partly).toMatchObject({ id: "show-Alpha", subtitle: "2 new" });
    expect(allDone).toMatchObject({ id: "show-Beta" });
    expect(allDone.subtitle).toBeUndefined();
  });
});
