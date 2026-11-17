import { ChevronLeft, ChevronRight, Folder, FolderTree, LayoutGrid, List } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { Button } from "@/components/ui/button";
import { useT } from "@/lib/i18n";
import { nodePath } from "@/lib/nodePath";
import { MediaItemCard } from "../components/MediaItemCard";
import { ViewerActions } from "../components/ViewerActions";
import { repeatedTitles, showsYear } from "../lib/cardTitles";
import { type LibraryViewMode, writeLibraryViewMode } from "../lib/libraryBrowse";
import {
  fetchLibraryFolder,
  folderPath,
  folderSearch,
  LIBRARY_FOLDER_PAGE,
  type LibraryFolderPage,
} from "../lib/libraryFolders";
import { playQueueOf } from "../lib/watchQueue";

// Library folder view (/n/:nodeId/all?view=folders, CORE-2359): the Library's
// roots, then the subfolders and Media of the open folder, one level at a time.
// Each folder is a link, so Back walks up the way the viewer came down and a
// folder can be shared like any other view.
export function LibraryFolders({
  libraryID,
  nodeID,
  heading,
}: {
  libraryID: string;
  nodeID: string;
  heading: string;
}) {
  const t = useT();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const path = folderPath(searchParams);
  const [page, setPage] = useState<LibraryFolderPage | null>(null);
  const [failed, setFailed] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    setPage(null);
    setFailed(false);
    fetchLibraryFolder(
      { libraryID, path, limit: LIBRARY_FOLDER_PAGE, offset: 0 },
      controller.signal,
    )
      .then(setPage)
      .catch(() => {
        if (!controller.signal.aborted) setFailed(true);
      });
    return () => controller.abort();
  }, [libraryID, path, retryTick]);

  async function loadMore() {
    if (!page) return;
    setLoadingMore(true);
    try {
      const next = await fetchLibraryFolder({
        libraryID,
        path,
        limit: LIBRARY_FOLDER_PAGE,
        offset: page.items.length,
      });
      setPage((current) =>
        current && current.path === next.path
          ? { ...current, total: next.total, items: [...current.items, ...next.items] }
          : current,
      );
    } catch {
      setFailed(true);
    } finally {
      setLoadingMore(false);
    }
  }

  function chooseViewMode(mode: LibraryViewMode) {
    writeLibraryViewMode(libraryID, mode);
    navigate({ search: "" });
  }

  const items = useMemo(() => page?.items ?? [], [page]);
  const queue = useMemo(() => playQueueOf(items), [items]);
  const repeated = useMemo(() => repeatedTitles(items), [items]);
  const crumbs = page?.crumbs ?? [];
  const openName = crumbs.at(-1)?.name ?? t("libraryFoldersAll");
  const empty = page !== null && page.folders.length === 0 && page.total === 0;

  return (
    <AppShell actions={<ViewerActions />}>
      <div className="flex flex-col gap-5">
        <div className="flex flex-col items-start gap-1">
          <Button
            variant="ghost"
            size="sm"
            className="-ml-2"
            onClick={() => navigate(nodePath(nodeID))}
          >
            <ChevronLeft aria-hidden />
            {t("back")}
          </Button>
          <div className="flex w-full flex-wrap items-center gap-x-3 gap-y-3">
            <h1 className="bf-page-title">{heading}</h1>
            <p role="status" className="text-sm text-[var(--text-muted)]">
              {page === null
                ? failed
                  ? ""
                  : t("loading")
                : page.path
                  ? t("libraryBrowseCount", { count: page.total })
                  : ""}
            </p>
            <div
              role="group"
              aria-label={t("libraryBrowseViewLabel")}
              className="flex items-center gap-2 sm:ml-auto"
            >
              <button
                type="button"
                className="bf-chip"
                aria-pressed={false}
                onClick={() => chooseViewMode("grid")}
              >
                <LayoutGrid aria-hidden className="size-4" />
                {t("libraryBrowseViewGrid")}
              </button>
              <button
                type="button"
                className="bf-chip"
                aria-pressed={false}
                onClick={() => chooseViewMode("list")}
              >
                <List aria-hidden className="size-4" />
                {t("libraryBrowseViewList")}
              </button>
              <button type="button" className="bf-chip" aria-pressed>
                <FolderTree aria-hidden className="size-4" />
                {t("libraryBrowseViewFolders")}
              </button>
            </div>
          </div>
        </div>

        <nav aria-label={t("libraryFoldersPathLabel")}>
          <ol className="bf-crumbs">
            {/* The root list is a step back only when it lists more than one root. */}
            {(page ? page.rootCount > 1 || !page.path : !path) && (
              <Crumb
                label={t("libraryFoldersAll")}
                search={folderSearch()}
                current={page ? !page.path : !path}
              />
            )}
            {crumbs.map((crumb, index) => (
              <Crumb
                key={crumb.path}
                label={crumb.name}
                search={folderSearch(crumb.path)}
                current={index === crumbs.length - 1}
              />
            ))}
          </ol>
        </nav>

        {failed && (
          <div role="alert" className="bf-empty flex flex-col gap-3">
            <p className="text-sm text-[var(--text-secondary)]">{t("libraryFoldersLoadFailed")}</p>
            <Button variant="outline" onClick={() => setRetryTick((tick) => tick + 1)}>
              {t("jobRetryAction")}
            </Button>
          </div>
        )}
        {empty && !failed && (
          <div role="status" className="bf-empty">
            <p className="text-sm text-[var(--text-secondary)]">{t("libraryFoldersEmpty")}</p>
          </div>
        )}

        {page && page.folders.length > 0 && (
          <ul aria-label={t("libraryFoldersListLabel")} className="bf-folder-list">
            {page.folders.map((folder) => (
              <li key={folder.path}>
                <Link to={{ search: folderSearch(folder.path) }} className="bf-folder">
                  <Folder aria-hidden className="size-5 shrink-0" />
                  <span className="bf-folder-name">{folder.name}</span>
                  <span className="bf-folder-count">
                    {t("libraryBrowseCount", { count: folder.mediaCount })}
                  </span>
                  <ChevronRight aria-hidden className="size-4 shrink-0" />
                </Link>
              </li>
            ))}
          </ul>
        )}

        {items.length > 0 && page && (
          <ul aria-label={openName} className="bf-grid">
            {items.map((item, index) => (
              <MediaItemCard
                key={item.id}
                item={item}
                queue={queue}
                posInSet={index + 1}
                setSize={page.total}
                showYear={showsYear(item, repeated)}
              />
            ))}
          </ul>
        )}
        {page && items.length < page.total && (
          <Button
            variant="outline"
            className="self-center"
            disabled={loadingMore}
            onClick={() => void loadMore()}
          >
            {t("libraryFoldersLoadMore")}
          </Button>
        )}
      </div>
    </AppShell>
  );
}

function Crumb({ label, search, current }: { label: string; search: string; current: boolean }) {
  return (
    <li>
      {current ? (
        <span aria-current="page" className="bf-crumb">
          {label}
        </span>
      ) : (
        <Link to={{ search }} className="bf-crumb">
          {label}
        </Link>
      )}
    </li>
  );
}
