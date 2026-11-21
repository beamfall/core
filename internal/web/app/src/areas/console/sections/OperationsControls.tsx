import { RefreshCw } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { RescanAllButton, useRescanState } from "@/components/beamfall/LibraryRescan";
import {
  ControlRow,
  EmptyState,
  ErrorLine,
  ErrorState,
  PresetSelect,
  StatusChip,
  type StatusTone,
} from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { formatRelativeTime, formatUtcTime } from "@/lib/activityDisplay";
import { Advanced } from "@/lib/advancedSettings";
import { ApiError } from "@/lib/api";
import {
  cancelJob,
  fetchConsoleConfig,
  fetchJobEvents,
  fetchLibraries,
  fetchOperationsGovernor,
  retryJob,
  runEventsCompaction,
  setConsoleConfig,
} from "@/lib/graphql";
import type { JobEventInfo, JobInfo } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import {
  jobEventDetail,
  jobEventLabel,
  jobKindDescription,
  jobKindLabel,
  jobProgressRead,
  jobStatusLabel,
  jobStatusState,
  jobTargetDetail,
} from "@/lib/jobDisplay";
import type { StringKey } from "@/strings";
import { ConfirmButton } from "../ConfirmButton";
import {
  arrangeJobQueue,
  collapseJobLog,
  fetchConsoleJobs,
  formatJobEventTime,
  jobRunSeconds,
  redactJobEventPayload,
  redactJobLogPayload,
} from "../lib/jobQueue";
import { Section } from "../Section";
import { useAsync } from "../useAsync";
import { useEventRefresh } from "./Dashboard";

function statusTone(status: string): StatusTone {
  const state = jobStatusState(status);
  return state === "red" || state === "green" || state === "amber" ? state : "neutral";
}

// A job is live (cancelable) while queued, running, or yielded; it is terminal
// (retryable) once completed, failed, or canceled. Mirrors the server-side
// jobCancel/jobRetry guards (ErrJobTerminal / ErrJobNotTerminal).
function isLive(status: string): boolean {
  return status === "queued" || status === "running" || status === "yielded";
}

// One status count tile in the governor snapshot grid.
function StatTile({ label, value, unit }: { label: StringKey; value: number; unit?: StringKey }) {
  const t = useT();
  return (
    <div className="console-card console-card-pad">
      <div className="console-control-label">{t(label)}</div>
      <div className="mt-1 text-[18px] font-semibold leading-none console-num">
        {value}
        {unit && <span className="ml-1 text-xs font-normal text-muted-foreground">{t(unit)}</span>}
      </div>
    </div>
  );
}

// Resource governor status (ADR-0013 §5 / ADR-0061): the live read-only posture
// behind background generation — worker count, CPU budget, in-flight transcodes
// and generation, whether generation is allowed, and whether realtime transcode
// is currently preempting background work. When the governor is not wired the
// query reports `available: false`: the snapshot reports the configured worker
// count and the panel says so plainly rather than blanking.
function GovernorStatus() {
  const t = useT();
  const { data, loading, failed, refresh } = useAsync(fetchOperationsGovernor);

  // A scan emits events many times a second: gather them, and keep the last
  // snapshot on screen while the next one loads.
  useEventRefresh(refresh);

  return (
    <Section
      heading="governorHeading"
      collapsible
      actions={
        <Button variant="outline" size="sm" onClick={() => void refresh()}>
          <RefreshCw className="console-nav-icon" aria-hidden />
          {t("governorRefresh")}
        </Button>
      }
    >
      {loading && !data ? (
        <Skeleton className="h-40 w-full" />
      ) : failed ? (
        <ErrorState message="governorUnavailable" />
      ) : !data?.available ? (
        <div className="space-y-1">
          <EmptyState message="governorUnwiredLine" />
          <p className="px-1 text-xs leading-snug text-muted-foreground">
            {t("governorUnwiredHelp")}
          </p>
        </div>
      ) : (
        <div className="space-y-4">
          <div className="flex items-center gap-2">
            <StatusChip tone="green">{t("governorActive")}</StatusChip>
          </div>

          <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
            <StatTile label="governorWorkers" value={data.generationWorkers} />
            <StatTile
              label="governorCPUBudget"
              value={data.backgroundCPUBudget}
              unit="governorCPUBudgetUnit"
            />
            <StatTile label="governorActiveTranscodes" value={data.activeTranscodes} />
            <StatTile label="governorActiveGeneration" value={data.activeGeneration} />
          </div>

          <div className="flex flex-wrap items-center gap-2">
            <StatusChip tone={data.generationAllowed ? "green" : "amber"}>
              {t(data.generationAllowed ? "governorGenerationAllowed" : "governorGenerationPaused")}
            </StatusChip>
            {data.transcodePreemptingBackground && (
              <StatusChip tone="amber">{t("governorPreempting")}</StatusChip>
            )}
            {data.lowPowerMode && <StatusChip tone="iris">{t("governorLowPower")}</StatusChip>}
          </div>
        </div>
      )}
    </Section>
  );
}

// Governor write settings (ADR-0013 §5): the operator controls over the
// background worker pool. The worker count is the governor's generation
// concurrency; low-power mode pins it to a single worker so background work
// yields hard to realtime playback. Both write through setConsoleConfig's
// backgroundWorkers field (the governor reads its configured worker count).
function GovernorSettings() {
  const t = useT();
  const config = useAsync(fetchConsoleConfig);
  const [compactionQueued, setCompactionQueued] = useState(false);
  const [compactionFailed, setCompactionFailed] = useState(false);

  // Every control reads the *persisted* values, so the switch and the
  // dropdowns always agree with what the server holds (CORE-2445).
  const workers = Math.max(1, config.data?.backgroundWorkers ?? 2);
  const retentionDays = Math.max(1, config.data?.eventsRetentionDays ?? 180);
  const lowPower = workers <= 1;
  // Low-power on or off covers one and two workers; any other count, or a
  // retention other than the default, keeps its field in the Normal view.
  const customWorkers = workers > 2;
  const customRetention = retentionDays !== 180;

  async function save(next: number) {
    config.setData(await setConsoleConfig({ backgroundWorkers: Math.max(1, Math.floor(next)) }));
  }

  async function saveRetentionDays(next: number) {
    config.setData(await setConsoleConfig({ eventsRetentionDays: Math.max(1, Math.floor(next)) }));
  }

  async function onToggleLowPower(on: boolean) {
    // Low-power pins to a single worker; turning it off restores at least two.
    await save(on ? 1 : Math.max(2, workers));
  }

  async function onRunCompaction() {
    setCompactionFailed(false);
    try {
      await runEventsCompaction();
      setCompactionQueued(true);
    } catch {
      setCompactionFailed(true);
    }
  }

  const days = (count: number) => t("sessionLengthDays", { count });

  return (
    <Section heading="governorSettingsHeading">
      {config.loading ? (
        <Skeleton className="h-32 w-full" />
      ) : config.failed ? (
        <ErrorState message="governorSettingsUnavailable" />
      ) : (
        <div className="space-y-3">
          {/* The section heading already names this group, so the fieldset
              carries it as its accessible name only and draws no second title
              (CORE-2352). */}
          <fieldset className="console-fieldset" aria-label={t("governorSettingsHeading")}>
            <div className="console-fieldset-body">
              <ControlRow
                label="governorLowPower"
                help="governorLowPowerHelp"
                tip="governorLowPowerTip"
                setting="console.operations.lowPower"
                control={
                  <Switch
                    checked={lowPower}
                    onCheckedChange={(on) => void onToggleLowPower(on)}
                    aria-label={t("governorLowPower")}
                  />
                }
              />
              <Advanced keep={customWorkers}>
                <ControlRow
                  label="governorBackgroundWorkers"
                  help="governorBackgroundWorkersHelp"
                  tip="governorBackgroundWorkersTip"
                  setting="console.operations.workers"
                  control={
                    <PresetSelect
                      value={workers}
                      presets={WORKER_PRESETS.map((value) => ({ value, label: String(value) }))}
                      onChange={(next) => void save(next)}
                      aria-label={t("governorBackgroundWorkers")}
                    />
                  }
                />
              </Advanced>
              <Advanced keep={customRetention}>
                <ControlRow
                  label="eventsRetentionDays"
                  help="eventsRetentionDaysHelp"
                  tip="eventsRetentionDaysTip"
                  setting="console.operations.eventsRetention"
                  control={
                    <PresetSelect
                      value={retentionDays}
                      presets={RETENTION_PRESETS.map((value) => ({ value, label: days(value) }))}
                      format={days}
                      onChange={(next) => void saveRetentionDays(next)}
                      aria-label={t("eventsRetentionDays")}
                    />
                  }
                />
              </Advanced>
              <Advanced>
                <ControlRow
                  label="jobKindEventsCompaction"
                  help="jobKindDescEventsCompaction"
                  control={
                    <>
                      <Button type="button" size="sm" onClick={() => void onRunCompaction()}>
                        {t("eventsCompactionRun")}
                      </Button>
                      {compactionQueued && (
                        <StatusChip tone="green">{t("eventsCompactionQueued")}</StatusChip>
                      )}
                      {compactionFailed && (
                        <StatusChip tone="red">{t("eventsCompactionFailed")}</StatusChip>
                      )}
                    </>
                  }
                />
              </Advanced>
            </div>
          </fieldset>
        </div>
      )}
    </Section>
  );
}

// Worker counts and event-retention windows offered as presets (CORE-2445);
// a saved value outside these still shows as its own choice.
const WORKER_PRESETS = [1, 2, 4, 8];
const RETENTION_PRESETS = [30, 90, 180, 365];

// The inline durable history/logs timeline for one job (jobEvents). Opened
// under a selected job row. Payload is omitted by the server for restricted
// targets — rendered as a redacted placeholder, never hidden. Runs of
// progress entries collapse into their latest one, and repeated pause and
// resume cycles into one summary line, until the operator asks for all of them
// (CORE-2352).
function JobLog({ jobID, version }: { jobID: string; version: number }) {
  const t = useT();
  const locale = useDisplayLocale();
  const [showAll, setShowAll] = useState(false);
  const { data, loading, failed, refresh } = useAsync(
    (signal) => fetchJobEvents(jobID, signal),
    [jobID],
  );
  const events: JobEventInfo[] = data ?? [];
  const collapsed = useMemo(() => collapseJobLog(events), [events]);

  // A newer version reloads the open log in place, keeping its rows on screen.
  const loaded = useRef(version);
  useEffect(() => {
    if (loaded.current === version) return;
    loaded.current = version;
    void refresh();
  }, [version, refresh]);

  if (loading && !data) return <Skeleton className="h-16 w-full" />;
  if (failed) return <ErrorState message="jobLogsUnavailable" />;
  if (events.length === 0) return <EmptyState message="jobLogsEmpty" />;

  const hidden = collapsed.reduce((sum, line) => sum + line.folded, 0);
  const lines = showAll ? events.map((event) => ({ event, folded: 0, pauses: 0 })) : collapsed;
  const now = new Date();
  return (
    <div className="console-card console-job-events">
      {lines.map(({ event: ev, pauses }) => {
        const at = new Date(ev.occurredAt);
        // The formatter reads decoded fields that were redacted first, since
        // formatting a unit or message can reshape a secret past the
        // patterns. A payload it does not recognise shows as redacted text.
        const detail =
          pauses > 0
            ? ""
            : ev.payload
              ? (jobEventDetail({ ...ev, payload: redactJobEventPayload(ev.payload) }, t) ??
                redactJobLogPayload(ev.payload))
              : t("jobLogPayloadRedacted");
        const label =
          pauses > 0 ? t("jobLogPauseCycles", { count: pauses }) : jobEventLabel(ev.type, t);
        return (
          <div key={pauses > 0 ? `${ev.id}-pauses` : ev.id} className="console-job-event">
            <time
              className="console-mono text-muted-foreground"
              dateTime={ev.occurredAt}
              title={Number.isNaN(at.getTime()) ? ev.occurredAt : formatUtcTime(at)}
            >
              {formatJobEventTime(ev.occurredAt, now, locale) || ev.occurredAt}
            </time>
            <span className="font-medium">{label}</span>
            <span className="console-job-event-detail console-mono" title={detail || undefined}>
              {detail}
            </span>
          </div>
        );
      })}
      {hidden > 0 && (
        <Button
          variant="ghost"
          size="sm"
          className="console-job-events-toggle"
          onClick={() => setShowAll((all) => !all)}
        >
          {showAll ? t("jobLogCollapse") : t("jobLogShowAll", { count: hidden })}
        </Button>
      )}
    </div>
  );
}

// Job queue with per-job operations (ADR-0013 §4): the live job substrate plus
// Cancel (queued/running/yielded → jobCancel) and Retry (terminal → jobRetry),
// and an inline logs view (jobEvents). Live via the SSE event stream (ADR-0040):
// any server-side job event triggers a refresh. Each row says what the job does
// and, when the session may see it, which Library folder it works on; Scan now
// force-queues a scan of every library folder.
/** Why Scan now could not queue a Library's folders; those folders get no job row to show it. */
function RescanQueueFailure({ libraryID, title }: { libraryID: string; title: string }) {
  const t = useT();
  const state = useRescanState(libraryID);
  const error =
    state.queueError || (state.phase === "failed" && state.jobs.length === 0 ? state.error : "");
  if (!error) return null;
  return (
    <p role="alert" className="console-job-help">
      {title}: {t("rescanFailed")} — {error}
    </p>
  );
}

function formatJobDuration(seconds: number, t: ReturnType<typeof useT>): string {
  if (seconds < 60) return t("jobDurationSeconds", { seconds });
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return t("jobDurationMinutes", { minutes, seconds: seconds % 60 });
  return t("jobDurationHours", { hours: Math.floor(minutes / 60), minutes: minutes % 60 });
}

// Times are elapsed since the first claim, including waits and yields, not
// active run time (CORE-2572). Paused jobs keep their paused note (CORE-2352).
function jobWhen(job: JobInfo, now: number, locale: string, t: ReturnType<typeof useT>): string {
  const live = isLive(job.status);
  const at = job.status === "queued" ? job.createdAt : live ? job.startedAt : job.finishedAt;
  const parsed = Date.parse(at ?? "");
  const parts: string[] = [];
  const attempts = job.attempts ?? 0;
  if (!Number.isNaN(parsed)) {
    const when = formatRelativeTime(new Date(parsed), new Date(now), locale);
    const key =
      job.status === "queued" ? "jobWhenQueued" : live ? "jobWhenStarted" : "jobWhenFinished";
    parts.push(t(key, { when }));
  }
  if (job.status === "yielded") {
    parts.push(t("jobPausedNote"));
    if (attempts > 1) parts.push(t("jobAttempt", { attempts }));
    return parts.join(" · ");
  }
  const seconds = job.status === "queued" ? null : jobRunSeconds(job, now);
  if (seconds !== null) {
    const duration = formatJobDuration(seconds, t);
    const key = live ? "jobSinceFirstStart" : attempts > 1 ? "jobTookOverAttempts" : "jobTook";
    parts.push(t(key, { duration, attempts }));
  }
  if (job.status === "running" && attempts > 1) parts.push(t("jobAttempt", { attempts }));
  return parts.join(" · ");
}

function JobRow({
  job,
  count,
  countLabel,
  now,
  logOpen,
  logVersion,
  onToggleLog,
  onCancel,
  onRetry,
}: {
  job: JobInfo;
  count: number;
  countLabel: "jobRunCount" | "jobFailureCount";
  now: number;
  logOpen: boolean;
  logVersion: number;
  onToggleLog: () => void;
  onCancel: (id: string) => Promise<void>;
  onRetry: (id: string) => Promise<void>;
}) {
  const t = useT();
  const locale = useDisplayLocale();
  const [retryError, setRetryError] = useState("");

  async function retry() {
    setRetryError("");
    try {
      await onRetry(job.id);
    } catch (err) {
      setRetryError(err instanceof ApiError && err.message ? err.message : t("jobsUnavailable"));
    }
  }

  const { current, total } = job.progress;
  const pct = total > 0 ? Math.min(100, Math.round((current / total) * 100)) : 0;
  const tone = statusTone(job.status);
  const live = isLive(job.status);
  const description = jobKindDescription(job, t);
  const target = jobTargetDetail(job);
  const when = jobWhen(job, now, locale, t);
  const progress = jobProgressRead(job.progress, t, locale);
  // Only work under way with a known total gets a bar (CORE-2352): a finished
  // job's bar says nothing its status does not, and work with no total shows
  // its count alone.
  const showBar = live && total > 0;
  // Every row reads the same way (CORE-2352): what and its status with the
  // actions, then its progress, then when, then the folder it works on, the
  // error and what the job does.
  return (
    <div className="console-job-row console-job-row-ops">
      <div className="console-job-head">
        <span className="console-job-name">{jobKindLabel(job.kind, t)}</span>
        <StatusChip tone={tone}>{jobStatusLabel(job.status, t)}</StatusChip>
        {count > 1 && <StatusChip tone="neutral">{t(countLabel, { count })}</StatusChip>}
        <span className="console-job-actions">
          <Button variant="ghost" size="sm" onClick={onToggleLog}>
            {t(logOpen ? "jobLogsHide" : "jobLogsShow")}
          </Button>
          {live ? (
            <ConfirmButton
              size="sm"
              label={t("jobCancelAction")}
              confirm="jobCancelConfirm"
              onConfirm={() => onCancel(job.id)}
            />
          ) : (
            <Button variant="outline" size="sm" onClick={() => void retry()}>
              {t("jobRetryAction")}
            </Button>
          )}
        </span>
      </div>
      {(showBar || progress) && (
        <div className="console-job-progress">
          {showBar && (
            <span
              className="console-jbar"
              role="progressbar"
              aria-valuenow={pct}
              aria-valuemin={0}
              aria-valuemax={100}
              aria-label={t("jobProgressAriaLabel")}
            >
              <i className={tone === "amber" ? "warn" : undefined} style={{ width: `${pct}%` }} />
            </span>
          )}
          {progress && (
            <span className="console-job-num" title={progress}>
              {progress}
            </span>
          )}
        </div>
      )}
      {when && <div className="console-job-when">{when}</div>}
      {target && (
        <div className="console-job-target">
          <span className="console-mono">{target}</span>
          {job.target?.backfill && <StatusChip tone="neutral">{t("jobTargetBackfill")}</StatusChip>}
        </div>
      )}
      {job.status === "failed" && job.error && (
        <p className="console-job-error">{redactJobLogPayload(job.error)}</p>
      )}
      <ErrorLine message={retryError} />
      {description && <p className="console-job-about">{description}</p>}
      {logOpen && (
        <div className="console-job-log">
          <JobLog key={job.id} jobID={job.id} version={logVersion} />
        </div>
      )}
    </div>
  );
}

// The job queue (CORE-2308): work under way and unresolved failures first,
// with their times and error text; finished runs fold into one row per kind
// under a collapsed history.
function JobQueue() {
  const t = useT();
  const { data, loading, failed, refresh } = useAsync(fetchConsoleJobs);
  const libraries = useAsync(fetchLibraries);
  const [openLog, setOpenLog] = useState<string | null>(null);
  const [logRefresh, setLogRefresh] = useState(0);
  const view = useMemo(() => arrangeJobQueue(data ?? []), [data]);
  const now = Date.now();

  const refreshWithLog = useCallback(async () => {
    await refresh();
    setLogRefresh((n) => n + 1);
  }, [refresh]);
  useEventRefresh(refreshWithLog);

  async function onCancel(id: string) {
    await cancelJob(id);
    await refresh();
  }

  async function onRetry(id: string) {
    await retryJob(id);
    await refresh();
  }

  function row(
    job: JobInfo,
    count = 1,
    countLabel: "jobRunCount" | "jobFailureCount" = "jobRunCount",
  ) {
    return (
      <JobRow
        key={job.id}
        job={job}
        count={count}
        countLabel={countLabel}
        now={now}
        logOpen={openLog === job.id}
        logVersion={logRefresh}
        onToggleLog={() => setOpenLog(openLog === job.id ? null : job.id)}
        onCancel={onCancel}
        onRetry={onRetry}
      />
    );
  }

  const front = view.live.length + view.failed.length;
  return (
    <Section
      heading="jobQueueHeading"
      actions={
        <>
          <RescanAllButton libraries={libraries.data ?? []} variant="outline" />
          <Button variant="outline" size="sm" onClick={() => void refresh()}>
            <RefreshCw className="console-nav-icon" aria-hidden />
            {t("jobQueueRefresh")}
          </Button>
        </>
      }
    >
      <p className="console-job-help">{t("jobQueueScanHelp")}</p>
      {(libraries.data ?? []).map((library) => (
        <RescanQueueFailure key={library.id} libraryID={library.id} title={library.title} />
      ))}
      {loading && !data ? (
        <Skeleton className="h-32 w-full" />
      ) : failed ? (
        <ErrorState message="jobQueueUnavailable" />
      ) : (data ?? []).length === 0 ? (
        <EmptyState message="noJobs" />
      ) : (
        <>
          {front === 0 ? (
            <EmptyState message="jobNothingActive" />
          ) : (
            <div className="console-card console-job-list">
              {view.live.map((job) => row(job))}
              {view.failed.map((group) => row(group.job, group.count, "jobFailureCount"))}
            </div>
          )}
          {view.history.length > 0 && (
            <details className="console-job-history">
              <summary>{t("jobHistorySummary", { count: view.history.length })}</summary>
              <div className="console-card console-job-list">
                {view.history.map((group) => row(group.job, group.count))}
              </div>
            </details>
          )}
        </>
      )}
    </Section>
  );
}

// Operations controls (WEB-5K, ADR-0013 §4-§5 / ADR-0061): the operator surface
// over the job-queue substrate and the resource governor — the job queue with
// per-job cancel/retry and an inline durable logs view, the live governor
// posture and the background-worker settings. Normal shows the queue and the
// low-power switch; the governor posture and tuning are Advanced (CORE-1117).
// Admin-session gated by the Console graph dispatch (the jobs/governor fields
// require admin).
export function OperationsControls() {
  return (
    <div className="space-y-8">
      <JobQueue />
      <GovernorSettings />
      <Advanced>
        <GovernorStatus />
      </Advanced>
    </div>
  );
}
