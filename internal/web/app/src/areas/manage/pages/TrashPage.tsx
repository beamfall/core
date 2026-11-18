import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  EmptyLine,
  ErrorLine,
  HelpTip,
  ManageSection,
  ManageTable,
  StatusBadge,
  TableSkeletonRows,
} from "@/components/beamfall/workbench";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { describeActivity } from "@/lib/activityDisplay";
import { fetchActivityRecords, fetchTrashList, purgeTrash, restoreMedia } from "@/lib/graphql";
import type { ActivityRecord } from "@/lib/graphql/activity";
import type { TrashEntry } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { formatDateTime } from "../lib/schedule";

// Trash policy + Activity undo (WEB-5L / ADR-0005 retention / ADR-0012 §11):
// three operator surfaces sharing one page in the Manage register —
//   1. Retained Trash: browse the server-side retention window and restore any
//      row (HARD invariant #6 — destruction is recoverable by default).
//   2. Purge: permanently remove retained items trashed before a cutoff. The
//      purge is irreversible, so it is confirm-gated twice — an explicit
//      checkbox plus an AlertDialog — and the server still re-checks
//      Admin-Session + reveal + the confirm flag.
//   3. Activity: the append-only feed (latest 50); reversible rows (a
//      media.trash) carry an Undo that scrolls to the Retained Trash table where
//      Restore lives, irreversible rows (trash.purge) are flagged with no undo.

// Activity actions the operator can reverse from this surface. A media.trash is
// undone by restoring the Media from the Retained Trash table above.
const REVERSIBLE_ACTIONS = new Set(["media.trash"]);
const RETAINED_TRASH_ANCHOR = "retained-trash";

// Quick purge cutoffs (CORE-2445): "older than N days" fills the datetime-local
// field so the operator does not have to work out a date. Both purge gates
// still apply.
const PURGE_QUICK_DAYS = [
  { days: 30, label: "manageTrashOlderThan30" as const },
  { days: 90, label: "manageTrashOlderThan90" as const },
];

// datetimeLocalDaysAgo is the datetime-local value (local time, no zone, to the
// minute) for `days` days before `now`.
export function datetimeLocalDaysAgo(days: number, now: Date = new Date()): string {
  const d = new Date(now.getTime());
  d.setDate(d.getDate() - days);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export function TrashPage() {
  const t = useT();
  const [entries, setEntries] = useState<TrashEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const loadGeneration = useRef(0);

  const load = useCallback(async () => {
    const generation = ++loadGeneration.current;
    setError("");
    setLoading(true);
    try {
      const next = await fetchTrashList();
      if (loadGeneration.current === generation) setEntries(next);
    } catch {
      if (loadGeneration.current === generation) setError(t("trashUnavailable"));
    } finally {
      if (loadGeneration.current === generation) setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void load();
    return () => {
      loadGeneration.current += 1;
    };
  }, [load]);

  const onRestore = async (entry: TrashEntry) => {
    setError("");
    try {
      // restoreMedia returns the updated trash list; reload to reflect the
      // server's retention window after the row leaves.
      await restoreMedia(entry.id);
      await load();
    } catch {
      setError(t("restoreFailed"));
    }
  };

  return (
    <div className="space-y-4">
      <ManageSection
        actions={
          <div className="flex items-center gap-2">
            <StatusBadge state="neutral">
              {t("trashRetainedCount", { count: entries.length })}
            </StatusBadge>
            <Button type="button" variant="outline" size="sm" onClick={load}>
              {t("refresh")}
            </Button>
          </div>
        }
      >
        <div id={RETAINED_TRASH_ANCHOR} className="scroll-mt-4">
          {!loading && entries.length === 0 ? (
            <EmptyLine message={t("trashIsEmpty")} />
          ) : (
            <ManageTable>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("title")}</TableHead>
                  <TableHead>{t("trash")}</TableHead>
                  <TableHead className="text-right">{t("restore")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {loading ? (
                  <TableSkeletonRows columns={3} />
                ) : (
                  entries.map((entry) => (
                    <TableRow key={entry.id}>
                      <TableCell className="manage-cell-primary">{entry.title}</TableCell>
                      <TableCell className="manage-cell-num">
                        {formatDateTime(entry.trashedAt)}
                      </TableCell>
                      <TableCell className="text-right">
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          onClick={() => onRestore(entry)}
                        >
                          {t("restore")}
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))
                )}
              </TableBody>
            </ManageTable>
          )}
        </div>
        <ErrorLine message={error} />
      </ManageSection>

      <PurgeSection onPurged={load} />

      <ActivitySection />
    </div>
  );
}

// PurgeSection: the irreversible purge of retained Trash older than a cutoff.
// Two gates guard it before the mutation fires — the operator must (1) tick the
// explicit acknowledgement checkbox, which alone enables the button, and (2)
// confirm in the AlertDialog. The server re-checks Admin-Session + reveal +
// confirm regardless; this is the affordance, not the authority.
function PurgeSection({ onPurged }: { onPurged: () => Promise<void> | void }) {
  const t = useT();
  // datetime-local value (no zone); converted to RFC3339 UTC at call time.
  const [before, setBefore] = useState("");
  const [acknowledged, setAcknowledged] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [purged, setPurged] = useState<number | null>(null);

  const canPurge = acknowledged && before.trim().length > 0 && !busy;

  const onConfirm = async () => {
    setError("");
    setPurged(null);
    if (!before.trim()) {
      setError(t("purgeNeedCutoff"));
      return;
    }
    const cutoff = new Date(before);
    if (Number.isNaN(cutoff.getTime())) {
      setError(t("purgeNeedCutoff"));
      return;
    }
    setBusy(true);
    try {
      const result = await purgeTrash(cutoff.toISOString(), true);
      setPurged(result.affected);
      setAcknowledged(false);
      setBefore("");
      await onPurged();
    } catch {
      setError(t("purgeFailed"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <ManageSection
      title={t("purgeHeading")}
      actions={<StatusBadge state="red">{t("purgeIrreversible")}</StatusBadge>}
    >
      <div className="space-y-3">
        <div className="flex flex-wrap items-end gap-3" data-setting="manage.trash.purgeBefore">
          <div className="space-y-1">
            <span className="bf-label-with-tip">
              <Label htmlFor="purge-before">{t("purgeBeforeLabel")}</Label>
              <HelpTip label={t("purgeBeforeLabel")} text="manageTrashPurgeBeforeHelp" />
            </span>
            <Input
              id="purge-before"
              type="datetime-local"
              value={before}
              onChange={(e) => setBefore(e.target.value)}
              className="w-60"
            />
          </div>
          {PURGE_QUICK_DAYS.map((quick) => (
            <Button
              key={quick.days}
              type="button"
              variant="outline"
              size="sm"
              onClick={() => setBefore(datetimeLocalDaysAgo(quick.days))}
            >
              {t(quick.label)}
            </Button>
          ))}
        </div>

        <label className="flex items-start gap-2">
          <Checkbox
            checked={acknowledged}
            onCheckedChange={(v) => setAcknowledged(v === true)}
            aria-label={t("purgeConfirmGate")}
          />
          <span className="text-sm text-muted-foreground">{t("purgeConfirmGate")}</span>
        </label>

        <div className="flex items-center gap-3">
          <AlertDialog>
            <AlertDialogTrigger asChild>
              <Button type="button" variant="danger" disabled={!canPurge}>
                {busy ? t("purgeRunning") : t("purgeAction")}
              </Button>
            </AlertDialogTrigger>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>{t("purgeHeading")}</AlertDialogTitle>
                <AlertDialogDescription>{t("purgeConfirmGate")}</AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
                <AlertDialogAction onClick={onConfirm}>{t("purgeAction")}</AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>

          {purged !== null && (
            <StatusBadge state="green">{t("purgeDone", { count: purged })}</StatusBadge>
          )}
        </div>

        <ErrorLine message={error} />
      </div>
    </ManageSection>
  );
}

const TRASH_ACTIVITY_LIMIT = 50;

// ActivitySection: the append-only operator feed (latest 50). Reversible rows
// expose an Undo that jumps to the Retained Trash table where the matching
// Restore lives; irreversible rows (trash.purge) are flagged with no undo. The
// feed is read-only — undo is a navigation affordance over the existing
// recoverable-delete surface, not a new server mutation.
function ActivitySection() {
  const t = useT();
  const [entries, setEntries] = useState<ActivityRecord[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setError("");
    setLoading(true);
    try {
      setEntries(await fetchActivityRecords(undefined, 0, TRASH_ACTIVITY_LIMIT));
    } catch {
      setError(t("activityLogUnavailable"));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void load();
  }, [load]);

  const onUndo = () => {
    document.getElementById(RETAINED_TRASH_ANCHOR)?.scrollIntoView({ behavior: "smooth" });
  };

  const rows = useMemo(() => entries, [entries]);

  return (
    <ManageSection
      title={t("activityUndoHeading")}
      lead={t("activityUndoLead")}
      actions={
        <Button type="button" variant="outline" size="sm" onClick={load}>
          {t("refresh")}
        </Button>
      }
    >
      {!loading && rows.length === 0 ? (
        <EmptyLine message={t("activityLogEmpty")} />
      ) : (
        <ManageTable>
          <TableHeader>
            <TableRow>
              <TableHead>{t("activityPageWhen")}</TableHead>
              <TableHead>{t("activityPageWhat")}</TableHead>
              <TableHead className="text-right">{t("activityUndo")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              <TableSkeletonRows columns={3} />
            ) : (
              rows.map((entry) => {
                const reversible = REVERSIBLE_ACTIONS.has(entry.action);
                const irreversible = entry.action === "trash.purge";
                return (
                  <TableRow key={entry.id}>
                    <TableCell className="manage-cell-num whitespace-nowrap">
                      {formatDateTime(entry.occurredAt)}
                    </TableCell>
                    {/* The same sentence the Console activity log shows; the raw
                        event key stays available on hover for operators. */}
                    <TableCell title={entry.action}>
                      {describeActivity(entry, t).sentence}
                    </TableCell>
                    <TableCell className="text-right">
                      {reversible ? (
                        <Button type="button" variant="outline" size="sm" onClick={onUndo}>
                          {t("activityUndo")}
                        </Button>
                      ) : irreversible ? (
                        <StatusBadge state="red">{t("activityNoUndo")}</StatusBadge>
                      ) : (
                        <span className="text-muted-foreground">—</span>
                      )}
                    </TableCell>
                  </TableRow>
                );
              })
            )}
          </TableBody>
        </ManageTable>
      )}
      <ErrorLine message={error} />
    </ManageSection>
  );
}
