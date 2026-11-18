import { describe, expect, it } from "vitest";
import type { SearchResultItem } from "@/lib/graphql/types";
import type { StringKey } from "@/strings";
import {
  groupSearchRows,
  nestUnderContainers,
  SEARCH_RESULT_CAP,
  searchCountLabel,
} from "./searchRows";

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

// CORE-2321: the server returns at most SEARCH_RESULT_CAP rows, so a full page
// must not read as the whole answer.
describe("searchCountLabel", () => {
  const label = (key: StringKey, values?: Record<string, string | number>) =>
    `${key}:${values?.count}`;

  it("counts a short answer", () => {
    expect(searchCountLabel(label, 12, "dune")).toBe("searchResultCount:12");
  });

  it("says a full page is only the first matches", () => {
    expect(searchCountLabel(label, SEARCH_RESULT_CAP, "dune")).toBe(
      `searchResultCountCapped:${SEARCH_RESULT_CAP}`,
    );
  });
});

describe("nestUnderContainers (CORE-2525)", () => {
  function media(id: string, title: string, container?: string): SearchResultItem {
    return {
      id,
      kind: "media",
      title,
      mode: "keyword",
      score: 0,
      mediaClass: "video",
      container: container ? { id: container, title: container } : undefined,
    };
  }

  it("folds rows found through a container that is itself a result", () => {
    const show = { ...entity("show", "Series"), title: "Harbour" };
    const { rows, nested } = nestUnderContainers(
      [
        show,
        media("e1", "Arrival", "show"),
        media("e2", "Harbour Lights", "show"),
        media("e3", "Departure", "show"),
        media("x1", "Return", "other"),
        media("f1", "Harbour Notes"),
      ],
      "harbour",
    );
    expect(rows.map((row) => row.id)).toEqual(["show", "e2", "x1", "f1"]);
    expect(nested.get("show")?.map((row) => row.id)).toEqual(["e1", "e3"]);
  });
});
