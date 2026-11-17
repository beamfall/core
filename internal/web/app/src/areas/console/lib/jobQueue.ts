import { graphql } from "@/lib/api";
import type { JobInfo } from "@/lib/graphql/types";

// The Console job queue (CORE-2308). The shared jobs read selects no times, so
// the Console asks for them itself: a row says when the job was queued,
// started or finished and how long it ran. The read is the same jobs field
// (Admin Session, ADR-0013), only with more of graph.JobInfo selected.
const CONSOLE_JOBS_QUERY =
  "query ConsoleJobs { jobs { id kind class status error attempts createdAt updatedAt startedAt finishedAt progress { current total unit message } target { libraryID libraryTitle root backfill } } }";

export async function fetchConsoleJobs(signal?: AbortSignal): Promise<JobInfo[]> {
  const data = await graphql<{ jobs: JobInfo[] | null }>(CONSOLE_JOBS_QUERY, {}, signal);
  return data.jobs ?? [];
}

/** Queued, running and yielded jobs still have work ahead of them. */
export function isLiveJob(status: string): boolean {
  return status === "queued" || status === "running" || status === "yielded";
}

/** One shown row standing for `count` jobs of the same kind (and target). */
export interface JobGroup {
  job: JobInfo;
  count: number;
}

export interface JobQueueView {
  /** Work under way: running first, then yielded, then queued. */
  live: JobInfo[];
  /** The newest failure per kind and Library folder that no later run cleared. */
  failed: JobGroup[];
  /** Everything else, one row per job kind (its newest run), newest first. */
  history: JobGroup[];
}

const LIVE_ORDER: Record<string, number> = { running: 0, yielded: 1, queued: 2 };

/** When the job last changed: finished, else updated, else created (epoch ms). */
export function jobTime(job: JobInfo): number {
  const at = Date.parse(job.finishedAt ?? job.updatedAt ?? job.createdAt ?? "");
  return Number.isNaN(at) ? 0 : at;
}

function targetKey(job: JobInfo): string {
  return [job.kind, job.target?.libraryID ?? "", job.target?.root ?? ""].join("\u0000");
}

function newestFirst(a: JobGroup, b: JobGroup): number {
  return jobTime(b.job) - jobTime(a.job);
}

// Recurring jobs (polls, sweeps, evictions) leave one row per run; the queue
// keeps live work and unresolved failures in front and folds the rest into one
// row per kind. A failure that a later run of the same kind and folder
// finished cleanly is history, not attention.
export function arrangeJobQueue(jobs: JobInfo[]): JobQueueView {
  const live = jobs
    .filter((job) => isLiveJob(job.status))
    .sort((a, b) => (LIVE_ORDER[a.status] ?? 3) - (LIVE_ORDER[b.status] ?? 3));

  const lastClean = new Map<string, number>();
  for (const job of jobs) {
    if (job.status !== "completed") continue;
    const key = targetKey(job);
    lastClean.set(key, Math.max(lastClean.get(key) ?? 0, jobTime(job)));
  }

  const failures = new Map<string, JobGroup>();
  const kinds = new Map<string, JobGroup>();
  for (const job of jobs) {
    if (isLiveJob(job.status)) continue;
    const key = targetKey(job);
    const unresolved = job.status === "failed" && jobTime(job) >= (lastClean.get(key) ?? 0);
    const groups = unresolved ? failures : kinds;
    const groupKey = unresolved ? key : job.kind;
    const group = groups.get(groupKey);
    if (!group) groups.set(groupKey, { job, count: 1 });
    else {
      group.count += 1;
      if (jobTime(job) > jobTime(group.job)) group.job = job;
    }
  }

  return {
    live,
    failed: [...failures.values()].sort(newestFirst),
    history: [...kinds.values()].sort(newestFirst),
  };
}

/** Failures still waiting on the operator, for the Overview attention list. */
export function unresolvedFailedJobs(jobs: JobInfo[]): JobInfo[] {
  return arrangeJobQueue(jobs).failed.map((group) => group.job);
}

/** Whole seconds a job ran: start to finish, or start to now while it runs. */
export function jobRunSeconds(job: JobInfo, now: number): number | null {
  const started = Date.parse(job.startedAt ?? "");
  if (Number.isNaN(started)) return null;
  const finished = isLiveJob(job.status) ? now : Date.parse(job.finishedAt ?? "");
  if (Number.isNaN(finished)) return null;
  return Math.max(0, Math.round((finished - started) / 1000));
}

const jobLogSecretPatterns = [
  /bfk_[A-Za-z0-9_-]+/gi,
  /Bearer\s+\S+/gi,
  /Basic\s+\S+/gi,
  /[?&]token=[^&\s"']+/gi,
  /\/api\/media\/[^\s"']+/gi,
  /\/api\/pack\/[^\s"']+/gi,
  /https?:\/\/[^\s"']+/gi,
];

/** Job error and log text with tokens, credentials and URLs blanked out. */
export function redactJobLogPayload(payload: string): string {
  return jobLogSecretPatterns.reduce(
    (next, pattern) => next.replace(pattern, "[REDACTED]"),
    payload,
  );
}
