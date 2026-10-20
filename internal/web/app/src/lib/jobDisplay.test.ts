import { describe, expect, it } from "vitest";
import {
  jobKindLabel,
  jobProgressDetail,
  jobProgressMessageLabel,
  jobStatusLabel,
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
  jobStatusYielded: "Yielded",
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
    ).toEqual(["Queued", "Running", "Yielded", "Completed", "Failed", "Canceled"]);
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
});
