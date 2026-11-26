import { useCallback, useEffect, useState } from "react";
import {
  EmptyLine,
  ErrorLine,
  HelpTip,
  ManageSection,
  ManageTable,
  StatusBadge,
  TableSkeletonRows,
  TimeOfDaySelect,
} from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import {
  fetchJobs,
  fetchLibraries,
  fetchSchedules,
  fetchWatcherStatus,
  setScheduleEnabled,
  setScheduleTrigger,
} from "@/lib/graphql";
import type { JobInfo, ScheduleInfo, WatcherStatusInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { jobKindLabel, jobProgressDetail, jobStatusLabel, jobStatusState } from "@/lib/jobDisplay";
import type { StringKey } from "@/strings";
import {
  formatDateTime,
  renderTrigger,
  type TriggerFields,
  type TriggerKind,
  triggerFieldsFrom,
  triggerInput,
  WEEKDAY_KEYS,
} from "../lib/schedule";

const WATCHER_STATE_LABELS: Record<string, StringKey> = {
  watching: "watcherStateWatching",
  available: "watcherStateAvailable",
  unavailable: "watcherStateUnavailable",
};

// Operations: the job substrate queue (point-in-time, explicit refresh — no
// polling, per app.js), the persisted Schedules with enable + structured-trigger
// editor (ADR-0032), and the read-only per-Library Watcher status. Ported from
// refreshJobs / refreshSchedules / refreshWatcherStatus.
export function OperationsPage() {
  return (
    <div className="space-y-4">
      <JobsSection />
      <SchedulesSection />
      <WatcherSection />
    </div>
  );
}

// Per-Library schedules carry the Library ID in their schedule ID
// (schedule.ScanScheduleID, playback precompute); the kind label alone cannot
// tell them apart.
const LIBRARY_SCHEDULE_PREFIXES = ["scan:", "playback-precompute:"];

/** The Library a per-Library schedule belongs to, or null for a server-wide one. */
export function scheduleLibraryID(scheduleID: string): string | null {
  const prefix = LIBRARY_SCHEDULE_PREFIXES.find((p) => scheduleID.startsWith(p));
  const libraryID = prefix ? scheduleID.slice(prefix.length) : "";
  return libraryID || null;
}

/** The Library title for a per-Library schedule, its raw ID when the Library is unknown. */
export function scheduleLibraryLabel(
  scheduleID: string,
  titles: ReadonlyMap<string, string>,
): string | null {
  const libraryID = scheduleLibraryID(scheduleID);
  if (!libraryID) return null;
  return titles.get(libraryID) || scheduleID;
}

function JobsSection() {
  const t = useT();
  const [jobs, setJobs] = useState<JobInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const load = useCallback(
    async (quiet = false) => {
      setError("");
      if (!quiet) setLoading(true);
      try {
        setJobs(await fetchJobs());
      } catch {
        setError(t("jobsUnavailable"));
      } finally {
        if (!quiet) setLoading(false);
      }
    },
    [t],
  );

  useEffect(() => {
    void load();
  }, [load]);

  const hasActive = jobs.some(
    (j) => j.status === "running" || j.status === "queued" || j.status === "pending",
  );
  useEffect(() => {
    if (!hasActive) return;
    const timer = window.setInterval(() => {
      void load(true);
    }, 4000);
    return () => window.clearInterval(timer);
  }, [hasActive, load]);

  return (
    <ManageSection
      actions={
        <Button type="button" variant="outline" size="sm" onClick={() => void load()}>
          {t("refresh")}
        </Button>
      }
    >
      {!loading && jobs.length === 0 ? (
        <EmptyLine message={t("noJobs")} />
      ) : (
        <ManageTable>
          <TableHeader>
            <TableRow>
              <TableHead>{t("jobs")}</TableHead>
              <TableHead>{t("consoleOverviewStatus")}</TableHead>
              <TableHead className="text-right">{t("info")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              <TableSkeletonRows columns={3} />
            ) : (
              jobs.map((job) => (
                <TableRow key={job.id}>
                  <TableCell>
                    <div className="manage-cell-primary">{jobKindLabel(job.kind, t)}</div>
                    {job.target?.libraryTitle && (
                      <div className="text-xs text-muted-foreground">{job.target.libraryTitle}</div>
                    )}
                    {job.error && (
                      <div className="text-xs text-[var(--state-danger)]">{job.error}</div>
                    )}
                  </TableCell>
                  <TableCell>
                    <StatusBadge state={jobStatusState(job.status)}>
                      {jobStatusLabel(job.status, t)}
                    </StatusBadge>
                  </TableCell>
                  <TableCell className="manage-cell-num text-right">
                    {jobProgressDetail(job, t)}
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </ManageTable>
      )}
      <ErrorLine message={error} />
    </ManageSection>
  );
}

function SchedulesSection() {
  const t = useT();
  const [schedules, setSchedules] = useState<ScheduleInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [libraryTitles, setLibraryTitles] = useState<ReadonlyMap<string, string>>(new Map());
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setError("");
    setLoading(true);
    // Titles only label rows; without them a row shows its raw schedule ID.
    void fetchLibraries()
      .then((libraries) =>
        setLibraryTitles(new Map(libraries.map((library) => [library.id, library.title]))),
      )
      .catch(() => {});
    try {
      setSchedules(await fetchSchedules());
    } catch {
      setError(t("schedulesUnavailable"));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void load();
  }, [load]);

  const onToggle = async (schedule: ScheduleInfo, enabled: boolean) => {
    setError("");
    try {
      const updated = await setScheduleEnabled(schedule.id, enabled);
      setSchedules((prev) =>
        prev.map((s) => (s.id === schedule.id ? { ...s, enabled: updated.enabled } : s)),
      );
    } catch {
      setError(t("couldNotUpdateSchedule"));
    }
  };

  const onSaveTrigger = async (
    schedule: ScheduleInfo,
    kind: TriggerKind,
    fields: TriggerFields,
  ): Promise<boolean> => {
    setError("");
    try {
      const updated = await setScheduleTrigger(schedule.id, triggerInput(kind, fields));
      setSchedules((prev) => prev.map((s) => (s.id === schedule.id ? updated : s)));
      return true;
    } catch {
      setError(t("couldNotUpdateSchedule"));
      return false;
    }
  };

  return (
    <ManageSection
      title={t("schedules")}
      collapsible
      defaultOpen
      actions={
        <Button type="button" variant="outline" size="sm" onClick={load}>
          {t("refresh")}
        </Button>
      }
    >
      <div data-setting="manage.operations.schedules">
        {loading ? (
          <ManageTable>
            <TableBody>
              <TableSkeletonRows columns={2} rows={2} />
            </TableBody>
          </ManageTable>
        ) : schedules.length === 0 ? (
          <EmptyLine message={t("noSchedules")} />
        ) : (
          <div>
            {schedules.map((schedule) => (
              <ScheduleRow
                key={schedule.id}
                schedule={schedule}
                library={scheduleLibraryLabel(schedule.id, libraryTitles)}
                onToggle={onToggle}
                onSaveTrigger={onSaveTrigger}
              />
            ))}
          </div>
        )}
      </div>
      <ErrorLine message={error} />
    </ManageSection>
  );
}

function ScheduleRow({
  schedule,
  library,
  onToggle,
  onSaveTrigger,
}: {
  schedule: ScheduleInfo;
  library: string | null;
  onToggle: (s: ScheduleInfo, enabled: boolean) => Promise<void>;
  onSaveTrigger: (s: ScheduleInfo, kind: TriggerKind, fields: TriggerFields) => Promise<boolean>;
}) {
  const t = useT();
  const [kind, setKind] = useState<TriggerKind>((schedule.trigger?.kind as TriggerKind) || "every");
  const [fields, setFields] = useState<TriggerFields>(triggerFieldsFrom(schedule.trigger));
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    setKind((schedule.trigger?.kind as TriggerKind) || "every");
    setFields(triggerFieldsFrom(schedule.trigger));
  }, [schedule.trigger]);

  const setField = <K extends keyof TriggerFields>(key: K, value: TriggerFields[K]) => {
    setSaved(false);
    setFields((prev) => ({ ...prev, [key]: value }));
  };

  const handleSave = async () => {
    setSaving(true);
    setSaved(false);
    try {
      const ok = await onSaveTrigger(schedule, kind, fields);
      if (ok) setSaved(true);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="manage-fieldrow">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <span className="font-semibold text-[var(--text-primary)]">
            {jobKindLabel(schedule.jobKind, t)}
          </span>
          {library && <span className="break-all text-sm text-muted-foreground">{library}</span>}
          {schedule.orphaned && (
            <span className="bf-label-with-tip">
              <StatusBadge state="amber">{t("orphaned")}</StatusBadge>
              <HelpTip label={t("orphaned")} text="manageScheduleOrphanedHelp" />
            </span>
          )}
        </div>
        <Label className="flex items-center gap-2">
          <Switch
            checked={schedule.enabled}
            onCheckedChange={(checked) => onToggle(schedule, checked)}
          />
          <span>{t("scheduleEnabled")}</span>
        </Label>
      </div>

      <p className="mt-1 text-sm text-muted-foreground">
        {renderTrigger(schedule.trigger, t)} · {t("nextDue")}: {formatDateTime(schedule.nextDue)} ·{" "}
        {t("lastEnqueued")}: {formatDateTime(schedule.lastEnqueued)}
      </p>

      <div className="mt-2 flex flex-wrap items-center gap-2">
        <span className="bf-label-with-tip" data-setting="manage.operations.repeat">
          <Select
            value={kind}
            onValueChange={(v) => {
              setSaved(false);
              setKind(v as TriggerKind);
            }}
          >
            <SelectTrigger className="w-32" aria-label={t("manageScheduleRepeat")}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="every">{t("every")}</SelectItem>
              <SelectItem value="daily">{t("daily")}</SelectItem>
              <SelectItem value="weekly">{t("weekly")}</SelectItem>
            </SelectContent>
          </Select>
          <HelpTip label={t("manageScheduleRepeat")} text="manageScheduleRepeatHelp" />
        </span>

        {kind === "every" && (
          <>
            <NumberField
              label={t("every")}
              min={1}
              max={999}
              value={fields.n}
              onChange={(n) => setField("n", n)}
            />
            <Select
              value={fields.unit}
              onValueChange={(v) => setField("unit", v as "minutes" | "hours")}
            >
              <SelectTrigger className="w-28" aria-label={t("scheduleUnit")}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="minutes">{t("minutes")}</SelectItem>
                <SelectItem value="hours">{t("hours")}</SelectItem>
              </SelectContent>
            </Select>
          </>
        )}

        {kind === "weekly" && (
          <Select
            value={String(fields.weekday)}
            onValueChange={(v) => setField("weekday", Number(v))}
          >
            <SelectTrigger className="w-32" aria-label={t("weekday")}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {WEEKDAY_KEYS.map((key, index) => (
                <SelectItem key={key} value={String(index)}>
                  {t(key)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}

        {(kind === "weekly" || kind === "daily") && (
          <span data-setting="manage.operations.time">
            <TimeOfDaySelect
              value={fields.hour * 60 + fields.minute}
              onChange={(minute) => {
                setSaved(false);
                setFields((prev) => ({
                  ...prev,
                  hour: Math.floor(minute / 60),
                  minute: minute % 60,
                }));
              }}
              aria-label={t("manageScheduleTime")}
              className="min-w-24"
            />
          </span>
        )}

        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={saving}
          onClick={() => void handleSave()}
        >
          {t("save")}
        </Button>
        {saved && (
          <span role="status" className="text-xs text-muted-foreground">
            {t("manageSaved")}
          </span>
        )}
      </div>
    </div>
  );
}

function NumberField({
  label,
  min,
  max,
  value,
  onChange,
}: {
  label: string;
  min: number;
  max: number;
  value: number;
  onChange: (value: number) => void;
}) {
  return (
    <Input
      type="number"
      aria-label={label}
      min={min}
      max={max}
      step={1}
      value={value}
      onChange={(e) => {
        const parsed = Number.parseInt(e.target.value, 10);
        const clamped = Number.isFinite(parsed) ? Math.max(min, Math.min(max, parsed)) : min;
        onChange(clamped);
      }}
      className="w-20"
    />
  );
}

function WatcherSection() {
  const t = useT();
  const [statuses, setStatuses] = useState<WatcherStatusInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setError("");
    setLoading(true);
    try {
      setStatuses(await fetchWatcherStatus());
    } catch {
      setError(t("watcherStatusUnavailable"));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <ManageSection
      title={t("watcherStatusHeading")}
      collapsible
      lead={t("watcherStatusLead")}
      actions={
        <Button type="button" variant="outline" size="sm" onClick={load}>
          {t("refresh")}
        </Button>
      }
    >
      {!loading && statuses.length === 0 ? (
        <EmptyLine message={t("watcherStatusEmpty")} />
      ) : (
        <ManageTable>
          <TableHeader>
            <TableRow>
              <TableHead>{t("library")}</TableHead>
              <TableHead>{t("consoleOverviewStatus")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              <TableSkeletonRows columns={2} rows={2} />
            ) : (
              statuses.map((status) => (
                <TableRow key={status.libraryID}>
                  <TableCell className="manage-cell-primary">
                    {status.title || status.libraryID}
                  </TableCell>
                  <TableCell className="text-[var(--text-muted)]">
                    {t(WATCHER_STATE_LABELS[status.state] ?? "watcherStateUnavailable")}
                    {status.reason ? ` - ${status.reason}` : ""}
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </ManageTable>
      )}
      <ErrorLine message={error} />
    </ManageSection>
  );
}
