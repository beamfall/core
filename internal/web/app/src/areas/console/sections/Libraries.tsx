import {
  Boxes,
  Folder,
  FolderPlus,
  LibraryBig,
  Plus,
  RefreshCw,
  ScanLine,
  SearchCheck,
  X,
} from "lucide-react";
import { type FormEvent, useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router";
import { DeleteLibraryButton } from "@/components/beamfall/LibraryDelete";
import { RescanAllButton, RescanButton, RescanStatus } from "@/components/beamfall/LibraryRescan";
import {
  EmptyState,
  ErrorLine,
  ErrorState,
  PathPickerField,
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
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { displayString } from "@/lib/display";
import {
  createLibrary,
  fetchFolderTree,
  fetchLibraries,
  fetchPlugins,
  fetchSkipDetectionEnabled,
  fetchWatcherStatus,
  scanLibrary,
  setLibraryPreviewClips,
  setLibraryRoots,
  setSkipDetectionEnabled,
} from "@/lib/graphql";
import type {
  FolderEntryInfo,
  LibraryInfo,
  PluginSummary,
  WatcherStatusInfo,
} from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";
import { ConfirmButton } from "../ConfirmButton";
import { Section } from "../Section";
import { useAsync } from "../useAsync";

const NO_PLUGIN = "__none__";

// Libraries screen styling (CORE-1131): the setup wizard's glass tiles and
// icon tiles (CORE-1114), one full-width card per library so long folder
// paths stay readable.
const CARD = "rounded-xl border border-white/10 bg-white/[0.03] backdrop-blur-sm";
const ICON_TILE =
  "flex size-10 shrink-0 items-center justify-center rounded-xl bg-primary/15 text-primary ring-1 ring-primary/25";
const PANEL = "rounded-lg border border-white/[0.07] bg-black/20 p-3";

function lastSegment(path: string): string {
  const parts = path.split(/[\\/]+/).filter(Boolean);
  return parts[parts.length - 1] ?? path;
}

function SettingRow({
  label,
  help,
  checked,
  onChange,
}: {
  label: string;
  help: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
}) {
  return (
    <label className="flex cursor-pointer items-start justify-between gap-4 py-2">
      <span className="min-w-0">
        <span className="block text-sm font-medium">{label}</span>
        <span className="block text-xs leading-snug text-muted-foreground">{help}</span>
      </span>
      <Switch checked={checked} onCheckedChange={onChange} className="mt-0.5" />
    </label>
  );
}

// One library card: a header with the library's status and its Rescan action,
// its folders (each add or remove saves at once), its options, and the
// single-folder scan tucked under Advanced. Each control writes through its
// own mutation.
function LibraryCard({
  library,
  plugins,
  skipDetectionEnabled,
  watcher,
  onChanged,
}: {
  library: LibraryInfo;
  plugins: PluginSummary[];
  skipDetectionEnabled: boolean;
  watcher?: WatcherStatusInfo;
  onChanged: () => void;
}) {
  const t = useT();
  const displayLocale = useDisplayLocale();
  // A save replaces the whole folder list, so edits run one at a time and each
  // builds on the last saved list, not a library prop the refresh hasn't
  // updated yet; otherwise a quick second edit would drop the first.
  const [savedRoots, setSavedRoots] = useState<string[] | null>(null);
  const [savingRoots, setSavingRoots] = useState(false);
  const savingRootsRef = useRef(false);
  const roots = savedRoots ?? library.roots ?? [];
  useEffect(() => {
    setSavedRoots(null);
  }, [library.roots]);
  const [newRoot, setNewRoot] = useState("");
  const [rootsError, setRootsError] = useState("");
  const [scanRoot, setScanRoot] = useState("");
  const [scanFolders, setScanFolders] = useState<FolderEntryInfo[]>([]);
  const [scanError, setScanError] = useState("");
  const plugin = plugins.find((p) => p.id === library.pluginID);

  const loadScanFolders = useCallback(async () => {
    if ((library.roots ?? []).length === 0) {
      setScanFolders([]);
      setScanRoot("");
      return;
    }
    try {
      const tree = await fetchFolderTree(library.id, undefined, 2);
      const folders: FolderEntryInfo[] = [];
      const visit = (entry: FolderEntryInfo) => {
        if (entry.path && entry.kind === "folder") folders.push(entry);
        entry.children?.forEach(visit);
      };
      visit(tree);
      setScanFolders(folders);
      setScanRoot((current) =>
        current && folders.some((folder) => folder.path === current)
          ? current
          : (folders[0]?.path ?? ""),
      );
    } catch {
      setScanFolders([]);
      setScanRoot("");
    }
  }, [library.id, library.roots]);

  useEffect(() => {
    void loadScanFolders();
  }, [loadScanFolders]);

  async function saveRoots(next: string[]): Promise<boolean> {
    if (savingRootsRef.current) return false;
    savingRootsRef.current = true;
    setSavingRoots(true);
    setRootsError("");
    try {
      await setLibraryRoots(library.id, next);
      setSavedRoots(next);
      onChanged();
      return true;
    } catch (err) {
      setRootsError(err instanceof Error ? err.message : t("consoleConfigUnavailable"));
      return false;
    } finally {
      savingRootsRef.current = false;
      setSavingRoots(false);
    }
  }

  // Reports false when the folder was not saved, so the folder browser stays
  // open with the selection instead of closing as if it had been added.
  async function addRootPath(path: string): Promise<boolean> {
    const root = path.trim();
    if (!root || roots.includes(root)) return true;
    const saved = await saveRoots([...roots, root]);
    if (saved) setNewRoot("");
    return saved;
  }

  async function addRoot(e: FormEvent) {
    e.preventDefault();
    await addRootPath(newRoot);
  }

  async function togglePreview(enabled: boolean) {
    await setLibraryPreviewClips(library.id, enabled);
    onChanged();
  }

  async function toggleSkipDetection(enabled: boolean) {
    await setSkipDetectionEnabled(library.id, enabled);
    onChanged();
  }

  async function scan(e: FormEvent) {
    e.preventDefault();
    if (!scanRoot.trim()) return;
    setScanError("");
    try {
      await scanLibrary(library.id, scanRoot.trim());
    } catch (err) {
      setScanError(err instanceof Error ? err.message : t("scanFailed"));
    }
  }

  const watching = watcher?.state === "watching";
  return (
    <article aria-labelledby={`library-${library.id}`} className={CARD}>
      <header className="flex flex-wrap items-start gap-3 border-b border-white/[0.07] p-4">
        <span className={ICON_TILE}>
          <LibraryBig aria-hidden className="size-5" />
        </span>
        <div className="min-w-0 flex-1 space-y-1">
          <h3 id={`library-${library.id}`} className="m-0 text-base font-semibold leading-tight">
            {library.title}
          </h3>
          <p className="m-0 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
            <span>
              {plugin ? displayString(plugin.displayName, displayLocale) : t("libraryNoKind")}
            </span>
            <span aria-hidden>·</span>
            <span>{t("libraryFolderCount", { count: roots.length })}</span>
            {watcher && (
              <>
                <span aria-hidden>·</span>
                <span
                  title={watcher.reason || undefined}
                  className={cn("inline-flex items-center gap-1.5", watching && "text-emerald-300")}
                >
                  <span
                    aria-hidden
                    className={cn(
                      "size-1.5 rounded-full",
                      watching
                        ? "bg-emerald-400 shadow-[0_0_6px_var(--color-emerald-400)]"
                        : "bg-muted-foreground",
                    )}
                  />
                  {watching ? t("autoScanOn") : t("autoScanOff")}
                </span>
              </>
            )}
          </p>
        </div>
        <div className="flex shrink-0 flex-wrap items-center gap-2">
          <Button asChild size="sm" variant="ghost">
            <Link to="/manage/unmatched">
              <SearchCheck className="console-nav-icon" aria-hidden />
              {t("fixUnmatched")}
            </Link>
          </Button>
          <RescanButton libraryID={library.id} roots={roots} />
        </div>
      </header>

      <div className="space-y-4 p-4">
        <RescanStatus libraryID={library.id} hasRoots={roots.length > 0} />
        <div className="grid gap-4 md:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
          <section aria-labelledby={`folders-${library.id}`} className="space-y-2">
            <h4
              id={`folders-${library.id}`}
              className="m-0 text-xs font-semibold uppercase tracking-wide text-muted-foreground"
            >
              {t("libraryFolders")}
            </h4>
            {roots.length > 0 && (
              <ul className="m-0 flex list-none flex-col gap-1.5 p-0">
                {roots.map((root) => (
                  <li key={root} className={cn(PANEL, "flex min-w-0 items-center gap-3 py-2")}>
                    <Folder aria-hidden className="size-4 shrink-0 text-primary" />
                    <span className="min-w-0 flex-1" title={root}>
                      <span className="block truncate text-sm font-medium">
                        {lastSegment(root)}
                      </span>
                      <span className="block truncate font-mono text-[11px] text-muted-foreground">
                        {root}
                      </span>
                    </span>
                    <ConfirmButton
                      variant="ghost"
                      size="sm"
                      confirm="confirmRemoveFolder"
                      actionLabel={t("remove")}
                      disabled={savingRoots}
                      onConfirm={async () => {
                        await saveRoots(roots.filter((item) => item !== root));
                      }}
                      label={
                        <>
                          <X aria-hidden className="size-4" />
                          <span className="sr-only">{t("removeFolder", { path: root })}</span>
                        </>
                      }
                    >
                      <span className="block break-all font-mono text-xs">{root}</span>
                    </ConfirmButton>
                  </li>
                ))}
              </ul>
            )}
            <form onSubmit={addRoot}>
              <PathPickerField
                id={`roots-${library.id}`}
                label={<span className="sr-only">{t("addFolderPath")}</span>}
                role="libraryRoot"
                value={newRoot}
                libraryID={library.id}
                placeholder={t("addFolderPath")}
                onChange={setNewRoot}
                inputClassName="min-w-0 flex-1 basis-48"
                selectLabel={t("pathPickerAddThis")}
                selectDisabled={savingRoots}
                onSelect={addRootPath}
                actions={
                  <Button
                    type="submit"
                    size="sm"
                    variant="outline"
                    disabled={savingRoots || !newRoot.trim()}
                  >
                    <FolderPlus className="console-nav-icon" aria-hidden />
                    {t("addFolder")}
                  </Button>
                }
              />
            </form>
            <ErrorLine message={rootsError} />
          </section>

          <section aria-labelledby={`options-${library.id}`} className="space-y-1">
            <h4
              id={`options-${library.id}`}
              className="m-0 text-xs font-semibold uppercase tracking-wide text-muted-foreground"
            >
              {t("libraryOptions")}
            </h4>
            <div className={cn(PANEL, "divide-y divide-white/[0.06] py-1")}>
              <SettingRow
                label={t("previewClips")}
                help={t("previewClipsHelp")}
                checked={library.previewClips}
                onChange={(c) => void togglePreview(c)}
              />
              <SettingRow
                label={t("skipDetection")}
                help={t("skipDetectionHelp")}
                checked={skipDetectionEnabled}
                onChange={(c) => void toggleSkipDetection(c)}
              />
            </div>
          </section>
        </div>

        <details className="console-disclosure">
          <summary>{t("scanOneFolder")}</summary>
          <form onSubmit={scan} className="console-disclosure-body flex items-end gap-2">
            <div className="flex-1 space-y-1.5">
              <Label htmlFor={`scan-${library.id}`}>{t("folderToScan")}</Label>
              <Select value={scanRoot} onValueChange={setScanRoot}>
                <SelectTrigger id={`scan-${library.id}`}>
                  <SelectValue placeholder={t("selectFolderToScan")} />
                </SelectTrigger>
                <SelectContent>
                  {scanFolders.map((folder) => (
                    <SelectItem key={folder.path} value={folder.path}>
                      {folder.path}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <Button type="submit" size="sm" disabled={!scanRoot.trim()}>
              <ScanLine className="console-nav-icon" aria-hidden />
              {t("scan")}
            </Button>
          </form>
          <ErrorLine message={scanError} />
        </details>
      </div>
      <footer className="flex justify-end border-t border-white/[0.07] px-4 py-3">
        <DeleteLibraryButton library={library} onDeleted={onChanged} />
      </footer>
    </article>
  );
}

// Add-library panel: shown on demand (or when there are no libraries yet).
function AddLibrary({
  plugins,
  onCreated,
  onCancel,
}: {
  plugins: PluginSummary[];
  onCreated: () => Promise<void>;
  onCancel?: () => void;
}) {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const [title, setTitle] = useState("");
  const [pluginID, setPluginID] = useState(NO_PLUGIN);

  async function onCreate(e: FormEvent) {
    e.preventDefault();
    if (!title.trim()) return;
    await createLibrary(title.trim(), pluginID === NO_PLUGIN ? null : pluginID);
    setTitle("");
    setPluginID(NO_PLUGIN);
    await onCreated();
  }

  return (
    <form
      onSubmit={onCreate}
      aria-labelledby="add-library-heading"
      className={cn(CARD, "flex flex-wrap items-end gap-4 p-4 ring-1 ring-primary/20")}
    >
      <div className="flex w-full items-center gap-3">
        <span className={ICON_TILE}>
          <FolderPlus aria-hidden className="size-5" />
        </span>
        <div>
          <h3 id="add-library-heading" className="m-0 text-base font-semibold">
            {t("taskAddLibrary")}
          </h3>
          <p className="m-0 text-xs text-muted-foreground">{t("taskAddLibraryLead")}</p>
        </div>
      </div>
      <div className="min-w-48 flex-1 space-y-1.5">
        <Label htmlFor="new-lib-title">{t("libraryName")}</Label>
        <Input id="new-lib-title" value={title} onChange={(e) => setTitle(e.target.value)} />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="new-lib-plugin">{t("libraryKind")}</Label>
        <Select value={pluginID} onValueChange={setPluginID}>
          <SelectTrigger id="new-lib-plugin" className="w-52">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={NO_PLUGIN}>{t("libraryNoKind")}</SelectItem>
            {plugins.map((p) => (
              <SelectItem key={p.id} value={p.id}>
                {displayString(p.displayName, displayLocale)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <div className="flex gap-2">
        {onCancel && (
          <Button type="button" variant="ghost" onClick={onCancel}>
            {t("cancel")}
          </Button>
        )}
        <Button type="submit" disabled={!title.trim()}>
          <Plus className="console-nav-icon" aria-hidden />
          {t("createLibrary")}
        </Button>
      </div>
    </form>
  );
}

// Libraries (ADR-0003): the manageable Library list with create + per-Library
// folders / options / scan controls, and the plugin binding at creation.
export function Libraries() {
  const t = useT();
  const libs = useAsync(fetchLibraries);
  const plugins = useAsync(fetchPlugins);
  const skipDetection = useAsync(fetchSkipDetectionEnabled);
  const watchers = useAsync(fetchWatcherStatus);
  const [adding, setAdding] = useState(false);

  // Watcher state follows roots, so it reloads with the library list.
  async function refreshLibraries() {
    await Promise.all([libs.refresh(), watchers.refresh()]);
  }

  const pluginList = plugins.data ?? [];
  const libraryList = libs.data ?? [];
  // Only the first load shows a skeleton. A later refresh (every folder save
  // starts one) keeps the cards mounted, so it cannot discard a path typed in
  // another card or a library name typed in the add panel.
  const firstLoad = libs.loading && !libs.data;
  const empty = !firstLoad && !libs.failed && libraryList.length === 0;
  const skipDetectionByLibrary = new Map(
    (skipDetection.data ?? []).map((info) => [info.libraryID, info.enabled]),
  );

  return (
    <Section
      heading="libraries"
      icon={LibraryBig}
      lead="librariesLead"
      actions={
        <>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => void refreshLibraries()}
            aria-label={t("refresh")}
            title={t("refresh")}
          >
            <RefreshCw className="console-nav-icon" aria-hidden />
          </Button>
          <RescanAllButton libraries={libraryList} />
          {!adding && !empty && (
            <Button size="sm" onClick={() => setAdding(true)}>
              <Plus className="console-nav-icon" aria-hidden />
              {t("taskAddLibrary")}
            </Button>
          )}
        </>
      }
    >
      {(adding || empty) && (
        <AddLibrary
          plugins={pluginList}
          onCancel={empty ? undefined : () => setAdding(false)}
          onCreated={async () => {
            setAdding(false);
            await refreshLibraries();
          }}
        />
      )}

      {firstLoad ? (
        <Skeleton className="h-40 w-full" />
      ) : libs.failed ? (
        <ErrorState message="consoleConfigUnavailable" />
      ) : empty ? (
        <EmptyState message="libraryEmptyLead" icon={Boxes} />
      ) : (
        <div className="grid gap-4">
          {libraryList.map((library) => (
            <LibraryCard
              key={library.id}
              library={library}
              plugins={pluginList}
              skipDetectionEnabled={skipDetectionByLibrary.get(library.id) ?? false}
              watcher={watchers.data?.find((w) => w.libraryID === library.id)}
              onChanged={() => {
                void refreshLibraries();
                void skipDetection.refresh();
              }}
            />
          ))}
        </div>
      )}
    </Section>
  );
}
