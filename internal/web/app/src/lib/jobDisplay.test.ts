import { describe, expect, it } from "vitest";
import {
  jobEventDetail,
  jobEventLabel,
  jobKindDescription,
  jobKindLabel,
  jobProgressCount,
  jobProgressDetail,
  jobProgressMessageLabel,
  jobProgressRead,
  jobStatusLabel,
  jobTargetDetail,
} from "./jobDisplay";

const labels = {
  jobKindScan: "Scan",
  jobKindRetentionSweep: "Retention sweep",
  jobKindDatabaseBackup: "Database backup",
  jobKindSubscriptionPoll: "Subscription poll",
  jobKindQBittorrentIngestPoll: "qBittorrent ingest poll",
  jobKindDecodeVerify: "Decode verify",
  jobKindArtworkSweep: "Artwork sweep",
  jobKindAssetSweep: "Asset sweep",
  jobKindSkipDetectSweep: "Skip detect sweep",
  jobKindMigration: "Migration",
  jobStatusQueued: "Queued",
  jobStatusRunning: "Running",
  jobStatusYielded: "Paused",
  jobStatusCompleted: "Completed",
  jobStatusFailed: "Failed",
  jobStatusCanceled: "Canceled",
  jobProgressUnitFiles: "files",
  jobProgressUnitItems: "items",
  jobProgressUnitDownloads: "downloads",
  jobProgressUnitPosters: "posters",
  jobProgressMessageScanning: "Scanning",
  jobProgressMessageScanComplete: "Scan complete",
  jobProgressMessagePollingSubscriptions: "Polling subscriptions",
  jobProgressMessageGeneratingArtwork: "Generating artwork",
  jobProgressMessageGeneratingAssets: "Generating assets",
  jobProgressMessageVerifyingDecodeIntegrity: "Verifying decode integrity",
  jobProgressMessageDetectingSkipSegments: "Detecting skip segments",
  jobProgressMessageMigrationMatched: "{matched} of {total} items matched",
  jobEventProgress: "Progress",
  jobEventScanSummary: "Scan summary",
  jobEventScanRootUnavailable: "Folder unavailable",
  jobProgressOfTotal: "{current} of {total}",
};

describe("job display labels", () => {
  it("maps known job wire values through string keys", () => {
    expect(
      [
        "scan",
        "retention-sweep",
        "database-backup",
        "subscription-poll",
        "qbittorrent-ingest-poll",
        "decode-verify",
        "artwork-sweep",
        "asset-sweep",
        "skip-detect-sweep",
        "migration",
      ].map((kind) => jobKindLabel(kind, labels)),
    ).toEqual([
      "Scan",
      "Retention sweep",
      "Database backup",
      "Subscription poll",
      "qBittorrent ingest poll",
      "Decode verify",
      "Artwork sweep",
      "Asset sweep",
      "Skip detect sweep",
      "Migration",
    ]);
    expect(
      ["queued", "running", "yielded", "completed", "failed", "canceled"].map((status) =>
        jobStatusLabel(status, labels),
      ),
    ).toEqual(["Queued", "Running", "Paused", "Completed", "Failed", "Canceled"]);
  });

  it("keeps unknown closed-vocabulary values deterministic without slug punctuation", () => {
    expect(jobKindLabel("custom-plugin-task", labels)).toBe("Custom plugin task");
    expect(jobStatusLabel("waiting_for_media", labels)).toBe("Waiting for media");
  });

  it("localizes progress units while preserving unknown server progress messages", () => {
    expect(
      jobProgressDetail(
        {
          id: "job-1",
          kind: "scan",
          status: "running",
          progress: { current: 3, total: 5, unit: "files", message: "HTTP 404: Not Found" },
        },
        labels,
      ),
    ).toBe("3/5 files - HTTP 404: Not Found");
  });

  it("maps known static progress messages and dynamic migration messages", () => {
    expect(jobProgressMessageLabel("Scanning", labels)).toBe("Scanning");
    expect(jobProgressMessageLabel("Scan complete", labels)).toBe("Scan complete");
    expect(jobProgressMessageLabel("Polling subscriptions", labels)).toBe("Polling subscriptions");
    expect(jobProgressMessageLabel("Generating artwork", labels)).toBe("Generating artwork");
    expect(jobProgressMessageLabel("Generating assets", labels)).toBe("Generating assets");
    expect(jobProgressMessageLabel("Verifying decode integrity", labels)).toBe(
      "Verifying decode integrity",
    );
    expect(jobProgressMessageLabel("Detecting skip-segments", labels)).toBe(
      "Detecting skip segments",
    );
    expect(jobProgressMessageLabel("12 of 18 items matched", labels)).toBe(
      "12 of 18 items matched",
    );
  });

  it("reads a job history entry as a line of text", () => {
    const progress = (payload: object) =>
      jobEventDetail({ type: "progress", payload: JSON.stringify(payload) });
    expect(
      progress({ type: "progress", current: 7, total: 10, unit: "files", message: "Scanning" }),
    ).toBe("7/10 files - Scanning");
    expect(progress({ type: "progress", current: 42, unit: "files", message: "Scanning" })).toBe(
      "42 files - Scanning",
    );
    expect(progress({ type: "progress", current: 42 })).toBe("42");
    // Entries written before progress carried its numbers have nothing to add.
    expect(progress({ type: "progress" })).toBe("");
    expect(jobEventDetail({ type: "running", payload: '{"status":"running"}' })).toBe("");
    expect(
      jobEventDetail({ type: "yielded", payload: '{"status":"yielded","reason":"shutdown"}' }),
    ).toBe("shutdown");
    // Anything else is left for the caller to show as stored.
    expect(jobEventDetail({ type: "failed", payload: '{"error":"disk full"}' })).toBeNull();
    expect(jobEventDetail({ type: "started", payload: "worker=w2" })).toBeNull();
    expect(jobEventDetail({ type: "started" })).toBeNull();
  });
});

describe("job purpose and target", () => {
  const descriptions = {
    jobKindDescScan: "Imports a folder.",
    jobKindDescScanBackfill: "Refreshes metadata.",
    jobKindDescArtworkSweep: "Fills in posters.",
  };

  it("describes known kinds, the backfill scan, and nothing for unknown kinds", () => {
    expect(jobKindDescription({ kind: "scan" }, descriptions)).toBe("Imports a folder.");
    expect(
      jobKindDescription(
        { kind: "scan", target: { libraryID: "l", libraryTitle: "Films", backfill: true } },
        descriptions,
      ),
    ).toBe("Refreshes metadata.");
    expect(jobKindDescription({ kind: "artwork-sweep" }, descriptions)).toBe("Fills in posters.");
    expect(jobKindDescription({ kind: "future-kind" }, descriptions)).toBe("");
  });

  it("formats the Library and folder a job works on", () => {
    expect(
      jobTargetDetail({ target: { libraryID: "l", libraryTitle: "Films", root: "/media/films" } }),
    ).toBe("Films · /media/films");
    expect(jobTargetDetail({ target: { libraryID: "l", libraryTitle: "Films" } })).toBe("Films");
    expect(jobTargetDetail({ target: null })).toBe("");
    expect(jobTargetDetail({})).toBe("");
  });

  it("humanizes only enum-like wire tokens and keeps free text as sent (CORE-2342)", () => {
    expect(jobStatusLabel("yielded", labels)).toBe("Paused");
    expect(jobEventLabel("scan-root-unavailable", labels)).toBe("Folder unavailable");
    expect(jobStatusLabel("awaiting.remote_peer", labels)).toBe("Awaiting remote peer");
    expect(jobEventLabel("Scan root /Volumes/Media/Films is offline", labels)).toBe(
      "Scan root /Volumes/Media/Films is offline",
    );
    expect(jobKindLabel("  Fetch from TheTVDB  ", labels)).toBe("Fetch from TheTVDB");
    expect(jobStatusLabel("HTTP_TIMEOUT", labels)).toBe("HTTP_TIMEOUT");
    expect(
      jobProgressRead(
        {
          current: 0,
          total: 0,
          unit: "",
          message: "Reading /Volumes/Media/Films/Alien (1979)",
        },
        labels,
        "en",
      ),
    ).toBe("Reading /Volumes/Media/Films/Alien (1979)");
    expect(jobProgressRead({ current: 2, total: 0, unit: "MB", message: "" }, labels, "en")).toBe(
      "2 MB",
    );
    expect(
      jobEventDetail({
        type: "failed",
        payload: JSON.stringify({ status: "failed", reason: "Scan root /Volumes/Media missing" }),
      }),
    ).toBe("Scan root /Volumes/Media missing");
  });

  it("humanizes dotted kinds it has no label for", () => {
    expect(jobKindLabel("plugin.thing-run", labels)).toBe("Plugin thing run");
  });

  it("counts work with no known total instead of reading N/0 (CORE-2352)", () => {
    expect(jobProgressCount({ current: 2055, total: 0, unit: "items" }, labels)).toBe("2055 items");
    expect(
      jobProgressCount({ current: 3, total: 5, unit: "files", message: "Scanning" }, labels),
    ).toBe("3/5 files - Scanning");
    expect(jobProgressCount({ current: 0, total: 0, unit: "items" }, labels)).toBe("");
  });

  it("reads job progress as words with grouped counts (CORE-2352)", () => {
    expect(
      jobProgressRead(
        { current: 117, total: 502, unit: "files", message: "Scanning" },
        labels,
        "en",
      ),
    ).toBe("117 of 502 files · Scanning");
    expect(jobProgressRead({ current: 2055, total: 0, unit: "files" }, labels, "en")).toBe(
      "2,055 files",
    );
    expect(jobProgressRead({ current: 1500, total: 2000, unit: "" }, labels, "de")).toBe(
      "1.500 of 2.000",
    );
    expect(jobProgressRead({ current: 0, total: 0, unit: "files" }, labels, "en")).toBe("");
  });

  it("labels job log entry types (CORE-2352)", () => {
    expect(jobEventLabel("progress", labels)).toBe("Progress");
    expect(jobEventLabel("running", labels)).toBe("Running");
    expect(jobEventLabel("scan-summary", labels)).toBe("Scan summary");
    expect(jobEventLabel("scan-root-unavailable", labels)).toBe("Folder unavailable");
    expect(jobEventLabel("custom_event", labels)).toBe("Custom event");
  });
});
