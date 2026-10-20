import type { JobInfo } from "@/lib/graphql/types";
import type { StringKey } from "@/strings";

type LabelSource = Partial<Record<StringKey, string>> | ((key: StringKey) => string);

export type JobStatusState = "neutral" | "green" | "amber" | "red";

const JOB_KIND_LABELS: Record<string, StringKey> = {
  scan: "jobKindScan",
  "subscription-poll": "jobKindSubscriptionPoll",
  "qbittorrent-ingest-poll": "jobKindQBittorrentIngestPoll",
  migration: "jobKindMigration",
  "retention-sweep": "jobKindRetentionSweep",
  "events-compaction": "jobKindEventsCompaction",
  "database-backup": "jobKindDatabaseBackup",
  "artwork-sweep": "jobKindArtworkSweep",
  "asset-sweep": "jobKindAssetSweep",
  "decode-verify": "jobKindDecodeVerify",
  "skip-detect-sweep": "jobKindSkipDetectSweep",
};

const JOB_STATUS_LABELS: Record<string, StringKey> = {
  queued: "jobStatusQueued",
  running: "jobStatusRunning",
  yielded: "jobStatusYielded",
  completed: "jobStatusCompleted",
  failed: "jobStatusFailed",
  canceled: "jobStatusCanceled",
};

const JOB_PROGRESS_UNIT_LABELS: Record<string, StringKey> = {
  downloads: "jobProgressUnitDownloads",
  files: "jobProgressUnitFiles",
  items: "jobProgressUnitItems",
  posters: "jobProgressUnitPosters",
};

const JOB_PROGRESS_MESSAGE_LABELS: Record<string, StringKey> = {
  Scanning: "jobProgressMessageScanning",
  "Scan complete": "jobProgressMessageScanComplete",
  "Polling subscriptions": "jobProgressMessagePollingSubscriptions",
  "Generating artwork": "jobProgressMessageGeneratingArtwork",
  "Generating assets": "jobProgressMessageGeneratingAssets",
  "Verifying decode integrity": "jobProgressMessageVerifyingDecodeIntegrity",
  "Detecting skip-segments": "jobProgressMessageDetectingSkipSegments",
};

function lookup(labels: LabelSource, key: StringKey): string {
  return typeof labels === "function" ? labels(key) : (labels[key] ?? key);
}

function humanizeWireValue(value: string): string {
  const words = String(value || "")
    .trim()
    .replace(/[_-]+/g, " ")
    .replace(/\s+/g, " ")
    .toLowerCase();
  if (!words) return "";
  return words.charAt(0).toUpperCase() + words.slice(1);
}

function mappedLabel(
  value: string,
  mapping: Record<string, StringKey>,
  labels: LabelSource,
): string {
  const key = mapping[value];
  return key ? lookup(labels, key) : humanizeWireValue(value);
}

function mappedDiagnostic(
  value: string,
  mapping: Record<string, StringKey>,
  labels: LabelSource,
): string {
  const key = mapping[value];
  return key ? lookup(labels, key) : value;
}

export function jobKindLabel(kind: string, labels: LabelSource): string {
  return mappedLabel(kind, JOB_KIND_LABELS, labels);
}

export function jobStatusLabel(status: string, labels: LabelSource): string {
  return mappedLabel(status, JOB_STATUS_LABELS, labels);
}

function jobProgressUnitLabel(unit: string, labels: LabelSource): string {
  return mappedLabel(unit, JOB_PROGRESS_UNIT_LABELS, labels);
}

export function jobProgressMessageLabel(message: string, labels: LabelSource): string {
  const migration = /^(\d+) of (\d+) items matched$/.exec(message);
  if (migration) {
    const template = lookup(labels, "jobProgressMessageMigrationMatched");
    return template.replace("{matched}", migration[1]).replace("{total}", migration[2]);
  }
  return mappedDiagnostic(message, JOB_PROGRESS_MESSAGE_LABELS, labels);
}

export function jobStatusState(status: string): JobStatusState {
  switch (status) {
    case "running":
    case "completed":
      return "green";
    case "queued":
    case "yielded":
      return "amber";
    case "failed":
    case "canceled":
      return "red";
    default:
      return "neutral";
  }
}

export function jobProgressDetail(job: JobInfo, labels?: LabelSource): string {
  const progress = job.progress ?? { current: 0, total: 0, unit: "" };
  let detail = "";
  if (progress.total > 0) {
    detail += `${progress.current}/${progress.total}`;
    if (progress.unit) {
      detail += ` ${labels ? jobProgressUnitLabel(progress.unit, labels) : progress.unit}`;
    }
  }
  if (progress.message) {
    detail +=
      (detail ? " - " : "") +
      (labels ? jobProgressMessageLabel(progress.message, labels) : progress.message);
  }
  return detail;
}
