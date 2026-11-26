import { useCallback, useEffect, useState } from "react";
import {
  EmptyLine,
  ErrorLine,
  HelpTip,
  ManageSection,
  ManageTable,
  PathPickerField,
  StatusBadge,
  TableSkeletonRows,
} from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { fetchLibraries } from "@/lib/graphql";
import type { JobInfo, LibraryInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { jobProgressDetail, jobStatusLabel, jobStatusState } from "@/lib/jobDisplay";
import { CONVERT_PRESETS, enqueueConvertJob, fetchConvertJobs } from "../lib/powertools";

// CONVERT (apple ADR-0003 §2): the canonical convert-to-managed-file surface.
// Source selection uses the host-aware PathPickerField — native ergonomics when
// the Companion injects window.beamfallHost, in-page / server-side browse
// otherwise (so it isn't dead on iPad). The preset picker names the server
// profiles; the queue + progress read the existing job substrate filtered to the
// convert job kind. When the server has no enqueue field the surface degrades to
// monitoring-only with a clear notice rather than a hard error.
export function ConvertPage() {
  const [refreshKey, setRefreshKey] = useState(0);
  return (
    <div className="space-y-4">
      <ConvertEnqueueSection onEnqueued={() => setRefreshKey((k) => k + 1)} />
      <ConvertQueueSection refreshKey={refreshKey} />
    </div>
  );
}

function ConvertEnqueueSection({ onEnqueued }: { onEnqueued: () => void }) {
  const t = useT();
  const [libraries, setLibraries] = useState<LibraryInfo[]>([]);
  const [loadingLibs, setLoadingLibs] = useState(true);
  const [libraryID, setLibraryID] = useState("");
  const [srcPath, setSrcPath] = useState("");
  const [preset, setPreset] = useState(CONVERT_PRESETS[0].id);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [unavailable, setUnavailable] = useState(false);

  useEffect(() => {
    void (async () => {
      setLoadingLibs(true);
      try {
        const libs = await fetchLibraries();
        setLibraries(libs);
        if (libs.length > 0) setLibraryID(libs[0].id);
      } catch {
        setError(t("convertLibrariesUnavailable"));
      } finally {
        setLoadingLibs(false);
      }
    })();
  }, [t]);

  const onConvert = async () => {
    setError("");
    setNotice("");
    if (!libraryID || !srcPath.trim()) {
      setError(t("convertChooseLibraryAndSource"));
      return;
    }
    setBusy(true);
    try {
      const result = await enqueueConvertJob(libraryID, srcPath.trim(), preset);
      if (!result.available) {
        setUnavailable(true);
        setNotice(t("convertUnavailableNotice"));
        return;
      }
      setNotice(t("convertQueued"));
      setSrcPath("");
      onEnqueued();
    } catch {
      setError(t("convertFailed"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <ManageSection>
      <div className="manage-fieldrow space-y-3">
        {!loadingLibs && libraries.length === 0 ? (
          <EmptyLine message={t("manageNoLibraries")} />
        ) : (
          <>
            <div className="grid gap-1">
              <Label htmlFor="convert-library">{t("library")}</Label>
              <Select value={libraryID} onValueChange={setLibraryID} disabled={loadingLibs}>
                <SelectTrigger id="convert-library" className="w-64">
                  <SelectValue placeholder={loadingLibs ? t("loading") : t("library")} />
                </SelectTrigger>
                <SelectContent>
                  {libraries.map((lib) => (
                    <SelectItem key={lib.id} value={lib.id}>
                      {lib.title || lib.id}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            <PathPickerField
              id="convert-source"
              label={t("convertSourceLabel")}
              role="incomingFolder"
              value={srcPath}
              onChange={setSrcPath}
              libraryID={libraryID || undefined}
              placeholder="/path/to/source"
            />

            <div className="grid gap-1" data-setting="manage.convert.preset">
              <span className="bf-label-with-tip">
                <Label htmlFor="convert-preset">{t("convertPresetLabel")}</Label>
                <HelpTip label={t("convertPresetLabel")} text="manageConvertPresetHelp" />
              </span>
              <Select value={preset} onValueChange={setPreset}>
                <SelectTrigger id="convert-preset" className="w-64">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {CONVERT_PRESETS.map((p) => (
                    <SelectItem key={p.id} value={p.id}>
                      {t(p.labelKey)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            <div className="flex items-center gap-3">
              <Button type="button" size="sm" disabled={busy || unavailable} onClick={onConvert}>
                {t("convert")}
              </Button>
              {notice && (
                <span role="status" className="text-sm text-muted-foreground">
                  {notice}
                </span>
              )}
            </div>
          </>
        )}
        <ErrorLine message={error} />
      </div>
    </ManageSection>
  );
}

function ConvertQueueSection({ refreshKey }: { refreshKey: number }) {
  const t = useT();
  const [jobs, setJobs] = useState<JobInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setError("");
    setLoading(true);
    try {
      setJobs(await fetchConvertJobs());
    } catch {
      setError(t("jobsUnavailable"));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void load();
  }, [load, refreshKey]);

  return (
    <ManageSection
      title={t("convertQueueTitle")}
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
                    <div className="manage-cell-primary">{job.target?.libraryTitle || job.id}</div>
                    {job.target?.libraryTitle && (
                      <div className="font-mono text-xs text-muted-foreground">{job.id}</div>
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
