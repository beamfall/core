import { useCallback, useEffect, useState } from "react";
import {
  EmptyLine,
  ErrorLine,
  ManageField,
  ManageInlineForm,
  ManageSection,
  ManageTable,
  PathPickerField,
  StatusChip,
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
import { Switch } from "@/components/ui/switch";
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tree } from "@/components/ui/tree";
import { displayString } from "@/lib/display";
import {
  createLibrary,
  createManagedFolder,
  deleteManagedPath,
  fetchFolderTree,
  fetchLibraries,
  fetchPlugins,
  moveManagedPath,
  renameManagedPath,
  restoreManagedPath,
  scanLibrary,
  setLibraryPreviewClips,
  setLibraryRoots,
} from "@/lib/graphql";
import type {
  FolderEntryInfo,
  LibraryInfo,
  ManagedTrashInfo,
  PluginSummary,
} from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";

const NONE = "__none__";

// Human-readable file size for a folder-tree leaf's meta column. Binary units,
// one significant decimal — matches the manage-folder-tree mockup's `.meta`.
function formatNodeSize(bytes: number): string {
  if (bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const exp = Math.min(units.length - 1, Math.floor(Math.log(bytes) / Math.log(1024)));
  const value = bytes / 1024 ** exp;
  const text = exp === 0 ? String(value) : value.toFixed(1);
  return `${text} ${units[exp]}`;
}

// Libraries: roots / preview-clips / scan per Library, plus Create Library with
// an optional plugin binding. Ported from app.js refreshManagerLibraries +
// renderManageLibraries + loadPluginOptions + create-library-form. Server is the
// authority; every toggle echoes the confirmed state and reverts on failure.
export function LibrariesPage() {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const [libraries, setLibraries] = useState<LibraryInfo[]>([]);
  const [plugins, setPlugins] = useState<PluginSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [newTitle, setNewTitle] = useState("");
  const [newPlugin, setNewPlugin] = useState(NONE);

  const load = useCallback(async () => {
    setError("");
    setLoading(true);
    try {
      const [libs, plugs] = await Promise.all([fetchLibraries(), fetchPlugins()]);
      setLibraries(libs);
      setPlugins(plugs);
    } catch (err) {
      setError(err instanceof Error ? err.message : t("couldNotUpdateLibrary"));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void load();
  }, [load]);

  const onTogglePreview = async (lib: LibraryInfo, enabled: boolean) => {
    setError("");
    try {
      const updated = await setLibraryPreviewClips(lib.id, enabled);
      setLibraries((prev) =>
        prev.map((l) => (l.id === lib.id ? { ...l, previewClips: updated.previewClips } : l)),
      );
    } catch (err) {
      setError(err instanceof Error ? err.message : t("couldNotUpdateLibrary"));
    }
  };

  const onCreate = async () => {
    const title = newTitle.trim();
    if (!title) return;
    setError("");
    try {
      await createLibrary(title, newPlugin === NONE ? null : newPlugin);
      setNewTitle("");
      setNewPlugin(NONE);
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : t("couldNotCreateLibrary"));
    }
  };

  return (
    <div className="space-y-4">
      <ManageSection title={t("libraries")}>
        {!loading && libraries.length === 0 ? (
          <EmptyLine message={t("libraryEmptyTitle")} />
        ) : (
          <ManageTable>
            <TableHeader>
              <TableRow>
                <TableHead>{t("title")}</TableHead>
                <TableHead>{t("previewClips")}</TableHead>
                <TableHead>{t("libraryRoots")}</TableHead>
                <TableHead>{t("scan")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {loading ? (
                <TableSkeletonRows columns={4} />
              ) : (
                libraries.map((lib) => (
                  <LibraryRow
                    key={lib.id}
                    library={lib}
                    onTogglePreview={onTogglePreview}
                    onError={setError}
                  />
                ))
              )}
            </TableBody>
          </ManageTable>
        )}
        <ErrorLine message={error} />
      </ManageSection>

      <ManageSection title={t("createLibrary")}>
        <ManageInlineForm>
          <ManageField id="library-title" label={t("title")}>
            <Input
              id="library-title"
              value={newTitle}
              autoComplete="off"
              spellCheck={false}
              onChange={(e) => setNewTitle(e.target.value)}
            />
          </ManageField>
          <ManageField id="library-plugin" label={t("plugin")}>
            <Select value={newPlugin} onValueChange={setNewPlugin}>
              <SelectTrigger id="library-plugin" className="w-56">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={NONE}>{t("none")}</SelectItem>
                {plugins.map((p) => (
                  <SelectItem key={p.id} value={p.id}>
                    {displayString(p.displayName, displayLocale)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </ManageField>
          <Button type="button" onClick={onCreate} disabled={!newTitle.trim()}>
            {t("createLibrary")}
          </Button>
        </ManageInlineForm>
      </ManageSection>
    </div>
  );
}

function LibraryRow({
  library,
  onTogglePreview,
  onError,
}: {
  library: LibraryInfo;
  onTogglePreview: (lib: LibraryInfo, enabled: boolean) => Promise<void>;
  onError: (message: string) => void;
}) {
  const t = useT();
  const [rootsText, setRootsText] = useState((library.roots ?? []).join("\n"));
  const [newRoot, setNewRoot] = useState("");
  const [tree, setTree] = useState<FolderEntryInfo | null>(null);
  const [selectedPath, setSelectedPath] = useState("");
  const [selectedKind, setSelectedKind] = useState<"folder" | "file">("folder");
  const [folderName, setFolderName] = useState("");
  const [renameTo, setRenameTo] = useState("");
  const [moveDestination, setMoveDestination] = useState("");
  const [lastTrash, setLastTrash] = useState<ManagedTrashInfo | null>(null);
  const [busy, setBusy] = useState(false);

  const loadTree = useCallback(async () => {
    if ((library.roots ?? []).length === 0 && !rootsText.trim()) {
      setTree(null);
      setSelectedPath("");
      return;
    }
    try {
      const next = await fetchFolderTree(library.id, undefined, 2);
      setTree(next);
      const firstRoot = next.children?.find((entry) => entry.kind === "folder");
      if (!selectedPath && firstRoot) {
        setSelectedPath(firstRoot.path);
        setSelectedKind("folder");
      }
    } catch {
      setTree(null);
    }
  }, [library.id, library.roots, rootsText, selectedPath]);

  useEffect(() => {
    void loadTree();
  }, [loadTree]);

  const onScan = async () => {
    const path = selectedPath.trim();
    if (!path) {
      onError(t("selectFolderToScan"));
      return;
    }
    if (selectedKind !== "folder") {
      onError(t("selectFolderToScan"));
      return;
    }
    setBusy(true);
    onError("");
    try {
      await scanLibrary(library.id, path);
    } catch (err) {
      onError(err instanceof Error ? err.message : t("scanFailed"));
    } finally {
      setBusy(false);
    }
  };

  const onSaveRoots = async () => {
    setBusy(true);
    onError("");
    const nextRoots = rootsText
      .split(/\n+/)
      .map((v) => v.trim())
      .filter(Boolean);
    try {
      const updated = await setLibraryRoots(library.id, nextRoots);
      setRootsText(updated.roots.join("\n"));
      await loadTree();
    } catch (err) {
      onError(err instanceof Error ? err.message : t("couldNotUpdateRoots"));
    } finally {
      setBusy(false);
    }
  };

  const rootsList = rootsText
    .split(/\n+/)
    .map((v) => v.trim())
    .filter(Boolean);

  const onAddRoot = () => {
    const root = newRoot.trim();
    if (!root || rootsList.includes(root)) return;
    setRootsText([...rootsList, root].join("\n"));
    setNewRoot("");
  };

  const onRemoveRoot = (root: string) => {
    setRootsText(rootsList.filter((item) => item !== root).join("\n"));
  };

  const parentForCreate = selectedKind === "folder" ? selectedPath : dirname(selectedPath);

  const onCreateFolder = async () => {
    const name = folderName.trim();
    if (!parentForCreate || !name) return;
    setBusy(true);
    onError("");
    try {
      const created = await createManagedFolder(library.id, parentForCreate, name);
      setFolderName("");
      setSelectedPath(created.path);
      setSelectedKind(created.kind);
      await loadTree();
    } catch (err) {
      onError(err instanceof Error ? err.message : t("managedFileOperationFailed"));
    } finally {
      setBusy(false);
    }
  };

  const onRename = async () => {
    const name = renameTo.trim();
    if (!selectedPath || !name) return;
    setBusy(true);
    onError("");
    try {
      const renamed = await renameManagedPath(library.id, selectedPath, name);
      setRenameTo("");
      setSelectedPath(renamed.path);
      setSelectedKind(renamed.kind);
      await loadTree();
    } catch (err) {
      onError(err instanceof Error ? err.message : t("managedFileOperationFailed"));
    } finally {
      setBusy(false);
    }
  };

  const onMove = async () => {
    const destination = moveDestination.trim();
    if (!selectedPath || !destination) return;
    setBusy(true);
    onError("");
    try {
      const moved = await moveManagedPath(library.id, selectedPath, destination);
      setMoveDestination("");
      setSelectedPath(moved.path);
      setSelectedKind(moved.kind);
      await loadTree();
    } catch (err) {
      onError(err instanceof Error ? err.message : t("managedFileOperationFailed"));
    } finally {
      setBusy(false);
    }
  };

  const onDelete = async () => {
    if (!selectedPath) return;
    if (!window.confirm(t("confirmDeleteManagedPath"))) return;
    setBusy(true);
    onError("");
    try {
      const trash = await deleteManagedPath(library.id, selectedPath);
      setLastTrash(trash);
      setSelectedPath(dirname(trash.originalPath));
      setSelectedKind("folder");
      await loadTree();
    } catch (err) {
      onError(err instanceof Error ? err.message : t("managedFileOperationFailed"));
    } finally {
      setBusy(false);
    }
  };

  const onRestore = async () => {
    if (!lastTrash) return;
    setBusy(true);
    onError("");
    try {
      const restored = await restoreManagedPath(library.id, lastTrash.trashID);
      setSelectedPath(restored.path);
      setSelectedKind(restored.kind);
      setLastTrash(null);
      await loadTree();
    } catch (err) {
      onError(err instanceof Error ? err.message : t("managedFileOperationFailed"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <TableRow>
      <TableCell className="manage-cell-primary">{library.title}</TableCell>
      <TableCell>
        <Switch
          aria-label={t("previewClips")}
          checked={library.previewClips !== false}
          onCheckedChange={(checked) => onTogglePreview(library, checked)}
        />
      </TableCell>
      <TableCell className="whitespace-normal">
        <div className="w-72 space-y-2">
          <PathPickerField
            id={`roots-${library.id}`}
            label={t("libraryRoots")}
            role="libraryRoot"
            value={newRoot}
            libraryID={library.id}
            placeholder={t("rootsPlaceholder")}
            onChange={setNewRoot}
            inputClassName="w-64"
          />
          {rootsList.length > 0 ? (
            <div className="grid gap-1">
              {rootsList.map((root) => (
                <div key={root} className="flex items-center gap-2 rounded border px-2 py-1">
                  <span className="min-w-0 flex-1 break-all text-xs">{root}</span>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    disabled={busy}
                    onClick={() => onRemoveRoot(root)}
                  >
                    {t("remove")}
                  </Button>
                </div>
              ))}
            </div>
          ) : (
            <p className="text-xs text-muted-foreground">{t("rootsPlaceholder")}</p>
          )}
          <div className="flex gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={busy || !newRoot.trim()}
              onClick={onAddRoot}
            >
              {t("stagePathPickerRoot")}
            </Button>
            <Button type="button" variant="outline" size="sm" disabled={busy} onClick={onSaveRoots}>
              {t("save")}
            </Button>
          </div>
        </div>
      </TableCell>
      <TableCell>
        <div className="manage-folder-tools">
          {tree ? (
            <Tree.Root className="manage-folder-tree" aria-label={t("folderTree")}>
              <FolderTree
                entry={tree}
                depth={0}
                selectedPath={selectedPath}
                onSelect={(entry) => {
                  setSelectedPath(entry.path);
                  setSelectedKind(entry.kind);
                  if (entry.kind === "folder") setMoveDestination(entry.path);
                }}
              />
            </Tree.Root>
          ) : (
            <div className="manage-folder-tree" role="status" aria-label={t("folderTree")}>
              <p className="text-xs text-[var(--text-muted)]">{t("folderTreeEmpty")}</p>
            </div>
          )}
          <div className="manage-folder-controls">
            <div className="manage-folder-selected">
              <span className="manage-folder-selected-label">{t("selected")}</span>
              <span className="manage-folder-selected-path">
                {selectedPath || t("noFolderSelected")}
              </span>
              {selectedPath && (
                <StatusChip tone={selectedKind === "folder" ? "green" : "neutral"}>
                  {t(selectedKind === "folder" ? "folderKindFolder" : "folderKindFile")}
                </StatusChip>
              )}
            </div>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={busy || selectedKind !== "folder" || !selectedPath}
              onClick={onScan}
            >
              {t("scan")}
            </Button>
            <div className="flex flex-wrap gap-2">
              <Input
                aria-label={t("newFolderName")}
                placeholder={t("newFolderName")}
                autoComplete="off"
                spellCheck={false}
                value={folderName}
                onChange={(e) => setFolderName(e.target.value)}
                className="w-40"
              />
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={busy || !parentForCreate || !folderName.trim()}
                onClick={onCreateFolder}
              >
                {t("createFolder")}
              </Button>
            </div>
            <div className="flex flex-wrap gap-2">
              <Input
                aria-label={t("renameTo")}
                placeholder={t("renameTo")}
                autoComplete="off"
                spellCheck={false}
                value={renameTo}
                onChange={(e) => setRenameTo(e.target.value)}
                className="w-40"
              />
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={busy || !selectedPath || !renameTo.trim()}
                onClick={onRename}
              >
                {t("rename")}
              </Button>
            </div>
            <div className="flex flex-wrap gap-2">
              <Input
                aria-label={t("destinationFolder")}
                placeholder={t("destinationFolder")}
                autoComplete="off"
                spellCheck={false}
                value={moveDestination}
                onChange={(e) => setMoveDestination(e.target.value)}
                className="w-48"
              />
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={busy || !selectedPath || !moveDestination.trim()}
                onClick={onMove}
              >
                {t("move")}
              </Button>
            </div>
            <div className="flex flex-wrap gap-2">
              <Button
                type="button"
                variant="destructive"
                size="sm"
                disabled={busy || !selectedPath}
                onClick={onDelete}
              >
                {t("delete")}
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={busy || !lastTrash}
                onClick={onRestore}
              >
                {t("restore")}
              </Button>
            </div>
          </div>
        </div>
      </TableCell>
    </TableRow>
  );
}

// FolderTree renders the bounded server folder tree to the manage-folder-tree
// mockup's node structure: each node is a flat `.node.depth-N` treeitem (files
// also carry `.file`) with a leading glyph (▸ folder / · file), the name, and a
// right-aligned `.meta` (folder child count / file size) — not a nested
// indented button. The synthetic root (no path) renders no row, only children;
// its real-root children render at depth 0.
function FolderTree({
  entry,
  depth,
  selectedPath,
  onSelect,
}: {
  entry: FolderEntryInfo;
  depth: number;
  selectedPath: string;
  onSelect: (entry: FolderEntryInfo) => void;
}) {
  const t = useT();
  const isSyntheticRoot = !entry.path;
  const isFolder = entry.kind === "folder";
  const childCount = entry.children?.length ?? 0;
  const meta = isFolder ? t("folderItemCount", { count: childCount }) : formatNodeSize(entry.size);

  return (
    <>
      {!isSyntheticRoot && (
        <Tree.Item
          aria-selected={entry.path === selectedPath}
          aria-expanded={isFolder && childCount > 0 ? true : undefined}
          aria-level={depth + 1}
          className={`manage-folder-node depth-${depth}${isFolder ? "" : " file"}`}
          onClick={() => onSelect(entry)}
          onKeyDown={(e) => {
            if (e.key === "Enter" || e.key === " ") {
              e.preventDefault();
              onSelect(entry);
            }
          }}
        >
          <span className="manage-folder-glyph" aria-hidden>
            {isFolder ? "▸" : "·"}
          </span>
          <span className="manage-folder-name">{entry.name}</span>
          <span className="manage-folder-meta">{meta}</span>
        </Tree.Item>
      )}
      {entry.children?.map((child) => (
        <FolderTree
          key={child.path}
          entry={child}
          depth={isSyntheticRoot ? 0 : depth + 1}
          selectedPath={selectedPath}
          onSelect={onSelect}
        />
      ))}
    </>
  );
}

function dirname(path: string) {
  const trimmed = path.replace(/\/+$/, "");
  const index = trimmed.lastIndexOf("/");
  return index > 0 ? trimmed.slice(0, index) : trimmed;
}
