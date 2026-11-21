import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { EmptyLine, ErrorLine, ManageSection } from "@/components/beamfall/workbench";
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
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  dismissUnmatched,
  fetchLibraries,
  fetchMediaFiles,
  fetchUnmatchedQueuePages,
  runHealthBulkOp,
} from "@/lib/graphql";
import type { LibraryInfo, UnmatchedItemInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { GroupFixPanel } from "./unmatched/GroupFixPanel";
import { folderHint, groupUnmatched } from "./unmatched/groups";
import { RecordInbox } from "./unmatched/RecordInbox";
import { groupProblem } from "./unmatched/reasons";

// Unmatched records (ADR-0003 §4, ADR-0037 §2.3, CORE-2910): records the
// pipeline could not auto-match, grouped so that items failing the same way
// against the same guess are fixed together. Each group says in plain words
// what went wrong and opens on candidates already searched with its folder's
// title and year.
const PATH_WORKERS = 4;

export function UnmatchedPage() {
  const t = useT();
  const [items, setItems] = useState<UnmatchedItemInfo[]>([]);
  const [libraries, setLibraries] = useState<LibraryInfo[]>([]);
  const [libraryID, setLibraryID] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [activeKey, setActiveKey] = useState("");
  // The first file path of each group's first item, keyed by mediaID: it
  // drives the folder hint and the naming panel.
  const [paths, setPaths] = useState<Record<string, string>>({});
  // Lookups waiting to start, shared by every worker so the open group can
  // move to the front; ids already started; workers running.
  const pathQueue = useRef<string[]>([]);
  const pathsStarted = useRef(new Set<string>());
  const pathWorkers = useRef(0);
  // False when the queue holds more rows than one load reads.
  const [complete, setComplete] = useState(true);
  // True while a panel batch writes: switching groups or starting another
  // batch then would race the reload that follows it.
  const [writing, setWriting] = useState(false);

  const groups = useMemo(() => groupUnmatched(items), [items]);
  const active = groups.find((group) => group.key === activeKey) ?? groups[0];

  // Only the latest load applies its answer: an older one finishing late
  // would put back rows a write has since changed.
  const loads = useRef(0);
  const load = useCallback(async () => {
    const mine = ++loads.current;
    setLoading(true);
    try {
      const [queue, libs] = await Promise.all([fetchUnmatchedQueuePages(), fetchLibraries()]);
      if (mine !== loads.current) return true;
      setItems(queue.items);
      setComplete(queue.complete);
      setLibraries(libs);
      setLibraryID((current) => current || libs[0]?.id || "");
      return true;
    } catch {
      if (mine !== loads.current) return true;
      setError(t("unmatchedUnavailable"));
      return false;
    } finally {
      if (mine === loads.current) setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void load();
  }, [load]);

  // Paths are a hint, never a requirement: a group whose file lookup fails
  // still renders and fixes, it just has no folder suggestion. Lookups run a
  // few at a time, the open group first, and each path shows as it arrives.
  const activeMediaID = active?.items[0]?.mediaID;
  useEffect(() => {
    const queue = pathQueue.current;
    const started = pathsStarted.current;
    for (const group of groups) {
      const id = group.items[0]?.mediaID;
      if (id && !(id in paths) && !started.has(id) && !queue.includes(id)) queue.push(id);
    }
    if (activeMediaID && queue.includes(activeMediaID)) {
      queue.splice(queue.indexOf(activeMediaID), 1);
      queue.unshift(activeMediaID);
    }
    const worker = async () => {
      pathWorkers.current++;
      for (let id = queue.shift(); id; id = queue.shift()) {
        started.add(id);
        let path = "";
        try {
          path = (await fetchMediaFiles(id))[0]?.path ?? "";
        } catch {
          // Leave the path empty: the group renders without a folder hint.
        }
        const found = path;
        const key = id;
        setPaths((prev) => (key in prev ? prev : { ...prev, [key]: found }));
      }
      pathWorkers.current--;
    };
    while (pathWorkers.current < PATH_WORKERS && queue.length > 0) void worker();
  }, [groups, paths, activeMediaID]);

  // After a write the rows on screen are stale. If the reload fails they are
  // dropped, not left actionable, so no second batch runs against them.
  const reloadAfterWrite = async () => {
    if (!(await load())) {
      setItems([]);
      setComplete(true);
    }
    setWriting(false);
  };

  const onFinished = async (message: string) => {
    setError("");
    setNotice("");
    await reloadAfterWrite();
    if (message) setNotice(message);
  };

  // Clear queue is a destructive batch: it is confirm-gated, and because the
  // server may have dismissed some rows before one call failed, the list is
  // reloaded from the server afterwards instead of trusting local state.
  const onClearQueue = async () => {
    setError("");
    setWriting(true);
    const results = await Promise.allSettled(items.map((item) => dismissUnmatched(item.mediaID)));
    await reloadAfterWrite();
    if (results.some((r) => r.status === "rejected")) setError(t("unmatchedUnavailable"));
  };

  // A rescan changes the queue like any batch, so it holds the same lock and
  // the same reload-or-drop rule.
  const onRescan = async () => {
    if (!libraryID) return;
    setError("");
    setNotice("");
    setWriting(true);
    let message = "";
    try {
      const result = await runHealthBulkOp("rescanUnmatched", libraryID);
      message = `${result.affected} ${t("bulkOpsAffected")}`;
    } catch {
      setError(t("unmatchedUnavailable"));
    }
    await reloadAfterWrite();
    if (message) setNotice(message);
  };

  const activePath = active ? paths[active.items[0]?.mediaID ?? ""] : undefined;

  return (
    <div className="space-y-4">
      <ManageSection
        actions={
          <div className="flex flex-wrap items-center gap-2">
            {libraries.length > 0 && (
              <Select value={libraryID} onValueChange={setLibraryID}>
                <SelectTrigger className="w-44" aria-label={t("library")}>
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
            )}
            <Button
              variant="outline"
              size="sm"
              disabled={!libraryID || writing}
              onClick={() => void onRescan()}
            >
              {t("unmatchedRescan")}
            </Button>
            <AlertDialog>
              <AlertDialogTrigger asChild>
                <Button variant="ghost" size="sm" disabled={items.length === 0 || writing}>
                  {t("unmatchedClearQueue")}
                </Button>
              </AlertDialogTrigger>
              <AlertDialogContent>
                <AlertDialogHeader>
                  <AlertDialogTitle>{t("unmatchedClearQueue")}</AlertDialogTitle>
                  <AlertDialogDescription>
                    {t("unmatchedGroupItems", { count: items.length })}. {t("unmatchedHideBody")}
                  </AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                  <AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
                  <AlertDialogAction
                    onClick={() => void onClearQueue()}
                    className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
                  >
                    {t("unmatchedClearQueue")}
                  </AlertDialogAction>
                </AlertDialogFooter>
              </AlertDialogContent>
            </AlertDialog>
          </div>
        }
      >
        <ErrorLine message={error} />
        {notice && (
          <p className="mb-2 text-sm text-muted-foreground" role="status">
            {notice}
          </p>
        )}
        {!complete && (
          <p className="mb-2 text-sm text-muted-foreground" role="status">
            {t("unmatchedTruncated", { count: items.length })}
          </p>
        )}
        {loading && items.length === 0 ? (
          <EmptyLine message={t("loading")} />
        ) : groups.length === 0 ? (
          !error && <EmptyLine message={t("unmatchedEmpty")} />
        ) : (
          <div className="grid gap-3 lg:grid-cols-[minmax(220px,0.8fr)_minmax(320px,1.4fr)]">
            <ul className="divide-y self-start rounded-md border border-border bg-card">
              {groups.map((group) => {
                const path = paths[group.items[0]?.mediaID ?? ""];
                const hint = folderHint(path);
                const name =
                  group.candidateTitle || hint?.title || group.items[0]?.title || group.key;
                return (
                  <li key={group.key}>
                    <button
                      type="button"
                      className="grid w-full gap-1 px-3 py-2 text-left hover:bg-muted/50 data-[active=true]:bg-muted"
                      data-active={active?.key === group.key}
                      aria-current={active?.key === group.key ? "true" : undefined}
                      disabled={writing}
                      onClick={() => setActiveKey(group.key)}
                    >
                      <span className="flex items-start justify-between gap-2">
                        <span className="min-w-0 break-words font-medium text-foreground">
                          {name}
                          {hint?.year ? ` (${hint.year})` : ""}
                        </span>
                        <Badge variant="secondary" className="shrink-0">
                          {t("unmatchedGroupItems", { count: group.items.length })}
                        </Badge>
                      </span>
                      <span className="text-xs text-muted-foreground">
                        {groupProblem(t, group, hint).why}
                      </span>
                    </button>
                  </li>
                );
              })}
            </ul>

            {/* The panel waits for its folder path so it opens already searched
                with the folder's title and year, not a guess it then replaces. */}
            {active && activePath === undefined && <EmptyLine message={t("loading")} />}
            {active && activePath !== undefined && (
              <GroupFixPanel
                key={active.key}
                group={active}
                hint={folderHint(activePath)}
                path={activePath}
                locked={writing}
                onWriteStart={() => setWriting(true)}
                onFinished={(message) => void onFinished(message)}
                onError={setError}
              />
            )}
          </div>
        )}
      </ManageSection>
      <RecordInbox libraryID={libraryID} />
    </div>
  );
}
