import type { JobInfo } from "@/lib/graphql/types";
import type { StringKey } from "@/strings";

type LabelSource = Partial<Record<StringKey, string>> | ((key: StringKey) => string);

export type JobStatusState = "neutral" | "green" | "amber" | "red";

const JOB_KIND_LABELS: Record<string, StringKey> = {
  scan: "jobKindScan",
  "library-metadata-refresh": "jobKindLibraryMetadataRefresh",
  "subscription-poll": "jobKindSubscriptionPoll",
  "qbittorrent-ingest-poll": "jobKindQBittorrentIngestPoll",
  migration: "jobKindMigration",
  "retention-sweep": "jobKindRetentionSweep",
  "events-compaction": "jobKindEventsCompaction",
  "database-backup": "jobKindDatabaseBackup",
  "artwork-sweep": "jobKindArtworkSweep",
  "entity-enrichment": "jobKindEntityEnrichment",
  "asset-sweep": "jobKindAssetSweep",
  "decode-verify": "jobKindDecodeVerify",
  "skip-detect-sweep": "jobKindSkipDetectSweep",
  "playback.precompute": "jobKindPlaybackPrecompute",
  "playback.precompute.evict": "jobKindPlaybackPrecomputeEvict",
  "playback-composed-sweep": "jobKindPlaybackComposedSweep",
  "offline-pack-build": "jobKindOfflinePackBuild",
  "offline-pack-expire": "jobKindOfflinePackExpire",
  "photo-export": "jobKindPhotoExport",
  "convert-managed-file": "jobKindConvertManagedFile",
  "subtitle-offset-prune": "jobKindSubtitleOffsetPrune",
};

// One plain sentence per job kind: what the job does and why it exists.
const JOB_KIND_DESCRIPTIONS: Record<string, StringKey> = {
  scan: "jobKindDescScan",
  "library-metadata-refresh": "jobKindDescLibraryMetadataRefresh",
  "subscription-poll": "jobKindDescSubscriptionPoll",
  "qbittorrent-ingest-poll": "jobKindDescQBittorrentIngestPoll",
  migration: "jobKindDescMigration",
  "retention-sweep": "jobKindDescRetentionSweep",
  "events-compaction": "jobKindDescEventsCompaction",
  "database-backup": "jobKindDescDatabaseBackup",
  "artwork-sweep": "jobKindDescArtworkSweep",
  "entity-enrichment": "jobKindDescEntityEnrichment",
  "asset-sweep": "jobKindDescAssetSweep",
  "decode-verify": "jobKindDescDecodeVerify",
  "skip-detect-sweep": "jobKindDescSkipDetectSweep",
  "playback.precompute": "jobKindDescPlaybackPrecompute",
  "playback.precompute.evict": "jobKindDescPlaybackPrecomputeEvict",
  "playback-composed-sweep": "jobKindDescPlaybackComposedSweep",
  "offline-pack-build": "jobKindDescOfflinePackBuild",
  "offline-pack-expire": "jobKindDescOfflinePackExpire",
  "photo-export": "jobKindDescPhotoExport",
  "convert-managed-file": "jobKindDescConvertManagedFile",
  "subtitle-offset-prune": "jobKindDescSubtitleOffsetPrune",
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
  "Refreshing metadata": "jobProgressMessageRefreshingMetadata",
  "Enriching catalog": "jobProgressMessageEnrichingCatalog",
};

function lookup(labels: LabelSource, key: StringKey): string {
  return typeof labels === "function" ? labels(key) : (labels[key] ?? key);
}

function humanizeWireValue(value: string): string {
  const words = String(value || "")
    .trim()
    .replace(/[._-]+/g, " ")
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

/** What a job does, or "" for a kind this client does not know. */
export function jobKindDescription(
  job: Pick<JobInfo, "kind" | "target">,
  labels: LabelSource,
): string {
  if (job.kind === "scan" && job.target?.backfill) return lookup(labels, "jobKindDescScanBackfill");
  const key = JOB_KIND_DESCRIPTIONS[job.kind];
  return key ? lookup(labels, key) : "";
}

/** "Library · /folder" for a job that names its Library, or "" when it does not. */
export function jobTargetDetail(job: Pick<JobInfo, "target">): string {
  const target = job.target;
  if (!target) return "";
  const title = target.libraryTitle || target.libraryID;
  return target.root ? `${title} · ${target.root}` : title;
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

export function jobProgressDetail(job: Partial<JobInfo>, labels?: LabelSource): string {
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

/**
 * A job's progress read that also counts work with no known total: "42 files -
 * Scanning", never "42/0". With a total it is the plain progress detail.
 */
export function jobProgressCount(progress: JobInfo["progress"], labels?: LabelSource): string {
  const detail = jobProgressDetail({ progress }, labels);
  if (progress.total > 0 || progress.current <= 0) return detail;
  const unit = labels ? jobProgressUnitLabel(progress.unit, labels) : progress.unit;
  const counted = unit ? `${progress.current} ${unit}` : String(progress.current);
  return detail ? `${counted} - ${detail}` : counted;
}

/**
 * A job's progress as the job queue shows it (CORE-2352): "117 of 502 files ·
 * Scanning", or "2,055 items · Generating assets" for work with no known total,
 * with counts in the display locale's digits.
 */
export function jobProgressRead(
  progress: JobInfo["progress"],
  labels: LabelSource,
  locale?: string,
): string {
  const number = new Intl.NumberFormat(locale);
  let count = "";
  if (progress.total > 0) {
    count = lookup(labels, "jobProgressOfTotal")
      .replace("{current}", number.format(progress.current))
      .replace("{total}", number.format(progress.total));
  } else if (progress.current > 0) {
    count = number.format(progress.current);
  }
  if (count && progress.unit) count += ` ${jobProgressUnitLabel(progress.unit, labels)}`;
  const message = progress.message ? jobProgressMessageLabel(progress.message, labels) : "";
  return [count, message].filter(Boolean).join(" · ");
}

const JOB_EVENT_LABELS: Record<string, StringKey> = {
  progress: "jobEventProgress",
  "scan-summary": "jobEventScanSummary",
  "scan-root-unavailable": "jobEventScanRootUnavailable",
};

/** A job history entry's type as a label: its status name, or what it records. */
export function jobEventLabel(type: string, labels: LabelSource): string {
  const key = JOB_EVENT_LABELS[type];
  return key ? lookup(labels, key) : jobStatusLabel(type, labels);
}

/**
 * What one job history entry says, for the job log. A progress entry reads as
 * the job's own progress line ("120/340 files - Scanning"); an entry whose
 * payload only repeats its status says nothing more than its type, so it is
 * empty. Returns null for a payload this does not recognise: the caller shows
 * that one as stored.
 */
export function jobEventDetail(
  event: { type: string; payload?: string },
  labels?: LabelSource,
): string | null {
  let parsed: unknown;
  try {
    parsed = JSON.parse(event.payload ?? "");
  } catch {
    return null;
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) return null;
  const payload = parsed as Record<string, unknown>;
  const keys = Object.keys(payload);
  if (payload.type === "progress") {
    const count = (value: unknown) => (typeof value === "number" && value > 0 ? value : 0);
    const text = (value: unknown) => (typeof value === "string" ? value : "");
    const progress = {
      current: count(payload.current),
      total: count(payload.total),
      unit: text(payload.unit),
      message: text(payload.message),
    };
    return jobProgressCount(progress, labels);
  }
  if (payload.status === event.type && keys.every((key) => key === "status" || key === "reason")) {
    return typeof payload.reason === "string" ? payload.reason : "";
  }
  return null;
}
