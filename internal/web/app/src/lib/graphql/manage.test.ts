import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setAdminEpochSignal, withAdminEpoch } from "@/areas/console/adminEpoch";
import {
  addCollectionMedia,
  assignTag,
  bulkAssignTags,
  createCollection,
  createMarker,
  createSmartCollection,
  createTag,
  deleteCollection,
  deleteMarker,
  deleteSmartCollection,
  deleteTag,
  dismissUnmatched,
  fetchBackupHistory,
  fetchMetadataExport,
  fixMatch,
  identifySearch,
  linkMediaRelation,
  purgeTrash,
  removeCollectionMedia,
  reorderCollectionMedia,
  restoreCollection,
  restoreMarker,
  restoreMedia,
  restoreTag,
  runBackup,
  unassignTag,
  updateCollection,
  updateMarker,
  updateSmartCollection,
  updateTag,
  upsertEntityRelation,
} from "./manage";

interface Recorded {
  query: string;
  variables: Record<string, unknown>;
}

const recorded: Recorded[] = [];
const recordedSignals: (AbortSignal | null | undefined)[] = [];

// The transport matrix: the Manage-module reads BackupExportPage issues from
// the Console route (/console/backups). Both transport assertions below run
// the whole matrix, so a reader that drops its signal fails on both counts.
const MANAGE_READS: [string, (signal: AbortSignal) => Promise<unknown>][] = [
  ["fetchBackupHistory", (s) => fetchBackupHistory("/backups", s)],
  ["fetchMetadataExport", (s) => fetchMetadataExport(s)],
];

beforeEach(() => {
  recorded.length = 0;
  recordedSignals.length = 0;
  setAdminEpochSignal();
  vi.stubGlobal(
    "fetch",
    vi.fn((_url: string, init?: RequestInit) => {
      const body = init?.body
        ? (JSON.parse(init.body as string) as Recorded)
        : { query: "", variables: {} };
      recorded.push(body);
      recordedSignals.push(init?.signal);
      const data = body.query.includes("purgeTrash")
        ? { purgeTrash: { affected: 2 } }
        : body.query.includes("backupHistory")
          ? { backupHistory: [] }
          : body.query.includes("backup")
            ? { backup: { manifest: "/backups/2026-06-20/manifest.json" } }
            : body.query.includes("metadataExport")
              ? {
                  metadataExport: {
                    schema: "v3",
                    exportedAt: "2026-06-20T14:02:00Z",
                    libraries: [{}],
                    media: [{}, {}],
                    entities: [],
                    relations: [],
                    mediaLinks: [],
                    collections: [{}],
                  },
                }
              : body.query.includes("identifySearch")
                ? { identifySearch: [{ id: "candidate-1", title: "The Thing", year: 1982 }] }
                : body.query.includes("fixMatch")
                  ? {
                      fixMatch: { id: "media-1", title: "The Thing", year: 1982, favorited: false },
                    }
                  : { restoreMedia: [] };
      return Promise.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ data }),
      } as Response);
    }),
  );
});

afterEach(() => {
  setAdminEpochSignal();
  vi.unstubAllGlobals();
});

describe("manage GraphQL requests", () => {
  // BackupExportPage renders at /console/backups, so its reads carry the same
  // abort contract as every Console read: the caller's signal must reach the
  // transport so an ended Admin Session aborts the request itself, not merely
  // drops the response.
  it("threads the caller's AbortSignal to the transport for every Console-routed Manage read", async () => {
    for (const [name, read] of MANAGE_READS) {
      recordedSignals.length = 0;
      const controller = new AbortController();
      await read(controller.signal);
      expect(recordedSignals, name).toHaveLength(1);
      expect(recordedSignals[0], name).toBeInstanceOf(AbortSignal);
      expect(recordedSignals[0], name).not.toBe(controller.signal);
      expect(recordedSignals[0]?.aborted, name).toBe(false);
      controller.abort();
      expect(recordedSignals[0]?.aborted, name).toBe(true);
    }
  });

  // The request-side half of the same contract: the object the transport
  // receives is the signal composed with the Admin-Session epoch
  // (areas/console/adminEpoch), so it reports aborted the moment the Admin
  // epoch ends (expiry / lock / Profile switch). A reader that drops the
  // argument records no signal at all here.
  it("aborts the in-flight request of every Console-routed Manage read when the Admin epoch ends", async () => {
    for (const [name, read] of MANAGE_READS) {
      const adminEpoch = new AbortController();
      setAdminEpochSignal(adminEpoch.signal);
      const own = new AbortController();
      const composed = withAdminEpoch(own.signal);

      recordedSignals.length = 0;
      await read(composed);
      const sent = recordedSignals[0];
      expect(recordedSignals, name).toHaveLength(1);
      expect(sent, name).toBeInstanceOf(AbortSignal);
      expect(sent?.aborted, name).toBe(false);

      adminEpoch.abort();
      expect(sent?.aborted, name).toBe(true);
    }
  });
  it("restoreMedia returns the updated trash list", async () => {
    await expect(restoreMedia("media-alpha")).resolves.toEqual([]);
    expect(recorded).toHaveLength(1);
    expect(recorded[0].query).toContain("restoreMedia");
    expect(recorded[0].variables).toEqual({ mediaID: "media-alpha" });
  });

  it("purgeTrash sends the cutoff and explicit confirmation", async () => {
    await expect(purgeTrash("2026-06-17T12:00:00Z", true)).resolves.toEqual({
      affected: 2,
    });
    expect(recorded).toHaveLength(1);
    expect(recorded[0].query).toContain("mutation PurgeTrash");
    expect(recorded[0].query).toContain("purgeTrash");
    expect(recorded[0].variables).toEqual({
      before: "2026-06-17T12:00:00Z",
      confirm: true,
    });
  });

  it("runBackup sends the destination and returns the manifest", async () => {
    await expect(runBackup("/backups/2026-06-20")).resolves.toEqual({
      manifest: "/backups/2026-06-20/manifest.json",
    });
    expect(recorded).toHaveLength(1);
    expect(recorded[0].query).toContain("mutation Backup");
    expect(recorded[0].query).toContain("backup(dest: $dest)");
    expect(recorded[0].variables).toEqual({ dest: "/backups/2026-06-20" });
  });

  it("fetchMetadataExport selects the full export payload", async () => {
    const result = await fetchMetadataExport();
    expect(result.schema).toBe("v3");
    expect(result.media).toHaveLength(2);
    expect(recorded).toHaveLength(1);
    expect(recorded[0].query).toContain("query MetadataExport");
    expect(recorded[0].query).toContain("metadataExport");
  });
});

// Request-recording coverage for the manage mutations (tests.md: web client
// request-recording residual). Each test asserts the operation name + the
// variables sent at the choke-point; the stubbed fetch above returns an empty
// `data` object (the falls-through `{ restoreMedia: [] }`), which is sufficient
// because every function below simply returns `data.<field>` without
// dereferencing nested properties of an absent payload.
describe("manage mutation request recording", () => {
  it("createTag sends CreateTag(name, parentTagID)", async () => {
    // feature:tags
    await createTag("Action", "parent-1");
    expect(recorded[0].query).toContain("mutation CreateTag");
    expect(recorded[0].query).toContain("createTag(name: $name, parentTagID: $parentTagID)");
    expect(recorded[0].variables).toEqual({
      name: "Action",
      parentTagID: "parent-1",
    });
  });

  it("updateTag sends UpdateTag(tagID, name, parentTagID)", async () => {
    // feature:tags
    await updateTag("tag-1", "Drama", "parent-2");
    expect(recorded[0].query).toContain("mutation UpdateTag");
    expect(recorded[0].query).toContain(
      "updateTag(tagID: $tagID, name: $name, parentTagID: $parentTagID)",
    );
    expect(recorded[0].variables).toEqual({
      tagID: "tag-1",
      name: "Drama",
      parentTagID: "parent-2",
    });
  });

  it("deleteTag sends DeleteTag(tagID)", async () => {
    // feature:tags
    await deleteTag("tag-1");
    expect(recorded[0].query).toContain("mutation DeleteTag");
    expect(recorded[0].query).toContain("deleteTag(tagID: $tagID)");
    expect(recorded[0].variables).toEqual({ tagID: "tag-1" });
  });

  it("restoreTag sends RestoreTag(tagID)", async () => {
    // feature:tags
    await restoreTag("tag-1");
    expect(recorded[0].query).toContain("mutation RestoreTag");
    expect(recorded[0].query).toContain("restoreTag(tagID: $tagID)");
    expect(recorded[0].variables).toEqual({ tagID: "tag-1" });
  });

  it("assignTag sends AssignTag(tagID, targetType, targetID)", async () => {
    // feature:tags
    await assignTag("tag-1", "media", "media-1");
    expect(recorded[0].query).toContain("mutation AssignTag");
    expect(recorded[0].query).toContain(
      "assignTag(tagID: $tagID, targetType: $targetType, targetID: $targetID)",
    );
    expect(recorded[0].variables).toEqual({
      tagID: "tag-1",
      targetType: "media",
      targetID: "media-1",
    });
  });

  it("unassignTag sends UnassignTag(tagID, targetType, targetID)", async () => {
    // feature:tags
    await unassignTag("tag-1", "entity", "entity-1");
    expect(recorded[0].query).toContain("mutation UnassignTag");
    expect(recorded[0].query).toContain(
      "unassignTag(tagID: $tagID, targetType: $targetType, targetID: $targetID)",
    );
    expect(recorded[0].variables).toEqual({
      tagID: "tag-1",
      targetType: "entity",
      targetID: "entity-1",
    });
  });

  it("bulkAssignTags sends BulkAssignTags(tagIDs, targetType, targetIDs, mode)", async () => {
    // feature:tags
    await bulkAssignTags(["t1", "t2"], "media", ["m1", "m2"], "add");
    expect(recorded[0].query).toContain("mutation BulkAssignTags");
    expect(recorded[0].query).toContain(
      "bulkAssignTags(tagIDs: $tagIDs, targetType: $targetType, targetIDs: $targetIDs, mode: $mode)",
    );
    expect(recorded[0].variables).toEqual({
      tagIDs: ["t1", "t2"],
      targetType: "media",
      targetIDs: ["m1", "m2"],
      mode: "add",
    });
  });

  it("createMarker sends CreateMarker(mediaID, ...input)", async () => {
    // feature:markers
    await createMarker("media-1", {
      title: "Intro",
      startSeconds: 1.5,
      endSeconds: 30,
      segmentKind: "intro",
      tags: ["t1"],
    });
    expect(recorded[0].query).toContain("mutation CreateMarker");
    expect(recorded[0].query).toContain(
      "createMarker(mediaID: $mediaID, title: $title, startSeconds: $startSeconds, endSeconds: $endSeconds, segmentKind: $segmentKind, tags: $tags)",
    );
    expect(recorded[0].variables).toEqual({
      mediaID: "media-1",
      title: "Intro",
      startSeconds: 1.5,
      endSeconds: 30,
      segmentKind: "intro",
      tags: ["t1"],
    });
  });

  it("updateMarker sends UpdateMarker(markerID, ...input)", async () => {
    // feature:markers
    await updateMarker("marker-1", { startSeconds: 5 });
    expect(recorded[0].query).toContain("mutation UpdateMarker");
    expect(recorded[0].query).toContain(
      "updateMarker(markerID: $markerID, title: $title, startSeconds: $startSeconds, endSeconds: $endSeconds, segmentKind: $segmentKind, tags: $tags)",
    );
    expect(recorded[0].variables).toEqual({
      markerID: "marker-1",
      startSeconds: 5,
    });
  });

  it("deleteMarker sends DeleteMarker(markerID)", async () => {
    // feature:markers
    await deleteMarker("marker-1");
    expect(recorded[0].query).toContain("mutation DeleteMarker");
    expect(recorded[0].query).toContain("deleteMarker(markerID: $markerID)");
    expect(recorded[0].variables).toEqual({ markerID: "marker-1" });
  });

  it("restoreMarker sends RestoreMarker(markerID)", async () => {
    // feature:markers
    await restoreMarker("marker-1");
    expect(recorded[0].query).toContain("mutation RestoreMarker");
    expect(recorded[0].query).toContain("restoreMarker(markerID: $markerID)");
    expect(recorded[0].variables).toEqual({ markerID: "marker-1" });
  });

  it("createSmartCollection sends CreateSmartCollection(libraryID, name, sourceExpr, sortExpr)", async () => {
    // feature:smart-collections
    await createSmartCollection({
      libraryID: "lib-1",
      name: "Recent",
      sourceExpr: "addedAt > 0",
      sortExpr: "addedAt desc",
    });
    expect(recorded[0].query).toContain("mutation CreateSmartCollection");
    expect(recorded[0].query).toContain(
      "createSmartCollection(libraryID: $libraryID, name: $name, sourceExpr: $sourceExpr, sortExpr: $sortExpr)",
    );
    expect(recorded[0].variables).toEqual({
      libraryID: "lib-1",
      name: "Recent",
      sourceExpr: "addedAt > 0",
      sortExpr: "addedAt desc",
    });
  });

  it("updateSmartCollection sends UpdateSmartCollection(id, ...)", async () => {
    // feature:smart-collections
    await updateSmartCollection({ id: "sc-1", name: "Renamed" });
    expect(recorded[0].query).toContain("mutation UpdateSmartCollection");
    expect(recorded[0].query).toContain(
      "updateSmartCollection(id: $id, name: $name, sourceExpr: $sourceExpr, sortExpr: $sortExpr)",
    );
    expect(recorded[0].variables).toEqual({ id: "sc-1", name: "Renamed" });
  });

  it("deleteSmartCollection sends DeleteSmartCollection(id)", async () => {
    // feature:smart-collections
    await deleteSmartCollection("sc-1");
    expect(recorded[0].query).toContain("mutation DeleteSmartCollection");
    expect(recorded[0].query).toContain("deleteSmartCollection(id: $id)");
    expect(recorded[0].variables).toEqual({ id: "sc-1" });
  });

  it("createCollection sends CreateCollection(libraryID, name, parentID, sortOrder)", async () => {
    // feature:collections
    await createCollection({
      libraryID: "lib-1",
      name: "Marvel",
      parentID: "col-0",
      sortOrder: 2,
    });
    expect(recorded[0].query).toContain("mutation CreateCollection");
    expect(recorded[0].query).toContain(
      "createCollection(libraryID: $libraryID, name: $name, parentID: $parentID, sortOrder: $sortOrder)",
    );
    expect(recorded[0].variables).toEqual({
      libraryID: "lib-1",
      name: "Marvel",
      parentID: "col-0",
      sortOrder: 2,
    });
  });

  it("updateCollection sends UpdateCollection(id, ...)", async () => {
    // feature:collections
    await updateCollection({ id: "col-1", name: "Renamed" });
    expect(recorded[0].query).toContain("mutation UpdateCollection");
    expect(recorded[0].query).toContain(
      "updateCollection(id: $id, name: $name, parentID: $parentID, sortOrder: $sortOrder)",
    );
    expect(recorded[0].variables).toEqual({ id: "col-1", name: "Renamed" });
  });

  it("deleteCollection sends DeleteCollection(id)", async () => {
    // feature:collections
    await deleteCollection("col-1");
    expect(recorded[0].query).toContain("mutation DeleteCollection");
    expect(recorded[0].query).toContain("deleteCollection(id: $id)");
    expect(recorded[0].variables).toEqual({ id: "col-1" });
  });

  it("restoreCollection sends RestoreCollection(id)", async () => {
    // feature:collections
    await restoreCollection("col-1");
    expect(recorded[0].query).toContain("mutation RestoreCollection");
    expect(recorded[0].query).toContain("restoreCollection(id: $id)");
    expect(recorded[0].variables).toEqual({ id: "col-1" });
  });

  it("addCollectionMedia sends AddCollectionMedia(collectionID, mediaIDs)", async () => {
    // feature:collections
    await addCollectionMedia("col-1", ["m1", "m2"]);
    expect(recorded[0].query).toContain("mutation AddCollectionMedia");
    expect(recorded[0].query).toContain(
      "addCollectionMedia(collectionID: $collectionID, mediaIDs: $mediaIDs)",
    );
    expect(recorded[0].variables).toEqual({
      collectionID: "col-1",
      mediaIDs: ["m1", "m2"],
    });
  });

  it("removeCollectionMedia sends RemoveCollectionMedia(collectionID, mediaIDs)", async () => {
    // feature:collections
    await removeCollectionMedia("col-1", ["m1"]);
    expect(recorded[0].query).toContain("mutation RemoveCollectionMedia");
    expect(recorded[0].query).toContain(
      "removeCollectionMedia(collectionID: $collectionID, mediaIDs: $mediaIDs)",
    );
    expect(recorded[0].variables).toEqual({
      collectionID: "col-1",
      mediaIDs: ["m1"],
    });
  });

  it("reorderCollectionMedia sends ReorderCollectionMedia(collectionID, mediaIDs)", async () => {
    // feature:collections
    await reorderCollectionMedia("col-1", ["m2", "m1"]);
    expect(recorded[0].query).toContain("mutation ReorderCollectionMedia");
    expect(recorded[0].query).toContain(
      "reorderCollectionMedia(collectionID: $collectionID, mediaIDs: $mediaIDs)",
    );
    expect(recorded[0].variables).toEqual({
      collectionID: "col-1",
      mediaIDs: ["m2", "m1"],
    });
  });

  it("upsertEntityRelation sends UpsertEntityRelation with the relation tuple", async () => {
    // feature:entity-relations
    await upsertEntityRelation({
      fromEntityID: "e1",
      relation: "appears-in",
      toMediaID: "m1",
      sortOrder: 3,
    });
    expect(recorded[0].query).toContain("mutation UpsertEntityRelation");
    expect(recorded[0].query).toContain(
      "upsertEntityRelation(fromEntityID: $fromEntityID, relation: $relation, toEntityID: $toEntityID, toMediaID: $toMediaID, sortOrder: $sortOrder)",
    );
    expect(recorded[0].variables).toEqual({
      fromEntityID: "e1",
      relation: "appears-in",
      toMediaID: "m1",
      sortOrder: 3,
    });
  });

  it("linkMediaRelation sends LinkMediaRelation(fromMediaID, relation, toMediaID)", async () => {
    // feature:media-relations
    await linkMediaRelation("m1", "extra-of", "m2");
    expect(recorded[0].query).toContain("mutation LinkMediaRelation");
    expect(recorded[0].query).toContain(
      "linkMediaRelation(fromMediaID: $fromMediaID, relation: $relation, toMediaID: $toMediaID)",
    );
    expect(recorded[0].variables).toEqual({
      fromMediaID: "m1",
      relation: "extra-of",
      toMediaID: "m2",
    });
  });

  it("dismissUnmatched sends DismissUnmatched(mediaID)", async () => {
    // feature:library-health
    await dismissUnmatched("media-1");
    expect(recorded[0].query).toContain("mutation DismissUnmatched");
    expect(recorded[0].query).toContain("dismissUnmatched(mediaID: $mediaID)");
    expect(recorded[0].variables).toEqual({ mediaID: "media-1" });
  });

  it("identifySearch sends the Media/plugin scraper search variables", async () => {
    // feature:unmatched-queue
    await identifySearch({
      mediaID: "media-1",
      pluginID: "filmtv",
      scraperID: "tmdb",
      query: "The Thing",
    });
    expect(recorded[0].query).toContain("query IdentifySearch");
    expect(recorded[0].query).toContain(
      "identifySearch(mediaID: $mediaID, pluginID: $pluginID, scraperID: $scraperID, query: $query)",
    );
    expect(recorded[0].variables).toEqual({
      mediaID: "media-1",
      pluginID: "filmtv",
      scraperID: "tmdb",
      query: "The Thing",
    });
  });

  it("fixMatch sends the selected candidate", async () => {
    // feature:unmatched-queue
    await fixMatch({
      mediaID: "media-1",
      pluginID: "filmtv",
      scraperID: "tmdb",
      candidateID: "candidate-1",
    });
    expect(recorded[0].query).toContain("mutation FixMatch");
    expect(recorded[0].query).toContain(
      "fixMatch(mediaID: $mediaID, pluginID: $pluginID, scraperID: $scraperID, candidateID: $candidateID)",
    );
    expect(recorded[0].variables).toEqual({
      mediaID: "media-1",
      pluginID: "filmtv",
      scraperID: "tmdb",
      candidateID: "candidate-1",
    });
  });
});
