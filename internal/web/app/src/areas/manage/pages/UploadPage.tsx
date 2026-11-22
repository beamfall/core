import { type DragEvent, useEffect, useRef, useState } from "react";
import {
  EmptyLine,
  ErrorLine,
  ManageSection,
  ManageTable,
  StatusBadge,
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
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { fetchLibraries } from "@/lib/graphql";
import type { LibraryInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import {
  cancelMediaUpload,
  commitMediaUploads,
  droppedRelativeDir,
  forgetMediaUpload,
  type MediaUploadCommitResult,
  type MediaUploadConflict,
  type MediaUploadParse,
  type MediaUploadProposal,
  type MediaUploadTarget,
  openMediaUpload,
  proposeMediaUploads,
  searchMediaUploadTargets,
  sendMediaUpload,
} from "../lib/mediaUpload";

// Manage → Upload (CORE-3193). Files go up in resumable chunks; the server
// then proposes an assignment for each from the library plugin's filename
// parse. The admin reviews every row, changes the item or folder, picks what
// happens on a name clash, and only then places the files.

type UploadRow = {
  key: string;
  file: File;
  relativeDir: string;
  sent: number;
  failed: boolean;
  uploadID?: string;
  proposal?: MediaUploadProposal;
  target?: MediaUploadTarget;
  destination: string;
  conflict: MediaUploadConflict;
  result?: MediaUploadCommitResult;
};

function parseLabel(parse?: MediaUploadParse): string {
  if (!parse) return "";
  let label = parse.title;
  if (parse.year) label += ` (${parse.year})`;
  if (parse.sequence) label += ` · ${parse.sequence}`;
  if (parse.index)
    label += parse.indexEnd ? ` · ${parse.index}–${parse.indexEnd}` : ` · ${parse.index}`;
  return label;
}

let rowSeq = 0;

export function UploadPage() {
  const t = useT();
  const [libraries, setLibraries] = useState<LibraryInfo[]>([]);
  const [libraryID, setLibraryID] = useState("");
  const [rows, setRows] = useState<UploadRow[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [dragging, setDragging] = useState(false);
  const input = useRef<HTMLInputElement>(null);

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

  const patch = (key: string, change: Partial<UploadRow>) =>
    setRows((current) => current.map((row) => (row.key === key ? { ...row, ...change } : row)));

  const addFiles = async (files: File[]) => {
    if (!libraryID || files.length === 0) return;
    setError("");
    const added: UploadRow[] = files.map((file) => ({
      key: `upload-${++rowSeq}`,
      file,
      relativeDir: droppedRelativeDir(file),
      sent: 0,
      failed: false,
      destination: "",
      conflict: "keep",
    }));
    setRows((current) => [...current, ...added]);
    setBusy(true);
    const sent: { key: string; uploadID: string }[] = [];
    for (const row of added) {
      try {
        const status = await openMediaUpload(libraryID, row.file, row.relativeDir);
        patch(row.key, { uploadID: status.uploadID });
        await sendMediaUpload(row.file, status, (offset) => patch(row.key, { sent: offset }));
        sent.push({ key: row.key, uploadID: status.uploadID });
      } catch {
        patch(row.key, { failed: true });
        setError(t("mediaUploadFailed", { name: row.file.name }));
      }
    }
    if (sent.length > 0) {
      try {
        const proposals: MediaUploadProposal[] = [];
        for (const batch of batches(sent.map((s) => s.uploadID))) {
          proposals.push(...(await proposeMediaUploads(libraryID, batch)));
        }
        const byID = new Map(proposals.map((p) => [p.uploadID, p]));
        setRows((current) =>
          current.map((row) => {
            const proposal = row.uploadID ? byID.get(row.uploadID) : undefined;
            if (!proposal) return row;
            return {
              ...row,
              proposal,
              target: proposal.target,
              destination: proposal.destination,
              conflict: proposal.conflict ? "fail" : "keep",
            };
          }),
        );
      } catch {
        setError(t("mediaUploadProposalsFailed"));
      }
    }
    setBusy(false);
  };

  // Removing a row that is staged but not placed discards the staged bytes
  // too, so they do not sit on the server until the session expires.
  const onRemove = (row: UploadRow) => {
    if (row.uploadID && !row.result?.path) {
      void cancelMediaUpload(libraryID, row.file, row.relativeDir, row.uploadID).catch(() => {});
    }
    setRows((current) => current.filter((r) => r.key !== row.key));
  };

  const onDrop = (event: DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    setDragging(false);
    void addFiles(Array.from(event.dataTransfer.files));
  };

  const placeable = rows.filter((row) => row.proposal && !row.result?.path && row.destination);

  const onPlace = async () => {
    setError("");
    setBusy(true);
    try {
      const results: MediaUploadCommitResult[] = [];
      const requests = placeable.map((row) => ({
        uploadID: row.proposal!.uploadID,
        destination: row.destination,
        conflict: row.conflict,
      }));
      for (const batch of batches(requests)) {
        results.push(...(await commitMediaUploads(batch)));
      }
      const byID = new Map(results.map((r) => [r.uploadID, r]));
      setRows((current) =>
        current.map((row) => {
          const result = row.uploadID ? byID.get(row.uploadID) : undefined;
          if (!result) return row;
          if (result.path) forgetMediaUpload(libraryID, row.file, row.relativeDir);
          return { ...row, result };
        }),
      );
    } catch {
      setError(t("mediaUploadPlaceFailed"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-4">
      <ManageSection>
        <div className="manage-fieldrow space-y-3">
          <div className="grid gap-1">
            <Label htmlFor="upload-library">{t("library")}</Label>
            <Select value={libraryID} onValueChange={setLibraryID} disabled={rows.length > 0}>
              <SelectTrigger id="upload-library" className="w-64">
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
          {libraries.length === 0 ? (
            <EmptyLine message={t("mediaUploadNoLibraries")} />
          ) : (
            <div
              data-testid="upload-drop"
              onDragOver={(event) => {
                event.preventDefault();
                setDragging(true);
              }}
              onDragLeave={() => setDragging(false)}
              onDrop={onDrop}
              className={`flex flex-col items-center gap-2 rounded-md border border-dashed p-6 text-sm text-muted-foreground ${
                dragging ? "border-primary bg-muted" : ""
              }`}
            >
              <span>{t("mediaUploadDrop")}</span>
              <Button
                type="button"
                size="sm"
                variant="outline"
                disabled={!libraryID}
                onClick={() => input.current?.click()}
              >
                {t("mediaUploadChoose")}
              </Button>
              <input
                ref={input}
                type="file"
                multiple
                hidden
                aria-label={t("mediaUploadChoose")}
                onChange={(event) => {
                  const files = Array.from(event.target.files ?? []);
                  event.target.value = "";
                  void addFiles(files);
                }}
              />
            </div>
          )}
          <ErrorLine message={error} />
        </div>
      </ManageSection>

      {rows.length > 0 && (
        <ManageSection
          actions={
            <Button
              type="button"
              size="sm"
              disabled={busy || placeable.length === 0}
              onClick={onPlace}
            >
              {t("mediaUploadPlace")}
            </Button>
          }
        >
          <ManageTable>
            <TableHeader>
              <TableRow>
                <TableHead>{t("mediaUploadFile")}</TableHead>
                <TableHead>{t("mediaUploadTarget")}</TableHead>
                <TableHead>{t("mediaUploadDestination")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((row) => (
                <UploadRowView
                  key={row.key}
                  row={row}
                  libraryID={libraryID}
                  onChange={(change) => patch(row.key, change)}
                  onRemove={() => onRemove(row)}
                />
              ))}
            </TableBody>
          </ManageTable>
        </ManageSection>
      )}
    </div>
  );
}

function UploadRowView({
  row,
  libraryID,
  onChange,
  onRemove,
}: {
  row: UploadRow;
  libraryID: string;
  onChange: (change: Partial<UploadRow>) => void;
  onRemove: () => void;
}) {
  const t = useT();
  const percent = row.file.size > 0 ? Math.floor((row.sent / row.file.size) * 100) : 100;
  const placed = Boolean(row.result?.path);
  const rescan = parseLabel(row.proposal?.rescan);
  return (
    <TableRow data-testid={`upload-row-${row.file.name}`}>
      <TableCell className="align-top">
        <div className="font-medium">{row.file.name}</div>
        {row.proposal?.parsed && (
          <div className="text-xs text-muted-foreground">{parseLabel(row.proposal.parsed)}</div>
        )}
        {!row.proposal && !row.failed && (
          <div className="text-xs text-muted-foreground">{t("mediaUploadSent", { percent })}</div>
        )}
        {row.failed && (
          <StatusBadge state="red">{t("mediaUploadFailed", { name: row.file.name })}</StatusBadge>
        )}
        {row.result?.error && <StatusBadge state="red">{row.result.error}</StatusBadge>}
        {placed && <StatusBadge state="green">{t("mediaUploadPlaced")}</StatusBadge>}
      </TableCell>
      <TableCell className="align-top">
        {row.proposal && (
          <TargetPicker
            libraryID={libraryID}
            target={row.target}
            disabled={placed}
            onPick={(target) =>
              onChange({ target, destination: target.destination || row.destination })
            }
          />
        )}
      </TableCell>
      <TableCell className="align-top space-y-2">
        {row.proposal && (
          <>
            <Input
              aria-label={`${t("mediaUploadDestination")} ${row.file.name}`}
              value={row.destination}
              disabled={placed}
              onChange={(event) => onChange({ destination: event.target.value })}
            />
            {rescan && row.destination === row.proposal.destination && (
              <div className="text-xs text-muted-foreground">
                {t("mediaUploadRescan", { title: rescan })}
              </div>
            )}
            {row.proposal.conflict && !placed && (
              <div className="grid gap-1">
                <span className="text-xs">{t("mediaUploadConflict")}</span>
                <Select
                  value={row.conflict}
                  onValueChange={(value) => onChange({ conflict: value as MediaUploadConflict })}
                >
                  <SelectTrigger
                    className="w-64"
                    aria-label={`${t("mediaUploadConflict")} ${row.file.name}`}
                  >
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="fail">{t("mediaUploadSkip")}</SelectItem>
                    <SelectItem value="keep">{t("mediaUploadKeep")}</SelectItem>
                    <SelectItem value="replace">{t("mediaUploadReplace")}</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            )}
          </>
        )}
        {!placed && (row.proposal || row.failed) && (
          <Button type="button" size="sm" variant="ghost" onClick={onRemove}>
            {t("mediaUploadRemove")}
          </Button>
        )}
      </TableCell>
    </TableRow>
  );
}

function TargetPicker({
  libraryID,
  target,
  disabled,
  onPick,
}: {
  libraryID: string;
  target?: MediaUploadTarget;
  disabled: boolean;
  onPick: (target: MediaUploadTarget) => void;
}) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<MediaUploadTarget[] | null>(null);
  const [error, setError] = useState("");

  const search = async () => {
    setError("");
    try {
      setResults(await searchMediaUploadTargets(libraryID, query.trim()));
    } catch {
      setError(t("mediaUploadSearchFailed"));
    }
  };

  return (
    <div className="space-y-1">
      <div className="flex items-center gap-2">
        <span>
          {target ? (
            <>
              <StatusBadge state="cyan">{target.kindLabel}</StatusBadge> {target.title}
              {target.year ? ` (${target.year})` : ""}
            </>
          ) : (
            <span className="text-muted-foreground">{t("mediaUploadNoTarget")}</span>
          )}
        </span>
        {!disabled && (
          <Button type="button" size="sm" variant="outline" onClick={() => setOpen(!open)}>
            {t("mediaUploadChange")}
          </Button>
        )}
      </div>
      {open && (
        <form
          className="space-y-1"
          onSubmit={(event) => {
            event.preventDefault();
            void search();
          }}
        >
          <Input
            aria-label={t("mediaUploadSearch")}
            placeholder={t("mediaUploadSearch")}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
          {results?.length === 0 && <EmptyLine message={t("mediaUploadNoResults")} />}
          {results?.map((result) => (
            <Button
              key={result.entityID}
              type="button"
              size="sm"
              variant="ghost"
              className="w-full justify-start"
              onClick={() => {
                onPick(result);
                setOpen(false);
              }}
            >
              {result.kindLabel}: {result.title}
              {result.year ? ` (${result.year})` : ""}
            </Button>
          ))}
          <ErrorLine message={error} />
        </form>
      )}
    </div>
  );
}

/** The server caps proposals and commits at 200 uploads per request. */
const BATCH = 200;

function batches<T>(items: T[]): T[][] {
  const out: T[][] = [];
  for (let i = 0; i < items.length; i += BATCH) out.push(items.slice(i, i + BATCH));
  return out;
}
