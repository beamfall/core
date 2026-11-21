import { Play, Shuffle } from "lucide-react";
import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Navigate, useLocation, useNavigate, useParams, useSearchParams } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { Button } from "@/components/ui/button";
import { displayString } from "@/lib/display";
import type { SourceItem } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { findNodeBySlug, nodePath, nodeSlug } from "@/lib/nodePath";
import { useSession } from "@/lib/session";
import {
  HideWatchedToggle,
  hiddenKeyMoved,
  useEngagementEpoch,
  useHiddenKey,
  useHideWatched,
} from "../actions";
import { LibraryBrowseEmpty, LibraryBrowseToolbar } from "../components/LibraryBrowseToolbar";
import { LibraryDisplayToggle, useLibraryViewMode } from "../components/LibraryDisplayToggle";
import { LibraryLetterRail } from "../components/LibraryLetterRail";
import { LibraryHeader } from "../components/LibraryPivots";
import { MediaItemCard } from "../components/MediaItemCard";
import { PosterSizeControl } from "../components/PosterSizeControl";
import { LETTER_RAIL_MIN_ITEMS, useLetterJump } from "../components/useLetterJump";
import { useVirtualGrid, VIRTUAL_GRID_MIN_ITEMS } from "../components/useVirtualGrid";
import { ViewerActions } from "../components/ViewerActions";
import { repeatedTitles, showsYear } from "../lib/cardTitles";
import {
  browseFollowsEngagement,
  browseStateParams,
  fetchLibraryItems,
  isAudioOnly,
  LIBRARY_BROWSE_PAGE,
  type LibraryBrowseState,
  type LibraryItemsFacets,
  type LibraryItemsLetter,
  libraryViewClass,
  parseBrowseState,
} from "../lib/libraryBrowse";
import { LIBRARY_VIEW_PARAM } from "../lib/libraryViews";
import { mediaPath } from "../lib/mediaAddress";
import { startPlayQueue } from "../lib/playQueue";
import { type PlayQueueItem, playQueueOf } from "../lib/watchQueue";
import { LibraryViews } from "./LibraryViews";

// A wide viewport shows more cards than the See All cap allows for.
const BROWSE_MAX_RENDERED = 240;

const EMPTY_FACETS: LibraryItemsFacets = { groupings: [], decades: [], qualities: [] };
// Library browse (/n/:nodeId/all, CORE-1190): every item of one Library the
// profile may see, counted, sorted and filtered by the server (libraryItems)
// and windowed here. Sort and filters live in the URL so a view can be linked
// and survives Back; the grid/list choice is remembered per library. It is
// the Media view of the Library's views (LibraryViews, CORE-2362).
export function LibraryBrowsePage() {
  const t = useT();
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
  // An old link names the node by its raw ID (CORE-2322).
  if (nodeId !== nodeSlug(node.id)) {
    return (
      <Navigate to={{ pathname: nodePath(node.id, "/all"), search: location.search }} replace />
    );
  }
  const { libraryID } = node;
  return (
    <LibraryViews
      key={libraryID}
      libraryID={libraryID}
      nodeID={node.id}
      title={node.title}
      media={(switcher) => (
        <LibraryBrowse
          libraryID={libraryID}
          nodeID={node.id}
          title={node.title}
          switcher={switcher}
        />
      )}
    />
  );
}

function LibraryBrowse({
  libraryID,
  nodeID,
  title,
  switcher,
}: {
  libraryID: string;
  nodeID: string;
  title: Parameters<typeof displayString>[0];
  /** The Library's view switcher, shown in the browse toolbar. */
  switcher?: ReactNode;
}) {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const state = useMemo(() => parseBrowseState(searchParams), [searchParams]);
  const stateKey = browseStateParams(state).toString();
  const heading = displayString(title, displayLocale);

  const [viewMode, chooseViewMode] = useLibraryViewMode(libraryID);

  const [total, setTotal] = useState<number | null>(null);
  const [facets, setFacets] = useState<LibraryItemsFacets>(EMPTY_FACETS);
  const [letters, setLetters] = useState<LibraryItemsLetter[]>([]);
  const [failed, setFailed] = useState(false);
  const [starting, setStarting] = useState(false);
  // Pages arrive out of order as the window moves; the map is keyed by page
  // index and `loadedTick` re-renders when one lands.
  const pagesRef = useRef(new Map<number, SourceItem[]>());
  const pendingRef = useRef(new Set<number>());
  const generationRef = useRef(0);
  const [loadedTick, setLoadedTick] = useState(0);
  // Decided by the first page loaded and kept for this library, so the browse
  // bar does not change shape as filters reload pages (CORE-2327).
  const [audioOnly, setAudioOnly] = useState<boolean | null>(null);
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
          setAudioOnly(
            (decided) => decided ?? (page.items.length > 0 ? isAudioOnly(page.items) : null),
          );
          setTotal(page.total);
          if (pageIndex === 0) {
            setFacets(page.facets);
            setLetters(page.letters);
          }
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
    setLetters([]);
    loadPage(0);
  }, [loadPage, retryTick]);

  // Watched state and the Profile's own ratings can change under a query that
  // sorts or filters by them. The cached pages are then stale: drop them and
  // reload the window in place, keeping the count so the scroll position holds.
  const engagementEpoch = useEngagementEpoch();
  const seenEpochRef = useRef(engagementEpoch);
  const [refreshTick, setRefreshTick] = useState(0);
  // Turning Hide watched on or off changes what a read with no watch filter
  // returns, so that read starts over too.
  const hideWatched = useHideWatched();
  const seenHideRef = useRef(hideWatched.version);
  const hiddenKey = useHiddenKey();
  const seenHiddenKeyRef = useRef<string | null>(null);
  const unfiltered = state.watch === "";
  const followsEngagement = browseFollowsEngagement(state, hideWatched.on === true);
  useEffect(() => {
    const toggled = seenHideRef.current !== hideWatched.version;
    const hid = hiddenKeyMoved(
      seenHiddenKeyRef,
      hiddenKey,
      seenEpochRef.current !== engagementEpoch,
    );
    if (seenEpochRef.current === engagementEpoch && !toggled && !hid) return;
    seenEpochRef.current = engagementEpoch;
    seenHideRef.current = hideWatched.version;
    if (!followsEngagement && !hid && !(toggled && unfiltered)) {
      // Order and membership hold, but the cards' watched marks are stale:
      // re-read the pages already loaded and swap them in place.
      const generation = generationRef.current;
      for (const pageIndex of pagesRef.current.keys()) {
        fetchLibraryItems({
          libraryID,
          state,
          limit: LIBRARY_BROWSE_PAGE,
          offset: pageIndex * LIBRARY_BROWSE_PAGE,
        })
          .then((page) => {
            if (generation !== generationRef.current) return;
            pagesRef.current.set(pageIndex, page.items);
            setLoadedTick((tick) => tick + 1);
          })
          // The cards keep what they showed; the next change tries again.
          .catch(() => {});
      }
      return;
    }
    generationRef.current += 1;
    pagesRef.current = new Map();
    pendingRef.current = new Set();
    // The first page carries the count, so an empty result can gain an item.
    loadPage(0);
    setRefreshTick((tick) => tick + 1);
  }, [engagementEpoch, hideWatched.version, hiddenKey, followsEngagement, unfiltered, loadPage]);

  const itemCount = total ?? 0;
  const listRef = useRef<HTMLUListElement | null>(null);
  // The list and table views draw one item per row.
  const rowColumns = viewMode === "list" || viewMode === "table" ? 1 : undefined;
  const gridWindow = useVirtualGrid({
    gridRef: listRef,
    itemCount,
    enabled: itemCount > VIRTUAL_GRID_MIN_ITEMS,
    columns: rowColumns,
    layout: viewMode,
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

  const showLetterRail =
    state.sort === "title" && letters.length > 0 && itemCount > LETTER_RAIL_MIN_ITEMS;
  const { jumpTo, currentLetter } = useLetterJump({
    listRef,
    letters,
    enabled: showLetterRail,
    columns: rowColumns,
    titleAt: (index) =>
      pagesRef.current.get(Math.floor(index / LIBRARY_BROWSE_PAGE))?.[index % LIBRARY_BROWSE_PAGE]
        ?.title,
    loadedTick,
    resultKey: `${stateKey}:${retryTick}`,
  });

  function update(patch: Partial<LibraryBrowseState>) {
    const params = browseStateParams({ ...state, ...patch });
    const view = searchParams.get(LIBRARY_VIEW_PARAM);
    if (view) params.set(LIBRARY_VIEW_PARAM, view);
    setSearchParams(params, { replace: true });
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
        navigate(mediaPath("watch", first.id), { state: { title: first.title } });
      }
    } catch {
      setFailed(true);
    } finally {
      setStarting(false);
    }
  }

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

  // Titles repeated across the loaded pages show their year (CORE-2320).
  const repeated = repeatedTitles([...pagesRef.current.values()].flat());
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
          playOnActivate={viewMode === "list"}
          showYear={showsYear(item, repeated)}
          display={viewMode}
        />
      ) : (
        <PendingCard key={`pending-${index}`} />
      ),
    );
  }

  return (
    <AppShell actions={<ViewerActions />}>
      <div className="flex flex-col gap-5">
        <LibraryHeader
          nodeID={nodeID}
          heading={heading}
          aside={
            <>
              <p role="status" className="text-sm text-[var(--text-muted)]">
                {total === null
                  ? failed
                    ? ""
                    : t("loading")
                  : t("libraryBrowseCount", { count: total })}
              </p>
              <div className="flex w-full flex-wrap items-center gap-2 sm:ml-auto sm:w-auto">
                <Button disabled={!itemCount || starting} onClick={() => void play(false)}>
                  <Play aria-hidden fill="currentColor" />
                  {t("libraryBrowsePlayAll")}
                </Button>
                <Button
                  variant="outline"
                  disabled={!itemCount || starting}
                  onClick={() => void play(true)}
                >
                  <Shuffle aria-hidden />
                  {t("libraryBrowseShuffle")}
                </Button>
              </div>
            </>
          }
        />
        <LibraryBrowseToolbar
          state={state}
          facets={facets}
          audioOnly={audioOnly}
          onUpdate={update}
          libraryName={heading}
          leading={switcher}
          trailing={
            <>
              {/* App-wide (CORE-2893), so offered in every view mode; List sizes its thumbnails by it too. */}
              <PosterSizeControl />
              <LibraryDisplayToggle mode={viewMode} onChange={chooseViewMode} />
              <HideWatchedToggle />
            </>
          }
        />

        {failed && (
          <div role="alert" className="bf-empty flex flex-col gap-3">
            <p className="text-sm text-[var(--text-secondary)]">{t("libraryBrowseLoadFailed")}</p>
            <Button variant="outline" onClick={() => setRetryTick((tick) => tick + 1)}>
              {t("jobRetryAction")}
            </Button>
          </div>
        )}
        {total === 0 && !failed && <LibraryBrowseEmpty state={state} onUpdate={update} />}

        <div className="flex items-start gap-2">
          <ul
            ref={listRef}
            aria-label={heading}
            aria-busy={total === null}
            className={`min-w-0 flex-1 ${libraryViewClass(viewMode)}`}
          >
            {gridWindow && gridWindow.topPad > 0 && <Spacer height={gridWindow.topPad} />}
            {cells}
            {gridWindow && gridWindow.bottomPad > 0 && <Spacer height={gridWindow.bottomPad} />}
          </ul>
          {showLetterRail && (
            <LibraryLetterRail letters={letters} current={currentLetter} onJump={jumpTo} />
          )}
        </div>
      </div>
    </AppShell>
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

function newSeed(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return String(Date.now());
}
