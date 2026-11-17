import { describe, expect, it } from "vitest";
import type { SourceItem } from "@/lib/graphql/types";
import { cardSubline, repeatedTitles, showsYear } from "./cardTitles";

function item(id: string, title: string, year?: number, summary?: string): SourceItem {
  return { id, kind: "media", title, year, summary } as SourceItem;
}

describe("card titles (CORE-2320)", () => {
  it("finds titles repeated in a list, ignoring case and spacing", () => {
    const repeated = repeatedTitles([
      item("a", "Dune", 1984),
      item("b", "dune ", 2021),
      item("c", "Arrival", 2016),
    ]);
    expect([...repeated]).toEqual(["dune"]);
  });

  it("shows the year only on a repeated title that has one", () => {
    const items = [item("a", "Dune", 1984), item("b", "Dune"), item("c", "Arrival", 2016)];
    const repeated = repeatedTitles(items);
    expect(items.map((entry) => showsYear(entry, repeated))).toEqual([true, false, false]);
  });

  it("puts the year before the summary and never doubles it", () => {
    expect(cardSubline(item("a", "Dune", 2021, "Part one"), true)).toBe("2021 · Part one");
    expect(cardSubline(item("a", "Dune", 2021), true)).toBe("2021");
    expect(cardSubline(item("a", "Dune", 2021, "2021"), true)).toBe("2021");
    expect(cardSubline(item("a", "Dune", 2021, "Part one"), false)).toBe("Part one");
  });
});
