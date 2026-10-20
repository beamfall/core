import { Boxes, Plus, RefreshCw, Save, ScanLine } from "lucide-react";
import { type FormEvent, useCallback, useEffect, useState } from "react";
import {
  EmptyState,
  ErrorLine,
  ErrorState,
  PathPickerField,
  StatusChip,
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
  scanLibrary,
  setLibraryPreviewClips,
  setLibraryRoots,
  setSkipDetectionEnabled,
} from "@/lib/graphql";
import type { FolderEntryInfo, LibraryInfo, PluginSummary } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { Section } from "../Section";
import { useAsync } from "../useAsync";

const NO_PLUGIN = "__none__";

// One editable Library card: roots (one folder per line), the preview-clips
// toggle, the skip-segment detection toggle (ENR-7, ADR-0048 Decision 3/6, off
// by default), an ad-hoc scan trigger, and the plugin binding (display-only —
// the binding is set at creation). Each control writes through its own
// mutation.
function LibraryCard({
  library,
  plugins,
  skipDetectionEnabled,
  onChanged,
}: {
  library: LibraryInfo;
  plugins: PluginSummary[];
  skipDetectionEnabled: boolean;
  onChanged: () => void;
}) {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const [roots, setRoots] = useState((library.roots ?? []).join("\n"));
  const [newRoot, setNewRoot] = useState("");
  const [scanRoot, setScanRoot] = useState("");
  const [scanFolders, setScanFolders] = useState<FolderEntryInfo[]>([]);
  const [scanError, setScanError] = useState("");
  const plugin = plugins.find((p) => p.id === library.pluginID);
  const rootsList = roots
    .split(/\n+/)
    .map((r) => r.trim())
    .filter(Boolean);

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

  async function saveRoots() {
    await setLibraryRoots(library.id, rootsList);
    onChanged();
  }

  function addRoot() {
    const root = newRoot.trim();
    if (!root || rootsList.includes(root)) return;
    setRoots([...rootsList, root].join("\n"));
    setNewRoot("");
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

  return (
    <fieldset className="console-fieldset">
      <div className="console-fieldset-head flex items-center justify-between gap-4">
        <div className="min-w-0">
          <legend className="console-fieldset-title">{library.title}</legend>
          <p className="console-fieldset-help">
            {plugin ? (
              <StatusChip tone="cyan" dot={false}>
                {displayString(plugin.displayName, displayLocale)}
              </StatusChip>
            ) : (
              t("none")
            )}
          </p>
        </div>
        <div className="flex shrink-0 flex-col items-end gap-1">
          <label className="flex items-center gap-2 text-sm">
            {t("previewClips")}
            <Switch checked={library.previewClips} onCheckedChange={(c) => void togglePreview(c)} />
          </label>
          <label className="flex items-center gap-2 text-sm">
            {t("skipDetection")}
            <Switch
              checked={skipDetectionEnabled}
              onCheckedChange={(c) => void toggleSkipDetection(c)}
            />
          </label>
        </div>
      </div>
      <div className="console-fieldset-body space-y-4">
        <div className="space-y-2">
          <PathPickerField
            id={`roots-${library.id}`}
            label={t("libraryRoots")}
            role="libraryRoot"
            value={newRoot}
            libraryID={library.id}
            placeholder={t("rootsPlaceholder")}
            onChange={setNewRoot}
          />
          {rootsList.length > 0 ? (
            <div className="grid gap-1">
              {rootsList.map((root) => (
                <div key={root} className="flex items-center gap-2 rounded border px-2 py-1">
                  <span className="min-w-0 flex-1 break-all text-xs">{root}</span>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    onClick={() => setRoots(rootsList.filter((item) => item !== root).join("\n"))}
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
              size="sm"
              variant="outline"
              disabled={!newRoot.trim()}
              onClick={addRoot}
            >
              {t("stagePathPickerRoot")}
            </Button>
            <Button size="sm" variant="outline" onClick={() => void saveRoots()}>
              <Save className="console-nav-icon" aria-hidden />
              {t("save")}
            </Button>
          </div>
        </div>

        <div>
          <form onSubmit={scan} className="flex items-end gap-2">
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
        </div>
      </div>
    </fieldset>
  );
}

// Libraries (ADR-0003): the manageable Library list with create + per-Library
// roots / preview-clips / scan controls, and the plugin binding at creation.
export function Libraries() {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const libs = useAsync(fetchLibraries);
  const plugins = useAsync(fetchPlugins);
  const skipDetection = useAsync(fetchSkipDetectionEnabled);
  const [title, setTitle] = useState("");
  const [pluginID, setPluginID] = useState(NO_PLUGIN);

  async function onCreate(e: FormEvent) {
    e.preventDefault();
    if (!title.trim()) return;
    await createLibrary(title.trim(), pluginID === NO_PLUGIN ? null : pluginID);
    setTitle("");
    setPluginID(NO_PLUGIN);
    await libs.refresh();
  }

  const pluginList = plugins.data ?? [];
  const skipDetectionByLibrary = new Map(
    (skipDetection.data ?? []).map((info) => [info.libraryID, info.enabled]),
  );

  return (
    <Section
      heading="libraries"
      actions={
        <Button variant="outline" size="sm" onClick={() => void libs.refresh()}>
          <RefreshCw className="console-nav-icon" aria-hidden />
          {t("refresh")}
        </Button>
      }
    >
      <form
        onSubmit={onCreate}
        className="console-card console-card-pad flex flex-wrap items-end gap-3"
      >
        <div className="space-y-1.5">
          <Label htmlFor="new-lib-title">{t("title")}</Label>
          <Input id="new-lib-title" value={title} onChange={(e) => setTitle(e.target.value)} />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="new-lib-plugin">{t("plugin")}</Label>
          <Select value={pluginID} onValueChange={setPluginID}>
            <SelectTrigger id="new-lib-plugin" className="w-48">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={NO_PLUGIN}>{t("none")}</SelectItem>
              {pluginList.map((p) => (
                <SelectItem key={p.id} value={p.id}>
                  {displayString(p.displayName, displayLocale)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <Button type="submit" disabled={!title.trim()}>
          <Plus className="console-nav-icon" aria-hidden />
          {t("createLibrary")}
        </Button>
      </form>

      {libs.loading ? (
        <Skeleton className="h-40 w-full" />
      ) : libs.failed ? (
        <ErrorState message="consoleConfigUnavailable" />
      ) : (libs.data ?? []).length === 0 ? (
        <EmptyState message="none" icon={Boxes} />
      ) : (
        <div className="grid gap-4 lg:grid-cols-2">
          {(libs.data ?? []).map((library) => (
            <LibraryCard
              key={library.id}
              library={library}
              plugins={pluginList}
              skipDetectionEnabled={skipDetectionByLibrary.get(library.id) ?? false}
              onChanged={() => {
                void libs.refresh();
                void skipDetection.refresh();
              }}
            />
          ))}
        </div>
      )}
    </Section>
  );
}
