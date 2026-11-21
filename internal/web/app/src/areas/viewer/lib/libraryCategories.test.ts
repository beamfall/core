import { describe, expect, it } from "vitest";
import type { SourceItem } from "@/lib/graphql/types";
import {
  CATEGORY_TONES,
  categoryCovers,
  categoryInitial,
  categoryTone,
  categoryValues,
  FEATURED_MAX,
  foldForSearch,
  groupByInitial,
  planCategories,
  runBounded,
  singularLabel,
  TOP_COUNT,
  takeCategoryArt,
} from "./libraryCategories";
import type { LibraryEntityKind } from "./libraryViews";

function many(prefix: string, n: number) {
  return Array.from({ length: n }, (_, i) => `${prefix} ${String(i).padStart(3, "0")}`);
}

describe("planCategories (CORE-2505)", () => {
  it("features short groupings first, drops empty ones and keeps a long tail's most used", () => {
    const studios = many("Studio", FEATURED_MAX + 5);
    const counts = studios.map((_, i) => (i < 3 ? 1 : i));
    const plans = planCategories([
      { kind: "studio", values: studios, counts },
      { kind: "country", values: [] },
      { kind: "genre", values: ["Drama", "Comedy"], counts: [5, 1] },
    ]);

    expect(plans.map((plan) => [plan.kind, plan.featured])).toEqual([
      ["genre", true],
      ["studio", false],
    ]);
    // A featured grouping keeps every value, even a single-item one.
    expect(plans[0].values).toEqual([
      { value: "Drama", count: 5 },
      { value: "Comedy", count: 1 },
    ]);
    const top = plans[1].top;
    expect(top).toHaveLength(TOP_COUNT);
    expect(top[0]).toEqual({ value: studios.at(-1), count: studios.length - 1 });
    expect(top.every((entry) => (entry.count ?? 0) >= 2)).toBe(true);
  });

  it("orders equal counts by title and shows no top list without counts", () => {
    const values = many("Net", FEATURED_MAX + 1);
    const tied = planCategories([{ kind: "network", values, counts: values.map(() => 3) }]);
    expect(tied[0].top.map((entry) => entry.value)).toEqual(values.slice(0, TOP_COUNT));

    const bare = planCategories([{ kind: "network", values }]);
    expect(bare[0].top).toEqual([]);
    expect(bare[0].values[0]).toEqual({ value: values[0] });
  });

  it("ignores counts that do not line up with the values", () => {
    expect(categoryValues({ kind: "genre", values: ["A", "B"], counts: [1] })).toEqual([
      { value: "A" },
      { value: "B" },
    ]);
  });
});

describe("A-Z index (CORE-2505)", () => {
  it("files values under their initial, ignoring case and accents, digits under #", () => {
    expect(categoryInitial("élan")).toBe("E");
    expect(categoryInitial("20th Century")).toBe("#");
    expect(foldForSearch("Pathé")).toBe("pathe");

    const groups = groupByInitial([
      { value: "Warner Bros." },
      { value: "20th Century" },
      { value: "amblin" },
      { value: "Éclair" },
      { value: "A24" },
    ]);
    expect(groups.map((group) => [group.letter, group.values.map((v) => v.value)])).toEqual([
      ["#", ["20th Century"]],
      ["A", ["A24", "amblin"]],
      ["E", ["Éclair"]],
      ["W", ["Warner Bros."]],
    ]);
  });

  it("filters by a folded substring", () => {
    const values = [{ value: "Warner Bros." }, { value: "Pathé" }, { value: "Warner Home" }];
    expect(groupByInitial(values, " WARNER ").flatMap((g) => g.values.map((v) => v.value))).toEqual(
      ["Warner Bros.", "Warner Home"],
    );
    expect(groupByInitial(values, "pathe")[0].values[0].value).toBe("Pathé");
    expect(groupByInitial(values, "zzz")).toEqual([]);
  });
});

describe("takeCategoryArt (CORE-2505)", () => {
  const item = (id: string, slots: Record<string, string>) =>
    ({ id, artworkSlots: slots }) as unknown as SourceItem;

  it("prefers a backdrop and never repeats a picture or an item", () => {
    const used = new Set<string>();
    const a = item("a", { backdrop: "/a-back", poster: "/a-post" });
    const b = item("b", { backdrop: "/shared", poster: "/b-post" });
    const c = item("c", { backdrop: "/shared" });

    expect(takeCategoryArt([a, b], used)).toBe("/a-back");
    expect(takeCategoryArt([a, b], used)).toBe("/shared");
    // c's backdrop is b's, and a and b are used: nothing left.
    expect(takeCategoryArt([a, c], used)).toBe("");
    expect(takeCategoryArt([item("d", { poster: "/d-post" })], used)).toBe("/d-post");
  });
});

describe("category card fallbacks (CORE-2885)", () => {
  const kinds: LibraryEntityKind[] = [
    { kind: "a", label: { en: "Units", fr: "Unités" }, kindDisplayName: { en: "Unit" }, total: 2 },
    { kind: "b", label: { en: "Sets" }, kindDisplayName: {}, total: 1 },
  ];

  it("finds the singular by the browse label, every locale matching", () => {
    expect(singularLabel({ en: "Units", fr: "Unités" }, kinds)).toEqual({ en: "Unit" });
    expect(singularLabel({ en: "Units" }, kinds)).toBeUndefined();
    expect(singularLabel({ en: "Sets" }, kinds)).toBeUndefined();
    expect(singularLabel({}, kinds)).toBeUndefined();
  });

  it("keeps the Media count behind a container count", () => {
    const [entry] = categoryValues(
      {
        kind: "genre",
        values: ["x"],
        counts: [9],
        browseCounts: [1],
        browseLabels: [{ en: "Units", fr: "Unités" }],
      },
      kinds,
    );
    expect(entry).toEqual({
      value: "x",
      count: 1,
      label: { en: "Units", fr: "Unités" },
      labelOne: { en: "Unit" },
      items: 9,
    });
  });

  it("takes distinct container covers in mosaic shapes of one, two or four", () => {
    const item = (container?: string) =>
      ({
        id: container ?? "loose",
        kind: "media",
        title: "",
        container: container
          ? { id: container, title: "", artwork: { poster: container } }
          : undefined,
      }) as unknown as SourceItem;
    expect(categoryCovers([item("a"), item(), item("a")])).toEqual(["a"]);
    expect(categoryCovers(["a", "b", "c"].map(item))).toEqual(["a", "b"]);
    expect(categoryCovers(["a", "b", "c", "d", "e"].map(item))).toEqual(["a", "b", "c", "d"]);
    expect(categoryCovers([])).toEqual([]);
  });

  it("picks the same tone for the same name, folding case and accents", () => {
    expect(categoryTone("Électro")).toBe(categoryTone("electro "));
    const tones = new Set(many("Value", 50).map(categoryTone));
    expect(tones.size).toBeGreaterThan(3);
    for (const tone of tones) expect(tone).toBeLessThan(CATEGORY_TONES);
  });
});

describe("runBounded (CORE-2505)", () => {
  it("runs at most the limit at once and reports a failure as null", async () => {
    let running = 0;
    let peak = 0;
    const reads = [1, 2, 3, 4, 5].map((n) => async () => {
      running++;
      peak = Math.max(peak, running);
      await new Promise((resolve) => setTimeout(resolve, 1));
      running--;
      if (n === 3) throw new Error("down");
      return n;
    });
    const seen: [number, number | null][] = [];
    await runBounded(reads, 2, (index, value) => seen.push([index, value]));

    expect(peak).toBe(2);
    expect(seen.sort((a, b) => a[0] - b[0])).toEqual([
      [0, 1],
      [1, 2],
      [2, null],
      [3, 4],
      [4, 5],
    ]);
  });

  it("stops after an abort", async () => {
    const controller = new AbortController();
    const seen: number[] = [];
    const reads = [0, 1, 2].map((n) => async () => {
      controller.abort();
      return n;
    });
    await runBounded(reads, 1, (index) => seen.push(index), controller.signal);
    expect(seen).toEqual([]);
  });
});
