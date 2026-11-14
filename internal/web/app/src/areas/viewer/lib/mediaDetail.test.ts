import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError, graphql } from "@/lib/api";
import type { Section, SourceItem } from "@/lib/graphql/types";
import { clearViewerCache, writeViewerCache } from "@/lib/viewerCache";
import { sectionCacheKey } from "../components/HydratedSection";
import {
  fetchMedia,
  findCachedSiblings,
  mediaLinkState,
  readMediaPreview,
  resumeProgress,
} from "./mediaDetail";

vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  graphql: vi.fn(),
}));

const identity = JSON.stringify(["viewer", "p1", "adult", false]);
const item = (id: string, extra: Partial<SourceItem> = {}): SourceItem => ({
  id,
  kind: "media",
  title: `Item ${id}`,
  ...extra,
});
const section = (id: string, kind = "shelf"): Section => ({
  id,
  kind,
  title: { en: `Section ${id}` },
  artworkSlot: "poster",
  source: { kind: "library" } as Section["source"],
});

afterEach(() => {
  clearViewerCache();
  vi.mocked(graphql).mockReset();
});

describe("media link state", () => {
  it("round-trips the card for the same item and identity only", () => {
    const card = item("m1", { artworkSlots: { poster: "/p" } });
    const state = mediaLinkState(card, identity, "sec-a");
    expect(readMediaPreview(state, identity, "m1")).toEqual({ item: card, sectionID: "sec-a" });
    expect(readMediaPreview(state, "other", "m1")).toBeNull();
    expect(readMediaPreview(state, identity, "m2")).toBeNull();
  });

  it("carries nothing without an identity and rejects a malformed card", () => {
    expect(mediaLinkState(item("m1"), null)).toBeUndefined();
    const state = { mediaPreview: { mediaID: "m1", identity, item: { id: "m1", title: "" } } };
    expect(readMediaPreview(state, identity, "m1")).toBeNull();
  });
});

describe("resumeProgress", () => {
  const withEngagement = (engagement: unknown) => ({ ...item("m1"), engagement }) as SourceItem;

  it("reports a started item's percent", () => {
    expect(resumeProgress(withEngagement({ positionSeconds: 600, durationSeconds: 2400 }))).toEqual(
      { positionSeconds: 600, durationSeconds: 2400, percent: 25 },
    );
  });

  it("keeps a barely started or nearly finished item off 0 and 100", () => {
    expect(
      resumeProgress(withEngagement({ positionSeconds: 1, durationSeconds: 10_000 }))?.percent,
    ).toBe(1);
    expect(
      resumeProgress(withEngagement({ positionSeconds: 9_999, durationSeconds: 10_000 }))?.percent,
    ).toBe(99);
  });

  it("knows no resume point for absent, completed or invalid engagement", () => {
    expect(resumeProgress(item("m1"))).toBeNull();
    expect(resumeProgress(undefined)).toBeNull();
    expect(
      resumeProgress(
        withEngagement({ positionSeconds: 600, durationSeconds: 2400, completed: true }),
      ),
    ).toBeNull();
    expect(
      resumeProgress(withEngagement({ positionSeconds: 0, durationSeconds: 2400 })),
    ).toBeNull();
    expect(
      resumeProgress(withEngagement({ positionSeconds: 2400, durationSeconds: 2400 })),
    ).toBeNull();
    expect(resumeProgress(withEngagement({ positionSeconds: "600" }))).toBeNull();
  });
});

describe("findCachedSiblings", () => {
  it("prefers the section the card came from and excludes the item", () => {
    writeViewerCache(identity, sectionCacheKey(section("a"), "lib", "", null), [
      item("m1"),
      item("m9"),
    ]);
    writeViewerCache(identity, sectionCacheKey(section("b"), "lib", "", null), [
      item("m2"),
      item("m1"),
      item("m3"),
    ]);

    const found = findCachedSiblings(identity, "m1", "b");
    expect(found?.section.id).toBe("b");
    expect(found?.items.map((i) => i.id)).toEqual(["m2", "m3"]);
    expect(findCachedSiblings(identity, "m1")?.section.id).toBe("a");
  });

  it("skips hero sections, sections holding only the item, and other identities", () => {
    writeViewerCache(identity, sectionCacheKey(section("h", "hero"), "lib", "", null), [
      item("m1"),
      item("m2"),
    ]);
    writeViewerCache(identity, sectionCacheKey(section("solo"), "lib", "", null), [item("m1")]);

    expect(findCachedSiblings(identity, "m1")).toBeNull();
    expect(findCachedSiblings("other", "m1")).toBeNull();
    expect(findCachedSiblings(null, "m1")).toBeNull();
  });
});

describe("fetchMedia", () => {
  it("returns the item", async () => {
    const media = { id: "m1", title: "One", favorited: false };
    vi.mocked(graphql).mockResolvedValue({ media });
    await expect(fetchMedia("m1")).resolves.toEqual(media);
    expect(vi.mocked(graphql).mock.calls[0][1]).toEqual({ id: "m1" });
  });

  it("maps the server's unknown-media answer to null and rethrows anything else", async () => {
    vi.mocked(graphql).mockRejectedValueOnce(new ApiError("unknown media", 200));
    await expect(fetchMedia("m1")).resolves.toBeNull();

    const failure = new TypeError("Failed to fetch");
    vi.mocked(graphql).mockRejectedValueOnce(failure);
    await expect(fetchMedia("m1")).rejects.toBe(failure);
  });
});
