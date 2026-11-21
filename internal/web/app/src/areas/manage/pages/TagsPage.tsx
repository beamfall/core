import { CornerDownRight, FolderInput, Pencil, Trash2, X } from "lucide-react";
import { type FormEvent, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Segmented } from "@/areas/console/Segmented";
import {
  EmptyLine,
  ErrorLine,
  ManageField,
  ManageInlineForm,
  ManageSection,
  ManageTable,
  StatusBadge,
  TableSkeletonRows,
} from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
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
// domain-agnostic Core Tags. Four surfaces — a hierarchy table
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

// Indent per tree level, in px, on top of the cell's own padding.
const INDENT = 20;

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

const submit = (action: () => void) => (event: FormEvent) => {
  event.preventDefault();
  action();
};

export function TagsPage() {
  const t = useT();
  const [tags, setTags] = useState<TagInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setError("");
    setLoading(true);
    try {
      // Always fetch including trashed so the live table and the Deleted table
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
        actions={
          <>
            <span className="manage-count">{t("tagsCount", { count: liveTags.length })}</span>
            <Button type="button" variant="outline" size="sm" onClick={load}>
              {t("refresh")}
            </Button>
          </>
        }
      >
        <TagTable tags={liveTags} loading={loading} onChanged={load} onError={setError} />
        <ErrorLine message={error} />
        <CreateTagForm tags={liveTags} onChanged={load} onError={setError} />
      </ManageSection>

      <AssignSection tags={liveTags} />

      <BulkSection tags={liveTags} />

      <ManageSection
        title={t("tagsDeletedHeading")}
        lead={t("tagsCount", { count: trashedTags.length })}
        collapsible
      >
        <TrashedTable tags={trashedTags} onChanged={load} onError={setError} />
      </ManageSection>
    </div>
  );
}

/** A removable tag pill: the shared chip for assigned and bulk-selected tags. */
function TagChip({ name, onRemove }: { name: string; onRemove: () => void }) {
  const t = useT();
  return (
    <span className="inline-flex max-w-full items-center gap-1 rounded-full border bg-muted/50 py-0.5 pl-3 pr-1 text-sm">
      <span className="truncate" title={name}>
        {name}
      </span>
      <Button
        type="button"
        variant="ghost"
        size="icon"
        aria-label={`${t("tagUnassign")} ${name}`}
        className="size-6 shrink-0 rounded-full text-muted-foreground hover:text-destructive"
        onClick={onRemove}
      >
        <X aria-hidden />
      </Button>
    </span>
  );
}

function TagTable({
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
  const [editing, setEditing] = useState<string | null>(null);
  const [editName, setEditName] = useState("");
  const [editParent, setEditParent] = useState(NO_PARENT);
  // Rename opens the editor on the name; Move opens it on the parent (reparent)
  // — both edit the same updateTag call, so this only steers initial focus.
  const [editFocus, setEditFocus] = useState<"name" | "parent">("name");

  const rows = useMemo(() => treeOrder(tags), [tags]);

  const beginEdit = (tag: TagInfo, focus: "name" | "parent") => {
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

  if (!loading && rows.length === 0) {
    return <EmptyLine message={t("tagsEmpty")} />;
  }

  return (
    <ManageTable>
      <TableHeader>
        <TableRow>
          <TableHead>{t("tagsHeading")}</TableHead>
          <TableHead className="w-24 text-right">{t("tagUsesHeading")}</TableHead>
          <TableHead className="w-32 text-right">
            <span className="sr-only">{t("tagActionsHeading")}</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {loading && rows.length === 0 ? (
          <TableSkeletonRows columns={3} rows={3} />
        ) : (
          rows.map(({ tag, depth }) =>
            editing === tag.id ? (
              <TableRow key={tag.id} data-selected>
                <TableCell colSpan={3}>
                  <form
                    className="flex flex-wrap items-center gap-2"
                    style={{ paddingLeft: depth * INDENT }}
                    onSubmit={submit(() => void onSaveEdit(tag))}
                    onKeyDown={(e) => {
                      if (e.key === "Escape") setEditing(null);
                    }}
                  >
                    <Input
                      aria-label={t("tagRename")}
                      value={editName}
                      autoFocus={editFocus === "name"}
                      autoComplete="off"
                      spellCheck={false}
                      onChange={(e) => setEditName(e.target.value)}
                      className="h-8 min-w-0 flex-1 basis-48"
                    />
                    <Select value={editParent} onValueChange={setEditParent}>
                      <SelectTrigger
                        className="h-8 w-44"
                        aria-label={t("tagMove")}
                        autoFocus={editFocus === "parent"}
                      >
                        <SelectValue placeholder={t("tagNoParent")} />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value={NO_PARENT}>{t("tagNoParent")}</SelectItem>
                        {tags
                          .filter((p) => p.id !== tag.id)
                          .map((p) => (
                            <SelectItem key={p.id} value={p.id}>
                              {p.name}
                            </SelectItem>
                          ))}
                      </SelectContent>
                    </Select>
                    <span className="ml-auto flex gap-2">
                      <Button type="submit" size="sm" disabled={!editName.trim()}>
                        {t("tagSave")}
                      </Button>
                      <Button
                        type="button"
                        size="sm"
                        variant="outline"
                        onClick={() => setEditing(null)}
                      >
                        {t("cancel")}
                      </Button>
                    </span>
                  </form>
                </TableCell>
              </TableRow>
            ) : (
              <TableRow key={tag.id}>
                <TableCell className="manage-cell-primary max-w-0">
                  <span
                    className="flex min-w-0 items-center gap-1.5"
                    style={{ paddingLeft: depth > 0 ? (depth - 1) * INDENT : 0 }}
                  >
                    {depth > 0 && (
                      <CornerDownRight
                        aria-hidden
                        className="size-3.5 shrink-0 text-muted-foreground"
                      />
                    )}
                    <span className="truncate" title={tag.name}>
                      {tag.name}
                    </span>
                  </span>
                </TableCell>
                <TableCell
                  className="manage-cell-num text-right"
                  aria-label={t("tagAssignmentCountAria", { count: tag.assignmentCount ?? 0 })}
                >
                  {tag.assignmentCount ?? 0}
                </TableCell>
                <TableCell className="text-right">
                  <span className="inline-flex gap-0.5">
                    <RowAction
                      label={t("tagRename")}
                      icon={Pencil}
                      onClick={() => beginEdit(tag, "name")}
                    />
                    <RowAction
                      label={t("tagMove")}
                      icon={FolderInput}
                      onClick={() => beginEdit(tag, "parent")}
                    />
                    <RowAction
                      label={t("tagDelete")}
                      icon={Trash2}
                      danger
                      onClick={() => void onDelete(tag)}
                    />
                  </span>
                </TableCell>
              </TableRow>
            ),
          )
        )}
      </TableBody>
    </ManageTable>
  );
}

function RowAction({
  label,
  icon: Icon,
  danger = false,
  onClick,
}: {
  label: string;
  icon: typeof Pencil;
  danger?: boolean;
  onClick: () => void;
}) {
  return (
    <Button
      type="button"
      variant="ghost"
      size="icon-sm"
      aria-label={label}
      title={label}
      className={danger ? "text-muted-foreground hover:text-destructive" : "text-muted-foreground"}
      onClick={onClick}
    >
      <Icon aria-hidden />
    </Button>
  );
}

function CreateTagForm({
  tags,
  onChanged,
  onError,
}: {
  tags: TagInfo[];
  onChanged: () => Promise<void> | void;
  onError: (message: string) => void;
}) {
  const t = useT();
  const [name, setName] = useState("");
  const [parentID, setParentID] = useState(NO_PARENT);

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

  return (
    <form className="manage-add-row mt-3 pt-3" onSubmit={submit(() => void onCreate())}>
      <ManageInlineForm>
        <ManageField id="tag-new-name" label={t("tagNewLabel")} className="min-w-48 flex-1">
          <Input
            id="tag-new-name"
            value={name}
            autoComplete="off"
            spellCheck={false}
            placeholder={t("tagNamePlaceholder")}
            onChange={(e) => setName(e.target.value)}
          />
        </ManageField>
        <ManageField id="tag-new-parent" label={t("tagParentLabel")}>
          <Select value={parentID} onValueChange={setParentID}>
            <SelectTrigger id="tag-new-parent" className="w-48">
              <SelectValue placeholder={t("tagNoParent")} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={NO_PARENT}>{t("tagNoParent")}</SelectItem>
              {tags.map((p) => (
                <SelectItem key={p.id} value={p.id}>
                  {p.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </ManageField>
        <Button type="submit" disabled={!name.trim()}>
          {t("tagCreate")}
        </Button>
      </ManageInlineForm>
    </form>
  );
}

function targetTypeLabel(t: ReturnType<typeof useT>, type: TagTargetType) {
  if (type === "media") return t("tagTargetMedia");
  if (type === "entity") return t("tagTargetEntity");
  return t("tagTargetMarker");
}

function TargetTypeField({
  id,
  value,
  onChange,
}: {
  id: string;
  value: TagTargetType;
  onChange: (value: TagTargetType) => void;
}) {
  const t = useT();
  return (
    <ManageField id={id} label={t("tagTargetType")}>
      <Select value={value} onValueChange={(v) => onChange(v as TagTargetType)}>
        <SelectTrigger id={id} className="w-40">
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
    </ManageField>
  );
}

function AssignSection({ tags }: { tags: TagInfo[] }) {
  const t = useT();
  const [targetType, setTargetType] = useState<TagTargetType>("media");
  const [targetID, setTargetID] = useState("");
  const [assignment, setAssignment] = useState<TagAssignmentInfo | null>(null);
  const [addTagID, setAddTagID] = useState("");
  const [error, setError] = useState("");

  // The loaded panel belongs to the target it was loaded for; changing the
  // target drops it, and a response that arrives after a change is ignored,
  // so an assign or unassign never lands on a different target.
  const generation = useRef(0);
  const changeTarget = (type: TagTargetType, id: string) => {
    generation.current += 1;
    setTargetType(type);
    setTargetID(id);
    setAssignment(null);
    setAddTagID("");
  };

  const run = async (action: () => Promise<TagAssignmentInfo>) => {
    const started = generation.current;
    setError("");
    try {
      const next = await action();
      if (started !== generation.current) return false;
      setAssignment(next);
      return true;
    } catch {
      if (started === generation.current) setError(t("tagsUnavailable"));
      return false;
    }
  };

  const id = targetID.trim();
  const onLoad = () => {
    if (id) void run(() => fetchTagAssignments(targetType, id));
  };
  // Mutations go to the target the panel was loaded for, not the inputs.
  const onAssign = async () => {
    if (!assignment || !addTagID) return;
    const { targetType: type, targetID: loaded } = assignment;
    if (await run(() => assignTag(addTagID, type, loaded))) setAddTagID("");
  };
  const onUnassign = (tagID: string) => {
    if (!assignment) return;
    const { targetType: type, targetID: loaded } = assignment;
    void run(() => unassignTag(tagID, type, loaded));
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
          <span className="manage-count">{t("tagsCount", { count: assigned.length })}</span>
        ) : undefined
      }
    >
      <form onSubmit={submit(onLoad)}>
        <ManageInlineForm>
          <TargetTypeField
            id="assign-target-type"
            value={targetType}
            onChange={(type) => changeTarget(type, targetID)}
          />
          <ManageField id="assign-target-id" label={t("tagTargetID")} className="min-w-48 flex-1">
            <Input
              id="assign-target-id"
              value={targetID}
              autoComplete="off"
              spellCheck={false}
              className="manage-mono"
              onChange={(e) => changeTarget(targetType, e.target.value)}
            />
          </ManageField>
          <Button type="submit" variant="outline" disabled={!id}>
            {t("tagLoad")}
          </Button>
        </ManageInlineForm>
      </form>

      {assignment && (
        <div className="manage-add-row mt-3 space-y-3 pt-3">
          {assigned.length === 0 ? (
            <EmptyLine message={t("tagNoneAssigned")} />
          ) : (
            <div className="flex flex-wrap gap-2">
              {assigned.map((tag) => (
                <TagChip key={tag.id} name={tag.name} onRemove={() => onUnassign(tag.id)} />
              ))}
            </div>
          )}
          <form onSubmit={submit(() => void onAssign())}>
            <ManageInlineForm>
              <ManageField id="assign-add-tag" label={t("tagAssign")} className="min-w-48 flex-1">
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
              </ManageField>
              <Button type="submit" disabled={!addTagID}>
                {t("tagAssign")}
              </Button>
            </ManageInlineForm>
          </form>
        </div>
      )}
      <ErrorLine message={error} />
    </ManageSection>
  );
}

function BulkSection({ tags }: { tags: TagInfo[] }) {
  const t = useT();
  const [mode, setMode] = useState<TagBulkMode>("add");
  const [targetType, setTargetType] = useState<TagTargetType>("media");
  const [targetIDs, setTargetIDs] = useState("");
  const [selected, setSelected] = useState<string[]>([]);
  const [affected, setAffected] = useState<number | null>(null);
  const [error, setError] = useState("");

  const idList = targetIDs
    .split(",")
    .map((id) => id.trim())
    .filter(Boolean);

  const toggle = (id: string) => {
    setAffected(null);
    setSelected((prev) => (prev.includes(id) ? prev.filter((s) => s !== id) : [...prev, id]));
  };

  const modeLabel = (m: TagBulkMode) =>
    m === "add"
      ? t("tagBulkModeAdd")
      : m === "remove"
        ? t("tagBulkModeRemove")
        : t("tagBulkModeReplace");

  const canApply = idList.length > 0 && selected.length > 0;

  const onApply = async () => {
    setError("");
    if (!canApply) return;
    try {
      const result = await bulkAssignTags(selected, targetType, idList, mode);
      setAffected(result.affected);
    } catch {
      setError(t("tagsUnavailable"));
    }
  };

  const byID = new Map(tags.map((tag) => [tag.id, tag]));
  const chosen = selected.flatMap((id) => byID.get(id) ?? []);
  // Tags not yet in the selection — the add-picker offers only these.
  const available = tags.filter((tag) => !selected.includes(tag.id));

  return (
    <ManageSection title={t("tagBulkHeading")} lead={t("tagBulkLead")}>
      <form className="space-y-3" onSubmit={submit(() => void onApply())}>
        <ManageInlineForm>
          <TargetTypeField id="bulk-target-type" value={targetType} onChange={setTargetType} />
          <ManageField
            id="bulk-target-ids"
            label={t("tagBulkTargetIDs")}
            className="min-w-48 flex-1"
          >
            <Input
              id="bulk-target-ids"
              value={targetIDs}
              autoComplete="off"
              spellCheck={false}
              className="manage-mono"
              onChange={(e) => {
                setAffected(null);
                setTargetIDs(e.target.value);
              }}
            />
          </ManageField>
        </ManageInlineForm>

        <ManageField id="bulk-add-tag" label={t("tagsHeading")}>
          <div className="flex flex-wrap items-center gap-2">
            {chosen.map((tag) => (
              <TagChip key={tag.id} name={tag.name} onRemove={() => toggle(tag.id)} />
            ))}
            <Select value="" onValueChange={toggle}>
              <SelectTrigger id="bulk-add-tag" className="w-48">
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
        </ManageField>

        <div className="manage-add-row flex flex-wrap items-center gap-3 pt-3">
          <Segmented
            label={t("tagBulkHeading")}
            value={mode}
            onChange={(m) => {
              setAffected(null);
              setMode(m);
            }}
            options={BULK_MODES.map((m) => ({ value: m, label: modeLabel(m) }))}
          />
          <span className="ml-auto flex items-center gap-3">
            {affected !== null && (
              <StatusBadge state="green">
                {t("tagBulkApplied")} · {affected} {t("tagBulkAffected")}
              </StatusBadge>
            )}
            <Button type="submit" disabled={!canApply}>
              {t("tagBulkApply")}
            </Button>
          </span>
        </div>
      </form>
      <ErrorLine message={error} />
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
          <TableHead className="w-48">{t("tagDeletedAt")}</TableHead>
          <TableHead className="w-32 text-right">
            <span className="sr-only">{t("tagActionsHeading")}</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {tags.map((tag) => (
          <TableRow key={tag.id}>
            <TableCell className="manage-cell-primary max-w-0">
              <span className="block truncate" title={tag.name}>
                {tag.name}
              </span>
            </TableCell>
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
