import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import type { LibrarySummary } from "@/lib/graphql/types";
import {
  canonicalMediaPath,
  librarySlug,
  MAX_SLUG_LENGTH,
  mediaAddressIDs,
  mediaPath,
  readableMediaWords,
  slugify,
} from "./mediaAddress";

const ID = "media-sha256-a371bff46385f7ded";
const mediaIdFromAddress = (rest: string | undefined) => mediaAddressIDs(rest)[0];
const libraries: LibrarySummary[] = [
  { id: "library-music", title: "Music", pluginID: "music", previewClips: false },
  { id: "library-films", title: "Films & Shows", pluginID: null, previewClips: false },
];

describe("routes built from known Media IDs", () => {
  // Keep the route-producing call sites on the shared encoder. The route
  // behavior itself is covered below and through the PlayerPage click test.
  it.each([
    ["viewer/components/ContainerChildren.tsx", ["media", "watch"]],
    ["viewer/components/ContainerMediaRows.tsx", ["media", "watch"]],
    ["viewer/components/Hero.tsx", ["watch"]],
    ["viewer/components/MediaDetailMoreMenu.tsx", ["media"]],
    ["viewer/components/MediaItemCard.tsx", ["media", "view", "watch"]],
    ["viewer/components/TrackList.tsx", ["media", "watch"]],
    ["viewer/components/WatchTogetherButton.tsx", ["watch"]],
    ["viewer/components/feeds/FeedCatalogue.tsx", ["media", "watch"]],
    ["viewer/lib/searchRows.ts", ["watch", "watch"]],
    ["viewer/pages/CastCrewPage.tsx", ["credits", "media"]],
    ["viewer/pages/CollectionPage.tsx", ["watch"]],
    ["viewer/pages/EntityPage.tsx", ["watch", "watch", "watch"]],
    ["viewer/pages/LibraryBrowsePage.tsx", ["watch"]],
    ["viewer/pages/ImageViewerPage.tsx", ["view", "view", "watch", "watch"]],
    ["viewer/pages/MediaDetailPage.tsx", ["view", "watch", "media"]],
    ["viewer/pages/MyDataPage.tsx", ["media"]],
    ["viewer/pages/NotificationsPage.tsx", ["watch"]],
    ["viewer/pages/PlayerPage.tsx", ["view", "watch", "watch", "media", "watch"]],
    ["viewer/pages/PlaylistPage.tsx", ["watch"]],
    ["viewer/remote/RemoteReceiver.tsx", ["watch"]],
    ["console/lib/previewMedia.ts", ["watch"]],
  ])("%s encodes each known-ID route", (file, routes) => {
    const source = readFileSync(resolve("src/areas", file), "utf8");
    const calls = [...source.matchAll(/\bmediaPath\("(media|view|watch|credits)",/g)];
    expect(calls.map(([, route]) => route).toSorted()).toEqual(routes.toSorted());
    expect(source).not.toMatch(/(?:`\/?|\})\b(?:media|watch)\/\$\{/);
  });
});

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

  it("builds and resolves sampled addresses alongside legacy ones", () => {
    const sampleID = "media-sample1-a371bff46385f7ded";
    expect(mediaPath("watch", sampleID, { library: "music", words: "New title" })).toBe(
      "/watch/music/new-title-sample1.a371bff46385f7ded",
    );
    expect(mediaIdFromAddress("old-library/old-title-sample1.a371bff46385f7ded")).toBe(sampleID);
    expect(mediaPath("watch", sampleID)).toBe("/watch/sample1.a371bff46385f7ded");
    expect(mediaAddressIDs("sample1.a371bff46385f7ded")).toEqual([sampleID]);
    expect(mediaIdFromAddress(sampleID)).toBe(sampleID);
    expect(mediaIdFromAddress("old-library/old-title-sample1-a371bff46385f7ded")).toBe(ID);
    const unlettered = mediaPath("watch", sampleID, { library: "music", words: "千と千尋" });
    expect(unlettered).toBe("/watch/music/sample1.a371bff46385f7ded");
    expect(mediaIdFromAddress(unlettered.slice("/watch/".length))).toBe(sampleID);
  });

  it("round-trips a legacy item titled Media Sample1", () => {
    const address = canonicalMediaPath(
      "watch",
      { id: ID, title: "Media Sample1", libraryID: "library-music" },
      libraries,
    );
    expect(address).toBe("/watch/music/media-sample1-a371bff46385f7ded");
    expect(mediaIdFromAddress(address.slice("/watch/".length))).toBe(ID);
    const unqualified = mediaPath("watch", ID, { words: "Media Sample1" });
    expect(unqualified).toBe(`/watch/${ID}`);
    expect(mediaIdFromAddress(unqualified.slice("/watch/".length))).toBe(ID);
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
