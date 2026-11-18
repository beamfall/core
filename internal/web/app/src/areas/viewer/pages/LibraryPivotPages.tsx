import { type MouseEvent, useCallback, useEffect, useId, useMemo, useRef, useState } from "react";
import { Link, Navigate, useLocation, useParams, useSearchParams } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { displayString } from "@/lib/display";
import { type CollectionInfo, fetchCollections } from "@/lib/graphql";
import type { SourceItem } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { findNodeBySlug, nodePath, nodeSlug } from "@/lib/nodePath";
import { useSession } from "@/lib/session";
import { LibraryHeader } from "../components/LibraryPivots";
import { ViewerActions } from "../components/ViewerActions";
import { artworkBucketURL, artworkSrcSet } from "../lib/artwork";
import { sortCollections } from "../lib/collections";
import { DEFAULT_BROWSE_STATE, fetchLibraryItems } from "../lib/libraryBrowse";
import {
  type CategoryPlan,
  type CategoryValue,
  groupByInitial,
  planCategories,
  runBounded,
  takeCategoryArt,
} from "../lib/libraryCategories";
import { libraryCategorySearch } from "../lib/libraryViews";
import "./library-categories.css";

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

// The Categories pivot (CORE-2505). Featured groupings (genres) lead as
// artwork tiles with their item counts; a long-tail grouping (studios,
// networks) shows its most used values and a See all list, A-Z, at ?kind=.
// Jump links reach each grouping. A value opens the Library pivot's Media view
// filtered to it.
function LibraryCategories({ libraryID, nodeID }: { libraryID: string; nodeID: string }) {
  const t = useT();
  const [searchParams] = useSearchParams();
  const read = useCallback(
    async (): Promise<CategoryPlan[]> =>
      planCategories(
        (await fetchLibraryItems({ libraryID, state: DEFAULT_BROWSE_STATE, limit: 1, offset: 0 }))
          .facets.groupings,
      ),
    [libraryID],
  );
  const { value: plans, failed, retry } = useRetryableRead(read);
  const listed = plans?.find((plan) => plan.kind === searchParams.get("kind"));

  return (
    <div className="bf-categories flex flex-col gap-6">
      {plans === null && !failed && (
        <p role="status" className="text-sm text-[var(--text-muted)]">
          {t("loading")}
        </p>
      )}
      {failed && <LoadFailed message={t("libraryCategoriesLoadFailed")} onRetry={retry} />}
      {plans?.length === 0 && (
        <p role="status" className="bf-empty text-sm text-[var(--text-secondary)]">
          {t("libraryCategoriesEmpty")}
        </p>
      )}
      {listed && <CategoryIndex plan={listed} nodeID={nodeID} />}
      {plans && plans.length > 0 && !listed && (
        <CategoryOverview plans={plans} libraryID={libraryID} nodeID={nodeID} />
      )}
    </div>
  );
}

function groupingTitle(plan: CategoryPlan, locale: string): string {
  return displayString(plan.grouping.title, locale) || plan.kind;
}

function categoryLink(nodeID: string, kind: string, value: string) {
  return { pathname: nodePath(nodeID, "/all"), search: libraryCategorySearch(kind, value) };
}

function sectionID(kind: string): string {
  return `bf-category-${kind.replace(/[^\w-]/g, "_")}`;
}

/** Scrolls to an in-page heading and moves focus there, leaving history alone. */
function jumpTo(event: MouseEvent<HTMLAnchorElement>, id: string) {
  const target = document.getElementById(id);
  if (!target) return;
  event.preventDefault();
  target.scrollIntoView?.({ block: "start" });
  target.focus({ preventScroll: true });
}

const ART_READS_AT_ONCE = 4;
const artKey = (kind: string, value: string) => `${kind}\u0000${value}`;
const ART_CANDIDATES = 6;

/**
 * The backdrop for each featured value, read from its best rated items, four
 * reads at a time. A tile takes its art once every tile before it has settled,
 * so no two tiles show the same picture and none changes after it appears.
 */
function useCategoryArt(libraryID: string, plans: CategoryPlan[]): Map<string, string> {
  const featured = useMemo(
    () =>
      plans
        .filter((plan) => plan.featured)
        .flatMap((plan) => plan.values.map((entry) => ({ kind: plan.kind, value: entry.value }))),
    [plans],
  );
  const [settled, setSettled] = useState<(SourceItem[] | null | undefined)[]>([]);
  useEffect(() => {
    const controller = new AbortController();
    setSettled([]);
    void runBounded(
      featured.map(
        ({ kind, value }) =>
          async () =>
            (
              await fetchLibraryItems(
                {
                  libraryID,
                  state: {
                    ...DEFAULT_BROWSE_STATE,
                    sort: "audience-score",
                    direction: "desc",
                    groups: { [kind]: value },
                  },
                  limit: ART_CANDIDATES,
                  offset: 0,
                },
                controller.signal,
              )
            ).items,
      ),
      ART_READS_AT_ONCE,
      (index, items) =>
        setSettled((prev) => {
          const next = [...prev];
          next[index] = items;
          return next;
        }),
      controller.signal,
    );
    return () => controller.abort();
  }, [libraryID, featured]);

  return useMemo(() => {
    const art = new Map<string, string>();
    const used = new Set<string>();
    for (const [index, { kind, value }] of featured.entries()) {
      if (settled[index] === undefined) break;
      const url = takeCategoryArt(settled[index] ?? [], used);
      if (url) art.set(artKey(kind, value), url);
    }
    return art;
  }, [featured, settled]);
}

function CategoryOverview({
  plans,
  libraryID,
  nodeID,
}: {
  plans: CategoryPlan[];
  libraryID: string;
  nodeID: string;
}) {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const art = useCategoryArt(libraryID, plans);
  const headed = plans.length > 1;

  return (
    <>
      {headed && (
        <nav aria-label={t("libraryCategoriesJump")}>
          <ul className="bf-category-chips">
            {plans.map((plan) => (
              <li key={plan.kind}>
                <a
                  className="bf-chip"
                  href={`#${sectionID(plan.kind)}`}
                  onClick={(event) => jumpTo(event, sectionID(plan.kind))}
                >
                  {groupingTitle(plan, displayLocale)}
                </a>
              </li>
            ))}
          </ul>
        </nav>
      )}
      {plans.map((plan) => {
        const title = groupingTitle(plan, displayLocale);
        const id = sectionID(plan.kind);
        return (
          <section key={plan.kind} aria-label={title} className="flex flex-col gap-3">
            {(headed || !plan.featured) && (
              <div className="bf-category-head">
                <h2 id={id} tabIndex={-1} className="bf-section-title">
                  {title}
                </h2>
                {!plan.featured && (
                  <Link
                    className="bf-category-see-all"
                    to={{ search: `?kind=${encodeURIComponent(plan.kind)}` }}
                    aria-label={t("libraryCategoriesSeeAllLabel", {
                      count: plan.values.length,
                      title,
                    })}
                  >
                    {t("libraryCategoriesSeeAll", { count: plan.values.length })}
                  </Link>
                )}
              </div>
            )}
            {plan.featured ? (
              <ul className="bf-category-grid">
                {plan.values.map((entry) => (
                  <li key={entry.value}>
                    <CategoryCard
                      to={categoryLink(nodeID, plan.kind, entry.value)}
                      entry={entry}
                      art={art.get(artKey(plan.kind, entry.value)) ?? ""}
                    />
                  </li>
                ))}
              </ul>
            ) : (
              plan.top.length > 0 && (
                <ul className="bf-category-chips">
                  {plan.top.map((entry) => (
                    <li key={entry.value}>
                      <Link className="bf-chip" to={categoryLink(nodeID, plan.kind, entry.value)}>
                        {entry.value}
                        {entry.count !== undefined && (
                          <span className="bf-category-count">
                            {t("libraryCategoriesItemCount", { count: entry.count })}
                          </span>
                        )}
                      </Link>
                    </li>
                  ))}
                </ul>
              )
            )}
          </section>
        );
      })}
    </>
  );
}

function CategoryCard({
  to,
  entry,
  art,
}: {
  to: ReturnType<typeof categoryLink>;
  entry: CategoryValue;
  art: string;
}) {
  const t = useT();
  const [broken, setBroken] = useState("");
  const shown = art && art !== broken ? art : "";
  return (
    <Link className="bf-category-card" to={to}>
      {shown && (
        <img
          className="bf-category-art"
          src={artworkBucketURL(shown, "w640")}
          srcSet={artworkSrcSet(shown, ["w320", "w640", "w1280"])}
          sizes="(max-width: 760px) 50vw, 320px"
          alt=""
          loading="lazy"
          decoding="async"
          onError={() => setBroken(shown)}
        />
      )}
      <span className="bf-category-label">
        <span className="bf-category-name">{entry.value}</span>
        {entry.count !== undefined && (
          <span className="bf-category-count">
            {t("libraryCategoriesItemCount", { count: entry.count })}
          </span>
        )}
      </span>
    </Link>
  );
}

/** Every value of one grouping, A-Z under its initials, with a filter. */
function CategoryIndex({ plan, nodeID }: { plan: CategoryPlan; nodeID: string }) {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const title = groupingTitle(plan, displayLocale);
  const [query, setQuery] = useState("");
  const groups = useMemo(() => groupByInitial(plan.values, query), [plan.values, query]);
  const sectionRef = useRef<HTMLElement>(null);
  const headingRef = useRef<HTMLHeadingElement>(null);
  const filterID = useId();
  // Entering from a scrolled overview keeps its scroll position, so bring the
  // list's top (back link, heading, filter) into view before focusing it.
  useEffect(() => {
    sectionRef.current?.scrollIntoView?.({ block: "start" });
    headingRef.current?.focus({ preventScroll: true });
  }, []);

  return (
    <section
      ref={sectionRef}
      aria-labelledby={sectionID(plan.kind)}
      className="bf-category-index flex flex-col gap-4"
    >
      <Link className="bf-category-back" to={{ search: "" }}>
        {t("libraryCategoriesBack")}
      </Link>
      <h2 id={sectionID(plan.kind)} ref={headingRef} tabIndex={-1} className="bf-section-title">
        {title}
      </h2>
      <Input
        id={filterID}
        type="search"
        className="bf-category-filter"
        aria-label={t("libraryCategoriesFilter", { title })}
        placeholder={t("libraryCategoriesFilter", { title })}
        value={query}
        onChange={(event) => setQuery(event.target.value)}
      />
      {groups.length > 1 && (
        <nav aria-label={t("libraryCategoriesLetters")}>
          <ul className="bf-category-letters">
            {groups.map((group) => (
              <li key={group.letter}>
                <a
                  className="bf-chip"
                  href={`#${sectionID(plan.kind)}-${group.letter === "#" ? "num" : group.letter}`}
                  onClick={(event) =>
                    jumpTo(
                      event,
                      `${sectionID(plan.kind)}-${group.letter === "#" ? "num" : group.letter}`,
                    )
                  }
                >
                  {group.letter}
                </a>
              </li>
            ))}
          </ul>
        </nav>
      )}
      {groups.length === 0 && (
        <p role="status" className="bf-empty text-sm text-[var(--text-secondary)]">
          {t("libraryCategoriesNoMatch", { query: query.trim() })}
        </p>
      )}
      {groups.map((group) => {
        const id = `${sectionID(plan.kind)}-${group.letter === "#" ? "num" : group.letter}`;
        return (
          <section key={group.letter} aria-labelledby={id} className="flex flex-col gap-2">
            <h3 id={id} tabIndex={-1} className="bf-category-letter">
              {group.letter}
            </h3>
            <ul className="bf-category-az">
              {group.values.map((entry) => (
                <li key={entry.value}>
                  <Link to={categoryLink(nodeID, plan.kind, entry.value)}>
                    <span className="bf-category-name">{entry.value}</span>
                    {entry.count !== undefined && (
                      <span className="bf-category-count">
                        {t("libraryCategoriesItemCount", { count: entry.count })}
                      </span>
                    )}
                  </Link>
                </li>
              ))}
            </ul>
          </section>
        );
      })}
    </section>
  );
}
