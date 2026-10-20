import { useCallback, useEffect, useState } from "react";
import {
  EmptyLine,
  ErrorLine,
  ManageSection,
  ManageTable,
  StatusBadge,
  TableSkeletonRows,
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

function JobsSection() {
  const t = useT();
  const [jobs, setJobs] = useState<JobInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setError("");
    setLoading(true);
    try {
      setJobs(await fetchJobs());
    } catch {
      setError(t("jobsUnavailable"));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <ManageSection
      title={t("jobs")}
      actions={
        <Button type="button" variant="outline" size="sm" onClick={load}>
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
              <TableHead>{t("active")}</TableHead>
              <TableHead className="text-right">{t("info")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              <TableSkeletonRows columns={3} />
            ) : (
              jobs.map((job) => (
                <TableRow key={job.id}>
                  <TableCell className="manage-cell-primary">{jobKindLabel(job.kind, t)}</TableCell>
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
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setError("");
    try {
      setSchedules(await fetchSchedules());
    } catch {
      setError(t("schedulesUnavailable"));
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
  ) => {
    setError("");
    try {
      const updated = await setScheduleTrigger(schedule.id, triggerInput(kind, fields));
      setSchedules((prev) => prev.map((s) => (s.id === schedule.id ? updated : s)));
    } catch {
      setError(t("couldNotUpdateSchedule"));
    }
  };

  return (
    <ManageSection
      title={t("schedules")}
      actions={
        <Button type="button" variant="outline" size="sm" onClick={load}>
          {t("refresh")}
        </Button>
      }
    >
      {schedules.length === 0 ? (
        <EmptyLine message={t("noSchedules")} />
      ) : (
        <div className="space-y-3">
          {schedules.map((schedule) => (
            <ScheduleRow
              key={schedule.id}
              schedule={schedule}
              onToggle={onToggle}
              onSaveTrigger={onSaveTrigger}
            />
          ))}
        </div>
      )}
      <ErrorLine message={error} />
    </ManageSection>
  );
}

function ScheduleRow({
  schedule,
  onToggle,
  onSaveTrigger,
}: {
  schedule: ScheduleInfo;
  onToggle: (s: ScheduleInfo, enabled: boolean) => Promise<void>;
  onSaveTrigger: (s: ScheduleInfo, kind: TriggerKind, fields: TriggerFields) => Promise<void>;
}) {
  const t = useT();
  const [kind, setKind] = useState<TriggerKind>((schedule.trigger?.kind as TriggerKind) || "every");
  const [fields, setFields] = useState<TriggerFields>(triggerFieldsFrom(schedule.trigger));

  const setField = <K extends keyof TriggerFields>(key: K, value: TriggerFields[K]) =>
    setFields((prev) => ({ ...prev, [key]: value }));

  return (
    <div className="manage-fieldrow">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <span className="font-semibold text-[var(--text-primary)]">
            {jobKindLabel(schedule.jobKind, t)}
          </span>
          {schedule.orphaned && <StatusBadge state="amber">{t("orphaned")}</StatusBadge>}
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

      <div className="mt-2 flex flex-wrap items-end gap-2">
        <Select value={kind} onValueChange={(v) => setKind(v as TriggerKind)}>
          <SelectTrigger size="sm" className="w-32">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="every">{t("every")}</SelectItem>
            <SelectItem value="daily">{t("daily")}</SelectItem>
            <SelectItem value="weekly">{t("weekly")}</SelectItem>
          </SelectContent>
        </Select>

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
              <SelectTrigger size="sm" className="w-28">
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
            <SelectTrigger size="sm" className="w-32">
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
          <>
            <NumberField
              label={t("hour")}
              min={0}
              max={23}
              value={fields.hour}
              onChange={(h) => setField("hour", h)}
            />
            <NumberField
              label={t("minute")}
              min={0}
              max={59}
              value={fields.minute}
              onChange={(m) => setField("minute", m)}
            />
          </>
        )}

        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => onSaveTrigger(schedule, kind, fields)}
        >
          {t("save")}
        </Button>
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
      onChange={(e) => onChange(Number(e.target.value))}
      className="w-20"
    />
  );
}

function WatcherSection() {
  const t = useT();
  const [statuses, setStatuses] = useState<WatcherStatusInfo[]>([]);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setError("");
    try {
      setStatuses(await fetchWatcherStatus());
    } catch {
      setError(t("watcherStatusUnavailable"));
    }
  }, [t]);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <ManageSection
      title={t("watcherStatusHeading")}
      lead={t("watcherStatusLead")}
      actions={
        <Button type="button" variant="outline" size="sm" onClick={load}>
          {t("refresh")}
        </Button>
      }
    >
      {statuses.length === 0 ? (
        <EmptyLine message={t("watcherStatusEmpty")} />
      ) : (
        <ManageTable>
          <TableHeader>
            <TableRow>
              <TableHead>{t("library")}</TableHead>
              <TableHead>{t("watcherStateWatching")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {statuses.map((status) => (
              <TableRow key={status.libraryID}>
                <TableCell className="manage-cell-primary">
                  {status.title || status.libraryID}
                </TableCell>
                <TableCell className="text-[var(--text-muted)]">
                  {t(WATCHER_STATE_LABELS[status.state] ?? "watcherStateUnavailable")}
                  {status.reason ? ` - ${status.reason}` : ""}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </ManageTable>
      )}
      <ErrorLine message={error} />
    </ManageSection>
  );
}
