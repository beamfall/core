import { Monitor, RefreshCw } from "lucide-react";
import { type FormEvent, useEffect, useRef, useState } from "react";
import {
  ControlRow,
  DataTable,
  EmptyState,
  ErrorState,
  Fieldset,
  StatusChip,
  type StatusTone,
} from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { TableCell, TableRow } from "@/components/ui/table";
import { subscribeEvents } from "@/lib/api";
import {
  fetchConsoleConfig,
  fetchJobs,
  fetchLiveSessions,
  fetchUpdateStatus,
  setConsoleConfig,
  terminateSession,
  type UpdateCheckState,
} from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { jobKindLabel, jobProgressDetail, jobStatusLabel, jobStatusState } from "@/lib/jobDisplay";
import type { StringKey } from "@/strings";
import { ConfirmButton } from "../ConfirmButton";
import { Section } from "../Section";
import { useAsync } from "../useAsync";

function clock(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds));
  const m = Math.floor(s / 60);
  const r = s % 60;
  return `${m}:${String(r).padStart(2, "0")}`;
}

function statusTone(status: string): StatusTone {
  const state = jobStatusState(status);
  return state === "red" || state === "green" || state === "amber" ? state : "neutral";
}

/**
 * How each update-check state presents. "upToDate" is the ONLY entry that reads
 * as reassuring; a check that could not answer gets its own red presentation so
 * an unreachable or refused release manifest is never mistaken for a current
 * server. Lookup-table dispatch, so adding a state is one row rather than
 * another branch in the chip.
 */
const UPDATE_STATE_DISPLAY: Record<UpdateCheckState, { tone: StatusTone; label: StringKey }> = {
  upToDate: { tone: "green", label: "updateStatusUpToDate" },
  updateAvailable: { tone: "amber", label: "updateStatusUpdateAvailable" },
  disabled: { tone: "neutral", label: "updateStatusDisabled" },
  unavailable: { tone: "red", label: "updateStatusUnavailable" },
};

/**
 * Resolves the presentation for a state token. An absent or unrecognized token
 * falls back to the unavailable row — fail-closed, matching the server, which
 * also never lets an unknown value read as a version claim.
 */
function updateStateDisplay(state: string | undefined) {
  return UPDATE_STATE_DISPLAY[state as UpdateCheckState] ?? UPDATE_STATE_DISPLAY.unavailable;
}

function UpdateAvailableGuidance({
  currentVersion,
  latestVersion,
}: {
  currentVersion: string;
  latestVersion: string;
}) {
  const t = useT();

  return (
    <section
      role="status"
      aria-label={t("updateMigrationCautionAria")}
      data-backup-freshness="unknown"
      className="space-y-3 rounded-md border border-[var(--amber-500)]/45 bg-[var(--amber-500)]/10 px-3 py-3 text-sm"
    >
      <div>
        <p className="m-0 font-semibold">{t("updateMigrationCautionLabel")}</p>
        <p className="mt-1 mb-0">{t("updateMigrationImpactUnknown")}</p>
      </div>

      <dl className="grid gap-2 sm:grid-cols-3">
        <div data-update-scope="core" className="rounded border bg-background/50 px-3 py-2">
          <dt className="font-semibold">{t("updateScopeCore")}</dt>
          <dd className="m-0 text-muted-foreground">
            {currentVersion} to {latestVersion}
          </dd>
        </div>
        <div data-update-scope="plugin" className="rounded border bg-background/50 px-3 py-2">
          <dt className="font-semibold">{t("updateScopePlugins")}</dt>
          <dd className="m-0 text-muted-foreground">{t("updatePluginCompatibilityUnknown")}</dd>
        </div>
        <div
          data-update-scope="compatibility"
          className="rounded border bg-background/50 px-3 py-2"
        >
          <dt className="font-semibold">{t("updateScopeCompatibility")}</dt>
          <dd className="m-0 text-muted-foreground">{t("updateCompatibilityReview")}</dd>
        </div>
      </dl>

      <div className="space-y-1 border-l-4 border-[var(--amber-500)] pl-3">
        <p className="m-0">{t("updateMigrationPossible")}</p>
        <p className="m-0 font-semibold">{t("updateBackupRequired")}</p>
        <p className="m-0">{t("updateExportNotBackup")}</p>
        <p className="m-0">{t("updateRollbackSnapshotNotice")}</p>
        <p className="m-0">{t("updateDowngradeOffline")}</p>
      </div>

      <div aria-label={t("updateRecoveryArtifactsHeading")}>
        <p className="m-0 font-semibold">{t("updateRecoveryArtifactsHeading")}</p>
        <ul className="mt-2 mb-0 space-y-2 pl-5 text-muted-foreground">
          <li data-artifact-class="v2-backup-set">
            <strong className="text-foreground">{t("updateArtifactV2Title")}</strong> —{" "}
            {t("updateArtifactV2Body")}
          </li>
          <li data-artifact-class="v1-backup">
            <strong className="text-foreground">{t("updateArtifactV1Title")}</strong> —{" "}
            {t("updateArtifactV1Body")}
          </li>
          <li data-artifact-class="rollback-snapshot">
            <strong className="text-foreground">{t("updateArtifactRollbackTitle")}</strong> —{" "}
            {t("updateArtifactRollbackBody")}
          </li>
        </ul>
      </div>

      <div className="flex flex-wrap gap-2">
        <Button asChild variant="outline" size="sm">
          <a href="/console/backups">{t("updateCreateVerifyBackup")}</a>
        </Button>
        <Button asChild variant="outline" size="sm">
          <a href="/console/backups">{t("updateExportMetadata")}</a>
        </Button>
        <Button asChild variant="outline" size="sm">
          <a href="#offline-restore-guidance">{t("updateOfflineRestoreGuidance")}</a>
        </Button>
      </div>

      <section
        id="offline-restore-guidance"
        role="region"
        aria-labelledby="offline-restore-guidance-title"
        tabIndex={-1}
        className="space-y-2 rounded border bg-background/50 px-3 py-3"
      >
        <h3 id="offline-restore-guidance-title" className="m-0 text-sm font-semibold">
          {t("updateOfflineRestoreGuidance")}
        </h3>
        <ol className="m-0 space-y-1 pl-5 text-muted-foreground">
          <li>{t("updateRollbackSelectVersion")}</li>
          <li>{t("updateRollbackStop")}</li>
          <li>
            {t("updateRollbackRun")}
            <code className="mt-1 block break-all text-foreground">
              {t("updateRollbackCommand")}
            </code>
          </li>
          <li>{t("updateRollbackRestart")}</li>
        </ol>
        <a
          href={t("updateCanonicalRestoreHref")}
          target="_blank"
          rel="noreferrer"
          className="underline underline-offset-2"
        >
          {t("updateCanonicalRestoreProcedure")}
        </a>
      </section>
    </section>
  );
}

function UpdateAwareness() {
  const t = useT();
  const config = useAsync(fetchConsoleConfig);
  const status = useAsync(fetchUpdateStatus);
  const [enabled, setEnabled] = useState(false);
  const [channel, setChannel] = useState("stable");

  useEffect(() => {
    if (!config.data) return;
    setEnabled(config.data.updateCheckEnabled);
    setChannel(config.data.updateCheckChannel || "stable");
  }, [config.data]);

  async function onSave(e: FormEvent) {
    e.preventDefault();
    const next = await setConsoleConfig({
      updateCheckEnabled: enabled,
      updateCheckChannel: channel,
    });
    config.setData(next);
    await status.refresh();
  }

  const latest = status.data?.latestVersion || t("updateStatusNoneKnown");
  const display = updateStateDisplay(status.data?.state);
  // Covers unrecognized tokens too: they resolve to the same unavailable row.
  const checkFailed = status.data != null && display === UPDATE_STATE_DISPLAY.unavailable;
  const availableUpdate =
    status.data?.state === "updateAvailable" && status.data.updateAvailable === true
      ? status.data
      : null;

  return (
    <Section
      heading="updateAwarenessHeading"
      lead="updateAwarenessLead"
      actions={
        <Button variant="outline" size="sm" onClick={() => void status.refresh()}>
          <RefreshCw className="console-nav-icon" aria-hidden />
          {t("updateStatusRefresh")}
        </Button>
      }
    >
      {config.loading ? (
        <Skeleton className="h-32 w-full" />
      ) : config.failed ? (
        <ErrorState message="updateAwarenessUnavailable" />
      ) : (
        <form onSubmit={onSave} className="space-y-3">
          <Fieldset title="updateAwarenessHeading" help="updateAwarenessHelp">
            <ControlRow
              label="updateCheckEnabled"
              help="updateCheckEnabledHelp"
              control={
                <Switch
                  checked={enabled}
                  onCheckedChange={setEnabled}
                  aria-label={t("updateCheckEnabled")}
                />
              }
            />
            <ControlRow
              label="updateCheckChannel"
              help="updateCheckChannelHelp"
              control={
                <div
                  className="console-segmented"
                  role="group"
                  aria-label={t("updateCheckChannel")}
                >
                  {(["stable", "beta"] as const).map((c) => (
                    <button
                      key={c}
                      type="button"
                      aria-pressed={channel === c}
                      disabled={!enabled}
                      className={channel === c ? "sel" : undefined}
                      onClick={() => setChannel(c)}
                    >
                      {c === "stable" ? t("updateChannelStable") : t("updateChannelBeta")}
                    </button>
                  ))}
                </div>
              }
            />
          </Fieldset>
          <div className="console-card console-card-pad space-y-3">
            {availableUpdate && (
              <>
                <div className="rounded-md border border-[var(--amber-500)]/45 bg-[var(--amber-500)]/10 px-3 py-2 text-sm">
                  {t("updateAvailableBanner", {
                    version: availableUpdate.latestVersion,
                    channel: availableUpdate.channel,
                  })}
                </div>
                <UpdateAvailableGuidance
                  currentVersion={availableUpdate.currentVersion}
                  latestVersion={availableUpdate.latestVersion}
                />
              </>
            )}
            {checkFailed && (
              <div
                role="status"
                className="rounded-md border border-[var(--red-500)]/45 bg-[var(--red-500)]/10 px-3 py-2 text-sm"
              >
                {t("updateStatusUnavailableNotice")}
              </div>
            )}
            <ControlRow
              label="updateStatusState"
              control={<StatusChip tone={display.tone}>{t(display.label)}</StatusChip>}
            />
            <ControlRow
              label="updateStatusCurrent"
              control={
                <span className="console-mono">{status.data?.currentVersion || "unknown"}</span>
              }
            />
            <ControlRow
              label="updateStatusLatest"
              control={<span className="console-mono">{latest}</span>}
            />
            <p className="m-0 text-[12px] leading-snug text-muted-foreground">
              {t("updateAwarenessDisclosure")}
            </p>
          </div>
          <Button type="submit">{t("updateAwarenessSave")}</Button>
        </form>
      )}
    </Section>
  );
}

// The live-connection state of the jobs stream, mirroring the mockup's three
// indicator states (console-jobs-live.html): live (SSE open, pulsing green),
// reconnecting (transport error, auto-retry, amber blink), and paused (retries
// exhausted — the manual Refresh fallback re-appears). "connecting" is the
// pre-first-open mount state and renders no indicator/button.
type StreamState = "connecting" | "live" | "reconnecting" | "paused";

// After this many consecutive transport errors with no intervening open we
// treat the stream as paused (retries effectively exhausted) and surface the
// manual refresh escape, per mockup State 3.
const PAUSE_AFTER_ERRORS = 3;

// The live-status indicator (mockup .live): a pulsing/blinking dot + label that
// signals stream liveness. In the paused state it becomes the manual-refresh
// fallback button — the only state with a button (honest, never-silent).
function LiveIndicator({ state, onRefresh }: { state: StreamState; onRefresh: () => void }) {
  const t = useT();
  if (state === "connecting") return null;
  if (state === "paused") {
    return (
      <button
        type="button"
        className="console-live is-paused console-live-refresh"
        onClick={onRefresh}
      >
        <span className="console-live-dot" aria-hidden />
        {t("jobsLivePaused")} · {t("jobsRefreshNow")}
        <RefreshCw className="console-nav-icon" aria-hidden />
      </button>
    );
  }
  const live = state === "live";
  return (
    <span className={`console-live ${live ? "is-live" : "is-reconnecting"}`} role="status">
      <span className="console-live-dot" aria-hidden />
      {live ? t("jobsLive") : t("jobsReconnecting")}
    </span>
  );
}

// Jobs queue (ADR-0013) — the job substrate with progress detail. Live via the
// SSE event stream (ADR-0040): any server-side job event triggers a refresh,
// and the EventSource lifecycle drives the live-status indicator. Last-good
// rows are retained across a reconnect (refresh only replaces on success), so
// the panel never blanks mid-stream (fail-soft, per mockup State 2).
function JobsQueue() {
  const t = useT();
  const { data, loading, failed, refresh } = useAsync(fetchJobs);
  const jobs = data ?? [];
  const [stream, setStream] = useState<StreamState>("connecting");
  const errorsRef = useRef(0);

  useEffect(() => {
    return subscribeEvents(
      () => void refresh(),
      undefined,
      (s) => {
        if (s === "live") {
          errorsRef.current = 0;
          setStream("live");
        } else {
          errorsRef.current += 1;
          setStream(errorsRef.current >= PAUSE_AFTER_ERRORS ? "paused" : "reconnecting");
        }
      },
    );
  }, [refresh]);

  return (
    <Section
      heading="jobs"
      actions={<LiveIndicator state={stream} onRefresh={() => void refresh()} />}
    >
      {loading ? (
        <Skeleton className="h-32 w-full" />
      ) : failed ? (
        <ErrorState message="jobsUnavailable" />
      ) : jobs.length === 0 ? (
        <EmptyState message="noJobs" />
      ) : (
        <div className="console-card console-job-list" data-stream={stream}>
          {jobs.map((job) => {
            const { current, total } = job.progress;
            const pct = total > 0 ? Math.round((current / total) * 100) : 0;
            const tone = statusTone(job.status);
            return (
              <div key={job.id} className="console-job-row">
                <span className="console-job-name">{jobKindLabel(job.kind, t)}</span>
                <span
                  className="console-jbar"
                  role="progressbar"
                  aria-valuenow={pct}
                  aria-valuemin={0}
                  aria-valuemax={100}
                  aria-label={t("jobsProgressLabel")}
                >
                  <i
                    className={tone === "amber" ? "warn" : undefined}
                    style={{ width: `${pct}%` }}
                  />
                </span>
                <span className="console-job-num console-mono">{jobProgressDetail(job, t)}</span>
                <StatusChip tone={tone}>{jobStatusLabel(job.status, t)}</StatusChip>
              </div>
            );
          })}
        </div>
      )}
    </Section>
  );
}

// Live Sessions dashboard (ADR-0007): active playback across devices, with a
// server-side terminate behind a confirm. Live via SSE. A restricted session
// the operator has not revealed shows its load without its media (the server
// omits the title) — rendered as a "Restricted" placeholder, never hidden.
function LiveSessions() {
  const t = useT();
  const { data, loading, failed, refresh } = useAsync(fetchLiveSessions);
  const sessions = data ?? [];

  useEffect(() => subscribeEvents(() => void refresh()), [refresh]);

  async function terminate(sessionID: string) {
    await terminateSession(sessionID);
    await refresh();
  }

  return (
    <Section
      heading="liveSessionsHeading"
      lead="liveSessionsLead"
      actions={
        <Button variant="outline" size="sm" onClick={() => void refresh()}>
          <RefreshCw className="console-nav-icon" aria-hidden />
          {t("liveSessionsRefresh")}
        </Button>
      }
    >
      {loading ? (
        <Skeleton className="h-32 w-full" />
      ) : failed ? (
        <ErrorState message="liveSessionsUnavailable" />
      ) : sessions.length === 0 ? (
        <EmptyState message="liveSessionsEmpty" icon={Monitor} />
      ) : (
        <DataTable
          columns={[
            { key: "device", label: t("deviceName") },
            { key: "profile", label: t("profile") },
            { key: "title", label: t("title") },
            { key: "engine", label: t("infoEngine") },
            { key: "action", label: "" },
          ]}
        >
          {sessions.map((s) => (
            <TableRow key={s.sessionID}>
              <TableCell className="font-medium">{s.deviceName}</TableCell>
              <TableCell>{s.profileName}</TableCell>
              <TableCell>
                {s.title ? (
                  <span className="flex items-center gap-2">
                    <span>{s.title}</span>
                    <span className="console-mono">
                      {clock(s.positionSeconds)} / {clock(s.durationSeconds)}
                    </span>
                  </span>
                ) : (
                  <StatusChip tone="iris">{t("liveSessionRestricted")}</StatusChip>
                )}
              </TableCell>
              <TableCell>
                {s.transcoding ? (
                  <StatusChip tone="amber">{t("liveSessionTranscoding")}</StatusChip>
                ) : (
                  <StatusChip tone="green">{t("liveSessionDirectPlay")}</StatusChip>
                )}
              </TableCell>
              <TableCell className="text-right">
                <ConfirmButton
                  size="sm"
                  label={t("terminateSession")}
                  confirm="terminateSession"
                  onConfirm={() => terminate(s.sessionID)}
                />
              </TableCell>
            </TableRow>
          ))}
        </DataTable>
      )}
    </Section>
  );
}

// Operations (ADR-0007 §4): the job queue + the live sessions dashboard.
export function Operations() {
  return (
    <div className="space-y-8">
      <UpdateAwareness />
      <JobsQueue />
      <LiveSessions />
    </div>
  );
}
