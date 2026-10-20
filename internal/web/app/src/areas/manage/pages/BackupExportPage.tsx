import { useCallback, useEffect, useRef, useState } from "react";
import { withAdminEpoch } from "@/areas/console/adminEpoch";
import {
  EmptyLine,
  ErrorLine,
  ManageSection,
  ManageTable,
  PathPickerField,
  StatusBadge,
  StatusChip,
  TableSkeletonRows,
} from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { fetchBackupHistory, fetchMetadataExport, runBackup, verifyBackup } from "@/lib/graphql";
import type { BackupHistoryEntry, MetadataExportInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { formatDateTime } from "../lib/schedule";

// Backup panel (MIGREV-3 / ADR-0077 dec. 2-3): the standalone Manage Backup page.
// It surfaces the MIGREV-1 backend in-app —
//   1. Run backup: run a database backup to a server-side destination; the
//      mutation returns the written manifest path (the newly published set).
//   2. History: the run record of every set under the destination
//      (backupHistory), each with its size and confined integrity verify state;
//      a per-row Verify re-runs verifyBackup and shows a pass/fail result.
//   3. Manifest detail: the selected set's manifest path and the binding
//      "Recovery Keys are NOT in this backup" exclusion callout.
//   4. Restore assistant: the destructive whole-DB restore is OPERATOR-RUN
//      OFFLINE (server stopped) — the assistant guides toward the offline
//      restore CLI with the schema-version guard, never a live-DB overwrite from
//      the Console.
//   5. Metadata export: the reveal-filtered portability export, downloaded JSON.
// The backend backupHistory/verifyBackup return only a sanitized pass/fail and a
// reason — never a checksum of an arbitrary server file — so this page derives
// its verify states from `valid` + `error`, not from client-computed hashes.

// The manifest file name inside a published set (store.backupManifestBasename).
const MANIFEST_BASENAME = "manifest.json";

// The event-handler reads on this page (backupHistory, metadataExport) compose
// the same two boundaries the common Console fetch path composes in
// areas/console/useAsync.beginRequest: the component's own lifetime plus the
// Admin-Session epoch (areas/console/adminEpoch — expiry, lock, Profile
// switch; published by AdminGate, cleared/aborted the way lib/session.tsx
// handles identity-epoch transitions). A response that lands after either
// boundary is prior-epoch work: its AbortSignal has fired, the transport is
// aborted, and no state write lands. Outside the Console (the Manage route),
// no Admin epoch is published and withAdminEpoch is a pass-through.
function useEpochRequest() {
  const epoch = useRef(0);
  const request = useRef<AbortController | null>(null);
  useEffect(
    () => () => {
      // Unmount is a boundary: abort the in-flight transport and advance the
      // epoch so a settled response cannot write to the unmounted component.
      request.current?.abort();
      epoch.current++;
    },
    [],
  );
  return useCallback(() => {
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    const started = ++epoch.current;
    const signal = withAdminEpoch(controller.signal);
    const current = () => epoch.current === started && !signal.aborted;
    return { current, signal };
  }, []);
}

function manifestPath(entry: BackupHistoryEntry): string {
  return `${entry.directory}/${MANIFEST_BASENAME}`;
}

function formatBytes(bytes: number): string {
  if (bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const exp = Math.min(units.length - 1, Math.floor(Math.log(bytes) / Math.log(1024)));
  const value = bytes / 1024 ** exp;
  const text = exp === 0 ? String(value) : value.toFixed(1);
  return `${text} ${units[exp]}`;
}

export function BackupExportPage() {
  // The backup destination is shared: the run form writes to it, and the
  // history + manifest detail read the sets published under it.
  const [dest, setDest] = useState("");
  const [entries, setEntries] = useState<BackupHistoryEntry[]>([]);
  const [selected, setSelected] = useState<BackupHistoryEntry | null>(null);

  return (
    <div className="space-y-4">
      <BackupHistorySection
        dest={dest}
        setDest={setDest}
        entries={entries}
        setEntries={setEntries}
        selected={selected}
        setSelected={setSelected}
      />
      <ManifestSection selected={selected} />
      <RestoreSection />
      <ExportSection />
    </div>
  );
}

function BackupHistorySection({
  dest,
  setDest,
  entries,
  setEntries,
  selected,
  setSelected,
}: {
  dest: string;
  setDest: (v: string) => void;
  entries: BackupHistoryEntry[];
  setEntries: (v: BackupHistoryEntry[]) => void;
  selected: BackupHistoryEntry | null;
  setSelected: (v: BackupHistoryEntry | null) => void;
}) {
  const t = useT();
  const [loading, setLoading] = useState(false);
  const [running, setRunning] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState("");
  // Per-row fresh verify result keyed by set directory; the row's own state
  // (valid/error) is the last verify — this is the operator-triggered re-check.
  const [verifying, setVerifying] = useState<string | null>(null);
  const [verifyResult, setVerifyResult] = useState<{
    directory: string;
    ok: boolean;
    error?: string;
  } | null>(null);

  const beginRequest = useEpochRequest();

  const loadHistory = useCallback(async () => {
    const trimmed = dest.trim();
    if (!trimmed) {
      setError(t("backupHistoryNeedDest"));
      return;
    }
    const { current, signal } = beginRequest();
    setError("");
    setLoading(true);
    try {
      const next = await fetchBackupHistory(trimmed, signal);
      if (!current()) return;
      setEntries(next);
      setLoaded(true);
      // Drop a stale selection that is no longer in the destination.
      if (selected && !next.some((e) => e.directory === selected.directory)) {
        setSelected(null);
      }
    } catch {
      if (!current()) return;
      setError(t("backupHistoryUnavailable"));
    } finally {
      if (current()) setLoading(false);
    }
  }, [beginRequest, dest, selected, setEntries, setSelected, t]);

  const onRun = async () => {
    const trimmed = dest.trim();
    if (!trimmed) {
      setError(t("backupNeedDest"));
      return;
    }
    setError("");
    setRunning(true);
    try {
      await runBackup(trimmed);
      // Reload so the newly published set appears in the run record.
      await loadHistory();
    } catch {
      setError(t("backupFailed"));
    } finally {
      setRunning(false);
    }
  };

  const onVerify = async (entry: BackupHistoryEntry) => {
    setError("");
    setVerifying(entry.directory);
    setVerifyResult(null);
    try {
      const result = await verifyBackup(manifestPath(entry));
      setVerifyResult({
        directory: entry.directory,
        ok: result.ok,
        error: result.error,
      });
      // Reflect the fresh verify state in the row.
      setEntries(
        entries.map((e) =>
          e.directory === entry.directory ? { ...e, valid: result.ok, error: result.error } : e,
        ),
      );
    } catch {
      setError(t("backupHistoryUnavailable"));
    } finally {
      setVerifying(null);
    }
  };

  return (
    <ManageSection
      title={t("backupHistoryHeading")}
      lead={t("backupHistoryLead")}
      actions={
        loaded ? (
          <StatusBadge state="neutral">
            {t("backupHistoryCount", { count: entries.length })}
          </StatusBadge>
        ) : undefined
      }
    >
      <div className="space-y-3">
        <div className="flex flex-wrap items-end gap-3">
          <PathPickerField
            id="backup-dest"
            label={t("backupDestLabel")}
            role="backupDestination"
            value={dest}
            placeholder={t("backupDestPlaceholder")}
            onChange={setDest}
            className="grow"
          />
          <Button
            type="button"
            variant="outline"
            onClick={loadHistory}
            disabled={loading || !dest.trim()}
          >
            {loading ? t("backupHistoryLoading") : t("backupHistoryLoad")}
          </Button>
          <Button type="button" onClick={onRun} disabled={running || !dest.trim()}>
            {running ? t("backupRunning") : t("backupRun")}
          </Button>
        </div>

        {loaded &&
          (entries.length === 0 ? (
            <EmptyLine message={t("backupHistoryEmpty")} />
          ) : (
            <ManageTable>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("backupColSet")}</TableHead>
                  <TableHead>{t("backupColCaptured")}</TableHead>
                  <TableHead className="text-right">{t("backupColSize")}</TableHead>
                  <TableHead>{t("backupColVerify")}</TableHead>
                  <TableHead className="text-right">{t("backupVerifyAction")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {loading ? (
                  <TableSkeletonRows columns={5} />
                ) : (
                  entries.map((entry) => (
                    <TableRow
                      key={entry.directory}
                      data-selected={selected?.directory === entry.directory || undefined}
                      className="cursor-pointer"
                      onClick={() => setSelected(entry)}
                    >
                      <TableCell className="manage-cell-num break-all">
                        {setLabel(entry.directory)}
                      </TableCell>
                      <TableCell className="manage-cell-num">
                        {formatDateTime(entry.createdAt)}
                      </TableCell>
                      <TableCell className="manage-cell-num text-right">
                        {formatBytes(entry.sizeBytes)}
                      </TableCell>
                      <TableCell>
                        <VerifyState entry={entry} />
                      </TableCell>
                      <TableCell className="text-right">
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          disabled={verifying === entry.directory}
                          onClick={(e) => {
                            e.stopPropagation();
                            void onVerify(entry);
                          }}
                        >
                          {verifying === entry.directory
                            ? t("backupVerifyRunning")
                            : t("backupVerifyAction")}
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))
                )}
              </TableBody>
            </ManageTable>
          ))}

        {verifyResult && (
          <div
            role="status"
            className={
              verifyResult.ok
                ? "rounded-md border border-[var(--green-500)]/40 bg-[var(--green-500)]/5 px-3 py-2 text-sm"
                : "rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-sm"
            }
          >
            <span className="font-medium">{setLabel(verifyResult.directory)}: </span>
            {verifyResult.ok ? t("backupVerifyPassBanner") : t("backupVerifyFailBanner")}
            {!verifyResult.ok && verifyResult.error && (
              <span className="block break-all text-muted-foreground">{verifyResult.error}</span>
            )}
          </div>
        )}

        <ErrorLine message={error} />
      </div>
    </ManageSection>
  );
}

// The last path segment of a set directory is its human-facing set id.
function setLabel(directory: string): string {
  const parts = directory.split("/").filter(Boolean);
  return parts.length > 0 ? parts[parts.length - 1] : directory;
}

// Derive the row's verify chip from the confined integrity result: valid sets
// are Verified (green); an invalid set with a reason Failed (red).
function VerifyState({ entry }: { entry: BackupHistoryEntry }) {
  const t = useT();
  if (entry.valid) {
    return <StatusChip tone="green">{t("backupVerified")}</StatusChip>;
  }
  if (entry.error) {
    return <StatusChip tone="red">{t("backupVerifyFailed")}</StatusChip>;
  }
  return <StatusChip tone="neutral">{t("backupVerifyNotRun")}</StatusChip>;
}

// ManifestSection: the selected set's manifest path plus the binding ADR-0077
// Recovery-Keys exclusion callout. The store never surfaces per-file hashes to
// clients (that lives on the confined verify path), so this stays to what the
// wire exposes: the manifest path, capture time, and the exclusion contract.
function ManifestSection({ selected }: { selected: BackupHistoryEntry | null }) {
  const t = useT();

  return (
    <ManageSection
      title={t("backupManifestHeading")}
      lead={t("backupManifestLead")}
      actions={
        selected ? (
          <StatusBadge state={selected.valid ? "green" : "red"}>
            {selected.valid ? t("backupVerified") : t("backupVerifyFailed")}
          </StatusBadge>
        ) : undefined
      }
    >
      {!selected ? (
        <EmptyLine message={t("backupManifestNone")} />
      ) : (
        <div className="space-y-3">
          <div className="space-y-1">
            <Label>{t("backupManifestPathLabel")}</Label>
            <p className="manage-cell-num break-all rounded-md border bg-muted/40 px-3 py-2 text-sm">
              {manifestPath(selected)}
            </p>
            <p className="text-sm text-muted-foreground">
              {formatDateTime(selected.createdAt)} · {formatBytes(selected.sizeBytes)}
            </p>
          </div>

          {/* Binding ADR-0077 callout: Recovery Keys are excluded by design. */}
          <div className="rounded-md border border-[var(--amber-500)]/45 bg-[var(--amber-500)]/5 px-3 py-3">
            <p className="flex items-center gap-2 text-sm font-semibold">
              <span
                className="inline-block h-2 w-2 rounded-full bg-[var(--amber-500)]"
                aria-hidden
              />
              {t("backupKeysCalloutTitle")}
            </p>
            <p className="mt-1 text-sm text-muted-foreground">{t("backupKeysCalloutBody")}</p>
          </div>
        </div>
      )}
    </ManageSection>
  );
}

// RestoreSection: the restore assistant. Per the approved MIGREV-3 mockup and
// ADR-0077, the destructive whole-DB restore is OPERATOR-RUN OFFLINE — the
// server refuses a live-DB overwrite, so the Console guides the operator through
// the offline restore CLI (schema-version guard included) rather than restoring
// over a live database.
function RestoreSection() {
  const t = useT();

  return (
    <ManageSection title={t("backupRestoreHeading")} lead={t("backupRestoreLead")}>
      <div id="restore" className="space-y-3" tabIndex={-1}>
        <ol className="list-decimal space-y-2 pl-5 text-sm text-muted-foreground">
          <li>{t("backupRestoreStepStop")}</li>
          <li>{t("backupRestoreStepChoose")}</li>
          <li>{t("backupRestoreStepRun")}</li>
          <li>{t("backupRestoreStepStart")}</li>
        </ol>
        <p className="manage-cell-num break-all rounded-md border bg-muted/40 px-3 py-2 text-sm">
          beamfall restore --manifest /path/to/backup/manifest.json --data-dir /var/lib/beamfall
        </p>
        <p className="text-sm text-muted-foreground">{t("backupRestoreSafety")}</p>
        <p className="text-sm text-muted-foreground">
          {t("backupRestoreNote")}
          <a
            href={t("backupRestoreNoteDocHref")}
            target="_blank"
            rel="noreferrer"
            className="underline"
          >
            {t("backupRestoreNoteDocLabel")}
          </a>
          {t("backupRestoreNoteAfterDoc")}
        </p>
      </div>
    </ManageSection>
  );
}

function ExportSection() {
  const t = useT();
  const [busy, setBusy] = useState(false);
  const [exportData, setExportData] = useState<MetadataExportInfo | null>(null);
  const [error, setError] = useState("");

  const beginRequest = useEpochRequest();

  const onGenerate = async () => {
    const { current, signal } = beginRequest();
    setError("");
    setBusy(true);
    try {
      const next = await fetchMetadataExport(signal);
      if (!current()) return;
      setExportData(next);
    } catch {
      if (!current()) return;
      setError(t("exportFailed"));
    } finally {
      if (current()) setBusy(false);
    }
  };

  // Download the verbatim export payload as a JSON file. Object URL is revoked
  // after the click so the blob does not leak.
  const onDownload = () => {
    if (!exportData) return;
    const blob = new Blob([JSON.stringify(exportData, null, 2)], {
      type: "application/json",
    });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `beamfall-metadata-${exportData.exportedAt || "export"}.json`;
    a.click();
    URL.revokeObjectURL(url);
  };

  return (
    <ManageSection
      title={t("exportHeading")}
      lead={t("exportLead")}
      actions={
        exportData ? (
          <StatusBadge state="neutral">
            {t("exportSchema")} {exportData.schema}
          </StatusBadge>
        ) : undefined
      }
    >
      <div className="space-y-3">
        {exportData && (
          <>
            <div className="grid grid-cols-3 gap-2">
              <Stat label={t("exportLibrariesCount")} value={exportData.libraries.length} />
              <Stat label={t("exportMediaCount")} value={exportData.media.length} />
              <Stat label={t("exportCollectionsCount")} value={exportData.collections.length} />
            </div>
            <p className="text-sm text-muted-foreground">
              {t("exportExportedAt")} {formatDateTime(exportData.exportedAt)}
            </p>
          </>
        )}

        <div className="flex items-center gap-3">
          <Button type="button" variant="outline" onClick={onGenerate} disabled={busy}>
            {busy ? t("exportGenerating") : t("exportGenerate")}
          </Button>
          <Button type="button" onClick={onDownload} disabled={!exportData}>
            {t("exportDownload")}
          </Button>
        </div>

        <ErrorLine message={error} />
      </div>
    </ManageSection>
  );
}

function Stat({ label, value }: { label: string; value: number }) {
  return (
    <div className="rounded-md border bg-muted/40 px-3 py-2">
      <div className="manage-cell-num text-lg font-semibold">{value}</div>
      <div className="text-xs uppercase tracking-wide text-muted-foreground">{label}</div>
    </div>
  );
}
