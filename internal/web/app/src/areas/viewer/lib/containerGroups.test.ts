import { describe, expect, it } from "vitest";
import type { Section, SourceItem } from "@/lib/graphql/types";
import { groupByContainer, groupsNewByContainer } from "./containerGroups";

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
      summary: "3 new",
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
