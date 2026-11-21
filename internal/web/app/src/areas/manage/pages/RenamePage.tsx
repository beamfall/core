import { useEffect, useMemo, useRef, useState } from "react";
import {
  EmptyLine,
  ErrorLine,
  HelpTip,
  ManageSection,
  ManageTable,
  PathPickerField,
  PresetSelect,
  StatusBadge,
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
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ApiError } from "@/lib/api";
import { fetchFolderTree, fetchLibraries } from "@/lib/graphql";
import type { FolderEntryInfo, LibraryInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";
import { batchRenameManagedPaths, undoBatchRename } from "../lib/powertools";
import {
  changedRenames,
  DEFAULT_PATTERN,
  previewRenames,
  type RenamePattern,
  type RenameSource,
} from "../lib/renamePattern";

// Number widths for the sequence number (CORE-2445): a few named choices
// instead of a free 0–6 field. 0 is no padding; the default stays 2.
const DIGIT_PRESETS = [2, 3, 4];

// RENAME (apple ADR-0003 §2): journaled, reversible batch rename over managed
// files (POWERTOOLS-2). The operator picks a folder with the host-aware picker,
// the flat file list is read from the bounded server folder tree, a pure
// client-side pattern preview shows old→new, APPLY drives the journaled batch and
// keeps its id, and UNDO rolls that journal back. The server is the authority on
// every name; the preview is only an affordance.
export function RenamePage() {
  const t = useT();
  const [libraries, setLibraries] = useState<LibraryInfo[]>([]);
  const [libraryID, setLibraryID] = useState("");
  const [folder, setFolder] = useState("");
  const [sources, setSources] = useState<RenameSource[]>([]);
  const [pattern, setPattern] = useState<RenamePattern>(DEFAULT_PATTERN);
  const [loadingFiles, setLoadingFiles] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [lastBatchID, setLastBatchID] = useState<string | null>(null);
  const [notice, setNotice] = useState("");
  // The Library and folder the loaded sources were read from (CORE-0755).
  // Sources hold absolute paths, so once the visible selection moves away from
  // them Apply stays off until Load files reads the new selection; Apply and
  // the post-change reload always target this loaded set, never the fields.
  const [loadedFrom, setLoadedFrom] = useState<{ libraryID: string; folder: string } | null>(null);
  const loadGenerationRef = useRef(0);
  const selectionMatches =
    loadedFrom !== null &&
    loadedFrom.libraryID === libraryID &&
    loadedFrom.folder === folder.trim();

  useEffect(() => {
    void (async () => {
      try {
        const libs = await fetchLibraries();
        setLibraries(libs);
        if (libs.length > 0) setLibraryID(libs[0].id);
      } catch {
        setError(t("convertLibrariesUnavailable"));
      }
    })();
  }, [t]);

  const previews = useMemo(() => previewRenames(sources, pattern), [sources, pattern]);
  const changeCount = useMemo(() => previews.filter((p) => p.changed).length, [previews]);

  const setField = <K extends keyof RenamePattern>(key: K, value: RenamePattern[K]) =>
    setPattern((prev) => ({ ...prev, [key]: value }));

  const onLoadFiles = async () => {
    if (!libraryID || !folder.trim()) {
      setError(t("renameChooseLibraryAndFolder"));
      return;
    }
    setError("");
    setNotice("");
    setLoadingFiles(true);
    const selection = { libraryID, folder: folder.trim() };
    const generation = ++loadGenerationRef.current;
    try {
      const tree = await fetchFolderTree(selection.libraryID, selection.folder, 1);
      if (generation !== loadGenerationRef.current) return;
      const files = (tree.children ?? []).filter((c) => c.kind === "file");
      setSources(files.map((f: FolderEntryInfo) => ({ path: f.path, name: f.name })));
      setLoadedFrom(selection);
      if (files.length === 0) setNotice(t("renameNoFiles"));
    } catch {
      if (generation !== loadGenerationRef.current) return;
      setError(t("renameListFolderFailed"));
      setSources([]);
      setLoadedFrom(null);
    } finally {
      if (generation === loadGenerationRef.current) setLoadingFiles(false);
    }
  };

  const onApply = async () => {
    const renames = changedRenames(previews);
    if (!loadedFrom || !selectionMatches) return;
    if (renames.length === 0) {
      setError(t("renameNoRenames"));
      return;
    }
    setError("");
    setNotice("");
    setBusy(true);
    try {
      const result = await batchRenameManagedPaths(loadedFrom.libraryID, renames);
      setLastBatchID(result.batchID);
      setNotice(t("renameApplied").replace("{count}", String(result.affected)));
      await onReloadAfterChange(loadedFrom);
    } catch (err) {
      setError(err instanceof ApiError && err.message ? err.message : t("renameApplyFailed"));
    } finally {
      setBusy(false);
    }
  };

  const onUndo = async () => {
    if (!lastBatchID) return;
    setError("");
    setNotice("");
    setBusy(true);
    try {
      const result = await undoBatchRename(lastBatchID);
      setNotice(t("renameReverted").replace("{count}", String(result.affected)));
      setLastBatchID(null);
      if (loadedFrom) await onReloadAfterChange(loadedFrom);
    } catch {
      setError(t("renameUndoFailed"));
    } finally {
      setBusy(false);
    }
  };

  // After a rename/undo the on-disk basenames changed; reload so the preview
  // reflects the current truth rather than stale source names.
  const onReloadAfterChange = async (selection: { libraryID: string; folder: string }) => {
    const generation = loadGenerationRef.current;
    try {
      const tree = await fetchFolderTree(selection.libraryID, selection.folder, 1);
      if (generation !== loadGenerationRef.current) return;
      const files = (tree.children ?? []).filter((c) => c.kind === "file");
      setSources(files.map((f: FolderEntryInfo) => ({ path: f.path, name: f.name })));
    } catch {
      // a reload failure is non-fatal; keep the prior list
    }
  };

  return (
    <div className="space-y-4">
      <ManageSection title={t("renameBatchTitle")}>
        <div className="manage-fieldrow space-y-3">
          <div className="grid gap-1">
            <Label htmlFor="rename-library">{t("library")}</Label>
            <Select value={libraryID} onValueChange={setLibraryID}>
              <SelectTrigger id="rename-library" className="w-64">
                <SelectValue placeholder={t("library")} />
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
            id="rename-folder"
            label={t("renameFolderLabel")}
            role="libraryRoot"
            value={folder}
            onChange={setFolder}
            libraryID={libraryID || undefined}
            placeholder="/path/to/folder"
          />

          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={loadingFiles}
            onClick={onLoadFiles}
          >
            {t("renameLoadFiles")}
          </Button>
        </div>
      </ManageSection>

      <ManageSection title={t("renamePatternTitle")}>
        <div className="manage-fieldrow flex flex-wrap items-end gap-3">
          <Field
            label={t("renameFindLabel")}
            tip="manageRenameFindHelp"
            setting="manage.rename.find"
            value={pattern.find}
            onChange={(v) => setField("find", v)}
          />
          <Field
            label={t("renameReplaceLabel")}
            value={pattern.replace}
            onChange={(v) => setField("replace", v)}
          />
          <Field
            label={t("renamePrefixLabel")}
            tip="manageRenamePrefixHelp"
            setting="manage.rename.prefix"
            value={pattern.prefix}
            onChange={(v) => setField("prefix", v)}
          />
          <NumField
            label={t("renameStartAtLabel")}
            value={pattern.startAt}
            min={0}
            onChange={(v) => setField("startAt", v)}
          />
          <div className="grid gap-1" data-setting="manage.rename.digits">
            <span className="bf-label-with-tip">
              <Label htmlFor="rename-digits">{t("manageRenameDigits")}</Label>
              <HelpTip label={t("manageRenameDigits")} text="manageRenameDigitsHelp" />
            </span>
            <PresetSelect
              id="rename-digits"
              value={pattern.pad}
              presets={[
                { value: 0, label: t("none") },
                ...DIGIT_PRESETS.map((n) => ({ value: n, label: formatDigits(n) })),
              ]}
              format={formatDigits}
              onChange={(v) => setField("pad", v)}
              className="w-32"
            />
          </div>
        </div>
      </ManageSection>

      <ManageSection
        title={t("renamePreviewTitle")}
        actions={
          <div className="flex items-center gap-2">
            <AlertDialog>
              <AlertDialogTrigger asChild>
                <Button
                  type="button"
                  size="sm"
                  disabled={busy || changeCount === 0 || !selectionMatches}
                >
                  {t("renameApply").replace("{count}", String(changeCount))}
                </Button>
              </AlertDialogTrigger>
              <AlertDialogContent>
                <AlertDialogHeader>
                  <AlertDialogTitle>{t("renamePreviewTitle")}</AlertDialogTitle>
                  <AlertDialogDescription>
                    {t("renameApply").replace("{count}", String(changeCount))}
                  </AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                  <AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
                  <AlertDialogAction
                    onClick={onApply}
                    className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
                  >
                    {t("renameApply").replace("{count}", String(changeCount))}
                  </AlertDialogAction>
                </AlertDialogFooter>
              </AlertDialogContent>
            </AlertDialog>
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={busy || !lastBatchID}
              onClick={onUndo}
            >
              {t("renameUndo")}
            </Button>
          </div>
        }
      >
        {notice && (
          <p role="status" className="mb-2 text-sm text-muted-foreground">
            {notice}
          </p>
        )}
        {sources.length === 0 ? (
          <EmptyLine message={t("renameLoadFolderNotice")} />
        ) : (
          <ManageTable>
            <TableHeader>
              <TableRow>
                <TableHead>{t("renameColCurrent")}</TableHead>
                <TableHead>{t("renameColNew")}</TableHead>
                <TableHead className="text-right">{t("active")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {previews.map((p) => (
                <TableRow key={p.path}>
                  <TableCell className="manage-cell-primary break-all">{p.oldName}</TableCell>
                  <TableCell className="break-all">{p.newName}</TableCell>
                  <TableCell className="text-right">
                    {p.changed ? (
                      <StatusBadge state="cyan">{t("renameStatusChanged")}</StatusBadge>
                    ) : (
                      <StatusBadge state="neutral">{t("renameStatusUnchanged")}</StatusBadge>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </ManageTable>
        )}
        <ErrorLine message={error} />
      </ManageSection>
    </div>
  );
}

// How a digit count reads in the dropdown: the number 1 padded to that width.
function formatDigits(digits: number): string {
  return "1".padStart(Math.max(digits, 1), "0");
}

function Field({
  label,
  tip,
  setting,
  value,
  onChange,
}: {
  label: string;
  tip?: StringKey;
  setting?: string;
  value: string;
  onChange: (value: string) => void;
}) {
  return (
    <div className="grid gap-1" data-setting={setting}>
      {tip ? (
        <span className="bf-label-with-tip">
          <Label>{label}</Label>
          <HelpTip label={label} text={tip} />
        </span>
      ) : (
        <Label>{label}</Label>
      )}
      <Input
        aria-label={label}
        value={value}
        autoComplete="off"
        spellCheck={false}
        onChange={(e) => onChange(e.target.value)}
        className="w-44"
      />
    </div>
  );
}

function NumField({
  label,
  value,
  min,
  max,
  onChange,
}: {
  label: string;
  value: number;
  min: number;
  max?: number;
  onChange: (value: number) => void;
}) {
  return (
    <div className="grid gap-1">
      <Label>{label}</Label>
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
    </div>
  );
}
