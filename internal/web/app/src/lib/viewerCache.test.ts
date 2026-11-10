import { afterEach, describe, expect, it } from "vitest";
import type { SessionInfo } from "@/lib/graphql/types";
import {
  clearViewerCache,
  deleteViewerCache,
  readViewerCache,
  VIEWER_CACHE_MAX_ENTRIES,
  viewerCacheEntries,
  viewerCacheSize,
  viewerIdentityKey,
  writeViewerCache,
} from "./viewerCache";

const base: SessionInfo = {
  deviceRole: "viewer",
  profileID: "p1",
  profile: "adult",
  revealed: false,
};

afterEach(() => clearViewerCache());

describe("viewerCache", () => {
  it("scopes every identity dimension: Profile, Reveal state and device role", () => {
    const identity = viewerIdentityKey(base);
    const others = [
      { ...base, profileID: "p2" },
      { ...base, revealed: true },
      { ...base, deviceRole: "manager" },
      { ...base, profile: "managed" },
    ].map(viewerIdentityKey);

    writeViewerCache(identity, "section:x", ["a"]);

    expect(readViewerCache(identity, "section:x")).toEqual(["a"]);
    for (const other of others) {
      expect(other).not.toBe(identity);
      expect(readViewerCache(other, "section:x")).toBeUndefined();
      expect(viewerCacheEntries(other)).toEqual([]);
    }
  });

  it("never stores or serves anything without a bound identity", () => {
    expect(viewerIdentityKey(null)).toBeNull();
    writeViewerCache(null, "section:x", ["a"]);
    expect(viewerCacheSize()).toBe(0);
    expect(readViewerCache(null, "section:x")).toBeUndefined();
    expect(viewerCacheEntries(null)).toEqual([]);
  });

  it("is bounded and evicts the least recently used entry first", () => {
    const identity = viewerIdentityKey(base);
    for (let i = 0; i < VIEWER_CACHE_MAX_ENTRIES; i++) writeViewerCache(identity, `k${i}`, i);
    // Touch the oldest so k1 becomes the eviction candidate.
    expect(readViewerCache(identity, "k0")).toBe(0);
    writeViewerCache(identity, "overflow", "x");

    expect(viewerCacheSize()).toBe(VIEWER_CACHE_MAX_ENTRIES);
    expect(readViewerCache(identity, "k0")).toBe(0);
    expect(readViewerCache(identity, "k1")).toBeUndefined();
    expect(readViewerCache(identity, "overflow")).toBe("x");
  });

  it("deletes one entry and clears every identity at once", () => {
    const a = viewerIdentityKey(base);
    const b = viewerIdentityKey({ ...base, revealed: true });
    writeViewerCache(a, "one", 1);
    writeViewerCache(a, "two", 2);
    writeViewerCache(b, "one", 3);

    deleteViewerCache(a, "one");
    expect(readViewerCache(a, "one")).toBeUndefined();
    expect(viewerCacheEntries(a)).toEqual([["two", 2]]);

    clearViewerCache();
    expect(viewerCacheSize()).toBe(0);
    expect(readViewerCache(b, "one")).toBeUndefined();
  });
});
