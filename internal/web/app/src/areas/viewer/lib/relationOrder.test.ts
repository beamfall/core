import { describe, expect, it } from "vitest";
import type { MediaCredit, SourceItem } from "@/lib/graphql/types";
import {
  creditAttribute,
  creditRoles,
  roleGroups,
  secondsLeft,
  sequenceLabel,
} from "./relationOrder";

const item = (id: string, over: Partial<SourceItem> = {}): SourceItem => ({
  id,
  kind: "media",
  title: id,
  ...over,
});
const credit = (entityID: string, attributes?: Record<string, string>): MediaCredit => ({
  entityID,
  title: entityID,
  kind: "series",
  relation: "contains",
  attributes,
});

describe("creditAttribute", () => {
  it("reads the named attribute from the credit of the given entity only", () => {
    const credits = [credit("other", { sequence: "9" }), credit("s1", { sequence: " 4.5 " })];
    expect(creditAttribute(credits, "s1", "sequence")).toBe("4.5");
    expect(creditAttribute(credits, "s1", "role")).toBe("");
    expect(creditAttribute(credits, "missing", "sequence")).toBe("");
    expect(creditAttribute(undefined, "s1", "sequence")).toBe("");
  });
});

describe("creditRoles", () => {
  it("splits, trims, lower-cases and de-duplicates", () => {
    expect(creditRoles("Author, narrator,,author ")).toEqual(["author", "narrator"]);
    expect(creditRoles("")).toEqual([]);
  });
});

describe("sequenceLabel", () => {
  it("formats numeric sequences for the locale and keeps others as stored", () => {
    expect(sequenceLabel("4.5", "en")).toBe("4.5");
    expect(sequenceLabel("4.5", "fr")).toBe("4,5");
    expect(sequenceLabel("2", "en")).toBe("2");
    expect(sequenceLabel("IV", "en")).toBe("IV");
  });
});

describe("roleGroups", () => {
  it("orders known roles first, other roles by first appearance and unroled Media last", () => {
    const items = [
      item("a"),
      item("b"),
      item("c"),
      item("d"),
      item("e"),
      item("x", { kind: "entity" }),
    ];
    const groups = roleGroups(items, {
      a: ["narrator"],
      b: ["author", "narrator"],
      c: ["editor"],
      e: ["author"],
    });
    expect(groups.map((g) => [g.role, g.items.map((i) => i.id)])).toEqual([
      ["author", ["b", "e"]],
      ["narrator", ["a", "b"]],
      ["editor", ["c"]],
      ["", ["d"]],
    ]);
  });

  it("is empty when no Media carries a role", () => {
    expect(roleGroups([item("a"), item("b")], {})).toEqual([]);
  });
});

describe("secondsLeft", () => {
  it("never goes negative", () => {
    expect(secondsLeft({ positionSeconds: 600, durationSeconds: 3600 })).toBe(3000);
    expect(secondsLeft({ positionSeconds: 4000, durationSeconds: 3600 })).toBe(0);
  });
});
