import { RefreshCw } from "lucide-react";
import { useEffect, useSyncExternalStore } from "react";
import { StatusChip } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { fetchJobs, scanLibrary } from "@/lib/graphql";
import type { JobInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { jobProgressDetail } from "@/lib/jobDisplay";

// One-click library rescan (CORE-1131). A rescan queues one scan job per
// library folder, so the card can follow every job it started; the tracker is
// module-level so the status survives moving between Console and Manage pages
// within one page load. Jobs carry no library ID on the wire, so the tracker
// only knows about scans started from this browser tab.

export type RescanPhase = "idle" | "starting" | "scanning" | "done" | "failed";

export interface RescanState {
  phase: RescanPhase;
  jobs: JobInfo[];
  error: string;
  /** Why a folder failed to queue; it outlives polling so the rescan can't end as a clean success. */
  queueError?: string;
}

const IDLE: RescanState = { phase: "idle", jobs: [], error: "" };
const ACTIVE = new Set(["queued", "running", "yielded"]);
const POLL_MS = 1500;

const states = new Map<string, RescanState>();
const listeners = new Set<() => void>();
let timer: number | undefined;

function emit() {
  for (const listener of listeners) listener();
}

function setState(libraryID: string, next: RescanState) {
  states.set(libraryID, next);
  emit();
}

export function rescanState(libraryID: string): RescanState {
  return states.get(libraryID) ?? IDLE;
}

/** Folds a library's scan jobs into one phase: any active job means scanning. */
export function phaseFor(jobs: JobInfo[]): { phase: RescanPhase; error: string } {
  if (jobs.some((job) => ACTIVE.has(job.status))) return { phase: "scanning", error: "" };
  const failed = jobs.find((job) => job.status === "failed" || job.status === "canceled");
  if (failed) return { phase: "failed", error: failed.error ?? "" };
  return { phase: "done", error: "" };
}

/** Like phaseFor, but a folder that never queued turns a finished rescan into a failure. */
function foldRescan(jobs: JobInfo[], queueError: string): RescanState {
  const folded = phaseFor(jobs);
  const state: RescanState = { jobs, ...folded, queueError: queueError || undefined };
  if (queueError && folded.phase !== "failed") {
    state.error = queueError;
    if (folded.phase === "done") state.phase = "failed";
  }
  return state;
}

async function poll() {
  const watching = [...states.entries()].filter(([, s]) => s.phase === "scanning");
  if (watching.length === 0) {
    window.clearInterval(timer);
    timer = undefined;
    return;
  }
  let jobs: JobInfo[];
  try {
    jobs = await fetchJobs();
  } catch {
    return; // keep the last known state; the next tick retries
  }
  const byID = new Map(jobs.map((job) => [job.id, job]));
  for (const [libraryID, state] of watching) {
    // A job missing from the list has aged out; treat it as finished.
    const next = state.jobs.map(
      (job) =>
        byID.get(job.id) ?? { ...job, status: ACTIVE.has(job.status) ? "completed" : job.status },
    );
    setState(libraryID, foldRescan(next, state.queueError ?? ""));
  }
}

function ensurePolling() {
  if (timer === undefined) timer = window.setInterval(() => void poll(), POLL_MS);
}

/** Queues a scan of every folder of the library and starts following the jobs. */
export async function startRescan(libraryID: string, roots: string[]): Promise<void> {
  if (roots.length === 0 || ACTIVE_PHASES.has(rescanState(libraryID).phase)) return;
  setState(libraryID, { phase: "starting", jobs: [], error: "" });
  const results = await Promise.allSettled(roots.map((root) => scanLibrary(libraryID, root)));
  const jobs: JobInfo[] = [];
  let error = "";
  for (const result of results) {
    if (result.status === "fulfilled") {
      jobs.push({
        id: result.value.id,
        kind: "scan",
        status: result.value.status,
        progress: { current: 0, total: 0, unit: "" },
      });
    } else if (!error) {
      error = result.reason instanceof Error ? result.reason.message : String(result.reason);
    }
  }
  if (jobs.length === 0) {
    setState(libraryID, { phase: "failed", jobs, error });
    return;
  }
  setState(libraryID, foldRescan(jobs, error));
  ensurePolling();
}

const ACTIVE_PHASES = new Set<RescanPhase>(["starting", "scanning"]);

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function useRescanState(libraryID: string): RescanState {
  return useSyncExternalStore(subscribe, () => rescanState(libraryID));
}

/** True while any library rescan started from this tab is still running. */
export function useAnyRescanActive(): boolean {
  return useSyncExternalStore(subscribe, () =>
    [...states.values()].some((s) => ACTIVE_PHASES.has(s.phase)),
  );
}

/** Test seam: forget every tracked scan. */
export function resetRescansForTest() {
  states.clear();
  window.clearInterval(timer);
  timer = undefined;
  emit();
}

export function RescanButton({
  libraryID,
  roots,
  size = "sm",
}: {
  libraryID: string;
  roots: string[];
  size?: "sm" | "default";
}) {
  const t = useT();
  const state = useRescanState(libraryID);
  const busy = ACTIVE_PHASES.has(state.phase);
  return (
    <Button
      type="button"
      size={size}
      disabled={busy || roots.length === 0}
      title={roots.length === 0 ? t("rescanNeedsFolder") : undefined}
      onClick={() => void startRescan(libraryID, roots)}
    >
      <RefreshCw className={busy ? "size-4 animate-spin" : "size-4"} aria-hidden />
      {busy ? t("rescanScanning") : t("rescanLibrary")}
    </Button>
  );
}

/** Rescans every library that has folders; one click for the whole server. */
export function RescanAllButton({
  libraries,
  variant = "outline",
}: {
  libraries: { id: string; roots?: string[] | null }[];
  variant?: "outline" | "default";
}) {
  const t = useT();
  const busy = useAnyRescanActive();
  const scannable = libraries.filter((library) => (library.roots ?? []).length > 0);
  return (
    <Button
      type="button"
      size="sm"
      variant={variant}
      disabled={busy || scannable.length === 0}
      onClick={() => {
        for (const library of scannable) void startRescan(library.id, library.roots ?? []);
      }}
    >
      <RefreshCw className={busy ? "size-4 animate-spin" : "size-4"} aria-hidden />
      {busy ? t("rescanScanning") : t("rescanAllLibraries")}
    </Button>
  );
}

/** The card's scan status line: nothing until a rescan starts. */
export function RescanStatus({ libraryID, hasRoots }: { libraryID: string; hasRoots: boolean }) {
  const t = useT();
  const state = useRescanState(libraryID);
  useEffect(() => {
    if (state.phase === "scanning") ensurePolling();
  }, [state.phase]);

  if (!hasRoots) {
    return <p className="text-xs text-muted-foreground">{t("rescanNeedsFolder")}</p>;
  }
  if (state.phase === "idle") return null;
  const running = state.jobs.find((job) => job.status === "running");
  const detail = running ? jobProgressDetail(running, t) : "";
  return (
    <div role="status" className="flex flex-wrap items-center gap-2 text-sm">
      {state.phase === "starting" || state.phase === "scanning" ? (
        <StatusChip tone="cyan">{t("rescanScanning")}</StatusChip>
      ) : state.phase === "done" ? (
        <StatusChip tone="green">{t("rescanDone")}</StatusChip>
      ) : (
        <StatusChip tone="red">{t("rescanFailed")}</StatusChip>
      )}
      {detail && <span className="text-muted-foreground">{detail}</span>}
      {state.phase === "failed" && state.error && (
        <span className="text-muted-foreground">{state.error}</span>
      )}
    </div>
  );
}
