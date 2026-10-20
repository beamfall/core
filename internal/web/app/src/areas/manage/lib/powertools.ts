// POWERTOOLS-3 — the Manage→Convert / Manage→Rename wire seam.
//
// Conversion (apple ADR-0003 §2) and batch rename are server-authoritative: this
// module only drives the existing GraphQL surface (POWERTOOLS-1 convert job kind,
// POWERTOOLS-2 journaled BatchRenameManagedPaths + UndoBatch). It lives inside the
// manage area (Allowed Paths) rather than the shared lib/graphql so the powertools
// wiring stays self-contained.
//
// Degrades gracefully: the convert *enqueue* field is not part of the current wire
// surface, so enqueueConvertJob reports `available: false` instead of throwing when
// the server rejects an unknown mutation field. Queue + progress monitoring works
// off the existing job substrate regardless.
import { ApiError, graphql } from "@/lib/api";
import { fetchJobs } from "@/lib/graphql";
import type { BatchResultInfo, JobInfo } from "@/lib/graphql/types";

/** The convert-managed-file job kind registered server-side (internal/convert). */
export const CONVERT_JOB_KIND = "convert-managed-file";

/** The server-side conversion presets (internal/ffmpeg/convert.go convertProfiles). */
export interface ConvertPreset {
  id: string;
  /** Human label — local to this surface (no global string key needed). */
  label: string;
  audioOnly: boolean;
}

export const CONVERT_PRESETS: readonly ConvertPreset[] = [
  { id: "h264-mp4", label: "H.264 / MP4 (compatible video)", audioOnly: false },
  { id: "hevc-mp4", label: "HEVC / MP4 (smaller video)", audioOnly: false },
  { id: "aac-m4a", label: "AAC / M4A (audio only)", audioOnly: true },
];

/** The convert jobs filtered out of the shared job substrate. */
export async function fetchConvertJobs(): Promise<JobInfo[]> {
  const jobs = await fetchJobs();
  return jobs.filter((job) => job.kind === CONVERT_JOB_KIND);
}

export interface EnqueueConvertResult {
  /** false when this server has no convert-enqueue field — the UI degrades. */
  available: boolean;
  job?: JobInfo;
}

const ENQUEUE_CONVERT_MUTATION =
  "mutation EnqueueConvertJob($libraryID: String!, $srcPath: String!, $profile: String!) { enqueueConvertJob(libraryID: $libraryID, srcPath: $srcPath, profile: $profile) { id kind status progress { current total } } }";

/**
 * Ask the server to enqueue a convert-to-managed-file job. The enqueue field is
 * not part of every server's wire surface; when it is missing the GraphQL layer
 * reports an unknown-field error (HTTP 200 with `errors`, surfaced as ApiError).
 * We treat that one case as `available: false` so the Console surface degrades to
 * monitoring-only rather than showing a hard error. Any other failure rethrows.
 */
export async function enqueueConvertJob(
  libraryID: string,
  srcPath: string,
  profile: string,
): Promise<EnqueueConvertResult> {
  try {
    const data = await graphql<{ enqueueConvertJob: JobInfo }>(ENQUEUE_CONVERT_MUTATION, {
      libraryID,
      srcPath,
      profile,
    });
    return { available: true, job: data.enqueueConvertJob };
  } catch (err) {
    if (err instanceof ApiError && isUnknownFieldError(err)) {
      return { available: false };
    }
    throw err;
  }
}

/** A GraphQL "unknown field / cannot query" error means the seam is unwired. */
function isUnknownFieldError(err: ApiError): boolean {
  const message = err.message.toLowerCase();
  return (
    message.includes("enqueueconvertjob") &&
    (message.includes("unknown") ||
      message.includes("cannot query") ||
      message.includes("unsupported") ||
      message.includes("no field"))
  );
}

/** One file targeted by a batch rename: its managed path and proposed new basename. */
export interface ManagedPathRename {
  path: string;
  name: string;
}

const BATCH_RENAME_MUTATION =
  "mutation BatchRenameManagedPaths($libraryID: String!, $renames: [ManagedPathRenameInput!]!) { batchRenameManagedPaths(libraryID: $libraryID, renames: $renames) { batchID kind affected } }";

/**
 * BatchRenameManagedPaths — POWERTOOLS-2's journaled, reversible batch rename.
 * Returns the batch id the caller keeps so an Undo can target it.
 */
export async function batchRenameManagedPaths(
  libraryID: string,
  renames: ManagedPathRename[],
): Promise<BatchResultInfo> {
  const data = await graphql<{ batchRenameManagedPaths: BatchResultInfo }>(BATCH_RENAME_MUTATION, {
    libraryID,
    renames,
  });
  return data.batchRenameManagedPaths;
}

const UNDO_BATCH_MUTATION =
  "mutation UndoBatch($batchID: String!) { undoBatch(batchID: $batchID) { batchID kind affected } }";

/** UndoBatch — roll back a journaled batch (rename) by its batch id. */
export async function undoBatchRename(batchID: string): Promise<BatchResultInfo> {
  const data = await graphql<{ undoBatch: BatchResultInfo }>(UNDO_BATCH_MUTATION, {
    batchID,
  });
  return data.undoBatch;
}
