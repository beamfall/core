import { Cpu, RefreshCw } from "lucide-react";
import { type FormEvent, useEffect, useState } from "react";
import {
  ControlRow,
  EmptyState,
  ErrorState,
  Fieldset,
  StatusChip,
  type StatusTone,
} from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { subscribeEvents } from "@/lib/api";
import {
  cancelJob,
  fetchConsoleConfig,
  fetchJobEvents,
  fetchJobs,
  fetchOperationsGovernor,
  retryJob,
  runEventsCompaction,
  setConsoleConfig,
} from "@/lib/graphql";
import type { JobEventInfo, JobInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { jobKindLabel, jobProgressDetail, jobStatusLabel, jobStatusState } from "@/lib/jobDisplay";
import type { StringKey } from "@/strings";
import { ConfirmButton } from "../ConfirmButton";
import { Section } from "../Section";
import { useAsync } from "../useAsync";

function statusTone(status: string): StatusTone {
  const state = jobStatusState(status);
  return state === "red" || state === "green" || state === "amber" ? state : "neutral";
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

function redactJobLogPayload(payload: string): string {
  return jobLogSecretPatterns.reduce(
    (next, pattern) => next.replace(pattern, "[REDACTED]"),
    payload,
  );
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
        {unit && (
          <span className="ml-1 text-[12px] font-normal text-muted-foreground">{t(unit)}</span>
        )}
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

  useEffect(() => subscribeEvents(() => void refresh()), [refresh]);

  return (
    <Section
      heading="governorHeading"
      actions={
        <Button variant="outline" size="sm" onClick={() => void refresh()}>
          <RefreshCw className="console-nav-icon" aria-hidden />
          {t("governorRefresh")}
        </Button>
      }
    >
      {loading ? (
        <Skeleton className="h-40 w-full" />
      ) : failed ? (
        <ErrorState message="governorUnavailable" />
      ) : !data?.available ? (
        <div className="space-y-1">
          <EmptyState message="governorUnwiredLine" icon={Cpu} />
          <p className="px-1 text-[12px] leading-snug text-muted-foreground">
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
  const [workers, setWorkers] = useState(2);
  const [retentionDays, setRetentionDays] = useState(180);
  const [compactionQueued, setCompactionQueued] = useState(false);
  const [compactionFailed, setCompactionFailed] = useState(false);

  useEffect(() => {
    if (!config.data) return;
    setWorkers(Math.max(1, config.data.backgroundWorkers));
    setRetentionDays(Math.max(1, config.data.eventsRetentionDays));
  }, [config.data]);

  // Low-power is the *persisted* posture (a single configured worker), not the
  // transient edit value — so editing the worker field never flips the toggle
  // or disables Save mid-keystroke. While low-power is on, the field locks to 1.
  const lowPower = (config.data?.backgroundWorkers ?? 2) <= 1;

  async function save(next: number) {
    const clamped = Math.max(1, Math.floor(next));
    const updated = await setConsoleConfig({ backgroundWorkers: clamped });
    config.setData(updated);
    setWorkers(Math.max(1, updated.backgroundWorkers));
  }

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    await save(workers);
  }

  async function saveRetentionDays() {
    const clamped = Math.max(1, Math.floor(retentionDays));
    const updated = await setConsoleConfig({ eventsRetentionDays: clamped });
    config.setData(updated);
    setRetentionDays(Math.max(1, updated.eventsRetentionDays));
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

  return (
    <Section heading="governorSettingsHeading">
      {config.loading ? (
        <Skeleton className="h-32 w-full" />
      ) : config.failed ? (
        <ErrorState message="governorSettingsUnavailable" />
      ) : (
        <form onSubmit={onSubmit} className="space-y-3">
          <Fieldset title="governorSettingsHeading">
            <ControlRow
              label="governorBackgroundWorkers"
              help="governorBackgroundWorkersHelp"
              control={
                <>
                  <Input
                    type="number"
                    min={1}
                    value={workers}
                    disabled={lowPower}
                    onChange={(e) => setWorkers(Math.max(1, Number(e.target.value) || 1))}
                    aria-label={t("governorBackgroundWorkers")}
                    className="w-20"
                  />
                  <Button type="submit" size="sm" disabled={lowPower}>
                    {t("governorBackgroundWorkersSave")}
                  </Button>
                </>
              }
            />
            <ControlRow
              label="governorLowPower"
              help="governorLowPowerHelp"
              control={
                <Switch
                  checked={lowPower}
                  onCheckedChange={(on) => void onToggleLowPower(on)}
                  aria-label={t("governorLowPower")}
                />
              }
            />
            <ControlRow
              label="eventsRetentionDays"
              help="eventsRetentionDaysHelp"
              control={
                <>
                  <Input
                    type="number"
                    min={1}
                    value={retentionDays}
                    onChange={(e) => setRetentionDays(Math.max(1, Number(e.target.value) || 1))}
                    aria-label={t("eventsRetentionDays")}
                    className="w-24"
                  />
                  <Button type="button" size="sm" onClick={() => void saveRetentionDays()}>
                    {t("eventsRetentionDaysSave")}
                  </Button>
                </>
              }
            />
            <ControlRow
              label="jobKindEventsCompaction"
              help="eventsRetentionDaysHelp"
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
          </Fieldset>
        </form>
      )}
    </Section>
  );
}

// The inline durable history/logs timeline for one job (jobEvents). Opened
// under a selected job row. Payload is omitted by the server for restricted
// targets — rendered as a redacted placeholder, never hidden.
function JobLog({ jobID }: { jobID: string }) {
  const t = useT();
  const { data, loading, failed } = useAsync((signal) => fetchJobEvents(jobID, signal), [jobID]);
  const events: JobEventInfo[] = data ?? [];

  if (loading) return <Skeleton className="h-16 w-full" />;
  if (failed) return <ErrorState message="jobLogsUnavailable" />;
  if (events.length === 0) return <EmptyState message="jobLogsEmpty" />;

  return (
    <div className="console-card space-y-1 px-3 py-2">
      {events.map((ev) => (
        <div key={ev.id} className="grid grid-cols-[10rem_8rem_1fr] gap-3 text-[12px] leading-snug">
          <span className="console-mono text-muted-foreground">{ev.occurredAt}</span>
          <span className="font-medium">{ev.type}</span>
          <span className="console-mono truncate">
            {ev.payload ? redactJobLogPayload(ev.payload) : t("jobLogPayloadRedacted")}
          </span>
        </div>
      ))}
    </div>
  );
}

// Job queue with per-job operations (ADR-0013 §4): the live job substrate plus
// Cancel (queued/running/yielded → jobCancel) and Retry (terminal → jobRetry),
// and an inline logs view (jobEvents). Live via the SSE event stream (ADR-0040):
// any server-side job event triggers a refresh.
function JobQueue() {
  const t = useT();
  const { data, loading, failed, refresh } = useAsync(fetchJobs);
  const jobs: JobInfo[] = data ?? [];
  const [openLog, setOpenLog] = useState<string | null>(null);
  const [logRefresh, setLogRefresh] = useState(0);

  useEffect(
    () =>
      subscribeEvents(() => {
        void refresh();
        setLogRefresh((n) => n + 1);
      }),
    [refresh],
  );

  async function onCancel(id: string) {
    await cancelJob(id);
    await refresh();
  }

  async function onRetry(id: string) {
    await retryJob(id);
    await refresh();
  }

  return (
    <Section
      heading="jobQueueHeading"
      actions={
        <Button variant="outline" size="sm" onClick={() => void refresh()}>
          <RefreshCw className="console-nav-icon" aria-hidden />
          {t("jobQueueRefresh")}
        </Button>
      }
    >
      {loading ? (
        <Skeleton className="h-32 w-full" />
      ) : failed ? (
        <ErrorState message="jobQueueUnavailable" />
      ) : jobs.length === 0 ? (
        <EmptyState message="noJobs" />
      ) : (
        <div className="console-card console-job-list">
          {jobs.map((job) => {
            const { current, total } = job.progress;
            const pct = total > 0 ? Math.round((current / total) * 100) : 0;
            const tone = statusTone(job.status);
            const live = isLive(job.status);
            const logOpen = openLog === job.id;
            return (
              <div key={job.id} className="console-job-row console-job-row-ops">
                <span className="console-job-name">{jobKindLabel(job.kind, t)}</span>
                <span
                  className="console-jbar"
                  role="progressbar"
                  aria-valuenow={pct}
                  aria-valuemin={0}
                  aria-valuemax={100}
                  aria-label={t("quality")}
                >
                  <i
                    className={tone === "red" ? "danger" : tone === "amber" ? "warn" : undefined}
                    style={{ width: `${pct}%` }}
                  />
                </span>
                <span className="console-job-num console-mono">{jobProgressDetail(job, t)}</span>
                <StatusChip tone={tone}>{jobStatusLabel(job.status, t)}</StatusChip>
                <span className="console-job-actions">
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => setOpenLog(logOpen ? null : job.id)}
                  >
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
                    <Button variant="outline" size="sm" onClick={() => void onRetry(job.id)}>
                      {t("jobRetryAction")}
                    </Button>
                  )}
                </span>
                {logOpen && (
                  <div className="console-job-log">
                    <JobLog key={`${job.id}:${logRefresh}`} jobID={job.id} />
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </Section>
  );
}

// Operations controls (WEB-5K, ADR-0013 §4-§5 / ADR-0061): the operator surface
// over the resource governor and the job-queue substrate — live governor
// posture, background-worker write settings with low-power mode, and the job
// queue with per-job cancel/retry and an inline durable logs view. Admin-session
// gated by the Console graph dispatch (the jobs/governor fields require admin).
export function OperationsControls() {
  return (
    <div className="space-y-8">
      <GovernorStatus />
      <GovernorSettings />
      <JobQueue />
    </div>
  );
}
