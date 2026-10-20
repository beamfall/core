import { useCallback, useEffect, useMemo, useState } from "react";
import {
  EmptyLine,
  ErrorLine,
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
import { Checkbox } from "@/components/ui/checkbox";
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
  createMarker,
  deleteMarker,
  fetchMarkers,
  fetchTrashedMarkers,
  restoreMarker,
  updateMarker,
} from "@/lib/graphql";
import type { MarkerInfo, MarkerInput } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { formatDateTime } from "../lib/schedule";

// Marker editor (WEB-5E / ADR-0033 / ADR-0048 / ADR-0061): the manager surface
// for content-level Markers on one Media item. Load by Media ID, edit each
// Marker's start/end seconds, title, skip-segment kind, and tags
// (createMarker / updateMarker), reorder by start time, bulk-retag or
// bulk-delete the selection, and restore soft-deleted Markers. Delete is soft
// (deleteMarker → trash, HARD invariant #6); restore brings a row back live
// with its marker_tag rows intact. The kind vocabulary is closed and domain-
// agnostic (ADR-0048 — "" / intro / recap / credits); the server is the
// authority on every mutation, this surface is only an affordance.

// The closed skip-segment vocabulary (ADR-0048 / store.SegmentKinds). "" is a
// plain chapter/point Marker. Clients ship the vocabulary compiled in; the
// server CHECK rejects anything else.
const SEGMENT_KINDS = ["intro", "recap", "credits"] as const;

// A blank draft for a new Marker on the loaded Media item.
function blankDraft(): MarkerInput {
  return { title: "", startSeconds: 0, endSeconds: null, segmentKind: "", tags: [] };
}

function toDraft(marker: MarkerInfo): MarkerInput {
  return {
    title: marker.title ?? "",
    startSeconds: marker.startSeconds,
    endSeconds: marker.endSeconds ?? null,
    segmentKind: marker.segmentKind ?? "",
    tags: marker.tags ?? [],
  };
}

// Sort live Markers by start time so reorder reads top-to-bottom in timeline
// order; ties fall back to id for a stable order.
function byStart(a: MarkerInfo, b: MarkerInfo): number {
  if (a.startSeconds !== b.startSeconds) return a.startSeconds - b.startSeconds;
  return a.id.localeCompare(b.id);
}

export function MarkersPage() {
  const t = useT();
  const [mediaInput, setMediaInput] = useState("");
  const [mediaID, setMediaID] = useState("");
  const [markers, setMarkers] = useState<MarkerInfo[]>([]);
  const [trashed, setTrashed] = useState<MarkerInfo[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState("");

  const load = useCallback(
    async (id: string) => {
      setError("");
      const target = id.trim();
      if (!target) return;
      try {
        const [live, dead] = await Promise.all([fetchMarkers(target), fetchTrashedMarkers(target)]);
        setMediaID(target);
        setMarkers([...live].sort(byStart));
        setTrashed(dead);
        setSelected(new Set());
        setLoaded(true);
      } catch {
        setError(t("markersUnavailable"));
      }
    },
    [t],
  );

  const onLoad = () => void load(mediaInput);
  const reload = useCallback(() => void load(mediaID), [load, mediaID]);

  const onCreate = async () => {
    if (!mediaID) return;
    setError("");
    try {
      await createMarker(mediaID, blankDraft());
      await load(mediaID);
    } catch {
      setError(t("markersSaveFailed"));
    }
  };

  const onSaved = useCallback(() => void load(mediaID), [load, mediaID]);

  const toggleSelected = (id: string, on: boolean) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (on) next.add(id);
      else next.delete(id);
      return next;
    });
  };

  const selectedMarkers = useMemo(
    () => markers.filter((m) => selected.has(m.id)),
    [markers, selected],
  );

  return (
    <div className="space-y-4">
      <ManageSection
        title={t("markersHeading")}
        lead={t("markersLead")}
        actions={
          loaded ? (
            <StatusBadge state="neutral">
              {t("markersCount", { count: markers.length })}
            </StatusBadge>
          ) : undefined
        }
      >
        <div className="flex flex-wrap items-end gap-3">
          <div className="grow space-y-1">
            <Label htmlFor="markers-media-id">{t("markersMediaID")}</Label>
            <Input
              id="markers-media-id"
              value={mediaInput}
              autoComplete="off"
              spellCheck={false}
              onChange={(e) => setMediaInput(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") onLoad();
              }}
            />
          </div>
          <Button type="button" variant="outline" onClick={onLoad}>
            {t("markersLoad")}
          </Button>
          <Button type="button" onClick={onCreate} disabled={!mediaID}>
            {t("markersNew")}
          </Button>
        </div>
        <ErrorLine message={error} />

        {loaded &&
          (markers.length === 0 ? (
            <EmptyLine message={t("markersEmpty")} />
          ) : (
            <div className="mt-3 space-y-2">
              {markers.map((marker, i) => (
                <MarkerRow
                  key={marker.id}
                  marker={marker}
                  selected={selected.has(marker.id)}
                  canMoveUp={i > 0}
                  canMoveDown={i < markers.length - 1}
                  neighborUp={markers[i - 1]}
                  neighborDown={markers[i + 1]}
                  onToggleSelected={(on) => toggleSelected(marker.id, on)}
                  onSaved={onSaved}
                  onError={setError}
                />
              ))}
            </div>
          ))}
      </ManageSection>

      {loaded && <BulkSection selected={selectedMarkers} onError={setError} onApplied={reload} />}

      {loaded && <TrashedSection markers={trashed} onError={setError} onRestored={reload} />}
    </div>
  );
}

// One editable Marker. Holds its own draft so edits stay local until Save; the
// reorder buttons swap the start seconds with the timeline neighbor and persist
// both (the list re-sorts by start on reload). Delete is soft and confirmed.
function MarkerRow({
  marker,
  selected,
  canMoveUp,
  canMoveDown,
  neighborUp,
  neighborDown,
  onToggleSelected,
  onSaved,
  onError,
}: {
  marker: MarkerInfo;
  selected: boolean;
  canMoveUp: boolean;
  canMoveDown: boolean;
  neighborUp?: MarkerInfo;
  neighborDown?: MarkerInfo;
  onToggleSelected: (on: boolean) => void;
  onSaved: () => void;
  onError: (message: string) => void;
}) {
  const t = useT();
  const [draft, setDraft] = useState<MarkerInput>(() => toDraft(marker));
  const [tagInput, setTagInput] = useState("");

  // A reload after a swap hands this row new seconds; the draft must follow or
  // the next Save would write the pre-swap values back.
  useEffect(() => {
    setDraft(toDraft(marker));
  }, [marker]);

  const set = (patch: Partial<MarkerInput>) => setDraft((d) => ({ ...d, ...patch }));

  const addTag = () => {
    const tag = tagInput.trim();
    if (!tag) return;
    const tags = draft.tags ?? [];
    if (!tags.includes(tag)) set({ tags: [...tags, tag] });
    setTagInput("");
  };

  const removeTag = (tag: string) => set({ tags: (draft.tags ?? []).filter((x) => x !== tag) });

  const onSave = async () => {
    onError("");
    try {
      await updateMarker(marker.id, draft);
      onSaved();
    } catch {
      onError(t("markersSaveFailed"));
    }
  };

  // Reorder: swap start seconds with the timeline neighbor and persist both, so
  // the order is durable (markers re-sort by start on reload).
  const swapWith = async (neighbor: MarkerInfo) => {
    onError("");
    try {
      await Promise.all([
        updateMarker(marker.id, { ...draft, startSeconds: neighbor.startSeconds }),
        updateMarker(neighbor.id, {
          ...toDraft(neighbor),
          startSeconds: marker.startSeconds,
        }),
      ]);
      onSaved();
    } catch {
      onError(t("markersSaveFailed"));
    }
  };

  const onDelete = async () => {
    onError("");
    try {
      await deleteMarker(marker.id);
      onSaved();
    } catch {
      onError(t("markersDeleteFailed"));
    }
  };

  const kindLabel = marker.segmentKind || t("markersChapter");

  return (
    <div className={`manage-section rounded-lg border p-3 ${selected ? "border-primary/60" : ""}`}>
      <div className="flex items-center gap-2">
        <Checkbox
          checked={selected}
          onCheckedChange={(v) => onToggleSelected(v === true)}
          aria-label={`${t("markersHeading")} ${marker.title || marker.id}`}
        />
        <span className="grow truncate text-sm font-semibold">{marker.title || marker.id}</span>
        <StatusBadge state={marker.segmentKind ? "cyan" : "neutral"}>{kindLabel}</StatusBadge>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          disabled={!canMoveUp}
          aria-label={t("markersMoveEarlier")}
          onClick={() => neighborUp && swapWith(neighborUp)}
        >
          ↑
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          disabled={!canMoveDown}
          aria-label={t("markersMoveLater")}
          onClick={() => neighborDown && swapWith(neighborDown)}
        >
          ↓
        </Button>
      </div>

      <div className="mt-3 grid grid-cols-3 gap-3">
        <div className="space-y-1">
          <Label htmlFor={`start-${marker.id}`}>{t("markersStart")}</Label>
          <Input
            id={`start-${marker.id}`}
            type="number"
            value={draft.startSeconds}
            onChange={(e) => set({ startSeconds: Number(e.target.value) })}
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor={`end-${marker.id}`}>{t("markersEnd")}</Label>
          <Input
            id={`end-${marker.id}`}
            type="number"
            value={draft.endSeconds ?? ""}
            onChange={(e) =>
              set({
                endSeconds: e.target.value === "" ? null : Number(e.target.value),
              })
            }
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor={`kind-${marker.id}`}>{t("markersKind")}</Label>
          <Select
            value={draft.segmentKind || "__none__"}
            onValueChange={(v) => set({ segmentKind: v === "__none__" ? "" : v })}
          >
            <SelectTrigger id={`kind-${marker.id}`}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="__none__">{t("markersKindNone")}</SelectItem>
              {SEGMENT_KINDS.map((kind) => (
                <SelectItem key={kind} value={kind}>
                  {kind}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </div>

      <div className="mt-3 space-y-1">
        <Label htmlFor={`title-${marker.id}`}>{t("title")}</Label>
        <Input
          id={`title-${marker.id}`}
          value={draft.title ?? ""}
          onChange={(e) => set({ title: e.target.value })}
        />
      </div>

      <div className="mt-3 space-y-1">
        <Label>{t("markersTags")}</Label>
        <div className="flex flex-wrap items-center gap-2">
          {(draft.tags ?? []).map((tag) => (
            <span
              key={tag}
              className="inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs"
            >
              {tag}
              <button
                type="button"
                aria-label={`${t("markersDelete")} ${tag}`}
                className="text-muted-foreground hover:text-destructive"
                onClick={() => removeTag(tag)}
              >
                ×
              </button>
            </span>
          ))}
          <Input
            className="w-32"
            value={tagInput}
            placeholder={t("markersAddTag")}
            autoComplete="off"
            spellCheck={false}
            onChange={(e) => setTagInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                addTag();
              }
            }}
          />
        </div>
      </div>

      <div className="mt-3 flex items-center gap-2">
        <span className="grow" />
        <ConfirmDelete onConfirm={onDelete} />
        <Button type="button" size="sm" onClick={onSave}>
          {t("save")}
        </Button>
      </div>
    </div>
  );
}

// The destructive-delete confirm gate (HARD invariant #6). Delete is soft, but
// the confirm matches the manage register's destructive pattern; restore is the
// recovery path from the Deleted markers panel.
function ConfirmDelete({ onConfirm }: { onConfirm: () => void }) {
  const t = useT();
  return (
    <AlertDialog>
      <AlertDialogTrigger asChild>
        <Button type="button" variant="outline" size="sm" className="text-destructive">
          {t("markersDelete")}
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t("markersDelete")}</AlertDialogTitle>
          <AlertDialogDescription>{t("markersBulkDeleteLead")}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
          <AlertDialogAction
            onClick={onConfirm}
            className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
          >
            {t("markersDelete")}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

// Bulk retag (add or replace) + bulk soft-delete over the current selection.
// Each marker is updated/deleted server-side; the parent reloads after apply so
// the list reflects the authoritative state.
function BulkSection({
  selected,
  onError,
  onApplied,
}: {
  selected: MarkerInfo[];
  onError: (message: string) => void;
  onApplied: () => void;
}) {
  const t = useT();
  const [mode, setMode] = useState<"add" | "replace">("add");
  const [tags, setTags] = useState<string[]>([]);
  const [tagInput, setTagInput] = useState("");
  const [applied, setApplied] = useState(0);

  const count = selected.length;
  const canRun = count > 0 && tags.length > 0;

  const addTag = () => {
    const tag = tagInput.trim();
    if (tag && !tags.includes(tag)) setTags([...tags, tag]);
    setTagInput("");
  };

  const onRetag = async () => {
    onError("");
    if (!canRun) return;
    try {
      await Promise.all(
        selected.map((m) => {
          const draft = toDraft(m);
          const merged = mode === "replace" ? tags : [...new Set([...(draft.tags ?? []), ...tags])];
          return updateMarker(m.id, { ...draft, tags: merged });
        }),
      );
      setApplied(count);
      onApplied();
    } catch {
      onError(t("markersSaveFailed"));
    }
  };

  const onBulkDelete = async () => {
    onError("");
    if (count === 0) return;
    try {
      await Promise.all(selected.map((m) => deleteMarker(m.id)));
      onApplied();
    } catch {
      onError(t("markersDeleteFailed"));
    }
  };

  return (
    <ManageSection
      title={t("markersBulkHeading")}
      lead={t("markersBulkLead")}
      actions={
        <StatusBadge state={count > 0 ? "green" : "neutral"}>
          {t("markersSelectedCount", { count })}
        </StatusBadge>
      }
    >
      <div className="space-y-3">
        <div className="space-y-1">
          <Label>{t("markersRetagMode")}</Label>
          <div className="inline-flex overflow-hidden rounded-md border">
            <button
              type="button"
              aria-pressed={mode === "add"}
              className={`px-3 py-1.5 text-sm ${mode === "add" ? "bg-primary text-primary-foreground" : "text-muted-foreground"}`}
              onClick={() => setMode("add")}
            >
              {t("markersRetagAdd")}
            </button>
            <button
              type="button"
              aria-pressed={mode === "replace"}
              className={`border-l px-3 py-1.5 text-sm ${mode === "replace" ? "bg-primary text-primary-foreground" : "text-muted-foreground"}`}
              onClick={() => setMode("replace")}
            >
              {t("markersRetagReplace")}
            </button>
          </div>
        </div>

        <div className="space-y-1">
          <Label>{t("markersTags")}</Label>
          <div className="flex flex-wrap items-center gap-2">
            {tags.map((tag) => (
              <span
                key={tag}
                className="inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs"
              >
                {tag}
                <button
                  type="button"
                  aria-label={`${t("markersDelete")} ${tag}`}
                  className="text-muted-foreground hover:text-destructive"
                  onClick={() => setTags(tags.filter((x) => x !== tag))}
                >
                  ×
                </button>
              </span>
            ))}
            <Input
              className="w-32"
              value={tagInput}
              placeholder={t("markersAddTag")}
              autoComplete="off"
              spellCheck={false}
              onChange={(e) => setTagInput(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  addTag();
                }
              }}
            />
          </div>
        </div>

        <div className="flex items-center gap-3">
          <p className="text-sm text-muted-foreground">{t("markersRetag", { count })}</p>
          <Button type="button" className="ml-auto" disabled={!canRun} onClick={onRetag}>
            {t("markersRetag", { count })}
          </Button>
        </div>

        {applied > 0 && (
          <StatusBadge state="green">{t("markersRetagDone", { count: applied })}</StatusBadge>
        )}

        <div className="flex items-center gap-3 border-t pt-3">
          <p className="text-sm text-muted-foreground">{t("markersBulkDeleteLead")}</p>
          <BulkDeleteConfirm count={count} disabled={count === 0} onConfirm={onBulkDelete} />
        </div>
      </div>
    </ManageSection>
  );
}

function BulkDeleteConfirm({
  count,
  disabled,
  onConfirm,
}: {
  count: number;
  disabled: boolean;
  onConfirm: () => void;
}) {
  const t = useT();
  return (
    <AlertDialog>
      <AlertDialogTrigger asChild>
        <Button
          type="button"
          variant="outline"
          className="ml-auto text-destructive"
          disabled={disabled}
        >
          {t("markersBulkDelete", { count })}
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t("markersBulkDelete", { count })}</AlertDialogTitle>
          <AlertDialogDescription>{t("markersBulkDeleteLead")}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
          <AlertDialogAction
            onClick={onConfirm}
            className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
          >
            {t("markersBulkDelete", { count })}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

// Soft-deleted Markers for this Media item, restorable one row at a time
// (HARD invariant #6). restoreMarker brings a row back live with its marker_tag
// rows intact; the parent reloads so it rejoins the editor list.
function TrashedSection({
  markers,
  onError,
  onRestored,
}: {
  markers: MarkerInfo[];
  onError: (message: string) => void;
  onRestored: () => void;
}) {
  const t = useT();

  const onRestore = async (marker: MarkerInfo) => {
    onError("");
    try {
      await restoreMarker(marker.id);
      onRestored();
    } catch {
      onError(t("markersRestoreFailed"));
    }
  };

  return (
    <ManageSection
      title={t("markersTrashedHeading")}
      actions={
        <StatusBadge state="neutral">
          {t("markersTrashedCount", { count: markers.length })}
        </StatusBadge>
      }
    >
      {markers.length === 0 ? (
        <EmptyLine message={t("markersTrashedEmpty")} />
      ) : (
        <ManageTable>
          <TableHeader>
            <TableRow>
              <TableHead>{t("title")}</TableHead>
              <TableHead>{t("markersTrashedStart")}</TableHead>
              <TableHead>{t("markersTrashedDeleted")}</TableHead>
              <TableHead className="text-right">{t("restore")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {markers.map((marker) => (
              <TableRow key={marker.id}>
                <TableCell className="manage-cell-primary">{marker.title || marker.id}</TableCell>
                <TableCell className="manage-cell-num">{marker.startSeconds}s</TableCell>
                <TableCell className="manage-cell-num">
                  {marker.trashedAt ? formatDateTime(marker.trashedAt) : "—"}
                </TableCell>
                <TableCell className="text-right">
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => onRestore(marker)}
                  >
                    {t("restore")}
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </ManageTable>
      )}
    </ManageSection>
  );
}
