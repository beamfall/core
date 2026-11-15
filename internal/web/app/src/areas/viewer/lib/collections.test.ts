import { afterEach, describe, expect, it, vi } from "vitest";
import { graphql } from "@/lib/api";
import type { CollectionInfo } from "@/lib/graphql/types";
import {
  COLLECTION_PAGE,
  fetchCollectionItems,
  fetchEntryLocks,
  MAX_COLLECTION_ENTRIES,
  memberOrder,
  moveEntry,
  sortCollections,
} from "./collections";

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return { ...actual, graphql: vi.fn() };
});

afterEach(() => {
  vi.mocked(graphql).mockReset();
});

function collection(partial: Partial<CollectionInfo>): CollectionInfo {
  return {
    id: "c1",
    libraryID: "lib",
    name: "Set",
    sortOrder: 0,
    memberCount: 0,
    members: [],
    ...partial,
  };
}

describe("moveEntry", () => {
  it("swaps an entry with its visible neighbour and leaves hidden members in place", () => {
    const members = ["a", "hidden", "b", "c"];
    const visible = ["a", "b", "c"];
    expect(moveEntry(members, visible, "b", -1)).toEqual(["b", "hidden", "a", "c"]);
    expect(moveEntry(members, visible, "b", 1)).toEqual(["a", "hidden", "c", "b"]);
  });

  it("refuses moves past either end or of an unknown entry", () => {
    expect(moveEntry(["a", "b"], ["a", "b"], "a", -1)).toBeNull();
    expect(moveEntry(["a", "b"], ["a", "b"], "b", 1)).toBeNull();
    expect(moveEntry(["a", "b"], ["a", "b"], "z", 1)).toBeNull();
  });
});

describe("collection ordering", () => {
  it("reads members in curated order", () => {
    const value = collection({
      members: [
        { mediaID: "late", sortOrder: 3, addedAt: "" },
        { mediaID: "early", sortOrder: 1, addedAt: "" },
      ],
    });
    expect(memberOrder(value)).toEqual(["early", "late"]);
  });

  it("lists collections by sort order, then name", () => {
    const sorted = sortCollections([
      collection({ id: "3", name: "Beta", sortOrder: 1 }),
      collection({ id: "2", name: "Alpha", sortOrder: 1 }),
      collection({ id: "1", name: "Zed", sortOrder: 0 }),
    ]);
    expect(sorted.map((entry) => entry.id)).toEqual(["1", "2", "3"]);
  });
});

describe("fetchCollectionItems", () => {
  it("reads the collection feed page by page until a short page", async () => {
    const full = Array.from({ length: COLLECTION_PAGE }, (_, i) => ({
      id: `m${i}`,
      kind: "media",
      title: `Item ${i}`,
    }));
    vi.mocked(graphql)
      .mockResolvedValueOnce({ sourceItems: full })
      .mockResolvedValueOnce({ sourceItems: [{ id: "last", kind: "media", title: "Last" }] });

    const items = await fetchCollectionItems("c1");

    expect(items).toHaveLength(COLLECTION_PAGE + 1);
    expect(graphql).toHaveBeenCalledTimes(2);
    const [query, vars] = vi.mocked(graphql).mock.calls[1];
    expect(query).toContain('kind: "collection"');
    expect(vars).toEqual({ value: "c1", limit: COLLECTION_PAGE, offset: COLLECTION_PAGE });
  });
});

describe("fetchEntryLocks (CORE-1876)", () => {
  it("reads the lock state across every page, past a page shortened by muting", async () => {
    const short = Array.from({ length: COLLECTION_PAGE - 1 }, (_, i) => ({
      id: `m${i}`,
      locked: i === 3,
    }));
    vi.mocked(graphql)
      .mockResolvedValueOnce({ sourceItems: short })
      .mockResolvedValueOnce({ sourceItems: [{ id: "last", locked: true }, { id: "open" }] });

    const locks = await fetchEntryLocks("c1", COLLECTION_PAGE + 2);

    expect([...locks].filter(([, locked]) => locked).map(([id]) => id)).toEqual(["m3", "last"]);
    expect(locks.get("open")).toBe(false);
    expect(locks.has("muted")).toBe(false);
    expect(graphql).toHaveBeenCalledTimes(2);
    const [query, vars] = vi.mocked(graphql).mock.calls[1];
    expect(query).toContain("locked");
    expect(vars).toEqual({ value: "c1", limit: COLLECTION_PAGE, offset: COLLECTION_PAGE });
  });

  it("reads past the page-view bound when the playlist is longer", async () => {
    vi.mocked(graphql).mockResolvedValue({ sourceItems: [] });

    await fetchEntryLocks("c1", MAX_COLLECTION_ENTRIES + 1);

    const offsets = vi
      .mocked(graphql)
      .mock.calls.map(([, vars]) => (vars as { offset: number }).offset);
    expect(offsets.at(-1)).toBe(MAX_COLLECTION_ENTRIES);
  });
});
