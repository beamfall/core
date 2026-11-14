import { describe, expect, it } from "vitest";
import type { SearchResultItem } from "@/lib/graphql/types";
import type { StringKey } from "@/strings";
import { groupSearchRows } from "./searchRows";

const t = (key: StringKey) => key;

function entity(id: string, displayName: string): SearchResultItem {
  return {
    id,
    kind: "entity",
    title: id,
    mode: "keyword",
    score: 0,
    entityKind: "container",
    entityKindDisplayName: { en: displayName },
  };
}

describe("groupSearchRows", () => {
  it("keeps same-named kinds from different plugins under their own labels", () => {
    const groups = groupSearchRows(
      [entity("a", "Album"), entity("b", "Series"), entity("c", "Album")],
      t,
      ["en"],
    );

    expect(groups.map((group) => [group.label, group.rows.map((row) => row.id)])).toEqual([
      ["Album", ["a", "c"]],
      ["Series", ["b"]],
    ]);
  });
});
