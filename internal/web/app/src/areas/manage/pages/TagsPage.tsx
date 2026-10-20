import { useCallback, useEffect, useMemo, useState } from "react";
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
import { Switch } from "@/components/ui/switch";
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import {
  assignTag,
  bulkAssignTags,
  createTag,
  deleteTag,
  fetchTagAssignments,
  fetchTags,
  restoreTag,
  unassignTag,
  updateTag,
} from "@/lib/graphql";
import type { TagAssignmentInfo, TagBulkMode, TagInfo, TagTargetType } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { formatDateTime } from "../lib/schedule";

// Tag manager (WEB-5D / ADR-0061): the manager surface for first-class,
// domain-agnostic Core Tags. Four surfaces — a hierarchy tree
// (create/rename/reparent/soft-delete), single assign/unassign, bulk
// add/remove/replace, and restore of soft-deleted Tags. Every mutation routes
// through the substrate's manager-gated GraphQL; the server is the authority.
// Delete is soft (HARD invariant #6 — recoverable via Restore). Labels carry no
// domain vocabulary (HARD invariant #1).

const TARGET_TYPES: TagTargetType[] = ["media", "entity", "marker"];
const BULK_MODES: TagBulkMode[] = ["add", "remove", "replace"];

// Radix Select forbids an empty-string item value, so "no parent" rides a
// sentinel that maps back to undefined at the call site.
const NO_PARENT = "__none__";

/** Order live tags as a parent-then-children tree, with depth for indent. */
function treeOrder(tags: TagInfo[]): { tag: TagInfo; depth: number }[] {
  const live = tags.filter((t) => !t.trashedAt);
  const byParent = new Map<string | undefined, TagInfo[]>();
  for (const t of live) {
    const key = t.parentTagID ?? undefined;
    const list = byParent.get(key) ?? [];
    list.push(t);
    byParent.set(key, list);
  }
  const ids = new Set(live.map((t) => t.id));
  const out: { tag: TagInfo; depth: number }[] = [];
  const visit = (parent: string | undefined, depth: number) => {
    const children = (byParent.get(parent) ?? []).sort((a, b) => a.name.localeCompare(b.name));
    for (const tag of children) {
      out.push({ tag, depth });
      visit(tag.id, Math.min(depth + 1, 2));
    }
  };
  // Roots: no parent, or a parent that no longer points at a live tag.
  visit(undefined, 0);
  for (const t of live) {
    if (t.parentTagID && !ids.has(t.parentTagID)) {
      out.push({ tag: t, depth: 0 });
      visit(t.id, 1);
    }
  }
  return out;
}

export function TagsPage() {
  const t = useT();
  const [tags, setTags] = useState<TagInfo[]>([]);
  const [showTrashed, setShowTrashed] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setError("");
    setLoading(true);
    try {
      // Always fetch including trashed so the live tree and the Deleted table
      // share one query; the live filter happens client-side in treeOrder.
      setTags(await fetchTags(true));
    } catch {
      setError(t("tagsUnavailable"));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void load();
  }, [load]);

  const liveTags = useMemo(() => tags.filter((tag) => !tag.trashedAt), [tags]);
  const trashedTags = useMemo(() => tags.filter((tag) => tag.trashedAt), [tags]);

  return (
    <div className="space-y-4">
      <ManageSection
        title={t("tagsHeading")}
        lead={t("tagsLead")}
        actions={
          <>
            <StatusBadge state="neutral">{t("tagsCount", { count: liveTags.length })}</StatusBadge>
            <Button type="button" variant="outline" size="sm" onClick={load}>
              {t("refresh")}
            </Button>
          </>
        }
      >
        <TagTree tags={liveTags} loading={loading} onChanged={load} onError={setError} />
        <ErrorLine message={error} />
      </ManageSection>

      <AssignSection tags={liveTags} onError={setError} />

      <BulkSection tags={liveTags} onError={setError} />

      <ManageSection
        title={t("tagsDeletedHeading")}
        actions={
          <div className="flex items-center gap-2">
            <Switch id="tags-show-trashed" checked={showTrashed} onCheckedChange={setShowTrashed} />
            <Label htmlFor="tags-show-trashed" className="text-sm">
              {showTrashed ? t("tagHideTrashed") : t("tagShowTrashed")}
            </Label>
          </div>
        }
      >
        {showTrashed ? (
          <TrashedTable tags={trashedTags} onChanged={load} onError={setError} />
        ) : (
          <p className="manage-empty">{t("tagShowTrashed")}</p>
        )}
      </ManageSection>
    </div>
  );
}

function TagTree({
  tags,
  loading,
  onChanged,
  onError,
}: {
  tags: TagInfo[];
  loading: boolean;
  onChanged: () => Promise<void> | void;
  onError: (message: string) => void;
}) {
  const t = useT();
  const [name, setName] = useState("");
  const [parentID, setParentID] = useState(NO_PARENT);
  const [editing, setEditing] = useState<string | null>(null);
  const [editName, setEditName] = useState("");
  const [editParent, setEditParent] = useState(NO_PARENT);
  // Rename opens the editor on the name; Move opens it on the parent (reparent)
  // — both edit the same updateTag call, so this only steers initial focus.
  const [editFocus, setEditFocus] = useState<"name" | "parent">("name");

  const rows = useMemo(() => treeOrder(tags), [tags]);

  const onCreate = async () => {
    onError("");
    const trimmed = name.trim();
    if (!trimmed) return;
    try {
      await createTag(trimmed, parentID === NO_PARENT ? undefined : parentID);
      setName("");
      setParentID(NO_PARENT);
      await onChanged();
    } catch {
      onError(t("tagsUnavailable"));
    }
  };

  const beginEdit = (tag: TagInfo, focus: "name" | "parent" = "name") => {
    setEditing(tag.id);
    setEditName(tag.name);
    setEditParent(tag.parentTagID ?? NO_PARENT);
    setEditFocus(focus);
  };

  const onSaveEdit = async (tag: TagInfo) => {
    onError("");
    const trimmed = editName.trim();
    if (!trimmed) return;
    try {
      await updateTag(tag.id, trimmed, editParent === NO_PARENT ? undefined : editParent);
      setEditing(null);
      await onChanged();
    } catch {
      onError(t("tagsUnavailable"));
    }
  };

  const onDelete = async (tag: TagInfo) => {
    onError("");
    try {
      await deleteTag(tag.id);
      await onChanged();
    } catch {
      onError(t("tagsUnavailable"));
    }
  };

  // A reparent target list excludes the tag itself (no self-parenting); deeper
  // cycle rejection is the server's job.
  const parentOptions = (exclude?: string) => tags.filter((tag) => tag.id !== exclude);

  return (
    <div className="space-y-3">
      {!loading && rows.length === 0 ? (
        <EmptyLine message={t("tagsEmpty")} />
      ) : (
        <div className="space-y-1">
          {rows.map(({ tag, depth }) =>
            editing === tag.id ? (
              <div
                key={tag.id}
                className="flex flex-wrap items-end gap-2 py-1"
                style={{ paddingLeft: depth * 18 }}
              >
                <Input
                  aria-label={t("tagRename")}
                  value={editName}
                  autoFocus={editFocus === "name"}
                  autoComplete="off"
                  spellCheck={false}
                  onChange={(e) => setEditName(e.target.value)}
                  className="w-48"
                />
                <Select value={editParent} onValueChange={setEditParent}>
                  <SelectTrigger
                    className="w-40"
                    aria-label={t("tagMove")}
                    autoFocus={editFocus === "parent"}
                  >
                    <SelectValue placeholder={t("tagNoParent")} />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value={NO_PARENT}>{t("tagNoParent")}</SelectItem>
                    {parentOptions(tag.id).map((p) => (
                      <SelectItem key={p.id} value={p.id}>
                        {p.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <Button type="button" size="sm" onClick={() => onSaveEdit(tag)}>
                  {t("tagSave")}
                </Button>
                <Button type="button" size="sm" variant="outline" onClick={() => setEditing(null)}>
                  {t("cancel")}
                </Button>
              </div>
            ) : (
              <div
                key={tag.id}
                className="flex items-center justify-between gap-2 rounded-md px-2 py-1.5 hover:bg-muted/50"
                style={{ paddingLeft: depth * 18 + 8 }}
              >
                <span className="flex min-w-0 items-center gap-2 text-sm">
                  {depth > 0 && <span className="text-muted-foreground">└</span>}
                  <span className="truncate">{tag.name}</span>
                  <span
                    className="manage-count"
                    aria-label={t("tagAssignmentCountAria", {
                      count: tag.assignmentCount ?? 0,
                    })}
                  >
                    {tag.assignmentCount ?? 0}
                  </span>
                </span>
                <span className="flex shrink-0 gap-1.5">
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    onClick={() => beginEdit(tag, "name")}
                  >
                    {t("tagRename")}
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    onClick={() => beginEdit(tag, "parent")}
                  >
                    {t("tagMove")}
                  </Button>
                  <Button type="button" size="sm" variant="ghost" onClick={() => onDelete(tag)}>
                    {t("tagDelete")}
                  </Button>
                </span>
              </div>
            ),
          )}
        </div>
      )}

      <div className="flex flex-wrap items-end gap-3 border-t pt-3">
        <div className="grow space-y-1">
          <Label htmlFor="tag-new-name">{t("tagNewLabel")}</Label>
          <Input
            id="tag-new-name"
            value={name}
            autoComplete="off"
            spellCheck={false}
            placeholder={t("tagNamePlaceholder")}
            onChange={(e) => setName(e.target.value)}
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor="tag-new-parent">{t("tagParentLabel")}</Label>
          <Select value={parentID} onValueChange={setParentID}>
            <SelectTrigger id="tag-new-parent" className="w-44">
              <SelectValue placeholder={t("tagNoParent")} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={NO_PARENT}>{t("tagNoParent")}</SelectItem>
              {parentOptions().map((p) => (
                <SelectItem key={p.id} value={p.id}>
                  {p.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <Button type="button" onClick={onCreate} disabled={!name.trim()}>
          {t("tagCreate")}
        </Button>
      </div>
    </div>
  );
}

function targetTypeLabel(t: ReturnType<typeof useT>, type: TagTargetType) {
  if (type === "media") return t("tagTargetMedia");
  if (type === "entity") return t("tagTargetEntity");
  return t("tagTargetMarker");
}

function AssignSection({ tags, onError }: { tags: TagInfo[]; onError: (message: string) => void }) {
  const t = useT();
  const [targetType, setTargetType] = useState<TagTargetType>("media");
  const [targetID, setTargetID] = useState("");
  const [assignment, setAssignment] = useState<TagAssignmentInfo | null>(null);
  const [addTagID, setAddTagID] = useState("");

  const onLoad = async () => {
    onError("");
    const id = targetID.trim();
    if (!id) return;
    try {
      setAssignment(await fetchTagAssignments(targetType, id));
    } catch {
      onError(t("tagsUnavailable"));
    }
  };

  const onAssign = async () => {
    onError("");
    const id = targetID.trim();
    if (!id || !addTagID) return;
    try {
      setAssignment(await assignTag(addTagID, targetType, id));
      setAddTagID("");
    } catch {
      onError(t("tagsUnavailable"));
    }
  };

  const onUnassign = async (tagID: string) => {
    onError("");
    const id = targetID.trim();
    if (!id) return;
    try {
      setAssignment(await unassignTag(tagID, targetType, id));
    } catch {
      onError(t("tagsUnavailable"));
    }
  };

  const assigned = assignment?.tags ?? [];
  const assignedIDs = new Set(assigned.map((tag) => tag.id));
  const available = tags.filter((tag) => !assignedIDs.has(tag.id));

  return (
    <ManageSection
      title={t("tagAssignHeading")}
      lead={t("tagAssignLead")}
      actions={
        assignment ? (
          <StatusBadge state="green">
            {assigned.length} {t("tagBulkAffected")}
          </StatusBadge>
        ) : undefined
      }
    >
      <div className="space-y-3">
        <div className="flex flex-wrap items-end gap-3">
          <div className="space-y-1">
            <Label htmlFor="assign-target-type">{t("tagTargetType")}</Label>
            <Select value={targetType} onValueChange={(v) => setTargetType(v as TagTargetType)}>
              <SelectTrigger id="assign-target-type" className="w-40">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {TARGET_TYPES.map((type) => (
                  <SelectItem key={type} value={type}>
                    {targetTypeLabel(t, type)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="grow space-y-1">
            <Label htmlFor="assign-target-id">{t("tagTargetID")}</Label>
            <Input
              id="assign-target-id"
              value={targetID}
              autoComplete="off"
              spellCheck={false}
              onChange={(e) => setTargetID(e.target.value)}
            />
          </div>
          <Button type="button" variant="outline" onClick={onLoad} disabled={!targetID.trim()}>
            {t("tagLoad")}
          </Button>
        </div>

        {assignment &&
          (assigned.length === 0 ? (
            <p className="text-sm text-muted-foreground">{t("tagNoneAssigned")}</p>
          ) : (
            <div className="flex flex-wrap gap-2">
              {assigned.map((tag) => (
                <span
                  key={tag.id}
                  className="inline-flex items-center gap-1.5 rounded-full border bg-muted/50 py-1 pl-3 pr-1.5 text-sm"
                >
                  {tag.name}
                  <button
                    type="button"
                    aria-label={`${t("tagUnassign")} ${tag.name}`}
                    className="text-muted-foreground hover:text-destructive"
                    onClick={() => onUnassign(tag.id)}
                  >
                    ×
                  </button>
                </span>
              ))}
            </div>
          ))}

        {assignment && (
          <div className="flex flex-wrap items-end gap-3">
            <div className="grow space-y-1">
              <Label htmlFor="assign-add-tag">{t("tagAssign")}</Label>
              <Select value={addTagID} onValueChange={setAddTagID}>
                <SelectTrigger id="assign-add-tag">
                  <SelectValue placeholder={t("tagChooseTag")} />
                </SelectTrigger>
                <SelectContent>
                  {available.map((tag) => (
                    <SelectItem key={tag.id} value={tag.id}>
                      {tag.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <Button type="button" onClick={onAssign} disabled={!addTagID}>
              {t("tagAssign")}
            </Button>
          </div>
        )}
      </div>
    </ManageSection>
  );
}

function BulkSection({ tags, onError }: { tags: TagInfo[]; onError: (message: string) => void }) {
  const t = useT();
  const [mode, setMode] = useState<TagBulkMode>("add");
  const [targetType, setTargetType] = useState<TagTargetType>("media");
  const [targetIDs, setTargetIDs] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [affected, setAffected] = useState<number | null>(null);

  const idList = () =>
    targetIDs
      .split(",")
      .map((id) => id.trim())
      .filter(Boolean);

  const toggle = (id: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const modeLabel = (m: TagBulkMode) =>
    m === "add"
      ? t("tagBulkModeAdd")
      : m === "remove"
        ? t("tagBulkModeRemove")
        : t("tagBulkModeReplace");

  const onApply = async () => {
    onError("");
    const ids = idList();
    const tagIDs = [...selected];
    if (ids.length === 0 || tagIDs.length === 0) return;
    try {
      const result = await bulkAssignTags(tagIDs, targetType, ids, mode);
      setAffected(result.affected);
    } catch {
      onError(t("tagsUnavailable"));
    }
  };

  const canApply = idList().length > 0 && selected.size > 0;
  // Tags not yet in the selection — the add-picker offers only these.
  const available = tags.filter((tag) => !selected.has(tag.id));

  return (
    <ManageSection
      title={t("tagBulkHeading")}
      lead={t("tagBulkLead")}
      actions={
        <div
          className="inline-flex overflow-hidden rounded-md border"
          role="group"
          aria-label={t("tagBulkHeading")}
        >
          {BULK_MODES.map((m) => (
            <button
              key={m}
              type="button"
              aria-pressed={mode === m}
              onClick={() => setMode(m)}
              className={
                "px-3 py-1.5 text-xs font-medium " +
                (mode === m
                  ? "bg-primary text-primary-foreground"
                  : "bg-muted/50 text-muted-foreground")
              }
            >
              {modeLabel(m)}
            </button>
          ))}
        </div>
      }
    >
      <div className="space-y-3">
        <div className="flex flex-wrap items-end gap-3">
          <div className="space-y-1">
            <Label htmlFor="bulk-target-type">{t("tagTargetType")}</Label>
            <Select value={targetType} onValueChange={(v) => setTargetType(v as TagTargetType)}>
              <SelectTrigger id="bulk-target-type" className="w-40">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {TARGET_TYPES.map((type) => (
                  <SelectItem key={type} value={type}>
                    {targetTypeLabel(t, type)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="grow space-y-1">
            <Label htmlFor="bulk-target-ids">{t("tagBulkTargetIDs")}</Label>
            <Input
              id="bulk-target-ids"
              value={targetIDs}
              autoComplete="off"
              spellCheck={false}
              onChange={(e) => setTargetIDs(e.target.value)}
            />
          </div>
        </div>

        <div className="space-y-2">
          <Label htmlFor="bulk-add-tag">{t("tagsHeading")}</Label>
          <div className="flex flex-wrap items-center gap-2">
            {[...selected].map((id) => {
              const tag = tags.find((tg) => tg.id === id);
              if (!tag) return null;
              return (
                <span
                  key={id}
                  className="inline-flex items-center gap-1.5 rounded-full border bg-muted/50 py-1 pl-3 pr-1.5 text-sm"
                >
                  {tag.name}
                  <button
                    type="button"
                    aria-label={`${t("tagUnassign")} ${tag.name}`}
                    className="text-muted-foreground hover:text-destructive"
                    onClick={() => toggle(id)}
                  >
                    ×
                  </button>
                </span>
              );
            })}
            <Select value="" onValueChange={toggle}>
              <SelectTrigger id="bulk-add-tag" className="w-44">
                <SelectValue placeholder={t("tagChooseTag")} />
              </SelectTrigger>
              <SelectContent>
                {available.map((tag) => (
                  <SelectItem key={tag.id} value={tag.id}>
                    {tag.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>

        <div className="flex items-center gap-3">
          <Button type="button" onClick={onApply} disabled={!canApply}>
            {t("tagBulkApply")}
          </Button>
          {affected !== null && (
            <StatusBadge state="green">
              {t("tagBulkApplied")} · {affected} {t("tagBulkAffected")}
            </StatusBadge>
          )}
        </div>
      </div>
    </ManageSection>
  );
}

function TrashedTable({
  tags,
  onChanged,
  onError,
}: {
  tags: TagInfo[];
  onChanged: () => Promise<void> | void;
  onError: (message: string) => void;
}) {
  const t = useT();

  const onRestore = async (tag: TagInfo) => {
    onError("");
    try {
      await restoreTag(tag.id);
      await onChanged();
    } catch {
      onError(t("tagsUnavailable"));
    }
  };

  if (tags.length === 0) {
    return <EmptyLine message={t("tagNoneDeleted")} />;
  }

  return (
    <ManageTable>
      <TableHeader>
        <TableRow>
          <TableHead>{t("tagsHeading")}</TableHead>
          <TableHead>{t("tagDeletedAt")}</TableHead>
          <TableHead className="text-right">{t("tagRestore")}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {tags.map((tag) => (
          <TableRow key={tag.id}>
            <TableCell className="manage-cell-primary">{tag.name}</TableCell>
            <TableCell className="manage-cell-num">
              {tag.trashedAt ? formatDateTime(tag.trashedAt) : ""}
            </TableCell>
            <TableCell className="text-right">
              <Button type="button" variant="outline" size="sm" onClick={() => onRestore(tag)}>
                {t("tagRestore")}
              </Button>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </ManageTable>
  );
}
