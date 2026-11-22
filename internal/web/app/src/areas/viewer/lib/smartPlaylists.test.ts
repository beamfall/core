import { afterEach, describe, expect, it, vi } from "vitest";
import {
  createSmartCollection,
  fetchSmartCollectionItems,
  fetchSmartCollections,
} from "@/lib/graphql";
import type { NavigationManifest, SourceItem } from "@/lib/graphql/types";
import {
  loadSmartPlaylistItems,
  loadSmartPlaylists,
  parseSmartRules,
  parseSmartSort,
  type SmartPlaylist,
  type SmartRuleSet,
  saveSmartPlaylist,
  serializeSmartRules,
  smartFieldOptions,
  smartOperators,
  smartRulesComplete,
  withSmartField,
} from "./smartPlaylists";

vi.mock("@/lib/graphql", () => ({
  createSmartCollection: vi.fn(),
  deleteSmartCollection: vi.fn(),
  fetchSmartCollectionItems: vi.fn(),
  fetchSmartCollections: vi.fn(),
  updateSmartCollection: vi.fn(),
}));

afterEach(() => {
  vi.mocked(createSmartCollection).mockReset();
  vi.mocked(fetchSmartCollectionItems).mockReset();
  vi.mocked(fetchSmartCollections).mockReset();
});

// feature:collection-editor (CORE-2771 smart playlists)
describe("smart playlist rule expressions", () => {
  const set: SmartRuleSet = {
    match: "any",
    rules: [
      { field: "duration", op: "gt", value: "4.5" },
      { field: "entity:genre", op: "is", value: " Ambient " },
      { field: "field:bpm", op: "is", value: "120" },
      { field: "field:explicit", op: "isFalse", value: "ignored" },
      { field: "lastPlayed", op: "notInLast", value: "30" },
    ],
    limit: 50,
  };

  it("serializes durations in seconds, numeric plugin values as numbers and flags without a value", () => {
    expect(JSON.parse(serializeSmartRules(set))).toEqual({
      kind: "rules",
      match: "any",
      rules: [
        { field: "duration", op: "gt", value: 270 },
        { field: "entity:genre", op: "is", value: "Ambient" },
        { field: "field:bpm", op: "is", value: 120 },
        { field: "field:explicit", op: "isFalse" },
        { field: "lastPlayed", op: "notInLast", value: 30 },
      ],
      limit: 50,
    });
  });

  it("parses its own serialization back to the editor form", () => {
    const parsed = parseSmartRules(serializeSmartRules(set));
    expect(parsed?.match).toBe("any");
    expect(parsed?.limit).toBe(50);
    expect(parsed?.rules[0]).toEqual({ field: "duration", op: "gt", value: "4.5" });
    expect(parsed?.rules[2]).toEqual({ field: "field:bpm", op: "is", value: "120" });
    expect(parsed?.rules[3]).toEqual({ field: "field:explicit", op: "isFalse", value: "" });
  });

  it("ignores Smart Collections that are not rule sources", () => {
    expect(parseSmartRules('{"kind":"filter","field":"year"}')).toBeNull();
    expect(parseSmartRules("not json")).toBeNull();
    expect(parseSmartSort('{"by":"random"}')).toEqual({ by: "random", dir: "asc" });
    expect(parseSmartSort("")).toEqual({ by: "title", dir: "asc" });
  });

  it("is complete only when every rule has a usable value and the limit is in range", () => {
    expect(smartRulesComplete(set)).toBe(true);
    expect(smartRulesComplete({ match: "all", rules: [], limit: null })).toBe(true);
    const blank = { match: "all", rules: [{ field: "title", op: "is", value: " " }], limit: null };
    expect(smartRulesComplete(blank as SmartRuleSet)).toBe(false);
    const badDays = { match: "all", rules: [{ field: "lastPlayed", op: "inLast", value: "0" }] };
    expect(smartRulesComplete({ ...badDays, limit: null } as SmartRuleSet)).toBe(false);
    const badDate = { match: "all", rules: [{ field: "lastPlayed", op: "before", value: "May" }] };
    expect(smartRulesComplete({ ...badDate, limit: null } as SmartRuleSet)).toBe(false);
    expect(smartRulesComplete({ ...set, limit: 5001 })).toBe(false);
    const unknownField = { match: "all", rules: [{ field: "genre", op: "is", value: "x" }] };
    expect(smartRulesComplete({ ...unknownField, limit: null } as SmartRuleSet)).toBe(false);
  });

  it("keeps the operator and value across a field change only when they still fit", () => {
    expect(smartOperators("year")).not.toContain("contains");
    expect(withSmartField({ field: "year", op: "gt", value: "1990" }, "playCount")).toEqual({
      field: "playCount",
      op: "gt",
      value: "1990",
    });
    expect(withSmartField({ field: "year", op: "gt", value: "1990" }, "title")).toEqual({
      field: "title",
      op: "is",
      value: "",
    });
  });

  it("offers the entity kinds and plugin fields the manifest names", () => {
    const manifest = {
      layouts: [
        {
          entityKind: "person",
          sections: [
            {
              source: { entityFilter: { kind: "genre" }, fieldFilters: { bpm: "120" } },
              filters: [{ field: "mood" }],
            },
          ],
        },
      ],
    } as unknown as NavigationManifest;
    expect(smartFieldOptions(manifest)).toEqual({
      entityKinds: ["genre", "person"],
      pluginFields: ["bpm", "mood"],
    });
    expect(smartFieldOptions(null)).toEqual({ entityKinds: [], pluginFields: [] });
  });
});

describe("smart playlist reads and writes", () => {
  const playlist: SmartPlaylist = {
    id: "smart-1",
    libraryID: "lib-a",
    name: "Long ones",
    rules: { match: "all", rules: [], limit: 450 },
    sort: { by: "title", dir: "asc" },
  };

  function items(count: number, start: number): SourceItem[] {
    return Array.from({ length: count }, (_, i) => ({
      id: `m-${start + i}`,
      kind: "",
      title: `Item ${start + i}`,
    }));
  }

  it("lists only rule-based Smart Collections across Libraries", async () => {
    vi.mocked(fetchSmartCollections).mockImplementation(async (libraryID: string) =>
      libraryID === "lib-a"
        ? [
            {
              id: "smart-1",
              libraryID,
              name: "Long ones",
              sourceExpr: '{"kind":"rules","match":"all","rules":[]}',
              sortExpr: '{"by":"year","dir":"desc"}',
            },
            { id: "old", libraryID, name: "Filter", sourceExpr: '{"kind":"filter"}', sortExpr: "" },
          ]
        : Promise.reject(new Error("forbidden")),
    );
    const list = await loadSmartPlaylists(["lib-a", "lib-b"]);
    expect(list.map((entry) => entry.id)).toEqual(["smart-1"]);
    expect(list[0].sort).toEqual({ by: "year", dir: "desc" });
  });

  it("creates the playlist with the serialized rule and sort expressions", async () => {
    vi.mocked(createSmartCollection).mockImplementation(async (input) => ({
      id: "smart-2",
      libraryID: input.libraryID,
      name: input.name,
      sourceExpr: input.sourceExpr,
      sortExpr: input.sortExpr ?? "",
    }));
    const saved = await saveSmartPlaylist({
      libraryID: "lib-a",
      name: "  Recent  ",
      rules: { match: "all", rules: [{ field: "playCount", op: "gte", value: "3" }], limit: null },
      sort: { by: "lastPlayed", dir: "desc" },
    });
    expect(createSmartCollection).toHaveBeenCalledWith({
      libraryID: "lib-a",
      name: "Recent",
      sourceExpr:
        '{"kind":"rules","match":"all","rules":[{"field":"playCount","op":"gte","value":3}]}',
      sortExpr: '{"by":"lastPlayed","dir":"desc"}',
    });
    expect(saved?.rules.rules).toEqual([{ field: "playCount", op: "gte", value: "3" }]);
  });

  it("reads items in pages up to the playlist limit", async () => {
    vi.mocked(fetchSmartCollectionItems).mockImplementation(async (_id, limit, offset) =>
      items(limit, offset),
    );
    const list = await loadSmartPlaylistItems(playlist);
    expect(list).toHaveLength(450);
    expect(
      vi.mocked(fetchSmartCollectionItems).mock.calls.map((call) => [call[1], call[2]]),
    ).toEqual([
      [200, 0],
      [200, 200],
      [50, 400],
    ]);
  });

  it("stops at a short page and at the read cap", async () => {
    vi.mocked(fetchSmartCollectionItems).mockResolvedValueOnce(items(200, 0));
    vi.mocked(fetchSmartCollectionItems).mockResolvedValueOnce(items(7, 200));
    expect(
      await loadSmartPlaylistItems({ ...playlist, rules: { ...playlist.rules, limit: null } }),
    ).toHaveLength(207);

    vi.mocked(fetchSmartCollectionItems).mockReset();
    vi.mocked(fetchSmartCollectionItems).mockImplementation(async (_id, limit, offset) =>
      items(limit, offset),
    );
    expect(
      await loadSmartPlaylistItems({ ...playlist, rules: { ...playlist.rules, limit: null } }),
    ).toHaveLength(1000);
  });
});
