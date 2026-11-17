import { describe, expect, it } from "vitest";
import type { LibrarySummary } from "@/lib/graphql/types";
import {
  canonicalMediaPath,
  librarySlug,
  MAX_SLUG_LENGTH,
  mediaIdFromAddress,
  mediaPath,
  readableMediaWords,
  slugify,
} from "./mediaAddress";

const ID = "media-sha256-a371bff46385f7ded";
const libraries: LibrarySummary[] = [
  { id: "library-music", title: "Music", pluginID: "music", previewClips: false },
  { id: "library-films", title: "Films & Shows", pluginID: null, previewClips: false },
];

describe("readable media addresses (CORE-2329)", () => {
  it("folds text to lowercase ASCII words", () => {
    expect(slugify("38 Planes (Reprise)")).toBe("38-planes-reprise");
    expect(slugify("Amélie — Le Fabuleux Destin")).toBe("amelie-le-fabuleux-destin");
    expect(slugify("Don't Stop Me Now!")).toBe("dont-stop-me-now");
    expect(slugify("千と千尋の神隠し")).toBe("");
    expect(slugify("   ")).toBe("");
  });

  it("caps the words at a word boundary", () => {
    const long = slugify("word ".repeat(40));
    expect(long.length).toBeLessThanOrEqual(MAX_SLUG_LENGTH);
    expect(long.endsWith("-")).toBe(false);
    expect(long.split("-").every((w) => w === "word")).toBe(true);
    expect(slugify("x".repeat(90))).toHaveLength(MAX_SLUG_LENGTH);
  });

  it("names a listening item by who made it and anything else by what it belongs to", () => {
    const credits = [
      { entityID: "a", title: "Come From Away", kind: "k", archetype: "container", relation: "r" },
      { entityID: "p", title: "Company", kind: "k", archetype: "person", relation: "r" },
    ];
    expect(readableMediaWords({ title: "38 Planes", mediaClass: "audio", credits })).toBe(
      "Company 38 Planes",
    );
    expect(readableMediaWords({ title: "Pilot", mediaClass: "video", credits })).toBe(
      "Come From Away Pilot",
    );
    expect(readableMediaWords({ title: "Arrival" })).toBe("Arrival");
  });

  it("builds the canonical address from the Library and the item's words", () => {
    expect(
      canonicalMediaPath(
        "watch",
        {
          id: ID,
          title: "38 Planes (Reprise)",
          mediaClass: "audio",
          libraryID: "library-music",
          credits: [
            {
              entityID: "p",
              title: "Come From Away Company",
              kind: "k",
              archetype: "person",
              relation: "r",
            },
          ],
        },
        libraries,
      ),
    ).toBe(`/watch/music/come-from-away-company-38-planes-reprise-a371bff46385f7ded`);
    expect(librarySlug("library-films", libraries)).toBe("films-shows");
    expect(librarySlug("library-gone", libraries)).toBe("gone");
    expect(librarySlug(undefined, libraries)).toBe("");
  });

  it("falls back to the bare ID where no words or no content ID exist", () => {
    expect(mediaPath("watch", ID, { library: "music", words: "千と千尋" })).toBe(
      `/watch/music/${ID}`,
    );
    expect(mediaPath("media", ID)).toBe(`/media/${ID}`);
    expect(
      mediaPath("watch", "media-enc-0123456789abcdef0123456789abcdef", { words: "Secret" }),
    ).toBe("/watch/media-enc-0123456789abcdef0123456789abcdef");
    expect(mediaPath("watch", "a b")).toBe("/watch/a%20b");
  });

  it("resolves readable, stale and bare addresses to the same item", () => {
    expect(
      mediaIdFromAddress("music/come-from-away-company-38-planes-reprise-a371bff46385f7ded"),
    ).toBe(ID);
    expect(mediaIdFromAddress("old-library/an-old-title-a371bff46385f7ded")).toBe(ID);
    expect(mediaIdFromAddress("a371bff46385f7ded")).toBe(ID);
    expect(mediaIdFromAddress(ID)).toBe(ID);
    expect(mediaIdFromAddress("music/media-sha256-a371bff46385f7ded")).toBe(ID);
    expect(mediaIdFromAddress("media-enc-0123456789abcdef0123456789abcdef")).toBe(
      "media-enc-0123456789abcdef0123456789abcdef",
    );
    expect(mediaIdFromAddress("a%20b")).toBe("a b");
    expect(mediaIdFromAddress("m1")).toBe("m1");
    expect(mediaIdFromAddress(undefined)).toBe("");
  });
});
