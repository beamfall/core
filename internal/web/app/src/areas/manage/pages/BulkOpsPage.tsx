import { useEffect, useState } from "react";
import { mediaAddressIDs } from "@/areas/viewer/lib/mediaAddress";
import { AssignDialog } from "@/components/assign/AssignDialog";
import { EntityMultiPicker } from "@/components/beamfall/EntityPicker";
import {
  ErrorLine,
  HelpTip,
  ManageSection,
  ManageTable,
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
import {
  bulkEditApply,
  bulkEditPreview,
  fetchLibraries,
  organizeApply,
  organizePreview,
  undoBatch,
} from "@/lib/graphql";
import type {
  BatchResultInfo,
  BulkEditChangeInfo,
  FileChangeInfo,
  LibraryInfo,
  MediaFieldInput,
} from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";

function fieldSummary(fields: MediaFieldInput[]): string {
  return fields.map((f) => `${f.name}=${f.value}`).join(", ");
}

// Bulk edit + file operations (ADR-0009 §6, ADR-0042): preview-first edits and
// organize/move over many Media, each apply one undoable batch. Ported from
// app.js bulk-edit-form / organize-form / undoBatch. Apply is destructive
// (HARD invariant #6) — it routes through a confirm dialog; the previewed change
// count is shown so the manager confirms against the real diff — Apply stays
// disabled until a preview exists for the CURRENT inputs — and Undo is the
// recovery affordance that stays live while a batch id is held.
export function BulkOpsPage() {
  const t = useT();
  const [error, setError] = useState("");
  const [batch, setBatch] = useState<BatchResultInfo | null>(null);
  const [undone, setUndone] = useState(false);
  const [undoing, setUndoing] = useState(false);

  const onApplied = (result: BatchResultInfo) => {
    setBatch(result.batchID ? result : null);
    setUndone(false);
  };

  const onUndo = async () => {
    if (!batch?.batchID || undoing) return;
    setError("");
    setUndoing(true);
    try {
      await undoBatch(batch.batchID);
      setBatch(null);
      setUndone(true);
    } catch {
      setError(t("bulkOpsUnavailable"));
    } finally {
      setUndoing(false);
    }
  };

  return (
    <div className="space-y-4">
      {(batch || undone || error) && (
        <ManageSection>
          {batch && (
            <div className="mb-2 flex items-center gap-3">
              <StatusBadge state="green">
                {t("bulkOpsBatchApplied")} · {batch.affected} {t("bulkOpsAffected")}
              </StatusBadge>
              <Button type="button" variant="outline" size="sm" disabled={undoing} onClick={onUndo}>
                {undoing ? t("loading") : t("bulkOpsUndo")}
              </Button>
            </div>
          )}
          {undone && !batch && (
            <p className="mb-2 text-sm text-muted-foreground">{t("bulkOpsUndone")}</p>
          )}
          <ErrorLine message={error} />
        </ManageSection>
      )}

      <BulkEditSection onApplied={onApplied} />
      <OrganizeSection onApplied={onApplied} />
    </div>
  );
}

const COMMON_BULK_FIELDS = ["title", "sortTitle", "year", "summary", "studio", "edition"];

function BulkEditSection({ onApplied }: { onApplied: (result: BatchResultInfo) => void }) {
  const t = useT();
  const { session, isManager } = useSession();
  const [ids, setIds] = useState<string[]>([]);
  const [fieldName, setFieldName] = useState("");
  const [fieldValue, setFieldValue] = useState("");
  const [changes, setChanges] = useState<BulkEditChangeInfo[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  // The inputs the held preview was computed from. Apply is only offered while
  // the form still matches them, so the confirmed count is the real diff.
  const [previewKey, setPreviewKey] = useState("");

  const mediaIDs = () =>
    ids.map((id) => {
      const address = id.split(/[?#]/, 1)[0];
      return /(?:^|[-/])sample1\.[0-9a-f]{17}\/?$/.test(address) ? mediaAddressIDs(address)[0] : id;
    });

  const fields = (): MediaFieldInput[] => {
    const name = fieldName.trim();
    if (!name) return [];
    return [{ name, type: "text", value: fieldValue }];
  };

  const onPreview = async () => {
    setError("");
    const m = mediaIDs();
    const f = fields();
    if (m.length === 0 || f.length === 0) return;
    setBusy(true);
    try {
      const previewed = await bulkEditPreview(m, f);
      setChanges(previewed);
      setPreviewKey(JSON.stringify([m, f]));
    } catch {
      setError(t("bulkOpsUnavailable"));
    } finally {
      setBusy(false);
    }
  };

  const onApply = async () => {
    setError("");
    const m = mediaIDs();
    const f = fields();
    if (m.length === 0 || f.length === 0) return;
    setBusy(true);
    try {
      onApplied(await bulkEditApply(m, f));
      setChanges(null);
      setPreviewKey("");
    } catch {
      setError(t("bulkOpsUnavailable"));
    } finally {
      setBusy(false);
    }
  };

  const canRun = mediaIDs().length > 0 && fields().length > 0;
  const previewCurrent = changes !== null && previewKey === JSON.stringify([mediaIDs(), fields()]);
  const count = changes?.length ?? 0;

  return (
    <ManageSection title={t("bulkEditPreviewHeading")}>
      <form
        className="space-y-3"
        onSubmit={(e) => {
          e.preventDefault();
          void onPreview();
        }}
      >
        <div className="space-y-1" data-setting="manage.bulk.mediaIds">
          <span className="bf-label-with-tip">
            <Label htmlFor="bulk-edit-ids">{t("bulkEditMediaIDs")}</Label>
            <HelpTip label={t("bulkEditMediaIDs")} text="manageBulkMediaIdsHelp" />
          </span>
          <EntityMultiPicker id="bulk-edit-ids" value={ids} onChange={setIds} />
        </div>
        <div className="flex flex-wrap items-end gap-3">
          <div className="space-y-1" data-setting="manage.bulk.fieldName">
            <span className="bf-label-with-tip">
              <Label htmlFor="bulk-edit-field-name">{t("bulkEditFieldName")}</Label>
              <HelpTip label={t("bulkEditFieldName")} text="manageBulkFieldNameHelp" />
            </span>
            <Input
              id="bulk-edit-field-name"
              list="bulk-edit-field-suggestions"
              value={fieldName}
              autoComplete="off"
              spellCheck={false}
              onChange={(e) => setFieldName(e.target.value)}
            />
            <datalist id="bulk-edit-field-suggestions">
              {COMMON_BULK_FIELDS.map((name) => (
                <option key={name} value={name} />
              ))}
            </datalist>
          </div>
          <div className="space-y-1">
            <Label htmlFor="bulk-edit-field-value">{t("bulkEditFieldValue")}</Label>
            <Input
              id="bulk-edit-field-value"
              value={fieldValue}
              onChange={(e) => setFieldValue(e.target.value)}
            />
          </div>
          <Button type="submit" variant="outline" disabled={busy || !canRun}>
            {t("bulkEditPreview")}
          </Button>
          <ConfirmApply
            label={t("bulkEditApply")}
            disabled={busy || !canRun || !previewCurrent || count === 0}
            count={count}
            onConfirm={onApply}
          />
          {isManager && session?.profile === "adult" && (
            <AssignDialog
              selection={mediaIDs().map((mediaID) => ({ mediaID }))}
              onChanged={() => setChanges(null)}
            />
          )}
        </div>
      </form>
      <ErrorLine message={error} />

      {changes !== null &&
        (changes.length === 0 ? (
          <p className="mt-3 text-sm text-muted-foreground">{t("bulkEditNoChanges")}</p>
        ) : (
          <ManageTable className="mt-3">
            <TableHeader>
              <TableRow>
                <TableHead>{t("title")}</TableHead>
                <TableHead>{t("bulkEditPreview")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {changes.map((change) => (
                <TableRow key={change.mediaID}>
                  <TableCell className="manage-cell-primary">
                    {change.title || change.mediaID}
                  </TableCell>
                  <TableCell className="manage-cell-num break-all">
                    {fieldSummary(change.old)} → {fieldSummary(change.new)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </ManageTable>
        ))}
    </ManageSection>
  );
}

function OrganizeSection({ onApplied }: { onApplied: (result: BatchResultInfo) => void }) {
  const t = useT();
  const [libraries, setLibraries] = useState<LibraryInfo[]>([]);
  const [libraryID, setLibraryID] = useState("");
  const [template, setTemplate] = useState("");
  const [changes, setChanges] = useState<FileChangeInfo[] | null>(null);
  const [previewKey, setPreviewKey] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    let active = true;
    void (async () => {
      try {
        const libs = await fetchLibraries();
        if (!active) return;
        setLibraries(libs);
        if (libs.length > 0) setLibraryID(libs[0].id);
      } catch {
        if (active) setError(t("convertLibrariesUnavailable"));
      }
    })();
    return () => {
      active = false;
    };
  }, [t]);

  const onPreview = async () => {
    setError("");
    if (!libraryID || !template.trim()) return;
    setBusy(true);
    try {
      const previewed = await organizePreview(libraryID, template.trim());
      setChanges(previewed);
      setPreviewKey(JSON.stringify([libraryID, template.trim()]));
    } catch {
      setError(t("bulkOpsUnavailable"));
    } finally {
      setBusy(false);
    }
  };

  const onApply = async () => {
    setError("");
    if (!libraryID || !template.trim()) return;
    setBusy(true);
    try {
      onApplied(await organizeApply(libraryID, template.trim()));
      setChanges(null);
      setPreviewKey("");
    } catch {
      setError(t("bulkOpsUnavailable"));
    } finally {
      setBusy(false);
    }
  };

  const canRun = Boolean(libraryID && template.trim());
  const previewCurrent =
    changes !== null && previewKey === JSON.stringify([libraryID, template.trim()]);
  const count = changes?.length ?? 0;

  return (
    <ManageSection title={t("organizeHeading")}>
      <form
        className="space-y-3"
        onSubmit={(e) => {
          e.preventDefault();
          void onPreview();
        }}
      >
        <div className="flex flex-wrap items-end gap-3">
          <div className="space-y-1">
            <Label htmlFor="organize-library">{t("organizeLibraryLabel")}</Label>
            <Select value={libraryID} onValueChange={setLibraryID}>
              <SelectTrigger id="organize-library" className="w-56">
                <SelectValue placeholder={t("library")} />
              </SelectTrigger>
              <SelectContent>
                {libraries.map((lib) => (
                  <SelectItem key={lib.id} value={lib.id}>
                    {lib.title}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="grow space-y-1" data-setting="manage.bulk.namingTemplate">
            <span className="bf-label-with-tip">
              <Label htmlFor="organize-template">{t("organizeTemplate")}</Label>
              <HelpTip label={t("organizeTemplate")} text="manageBulkTemplateHelp" />
            </span>
            <Input
              id="organize-template"
              value={template}
              placeholder="{year}/{title}"
              autoComplete="off"
              spellCheck={false}
              onChange={(e) => setTemplate(e.target.value)}
            />
          </div>
        </div>
        <div className="flex items-end gap-3">
          <Button type="submit" variant="outline" disabled={busy || !canRun}>
            {t("organizePreview")}
          </Button>
          <ConfirmApply
            label={t("organizeApply")}
            disabled={busy || !canRun || !previewCurrent || count === 0}
            count={count}
            onConfirm={onApply}
          />
        </div>
      </form>
      <ErrorLine message={error} />

      {changes !== null &&
        (changes.length === 0 ? (
          <p className="mt-3 text-sm text-muted-foreground">{t("organizeNoChanges")}</p>
        ) : (
          <ManageTable className="mt-3">
            <TableHeader>
              <TableRow>
                <TableHead>{t("title")}</TableHead>
                <TableHead>{t("organizePreview")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {changes.map((change) => (
                <TableRow key={change.mediaID}>
                  <TableCell className="manage-cell-primary">
                    {change.title || change.mediaID}
                  </TableCell>
                  <TableCell className="manage-cell-num break-all">
                    {change.oldPath} → {change.newPath}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </ManageTable>
        ))}
    </ManageSection>
  );
}

// The destructive-apply confirm gate (HARD invariant #6): every apply names the
// previewed change count and waits for an explicit confirm. Undo is still the
// recovery path, but the dialog matches the vanilla confirmation behavior.
function ConfirmApply({
  label,
  disabled,
  count,
  onConfirm,
}: {
  label: string;
  disabled: boolean;
  count: number;
  onConfirm: () => void;
}) {
  const t = useT();
  return (
    <AlertDialog>
      <AlertDialogTrigger asChild>
        <Button type="button" disabled={disabled}>
          {label}
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{label}</AlertDialogTitle>
          <AlertDialogDescription>
            {count} {t("bulkOpsAffected")}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
          <AlertDialogAction
            onClick={onConfirm}
            className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
          >
            {label}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
