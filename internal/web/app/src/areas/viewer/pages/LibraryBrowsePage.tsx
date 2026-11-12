import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Navigate, useNavigate, useParams, useSearchParams } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { displayString } from "@/lib/display";
import type { SourceItem } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import type { StringKey } from "@/strings";
import { useEngagementEpoch } from "../actions";
import { MediaItemCard } from "../components/MediaItemCard";
import { RevealControls } from "../components/RevealControls";
import { useVirtualGrid, VIRTUAL_GRID_MIN_ITEMS } from "../components/useVirtualGrid";
import { ViewerActions } from "../components/ViewerActions";
import {
  browseStateParams,
  fetchLibraryItems,
  LIBRARY_BROWSE_PAGE,
  LIBRARY_MIN_RATINGS,
  LIBRARY_SORTS,
  LIBRARY_WATCH_STATES,
  type LibraryBrowseState,
  type LibraryItemsFacets,
  type LibrarySort,
  type LibraryViewMode,
  parseBrowseState,
  readLibraryViewMode,
  writeLibraryViewMode,
} from "../lib/libraryBrowse";
import { startPlayQueue } from "../lib/playQueue";
import { type PlayQueueItem, playQueueOf } from "../lib/watchQueue";

const ALL = "all";
// Grouping values are free text from the library, so their option values are
// prefixed to stay distinct from the "Any" option.
const GROUP_VALUE = "value:";
// A wide viewport shows more cards than the See All cap allows for.
const BROWSE_MAX_RENDERED = 240;

const SORT_LABELS: Record<LibrarySort, StringKey> = {
  title: "libraryBrowseSortTitle",
  year: "libraryBrowseSortYear",
  added: "libraryBrowseSortAdded",
  duration: "libraryBrowseSortDuration",
  "last-watched": "libraryBrowseSortLastWatched",
  rating: "libraryBrowseSortRating",
};
const WATCH_LABELS: Record<string, StringKey> = {
  unwatched: "libraryBrowseWatchUnwatched",
  "in-progress": "libraryBrowseWatchInProgress",
  watched: "libraryBrowseWatchWatched",
};
const QUALITY_LABELS: Record<string, StringKey> = {
  uhd: "libraryBrowseQualityUhd",
  hd1080: "libraryBrowseQualityHd1080",
  hd720: "libraryBrowseQualityHd720",
  sd: "libraryBrowseQualitySd",
};

const EMPTY_FACETS: LibraryItemsFacets = { groupings: [], decades: [], qualities: [] };

// Library browse (/n/:nodeId/all, CORE-1190): every item of one Library the
// profile may see, counted, sorted and filtered by the server (libraryItems)
// and windowed here. Sort and filters live in the URL so a view can be linked
// and survives Back; the grid/list choice is remembered per library.
export function LibraryBrowsePage() {
  const t = useT();
  const { nodeId } = useParams();
  const { manifest, loading } = useSession();

  if (loading && !manifest) {
    return (
      <AppShell actions={<ViewerActions />}>
        <p className="text-sm text-[var(--text-muted)]">{t("loading")}</p>
      </AppShell>
    );
  }
  const node = (manifest?.navigation || []).find(
    (entry) => entry.view === "layout" && entry.id === nodeId,
  );
  if (!node?.libraryID) {
    return <Navigate to={node ? `/n/${encodeURIComponent(node.id)}` : "/"} replace />;
  }
  return (
    <LibraryBrowse
      key={node.libraryID}
      libraryID={node.libraryID}
      nodeID={node.id}
      title={node.title}
    />
  );
}

function LibraryBrowse({
  libraryID,
  nodeID,
  title,
}: {
  libraryID: string;
  nodeID: string;
  title: Parameters<typeof displayString>[0];
}) {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const state = useMemo(() => parseBrowseState(searchParams), [searchParams]);
  const stateKey = browseStateParams(state).toString();
  const heading = displayString(title, displayLocale);

  const [viewMode, setViewMode] = useState<LibraryViewMode>(() => readLibraryViewMode(libraryID));

  const [total, setTotal] = useState<number | null>(null);
  const [facets, setFacets] = useState<LibraryItemsFacets>(EMPTY_FACETS);
  const [failed, setFailed] = useState(false);
  const [starting, setStarting] = useState(false);
  // Pages arrive out of order as the window moves; the map is keyed by page
  // index and `loadedTick` re-renders when one lands.
  const pagesRef = useRef(new Map<number, SourceItem[]>());
  const pendingRef = useRef(new Set<number>());
  const generationRef = useRef(0);
  const [, setLoadedTick] = useState(0);
  const [retryTick, setRetryTick] = useState(0);

  const loadPage = useCallback(
    (pageIndex: number) => {
      if (pagesRef.current.has(pageIndex) || pendingRef.current.has(pageIndex)) return;
      const generation = generationRef.current;
      pendingRef.current.add(pageIndex);
      fetchLibraryItems({
        libraryID,
        state,
        limit: LIBRARY_BROWSE_PAGE,
        offset: pageIndex * LIBRARY_BROWSE_PAGE,
      })
        .then((page) => {
          if (generation !== generationRef.current) return;
          pendingRef.current.delete(pageIndex);
          pagesRef.current.set(pageIndex, page.items);
          setTotal(page.total);
          if (pageIndex === 0) setFacets(page.facets);
          setLoadedTick((tick) => tick + 1);
        })
        .catch(() => {
          if (generation !== generationRef.current) return;
          pendingRef.current.delete(pageIndex);
          setFailed(true);
        });
    },
    // `state` is derived from stateKey; keying on the string keeps loadPage
    // stable across unrelated renders.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [libraryID, stateKey],
  );

  // A new query (or a retry) drops every cached page and starts again.
  useEffect(() => {
    generationRef.current += 1;
    pagesRef.current = new Map();
    pendingRef.current = new Set();
    setFailed(false);
    setTotal(null);
    loadPage(0);
  }, [loadPage, retryTick]);

  // Watched state and the Profile's own ratings can change under a query that
  // sorts or filters by them. The cached pages are then stale: drop them and
  // reload the window in place, keeping the count so the scroll position holds.
  const engagementEpoch = useEngagementEpoch();
  const seenEpochRef = useRef(engagementEpoch);
  const [refreshTick, setRefreshTick] = useState(0);
  const followsEngagement =
    state.watch !== "" ||
    state.minRating > 0 ||
    state.sort === "rating" ||
    state.sort === "last-watched";
  useEffect(() => {
    if (seenEpochRef.current === engagementEpoch) return;
    seenEpochRef.current = engagementEpoch;
    if (!followsEngagement) return;
    generationRef.current += 1;
    pagesRef.current = new Map();
    pendingRef.current = new Set();
    setRefreshTick((tick) => tick + 1);
  }, [engagementEpoch, followsEngagement]);

  const itemCount = total ?? 0;
  const listRef = useRef<HTMLUListElement | null>(null);
  const gridWindow = useVirtualGrid({
    gridRef: listRef,
    itemCount,
    enabled: itemCount > VIRTUAL_GRID_MIN_ITEMS,
    columns: viewMode === "list" ? 1 : undefined,
    maxItems: BROWSE_MAX_RENDERED,
  });
  const start = gridWindow?.startIndex ?? 0;
  const end = gridWindow?.endIndex ?? Math.min(itemCount, LIBRARY_BROWSE_PAGE);

  useEffect(() => {
    if (end <= start) return;
    const first = Math.floor(start / LIBRARY_BROWSE_PAGE);
    const last = Math.floor((end - 1) / LIBRARY_BROWSE_PAGE);
    for (let pageIndex = first; pageIndex <= last; pageIndex += 1) loadPage(pageIndex);
    // refreshTick re-runs this after an engagement change emptied the cache.
  }, [start, end, loadPage, refreshTick]);

  function update(patch: Partial<LibraryBrowseState>) {
    setSearchParams(browseStateParams({ ...state, ...patch }), { replace: true });
  }

  function chooseViewMode(mode: LibraryViewMode) {
    setViewMode(mode);
    writeLibraryViewMode(libraryID, mode);
  }

  // Leaving the page, or changing library, drops a Play all still loading:
  // a slow start must not pull the viewer into the player from elsewhere.
  const leavingRef = useRef(new AbortController());
  useEffect(() => {
    const leaving = new AbortController();
    leavingRef.current = leaving;
    return () => leaving.abort();
  }, [libraryID]);

  async function play(shuffle: boolean) {
    setStarting(true);
    try {
      const first = await startPlayQueue(
        { libraryID, state, shuffleSeed: shuffle ? newSeed() : undefined },
        leavingRef.current.signal,
      );
      // The player reads its heading from navigation state.
      if (first) {
        navigate(`/watch/${encodeURIComponent(first.id)}`, { state: { title: first.title } });
      }
    } catch {
      setFailed(true);
    } finally {
      setStarting(false);
    }
  }

  const decadeLabel = (decade: number) => t("libraryBrowseDecade", { decade: String(decade) });
  const qualityLabel = (quality: string) =>
    QUALITY_LABELS[quality] ? t(QUALITY_LABELS[quality]) : quality;
  const ratingLabel = (minRating: number) => {
    if (minRating <= 1) return t("libraryBrowseRatingAny");
    const key = minRating >= 10 ? "libraryBrowseRatingTop" : "libraryBrowseRatingAtLeast";
    return t(key, { stars: String(minRating / 2) });
  };
  // The selected values stay offered even when the current result has none.
  // A grouping selected in the URL is offered even before (or without) the
  // server naming it, so the choice can always be seen and cleared.
  const groupings = [
    ...facets.groupings,
    ...Object.keys(state.groups)
      .filter((kind) => !facets.groupings.some((grouping) => grouping.kind === kind))
      .map((kind) => ({ kind, title: undefined, values: [] })),
  ].map((grouping) => ({
    kind: grouping.kind,
    label: displayString(grouping.title, displayLocale) || grouping.kind,
    values: withSelected(grouping.values, state.groups[grouping.kind]),
  }));
  const withGroup = (kind: string, value: string) => {
    const groups = { ...state.groups };
    if (value) groups[kind] = value;
    else delete groups[kind];
    return groups;
  };
  const decades = withSelected(facets.decades, state.decade || undefined);
  const qualities = withSelected(facets.qualities, state.quality);

  const activeFilters: { label: string; clear: Partial<LibraryBrowseState> }[] = [];
  if (state.watch) {
    activeFilters.push({ label: t(WATCH_LABELS[state.watch]), clear: { watch: "" } });
  }
  for (const grouping of groupings) {
    const selected = state.groups[grouping.kind];
    if (selected) {
      activeFilters.push({
        label: `${grouping.label}: ${selected}`,
        clear: { groups: withGroup(grouping.kind, "") },
      });
    }
  }
  if (state.decade) activeFilters.push({ label: decadeLabel(state.decade), clear: { decade: 0 } });
  if (state.quality) {
    activeFilters.push({ label: qualityLabel(state.quality), clear: { quality: "" } });
  }
  if (state.minRating) {
    activeFilters.push({ label: ratingLabel(state.minRating), clear: { minRating: 0 } });
  }
  const clearAll = { watch: "", groups: {}, decade: 0, quality: "", minRating: 0 } as const;

  // A card hands the player the loaded items around it as its queue
  // (CORE-1143): the run of contiguous loaded pages, so Previous and Next
  // never skip over a page that has not arrived yet.
  const runQueues = new Map<number, PlayQueueItem[]>();
  const queueForPage = (pageIndex: number): PlayQueueItem[] => {
    const known = runQueues.get(pageIndex);
    if (known) return known;
    const pages = pagesRef.current;
    let first = pageIndex;
    while (pages.has(first - 1)) first -= 1;
    let last = pageIndex;
    while (pages.has(last + 1)) last += 1;
    const items: SourceItem[] = [];
    for (let p = first; p <= last; p += 1) items.push(...(pages.get(p) ?? []));
    const queue = playQueueOf(items);
    for (let p = first; p <= last; p += 1) runQueues.set(p, queue);
    return queue;
  };

  const cells = [];
  for (let index = start; index < end; index += 1) {
    const pageIndex = Math.floor(index / LIBRARY_BROWSE_PAGE);
    const page = pagesRef.current.get(pageIndex);
    const item = page?.[index % LIBRARY_BROWSE_PAGE];
    cells.push(
      item ? (
        <MediaItemCard
          key={item.id}
          item={item}
          queue={queueForPage(pageIndex)}
          posInSet={index + 1}
          setSize={itemCount}
        />
      ) : (
        <PendingCard key={`pending-${index}`} />
      ),
    );
  }

  return (
    <AppShell
      actions={
        <>
          <ViewerActions />
          <RevealControls />
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <div className="flex flex-wrap items-center gap-3">
          <Button
            variant="outline"
            size="sm"
            onClick={() => navigate(`/n/${encodeURIComponent(nodeID)}`)}
          >
            {t("back")}
          </Button>
          <h1 className="bf-page-title">{heading}</h1>
          <p role="status" className="text-sm text-[var(--text-muted)]">
            {total === null ? t("loading") : t("libraryBrowseCount", { count: total })}
          </p>
          <div className="ml-auto flex flex-wrap items-center gap-2">
            <Button size="sm" disabled={!itemCount || starting} onClick={() => void play(false)}>
              {t("libraryBrowsePlayAll")}
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={!itemCount || starting}
              onClick={() => void play(true)}
            >
              {t("libraryBrowseShuffle")}
            </Button>
          </div>
        </div>

        <div className="flex flex-wrap items-end gap-3">
          <BrowseSelect
            id="library-browse-sort"
            label={t("libraryBrowseSortLabel")}
            value={state.sort}
            onChange={(value) => update({ sort: value as LibrarySort })}
            options={LIBRARY_SORTS.map((sort) => ({ value: sort, label: t(SORT_LABELS[sort]) }))}
          />
          <Button
            variant="outline"
            size="sm"
            aria-label={t("libraryBrowseDirectionLabel", {
              direction: t(
                state.direction === "asc" ? "libraryBrowseAscending" : "libraryBrowseDescending",
              ),
            })}
            onClick={() => update({ direction: state.direction === "asc" ? "desc" : "asc" })}
          >
            <span aria-hidden="true">{state.direction === "asc" ? "↑" : "↓"}</span>{" "}
            {t(state.direction === "asc" ? "libraryBrowseAscending" : "libraryBrowseDescending")}
          </Button>
          <BrowseSelect
            id="library-browse-watch"
            label={t("libraryBrowseWatchLabel")}
            value={state.watch || ALL}
            onChange={(value) =>
              update({ watch: value === ALL ? "" : (value as LibraryBrowseState["watch"]) })
            }
            options={[
              { value: ALL, label: t("libraryBrowseAny") },
              ...LIBRARY_WATCH_STATES.map((watch) => ({
                value: watch,
                label: t(WATCH_LABELS[watch]),
              })),
            ]}
          />
          <BrowseSelect
            id="library-browse-rating"
            label={t("libraryBrowseRatingLabel")}
            value={state.minRating ? String(state.minRating) : ALL}
            onChange={(value) => update({ minRating: value === ALL ? 0 : Number(value) })}
            options={[
              { value: ALL, label: t("libraryBrowseAny") },
              ...LIBRARY_MIN_RATINGS.map((minRating) => ({
                value: String(minRating),
                label: ratingLabel(minRating),
              })),
            ]}
          />
          {groupings.map((grouping) => (
            <BrowseSelect
              key={grouping.kind}
              id={`library-browse-group-${grouping.kind}`}
              label={grouping.label}
              value={state.groups[grouping.kind] ? GROUP_VALUE + state.groups[grouping.kind] : ALL}
              onChange={(value) =>
                update({
                  groups: withGroup(
                    grouping.kind,
                    value === ALL ? "" : value.slice(GROUP_VALUE.length),
                  ),
                })
              }
              options={[
                { value: ALL, label: t("libraryBrowseAny") },
                ...grouping.values.map((value) => ({ value: GROUP_VALUE + value, label: value })),
              ]}
            />
          ))}
          {decades.length > 0 && (
            <BrowseSelect
              id="library-browse-decade"
              label={t("libraryBrowseDecadeLabel")}
              value={state.decade ? String(state.decade) : ALL}
              onChange={(value) => update({ decade: value === ALL ? 0 : Number(value) })}
              options={[
                { value: ALL, label: t("libraryBrowseAny") },
                ...decades.map((decade) => ({ value: String(decade), label: decadeLabel(decade) })),
              ]}
            />
          )}
          {qualities.length > 0 && (
            <BrowseSelect
              id="library-browse-quality"
              label={t("libraryBrowseQualityLabel")}
              value={state.quality || ALL}
              onChange={(value) =>
                update({ quality: value === ALL ? "" : (value as LibraryBrowseState["quality"]) })
              }
              options={[
                { value: ALL, label: t("libraryBrowseAny") },
                ...qualities.map((quality) => ({ value: quality, label: qualityLabel(quality) })),
              ]}
            />
          )}
          <div
            role="group"
            aria-label={t("libraryBrowseViewLabel")}
            className="ml-auto flex items-center gap-2"
          >
            <button
              type="button"
              className="bf-chip"
              aria-pressed={viewMode === "grid"}
              onClick={() => chooseViewMode("grid")}
            >
              {t("libraryBrowseViewGrid")}
            </button>
            <button
              type="button"
              className="bf-chip"
              aria-pressed={viewMode === "list"}
              onClick={() => chooseViewMode("list")}
            >
              {t("libraryBrowseViewList")}
            </button>
          </div>
        </div>

        {activeFilters.length > 0 && (
          <ul aria-label={t("libraryBrowseActiveFilters")} className="bf-filter-chips list-none">
            {activeFilters.map((filter) => (
              <li key={filter.label}>
                <button
                  type="button"
                  className="bf-chip"
                  aria-label={t("libraryBrowseRemoveFilter", { filter: filter.label })}
                  onClick={() => update(filter.clear)}
                >
                  {filter.label} <span aria-hidden="true">×</span>
                </button>
              </li>
            ))}
            {activeFilters.length > 1 && (
              <li>
                <button type="button" className="bf-chip" onClick={() => update(clearAll)}>
                  {t("libraryBrowseClearFilters")}
                </button>
              </li>
            )}
          </ul>
        )}

        {failed && (
          <div role="alert" className="flex items-center gap-3">
            <p className="text-sm text-[var(--text-muted)]">{t("libraryBrowseLoadFailed")}</p>
            <Button variant="outline" size="sm" onClick={() => setRetryTick((tick) => tick + 1)}>
              {t("jobRetryAction")}
            </Button>
          </div>
        )}
        {total === 0 && !failed && (
          <p className="text-sm text-[var(--text-muted)]">
            {t(activeFilters.length ? "libraryBrowseNoMatches" : "libraryEmptyLead")}
          </p>
        )}

        <ul
          ref={listRef}
          aria-label={heading}
          aria-busy={total === null}
          className={viewMode === "list" ? "bf-stack" : "bf-grid"}
        >
          {gridWindow && gridWindow.topPad > 0 && <Spacer height={gridWindow.topPad} />}
          {cells}
          {gridWindow && gridWindow.bottomPad > 0 && <Spacer height={gridWindow.bottomPad} />}
        </ul>
      </div>
    </AppShell>
  );
}

function BrowseSelect({
  id,
  label,
  value,
  onChange,
  options,
}: {
  id: string;
  label: string;
  value: string;
  onChange: (value: string) => void;
  options: { value: string; label: string }[];
}) {
  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={id} className="bf-eyebrow">
        {label}
      </label>
      <Select value={value} onValueChange={onChange}>
        <SelectTrigger id={id} size="sm" className="min-w-32">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {options.map((option) => (
            <SelectItem key={option.value} value={option.value}>
              {option.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  );
}

function Spacer({ height }: { height: number }) {
  return (
    <li
      aria-hidden
      role="presentation"
      className="bf-grid-spacer list-none"
      style={{ gridColumn: "1 / -1", height: `${height}px`, flex: "none" }}
    />
  );
}

// Holds the place (and the measured height) of an item whose page has not
// arrived yet; never focusable and hidden from assistive tech.
function PendingCard() {
  return (
    <li className="list-none" aria-hidden="true">
      <div className="bf-card" style={{ pointerEvents: "none" }}>
        <div className="bf-poster" />
        <div className="bf-card-copy">
          <span className="bf-card-title">&nbsp;</span>
        </div>
      </div>
    </li>
  );
}

function withSelected<T>(values: T[], selected: T | "" | undefined): T[] {
  if (selected === "" || selected === undefined || values.includes(selected)) return values;
  return [selected, ...values];
}

function newSeed(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return String(Date.now());
}
