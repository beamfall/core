import { useCallback, useEffect, useState } from "react";
import { Link, Navigate, useLocation, useParams } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { Button } from "@/components/ui/button";
import { displayString } from "@/lib/display";
import { type CollectionInfo, fetchCollections } from "@/lib/graphql";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { findNodeBySlug, nodePath, nodeSlug } from "@/lib/nodePath";
import { useSession } from "@/lib/session";
import { LibraryHeader } from "../components/LibraryPivots";
import { ViewerActions } from "../components/ViewerActions";
import { sortCollections } from "../lib/collections";
import {
  DEFAULT_BROWSE_STATE,
  fetchLibraryItems,
  type LibraryItemsGrouping,
} from "../lib/libraryBrowse";
import { libraryCategorySearch } from "../lib/libraryViews";

export type LibraryPivotName = "collections" | "categories";

// The Collections and Categories pivots of a Library page (CORE-2437), at
// /n/:nodeId/collections and /n/:nodeId/categories. The node resolves the way
// the Library browse does: a node with no Library goes back to its own page,
// and an old raw-ID address moves to the readable one.
export function LibraryPivotPage({ pivot }: { pivot: LibraryPivotName }) {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const { nodeId } = useParams();
  const location = useLocation();
  const { manifest, loading } = useSession();

  if (loading && !manifest) {
    return (
      <AppShell actions={<ViewerActions />}>
        <p className="text-sm text-[var(--text-muted)]">{t("loading")}</p>
      </AppShell>
    );
  }
  const node = findNodeBySlug(
    (manifest?.navigation || []).filter((entry) => entry.view === "layout"),
    nodeId,
  );
  if (!node?.libraryID) {
    return <Navigate to={node ? nodePath(node.id) : "/"} replace />;
  }
  if (nodeId !== nodeSlug(node.id)) {
    return (
      <Navigate
        to={{ pathname: nodePath(node.id, `/${pivot}`), search: location.search }}
        replace
      />
    );
  }
  return (
    <AppShell actions={<ViewerActions />}>
      <div className="flex flex-col gap-5">
        <LibraryHeader nodeID={node.id} heading={displayString(node.title, displayLocale)} />
        {pivot === "collections" ? (
          <LibraryCollections key={node.libraryID} libraryID={node.libraryID} />
        ) : (
          <LibraryCategories key={node.libraryID} libraryID={node.libraryID} nodeID={node.id} />
        )}
      </div>
    </AppShell>
  );
}

/** A read that can fail and be retried: null while loading. */
function useRetryableRead<T>(read: () => Promise<T>) {
  const [value, setValue] = useState<T | null>(null);
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let live = true;
    setValue(null);
    setFailed(false);
    read()
      .then((result) => {
        if (live) setValue(result);
      })
      .catch(() => {
        if (live) setFailed(true);
      });
    return () => {
      live = false;
    };
  }, [read, attempt]);
  const retry = useCallback(() => setAttempt((n) => n + 1), []);
  return { value, failed, retry };
}

function LoadFailed({ message, onRetry }: { message: string; onRetry: () => void }) {
  const t = useT();
  return (
    <div role="alert" className="bf-empty flex flex-col gap-3">
      <p className="text-sm text-[var(--text-secondary)]">{message}</p>
      <Button variant="outline" className="self-start" onClick={onRetry}>
        {t("jobRetryAction")}
      </Button>
    </div>
  );
}

// Only this Library's Collections, in the household page's curated order, each
// opening the existing Collection page. Creating one stays on the household
// Collections page, linked below.
function LibraryCollections({ libraryID }: { libraryID: string }) {
  const t = useT();
  const read = useCallback(
    async (): Promise<CollectionInfo[]> => sortCollections(await fetchCollections(libraryID)),
    [libraryID],
  );
  const { value: collections, failed, retry } = useRetryableRead(read);

  return (
    <section aria-label={t("libraryPivotCollections")} className="flex flex-col gap-4">
      {collections === null && !failed && (
        <p role="status" className="text-sm text-[var(--text-muted)]">
          {t("loading")}
        </p>
      )}
      {failed && <LoadFailed message={t("viewerCollectionsLoadFailed")} onRetry={retry} />}
      {collections?.length === 0 && (
        <p role="status" className="bf-empty text-sm text-[var(--text-secondary)]">
          {t("libraryCollectionsEmpty")}
        </p>
      )}
      {collections && collections.length > 0 && (
        <ul className="bf-tile-grid">
          {collections.map((collection) => (
            <li key={collection.id}>
              <Link
                className="bf-tile"
                to={`/collections/${encodeURIComponent(libraryID)}/${encodeURIComponent(collection.id)}`}
              >
                <span>{collection.name}</span>
                <span className="bf-tile-meta">
                  {t("viewerCollectionsItemCount", { count: collection.memberCount })}
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
      <Link className="self-start text-sm text-[var(--text-secondary)] underline" to="/collections">
        {t("libraryCollectionsAll")}
      </Link>
    </section>
  );
}

// One tile per value of each grouping the Library's plugin reports, before any
// filter, under the grouping's title. A tile opens the Library pivot's Media
// view filtered to that value.
function LibraryCategories({ libraryID, nodeID }: { libraryID: string; nodeID: string }) {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const read = useCallback(
    async (): Promise<LibraryItemsGrouping[]> =>
      (
        await fetchLibraryItems({ libraryID, state: DEFAULT_BROWSE_STATE, limit: 1, offset: 0 })
      ).facets.groupings.filter((grouping) => grouping.values.length > 0),
    [libraryID],
  );
  const { value: groupings, failed, retry } = useRetryableRead(read);

  return (
    <div className="flex flex-col gap-6">
      {groupings === null && !failed && (
        <p role="status" className="text-sm text-[var(--text-muted)]">
          {t("loading")}
        </p>
      )}
      {failed && <LoadFailed message={t("libraryCategoriesLoadFailed")} onRetry={retry} />}
      {groupings?.length === 0 && (
        <p role="status" className="bf-empty text-sm text-[var(--text-secondary)]">
          {t("libraryCategoriesEmpty")}
        </p>
      )}
      {groupings?.map((grouping) => {
        const title = displayString(grouping.title, displayLocale) || grouping.kind;
        return (
          <section key={grouping.kind} aria-label={title} className="flex flex-col gap-3">
            {groupings.length > 1 && <h2 className="bf-section-title">{title}</h2>}
            <ul className="bf-tile-grid">
              {grouping.values.map((value) => (
                <li key={value}>
                  <Link
                    className="bf-tile"
                    to={{
                      pathname: nodePath(nodeID, "/all"),
                      search: libraryCategorySearch(grouping.kind, value),
                    }}
                  >
                    {value}
                  </Link>
                </li>
              ))}
            </ul>
          </section>
        );
      })}
    </div>
  );
}
