import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { EmptyLine, ErrorLine, ManageSection, ManageTable } from "@/components/beamfall/workbench";
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
import { Tabs } from "@/components/ui/astryx-primitives";
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
import { Textarea } from "@/components/ui/textarea";
import {
  addCollectionMedia,
  createCollection,
  createSmartCollection,
  deleteCollection,
  deleteSmartCollection,
  fetchCollections,
  fetchSmartCollections,
  removeCollectionMedia,
  reorderCollectionMedia,
  restoreCollection,
  updateCollection,
  updateSmartCollection,
} from "@/lib/graphql";
import type {
  CollectionInfo,
  CollectionMemberInfo,
  SmartCollectionInfo,
} from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";

// Collections + Smart Collections (WEB-5F / ADR-0001 / ADR-0061): the manager-
// gated curation surface for manual, ordered, nestable Collections and for
// Smart Collection Sources. Every mutation is authoritative server-side; this
// page is only an affordance. The wire entity is Media; "Collection" is the
// ADR-0001 curation noun, never a domain label (HARD invariant #1/#2). Media
// IDs render as the server's opaque identifiers — never resolved to names here.
// Delete is recoverable: it trashes the Collection (trashedAt) and restores via
// restoreCollection (HARD invariant #6). Built on the SubscriptionsPage register
// pattern (library picker → list → add form).

type Tab = "manual" | "smart";

// Radix Select forbids an empty-string item value, so the "top level" parent
// choice uses a sentinel that maps back to a null parentID on create.
const TOP_LEVEL = "__top__";

export function CollectionsPage() {
  const t = useT();
  const { manifest } = useSession();
  const libraries = manifest?.libraries ?? [];

  const [libraryID, setLibraryID] = useState("");
  const [tab, setTab] = useState<Tab>("manual");

  // Default the picker to the first Library once the manifest arrives.
  useEffect(() => {
    if (!libraryID && libraries.length > 0) setLibraryID(libraries[0].id);
  }, [libraries, libraryID]);

  return (
    <Tabs.Root value={tab} onValueChange={(value) => setTab(value as Tab)} className="space-y-4">
      <ManageSection
        title={t("collections")}
        actions={
          <div className="flex items-center gap-2">
            <Tabs.List className="manage-tabs" aria-label={t("collections")}>
              <Tabs.Tab
                value="manual"
                render={
                  <Button
                    type="button"
                    variant={tab === "manual" ? "default" : "outline"}
                    size="sm"
                  />
                }
              >
                {t("collectionManual")}
              </Tabs.Tab>
              <Tabs.Tab
                value="smart"
                render={
                  <Button
                    type="button"
                    variant={tab === "smart" ? "default" : "outline"}
                    size="sm"
                  />
                }
              >
                {t("collectionSmart")}
              </Tabs.Tab>
            </Tabs.List>
            <Label htmlFor="collections-library" className="sr-only">
              {t("library")}
            </Label>
            <Select value={libraryID} onValueChange={setLibraryID}>
              <SelectTrigger id="collections-library" size="sm" className="w-56">
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
        }
      >
        <p className="manage-empty">{libraries.length === 0 ? t("noCollections") : ""}</p>
      </ManageSection>

      <Tabs.Panel value="manual" className="space-y-4">
        <ManualCollections libraryID={libraryID} />
      </Tabs.Panel>
      <Tabs.Panel value="smart" className="space-y-4">
        <SmartCollections libraryID={libraryID} />
      </Tabs.Panel>
    </Tabs.Root>
  );
}

// --- Manual Collections ----------------------------------------------------

function ManualCollections({ libraryID }: { libraryID: string }) {
  const t = useT();
  const [collections, setCollections] = useState<CollectionInfo[]>([]);
  const [includeTrashed, setIncludeTrashed] = useState(false);
  const [selectedID, setSelectedID] = useState("");
  const [error, setError] = useState("");

  // New-collection form state.
  const [newName, setNewName] = useState("");
  const [newParent, setNewParent] = useState(TOP_LEVEL);
  // Load generation guard (as SubscriptionsPage): a slow response for a Library
  // the picker has since left must not overwrite the current Library's list.
  const loadGeneration = useRef(0);

  const load = useCallback(
    async (id: string, trashed: boolean) => {
      const generation = ++loadGeneration.current;
      setError("");
      setCollections([]);
      if (!id) return;
      try {
        const next = await fetchCollections(id, trashed);
        if (loadGeneration.current === generation) setCollections(next);
      } catch {
        if (loadGeneration.current === generation) setError(t("collectionsUnavailable"));
      }
    },
    [t],
  );

  useEffect(() => {
    void load(libraryID, includeTrashed);
    return () => {
      loadGeneration.current += 1;
    };
  }, [libraryID, includeTrashed, load]);

  // Live (non-trashed) Collections, ordered for display and reordering.
  const live = useMemo(
    () =>
      collections
        .filter((c) => !c.trashedAt)
        .sort((a, b) => a.sortOrder - b.sortOrder || a.name.localeCompare(b.name)),
    [collections],
  );
  const trashed = useMemo(() => collections.filter((c) => c.trashedAt), [collections]);
  const selected = useMemo(() => live.find((c) => c.id === selectedID) ?? null, [live, selectedID]);

  const reload = () => load(libraryID, includeTrashed);

  const onCreate = async () => {
    setError("");
    const name = newName.trim();
    if (!libraryID || !name) return;
    try {
      await createCollection({
        libraryID,
        name,
        parentID: newParent === TOP_LEVEL ? null : newParent,
      });
      setNewName("");
      setNewParent(TOP_LEVEL);
      await reload();
    } catch {
      setError(t("couldNotCreateCollection"));
    }
  };

  const onRename = async (c: CollectionInfo, name: string) => {
    setError("");
    try {
      await updateCollection({ id: c.id, name });
      await reload();
    } catch {
      setError(t("couldNotUpdateCollection"));
    }
  };

  // Reorder a live Collection by swapping its sortOrder with its neighbour.
  // The schema offers no single swap call, so a failed second write rolls the
  // first back, and the list is reloaded either way so the displayed order is
  // always the server's.
  const onReorder = async (index: number, dir: -1 | 1) => {
    const target = index + dir;
    if (target < 0 || target >= live.length) return;
    setError("");
    const a = live[index];
    const b = live[target];
    try {
      await updateCollection({ id: a.id, sortOrder: b.sortOrder });
    } catch {
      setError(t("couldNotUpdateCollection"));
      return;
    }
    let failed = false;
    try {
      await updateCollection({ id: b.id, sortOrder: a.sortOrder });
    } catch {
      failed = true;
      await updateCollection({ id: a.id, sortOrder: a.sortOrder }).catch(() => {});
    }
    await reload();
    if (failed) setError(t("couldNotUpdateCollection"));
  };

  const onDelete = async (c: CollectionInfo) => {
    setError("");
    try {
      await deleteCollection(c.id);
      if (selectedID === c.id) setSelectedID("");
      await reload();
    } catch {
      setError(t("couldNotDeleteCollection"));
    }
  };

  const onRestore = async (c: CollectionInfo) => {
    setError("");
    try {
      await restoreCollection(c.id);
      await reload();
    } catch {
      setError(t("couldNotRestoreCollection"));
    }
  };

  return (
    <>
      <ManageSection
        title={t("collections")}
        actions={
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={includeTrashed}
              onChange={(e) => setIncludeTrashed(e.target.checked)}
            />
            {t("showTrashed")}
          </label>
        }
      >
        {live.length === 0 ? (
          <EmptyLine message={t("noCollections")} />
        ) : (
          <ManageTable>
            <TableHeader>
              <TableRow>
                <TableHead>{t("collectionName")}</TableHead>
                <TableHead>{t("collectionMembers")}</TableHead>
                <TableHead className="text-right">{t("rename")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {live.map((c, i) => (
                <CollectionRow
                  key={c.id}
                  collection={c}
                  selected={c.id === selectedID}
                  canMoveUp={i > 0}
                  canMoveDown={i < live.length - 1}
                  onSelect={() => setSelectedID(c.id)}
                  onMoveUp={() => onReorder(i, -1)}
                  onMoveDown={() => onReorder(i, 1)}
                  onRename={(name) => onRename(c, name)}
                  onDelete={() => onDelete(c)}
                />
              ))}
            </TableBody>
          </ManageTable>
        )}
        <ErrorLine message={error} />

        <div className="manage-add-row mt-3 flex flex-wrap items-end gap-3 border-t pt-3">
          <div className="space-y-1">
            <Label htmlFor="collection-name">{t("newCollectionName")}</Label>
            <Input
              id="collection-name"
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              className="w-60"
            />
          </div>
          <div className="space-y-1">
            <Label htmlFor="collection-parent" className="block">
              {t("collectionParent")}
            </Label>
            <Select value={newParent} onValueChange={setNewParent}>
              <SelectTrigger id="collection-parent" size="sm" className="w-52">
                <SelectValue placeholder={t("collectionTopLevel")} />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={TOP_LEVEL}>{t("collectionTopLevel")}</SelectItem>
                {live.map((c) => (
                  <SelectItem key={c.id} value={c.id}>
                    {c.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <Button type="button" onClick={onCreate}>
            {t("create")}
          </Button>
        </div>
      </ManageSection>

      {selected && (
        <MembersEditor
          collection={selected}
          onChanged={reload}
          onError={() => setError(t("couldNotUpdateMembers"))}
        />
      )}

      {includeTrashed && trashed.length > 0 && (
        <ManageSection title={t("trashedCollections")}>
          <ManageTable>
            <TableHeader>
              <TableRow>
                <TableHead>{t("collectionName")}</TableHead>
                <TableHead className="text-right">{t("restore")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {trashed.map((c) => (
                <TableRow key={c.id}>
                  <TableCell className="manage-cell-primary">{c.name}</TableCell>
                  <TableCell className="text-right">
                    <Button type="button" variant="outline" size="sm" onClick={() => onRestore(c)}>
                      {t("restore")}
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </ManageTable>
        </ManageSection>
      )}
    </>
  );
}

function CollectionRow({
  collection,
  selected,
  canMoveUp,
  canMoveDown,
  onSelect,
  onMoveUp,
  onMoveDown,
  onRename,
  onDelete,
}: {
  collection: CollectionInfo;
  selected: boolean;
  canMoveUp: boolean;
  canMoveDown: boolean;
  onSelect: () => void;
  onMoveUp: () => void;
  onMoveDown: () => void;
  onRename: (name: string) => void;
  onDelete: () => void;
}) {
  const t = useT();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(collection.name);

  const commit = () => {
    const name = draft.trim();
    setEditing(false);
    if (name && name !== collection.name) onRename(name);
  };

  return (
    <TableRow data-selected={selected || undefined}>
      <TableCell className="manage-cell-primary">
        <div className="flex items-center gap-2">
          <span className="manage-reorder inline-flex flex-col">
            <button
              type="button"
              aria-label={t("reorderUp")}
              disabled={!canMoveUp}
              onClick={onMoveUp}
            >
              ▲
            </button>
            <button
              type="button"
              aria-label={t("reorderDown")}
              disabled={!canMoveDown}
              onClick={onMoveDown}
            >
              ▼
            </button>
          </span>
          {collection.parentID && (
            <span aria-hidden className="text-muted-foreground">
              ↳
            </span>
          )}
          {editing ? (
            <Input
              autoFocus
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onBlur={commit}
              onKeyDown={(e) => {
                if (e.key === "Enter") commit();
                if (e.key === "Escape") setEditing(false);
              }}
              className="h-7 w-44"
            />
          ) : (
            <button type="button" className="text-left hover:underline" onClick={onSelect}>
              {collection.name}
            </button>
          )}
        </div>
      </TableCell>
      <TableCell className="manage-mono text-muted-foreground">{collection.memberCount}</TableCell>
      <TableCell className="text-right">
        <div className="flex justify-end gap-2">
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => {
              setDraft(collection.name);
              setEditing(true);
            }}
          >
            {t("rename")}
          </Button>
          <AlertDialog>
            <AlertDialogTrigger asChild>
              <Button type="button" variant="destructive" size="sm">
                {t("collectionDelete")}
              </Button>
            </AlertDialogTrigger>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>{t("collectionDelete")}</AlertDialogTitle>
                <AlertDialogDescription>{collection.name}</AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
                <AlertDialogAction onClick={onDelete}>{t("collectionDelete")}</AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        </div>
      </TableCell>
    </TableRow>
  );
}

// --- Members editor (reorder / bulk-add / remove) --------------------------

function MembersEditor({
  collection,
  onChanged,
  onError,
}: {
  collection: CollectionInfo;
  onChanged: () => Promise<void> | void;
  onError: () => void;
}) {
  const t = useT();
  // Work against a local copy so reordering feels immediate; the order is
  // written whole through reorderCollectionMedia (matches the GraphQL shape).
  const [members, setMembers] = useState<CollectionMemberInfo[]>([]);
  const [addIDs, setAddIDs] = useState("");

  useEffect(() => {
    setMembers([...collection.members].sort((a, b) => a.sortOrder - b.sortOrder));
  }, [collection.members]);

  // The optimistic local order is restored from the server on failure: a
  // rejected write must not leave the UI claiming an order the server never
  // took.
  const persistOrder = async (ordered: CollectionMemberInfo[]) => {
    try {
      await reorderCollectionMedia(
        collection.id,
        ordered.map((m) => m.mediaID),
      );
      await onChanged();
    } catch {
      setMembers([...collection.members].sort((a, b) => a.sortOrder - b.sortOrder));
      await onChanged();
      onError();
    }
  };

  const onMove = (index: number, dir: -1 | 1) => {
    const target = index + dir;
    if (target < 0 || target >= members.length) return;
    const next = [...members];
    [next[index], next[target]] = [next[target], next[index]];
    setMembers(next);
    void persistOrder(next);
  };

  const onRemove = async (mediaID: string) => {
    try {
      await removeCollectionMedia(collection.id, [mediaID]);
      await onChanged();
    } catch {
      onError();
    }
  };

  const onAdd = async () => {
    const ids = addIDs
      .split(/[\s,]+/)
      .map((s) => s.trim())
      .filter(Boolean);
    if (ids.length === 0) return;
    try {
      await addCollectionMedia(collection.id, ids);
      setAddIDs("");
      await onChanged();
    } catch {
      onError();
    }
  };

  return (
    <ManageSection
      title={collection.name}
      actions={
        <span className="text-sm text-muted-foreground">
          {t("memberCountLabel", { n: members.length })}
        </span>
      }
    >
      {members.length === 0 ? (
        <EmptyLine message={t("noMembers")} />
      ) : (
        <ManageTable>
          <TableHeader>
            <TableRow>
              <TableHead>{t("mediaID")}</TableHead>
              <TableHead>{t("added")}</TableHead>
              <TableHead className="text-right">{t("remove")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {members.map((m, i) => (
              <TableRow key={m.mediaID}>
                <TableCell className="manage-cell-primary">
                  <div className="flex items-center gap-2">
                    <span className="manage-reorder inline-flex flex-col">
                      <button
                        type="button"
                        aria-label={t("reorderUp")}
                        disabled={i === 0}
                        onClick={() => onMove(i, -1)}
                      >
                        ▲
                      </button>
                      <button
                        type="button"
                        aria-label={t("reorderDown")}
                        disabled={i === members.length - 1}
                        onClick={() => onMove(i, 1)}
                      >
                        ▼
                      </button>
                    </span>
                    <span className="manage-mono">{m.mediaID}</span>
                  </div>
                </TableCell>
                <TableCell className="manage-mono text-muted-foreground">
                  {m.addedAt ?? ""}
                </TableCell>
                <TableCell className="text-right">
                  <Button
                    type="button"
                    variant="destructive"
                    size="sm"
                    onClick={() => onRemove(m.mediaID)}
                  >
                    {t("remove")}
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </ManageTable>
      )}

      <div className="manage-add-row mt-3 flex flex-wrap items-end gap-3 border-t pt-3">
        <div className="grow space-y-1">
          <Label htmlFor="member-ids">{t("addMediaIDsLabel")}</Label>
          <Input
            id="member-ids"
            value={addIDs}
            autoComplete="off"
            spellCheck={false}
            onChange={(e) => setAddIDs(e.target.value)}
            className="w-full"
          />
        </div>
        <Button type="button" onClick={onAdd}>
          {t("addMembers")}
        </Button>
      </div>
    </ManageSection>
  );
}

// --- Smart Collections (Source editor) -------------------------------------

function SmartCollections({ libraryID }: { libraryID: string }) {
  const t = useT();
  const [smart, setSmart] = useState<SmartCollectionInfo[]>([]);
  const [error, setError] = useState("");
  const [selectedID, setSelectedID] = useState<string>("");
  const loadGeneration = useRef(0);

  const load = useCallback(
    async (id: string) => {
      const generation = ++loadGeneration.current;
      setError("");
      setSmart([]);
      if (!id) return;
      try {
        const next = await fetchSmartCollections(id);
        if (loadGeneration.current === generation) setSmart(next);
      } catch {
        if (loadGeneration.current === generation) setError(t("collectionsUnavailable"));
      }
    },
    [t],
  );

  useEffect(() => {
    void load(libraryID);
    return () => {
      loadGeneration.current += 1;
    };
  }, [libraryID, load]);

  const selected = useMemo(
    () => smart.find((s) => s.id === selectedID) ?? null,
    [smart, selectedID],
  );

  const onDelete = async (s: SmartCollectionInfo) => {
    setError("");
    try {
      await deleteSmartCollection(s.id);
      if (selectedID === s.id) setSelectedID("");
      await load(libraryID);
    } catch {
      setError(t("couldNotDeleteSmartCollection"));
    }
  };

  return (
    <>
      <ManageSection title={t("smartCollections")}>
        {smart.length === 0 ? (
          <EmptyLine message={t("noSmartCollections")} />
        ) : (
          <ManageTable>
            <TableHeader>
              <TableRow>
                <TableHead>{t("collectionName")}</TableHead>
                <TableHead className="text-right">{t("remove")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {smart.map((s) => (
                <TableRow key={s.id} data-selected={s.id === selectedID || undefined}>
                  <TableCell className="manage-cell-primary">
                    <button
                      type="button"
                      className="text-left hover:underline"
                      onClick={() => setSelectedID(s.id)}
                    >
                      {s.name}
                    </button>
                  </TableCell>
                  <TableCell className="text-right">
                    <AlertDialog>
                      <AlertDialogTrigger asChild>
                        <Button type="button" variant="destructive" size="sm">
                          {t("remove")}
                        </Button>
                      </AlertDialogTrigger>
                      <AlertDialogContent>
                        <AlertDialogHeader>
                          <AlertDialogTitle>{t("remove")}</AlertDialogTitle>
                          <AlertDialogDescription>{s.name}</AlertDialogDescription>
                        </AlertDialogHeader>
                        <AlertDialogFooter>
                          <AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
                          <AlertDialogAction onClick={() => onDelete(s)}>
                            {t("remove")}
                          </AlertDialogAction>
                        </AlertDialogFooter>
                      </AlertDialogContent>
                    </AlertDialog>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </ManageTable>
        )}
        <ErrorLine message={error} />
        <div className="mt-3 border-t pt-3">
          <Button type="button" variant="outline" size="sm" onClick={() => setSelectedID("")}>
            {t("newSmartCollection")}
          </Button>
        </div>
      </ManageSection>

      <SmartCollectionEditor
        key={selected?.id ?? "new"}
        libraryID={libraryID}
        smart={selected}
        onSaved={() => load(libraryID)}
        onError={() => setError(t("couldNotSaveSmartCollection"))}
      />
    </>
  );
}

function SmartCollectionEditor({
  libraryID,
  smart,
  onSaved,
  onError,
}: {
  libraryID: string;
  smart: SmartCollectionInfo | null;
  onSaved: () => Promise<void> | void;
  onError: () => void;
}) {
  const t = useT();
  const [name, setName] = useState(smart?.name ?? "");
  const [sourceExpr, setSourceExpr] = useState(smart?.sourceExpr ?? "");
  const [sortExpr, setSortExpr] = useState(smart?.sortExpr ?? "");

  const onSave = async () => {
    const trimmedName = name.trim();
    const trimmedSource = sourceExpr.trim();
    if (!trimmedName || !trimmedSource) return;
    try {
      if (smart) {
        await updateSmartCollection({
          id: smart.id,
          name: trimmedName,
          sourceExpr: trimmedSource,
          sortExpr: sortExpr.trim() || null,
        });
      } else {
        if (!libraryID) return;
        await createSmartCollection({
          libraryID,
          name: trimmedName,
          sourceExpr: trimmedSource,
          sortExpr: sortExpr.trim() || null,
        });
      }
      await onSaved();
    } catch {
      onError();
    }
  };

  return (
    <ManageSection title={smart ? smart.name : t("newSmartCollection")}>
      <div className="space-y-3">
        <div className="space-y-1">
          <Label htmlFor="smart-name">{t("collectionName")}</Label>
          <Input
            id="smart-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="w-72"
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor="smart-source">{t("sourceExpr")}</Label>
          <Textarea
            id="smart-source"
            value={sourceExpr}
            spellCheck={false}
            onChange={(e) => setSourceExpr(e.target.value)}
            className="manage-mono min-h-20"
          />
          <p className="text-xs text-muted-foreground">{t("sourceExprHelp")}</p>
        </div>
        <div className="space-y-1">
          <Label htmlFor="smart-sort">{t("sortExpr")}</Label>
          <Textarea
            id="smart-sort"
            value={sortExpr}
            spellCheck={false}
            onChange={(e) => setSortExpr(e.target.value)}
            className="manage-mono min-h-16"
          />
          <p className="text-xs text-muted-foreground">{t("sortExprHelp")}</p>
        </div>
        <div className="flex justify-end">
          <Button type="button" onClick={onSave}>
            {t("saveSource")}
          </Button>
        </div>
      </div>
    </ManageSection>
  );
}
