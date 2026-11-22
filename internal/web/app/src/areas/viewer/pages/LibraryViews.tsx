import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { Button } from "@/components/ui/button";
import { displayString } from "@/lib/display";
import type { DisplayString } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import {
  HideWatchedToggle,
  hiddenKeyMoved,
  useEngagementEpoch,
  useHiddenKey,
  useHideWatched,
} from "../actions";
import { LibraryBrowseEmpty, LibraryBrowseToolbar } from "../components/LibraryBrowseToolbar";
import { LibraryLetterRail } from "../components/LibraryLetterRail";
import { LibraryHeader } from "../components/LibraryPivots";
import { MediaItemCard } from "../components/MediaItemCard";
import { PosterSizeControl } from "../components/PosterSizeControl";
import { LETTER_RAIL_MIN_ITEMS, useLetterJump } from "../components/useLetterJump";
import { useVirtualGrid, VIRTUAL_GRID_MIN_ITEMS } from "../components/useVirtualGrid";
import { ViewerActions } from "../components/ViewerActions";
import {
  browseFollowsEngagement,
  browseStateParams,
  DEFAULT_BROWSE_STATE,
  fetchLibraryItems,
  isAudioOnly,
  type LibraryBrowseState,
  type LibraryItemsFacets,
  type LibraryItemsLetter,
  parseBrowseState,
} from "../lib/libraryBrowse";
import { FOLDER_VIEW } from "../lib/libraryFolders";
import {
  cachedLibraryEntities,
  entityCardLine,
  fetchLibraryEntities,
  LIBRARY_ENTITY_PAGE,
  LIBRARY_VIEW_PARAM,
  type LibraryEntitiesPage,
  type LibraryEntitiesRequest,
  type LibraryEntityChildKind,
  type LibraryEntityItem,
  type LibraryView,
  libraryViews,
  MEDIA_VIEW,
  selectedView,
  viewSearchParams,
} from "../lib/libraryViews";
import { LibraryFolders } from "./LibraryFolders";

// A wide viewport shows more cards than the See All cap allows for.
const ENTITY_MAX_RENDERED = 240;

// Enough Media to tell an audio-only Library from the facets read.
const FACETS_SAMPLE = 24;

// A cached plan names the kinds at once; its page is not a seed, since the
// grid paints that page from the cache and reads it afresh itself.
type Plan =
  | {
      page: LibraryEntitiesPage;
      limit: number;
      stateKey: string;
      epoch: number;
      cached: boolean;
    }
  | "failed"
  | null;
/** The Library's filter values before any filter, and whether it is all audio. */
type LibraryFacets = { facets: LibraryItemsFacets; audioOnly: boolean | null };

const NO_FACETS: LibraryFacets = {
  facets: { groupings: [], decades: [], qualities: [] },
  audioOnly: null,
};

// The Library page's views (CORE-2362). It reads which Entity kinds the
// Library is browsed by, then shows the view the URL names: an Entity kind's
// paged card grid, the Media view (`media`, the flat Library browse) or the
// Folders view (`folders`, CORE-2359). With no view named it opens on the first
// kind. A Library with no browse kinds, or whose kinds cannot be read, opens on
// the Media view with a switcher to Folders.
export function LibraryViews({
  libraryID,
  nodeID,
  title,
  media,
}: {
  libraryID: string;
  nodeID: string;
  title: DisplayString;
  /** Renders the Media view, with the switcher to place in its browse toolbar. */
  media: (switcher: ReactNode) => ReactNode;
}) {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const [searchParams, setSearchParams] = useSearchParams();
  const requested = searchParams.get(LIBRARY_VIEW_PARAM);
  // One read per Library learns its kinds. It already asks for the first page
  // of the kind the URL names (or of the first kind), so that grid paints from
  // it; the Media and Folders views need only the kinds. A Library opened
  // again paints its kinds from the cached read while this one runs.
  const [first] = useState(() => {
    const kindless = requested === MEDIA_VIEW || requested === FOLDER_VIEW;
    const limit = kindless ? 1 : LIBRARY_ENTITY_PAGE;
    const state = parseBrowseState(searchParams);
    const request: LibraryEntitiesRequest = {
      libraryID,
      kind: requested && !kindless ? requested : undefined,
      ...(kindless ? {} : { state }),
      limit,
      offset: 0,
    };
    const cached = cachedLibraryEntities(request);
    // Naming the cached kind makes this the same read the grid makes for its
    // first page, so the two share one request.
    if (cached && !kindless && !request.kind && cached.kind) request.kind = cached.kind;
    return { limit, stateKey: browseStateParams(state).toString(), request, cached };
  });
  const [plan, setPlan] = useState<Plan>(() =>
    first.cached
      ? {
          page: first.cached,
          limit: first.limit,
          stateKey: first.stateKey,
          // Never a seed, so no epoch applies.
          epoch: -1,
          cached: true,
        }
      : null,
  );
  // The opening page paints only the first grid that takes it: a grid opened
  // again later (after the Media view, say) reads afresh, since watched state
  // or ratings may have changed what the query returns since.
  const seedTakenRef = useRef(false);
  // A hide or watched change made while the opening read is in flight makes
  // that page stale before any grid sees it (CORE-2854): the grid starts at
  // the newer epoch, so it would never read again. The seed records the epoch
  // its read began under and is refused once the epoch has moved.
  const engagementEpoch = useEngagementEpoch();
  const epochRef = useRef(engagementEpoch);
  epochRef.current = engagementEpoch;

  useEffect(() => {
    const { limit, stateKey, request } = first;
    const controller = new AbortController();
    const epoch = epochRef.current;
    fetchLibraryEntities({ ...request, libraryID }, controller.signal)
      .then((page) => setPlan({ page, limit, stateKey, epoch, cached: false }))
      .catch(() => {
        if (!controller.signal.aborted)
          setPlan((prior) => (prior && prior !== "failed" ? prior : "failed"));
      });
    return () => controller.abort();
  }, [libraryID, first]);

  const read = plan && plan !== "failed" ? plan.page : null;
  const views =
    plan === null
      ? []
      : libraryViews(
          read?.kinds ?? [],
          read?.allLabel,
          displayLocale,
          t("libraryViewAll"),
          t("libraryBrowseViewFolders"),
        );
  const view = selectedView(views, requested);
  const facets = useLibraryFacets(libraryID, !!view?.kind && plan !== null && plan !== "failed");
  const choose = (id: string) =>
    setSearchParams(viewSearchParams(searchParams, id), { replace: true });
  const switcher =
    views.length > 0 && view ? (
      <LibraryViewSwitcher views={views} selected={view.id} onSelect={choose} />
    ) : null;

  if (requested === FOLDER_VIEW) {
    return (
      <LibraryFolders
        libraryID={libraryID}
        nodeID={nodeID}
        heading={displayString(title, displayLocale)}
        switcher={switcher}
      />
    );
  }
  if (plan === null && requested !== MEDIA_VIEW) {
    return (
      <EntityPageFrame
        nodeID={nodeID}
        libraryID={libraryID}
        heading={displayString(title, displayLocale)}
        status=""
      >
        <p className="text-sm text-[var(--text-muted)]">{t("loading")}</p>
      </EntityPageFrame>
    );
  }
  if (!view?.kind || plan === null || plan === "failed") return media(switcher);
  const seed =
    !plan.cached && plan.limit === LIBRARY_ENTITY_PAGE && plan.page.kind === view.kind
      ? plan
      : null;
  const takeSeed = () => {
    if (!seed || seedTakenRef.current || seed.epoch !== engagementEpoch) return null;
    seedTakenRef.current = true;
    return seed;
  };
  return (
    <LibraryEntityGrid
      key={view.kind}
      libraryID={libraryID}
      nodeID={nodeID}
      heading={displayString(title, displayLocale)}
      kind={view.kind}
      label={view.label}
      takeSeed={takeSeed}
      facets={facets}
      switcher={switcher}
    />
  );
}

// The Library's filter values for an Entity kind's toolbar (CORE-2440): one
// unfiltered libraryItems read, made once an Entity kind's view opens, so the
// toolbar offers the same values the Media view does before any filter. A
// failed read leaves the sort, direction, watch and rating controls.
function useLibraryFacets(libraryID: string, wanted: boolean): LibraryFacets {
  const [read, setRead] = useState<LibraryFacets | null>(null);
  const started = useRef(false);
  useEffect(() => {
    if (!wanted || started.current) return;
    started.current = true;
    fetchLibraryItems({
      libraryID,
      state: DEFAULT_BROWSE_STATE,
      limit: FACETS_SAMPLE,
      offset: 0,
    })
      .then((page) =>
        setRead({
          facets: page.facets,
          audioOnly: page.items.length > 0 ? isAudioOnly(page.items) : null,
        }),
      )
      .catch(() => {});
  }, [libraryID, wanted]);
  return read ?? NO_FACETS;
}

/** The row of view chips: one per view, the open one pressed. */
export function LibraryViewSwitcher({
  views,
  selected,
  onSelect,
}: {
  views: readonly LibraryView[];
  selected: string;
  onSelect: (id: string) => void;
}) {
  const t = useT();
  return (
    <div role="group" aria-label={t("libraryViewSwitchLabel")} className="flex flex-wrap gap-2">
      {views.map((view) => (
        <button
          key={view.id}
          type="button"
          className="bf-chip"
          aria-pressed={view.id === selected}
          onClick={() => onSelect(view.id)}
        >
          {view.label}
        </button>
      ))}
    </div>
  );
}

function EntityPageFrame({
  nodeID,
  libraryID,
  heading,
  status,
  children,
}: {
  nodeID: string;
  libraryID: string;
  heading: string;
  status: string;
  children: ReactNode;
}) {
  return (
    <AppShell actions={<ViewerActions />}>
      <div className="flex flex-col gap-5">
        <LibraryHeader
          nodeID={nodeID}
          libraryID={libraryID}
          heading={heading}
          aside={
            <p role="status" className="text-sm text-[var(--text-muted)]">
              {status}
            </p>
          }
        />
        {children}
      </div>
    </AppShell>
  );
}

// One Entity kind's cards, paged by the server and windowed here like the
// Media view, under the Media view's toolbar (CORE-2440): the same sort and
// filters, read from and written to the same URL keys, judge each Entity by
// the Media it reaches. A title-sorted long list gets the letter rail. A
// card's line names the Entities it sits under, else how many Media it holds.
function LibraryEntityGrid({
  libraryID,
  nodeID,
  heading,
  kind,
  label,
  takeSeed,
  facets,
  switcher,
}: {
  libraryID: string;
  nodeID: string;
  heading: string;
  kind: string;
  label: string;
  /**
   * Hands over the kind's first page, when the page already read it, and the
   * state it was read under; it does so once per Library page.
   */
  takeSeed: () => { page: LibraryEntitiesPage; stateKey: string } | null;
  facets: LibraryFacets;
  switcher: ReactNode;
}) {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const [searchParams, setSearchParams] = useSearchParams();
  const state = useMemo(() => parseBrowseState(searchParams), [searchParams]);
  const stateKey = browseStateParams(state).toString();
  const takeSeedRef = useRef(takeSeed);
  const [total, setTotal] = useState<number | null>(null);
  const [letters, setLetters] = useState<LibraryItemsLetter[]>([]);
  const [childKinds, setChildKinds] = useState<LibraryEntityChildKind[]>([]);
  const [failed, setFailed] = useState(false);
  const [loadedTick, setLoadedTick] = useState(0);
  const [retryTick, setRetryTick] = useState(0);
  const pagesRef = useRef(new Map<number, LibraryEntityItem[]>());
  const pendingRef = useRef(new Set<number>());
  // Pages painted from the cache whose fresh read has not landed yet.
  const stalePagesRef = useRef(new Set<number>());
  const generationRef = useRef(0);

  const loadPage = useCallback(
    (pageIndex: number) => {
      if (pendingRef.current.has(pageIndex)) return;
      if (pagesRef.current.has(pageIndex) && !stalePagesRef.current.has(pageIndex)) return;
      const generation = generationRef.current;
      const request = {
        libraryID,
        kind,
        state,
        limit: LIBRARY_ENTITY_PAGE,
        offset: pageIndex * LIBRARY_ENTITY_PAGE,
      };
      const show = (page: LibraryEntitiesPage) => {
        pagesRef.current.set(pageIndex, page.items);
        setTotal(page.total);
        if (pageIndex === 0) {
          setLetters(page.letters);
          setChildKinds(page.childKinds ?? []);
        }
        setLoadedTick((tick) => tick + 1);
      };
      // A page read before paints at once and is read afresh behind it.
      const cached = pagesRef.current.has(pageIndex) ? undefined : cachedLibraryEntities(request);
      if (cached) {
        stalePagesRef.current.add(pageIndex);
        show(cached);
      }
      pendingRef.current.add(pageIndex);
      fetchLibraryEntities(request)
        .then((page) => {
          if (generation !== generationRef.current) return;
          pendingRef.current.delete(pageIndex);
          stalePagesRef.current.delete(pageIndex);
          show(page);
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
    [libraryID, kind, stateKey],
  );

  // A new query (or a retry) drops every cached page and starts again; the
  // opening page the Library read already holds paints at once.
  useEffect(() => {
    generationRef.current += 1;
    pendingRef.current = new Set();
    stalePagesRef.current = new Set();
    setFailed(false);
    const opening = takeSeedRef.current();
    takeSeedRef.current = () => null;
    if (opening && opening.stateKey === stateKey && retryTick === 0) {
      pagesRef.current = new Map([[0, opening.page.items]]);
      setTotal(opening.page.total);
      setLetters(opening.page.letters);
      setChildKinds(opening.page.childKinds ?? []);
      setLoadedTick((tick) => tick + 1);
      return;
    }
    pagesRef.current = new Map();
    setTotal(null);
    setLetters([]);
    loadPage(0);
  }, [loadPage, stateKey, retryTick]);

  // Watched state and ratings can change under a query that sorts or filters
  // by them; the cached pages are then stale, so the query starts again. So
  // does a read with no watch filter when Hide watched flips.
  const engagementEpoch = useEngagementEpoch();
  const seenEpochRef = useRef(engagementEpoch);
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
    if (followsEngagement || hid || (toggled && unfiltered)) setRetryTick((tick) => tick + 1);
  }, [engagementEpoch, hideWatched.version, hiddenKey, followsEngagement, unfiltered]);

  const itemCount = total ?? 0;
  const listRef = useRef<HTMLUListElement | null>(null);
  const gridWindow = useVirtualGrid({
    gridRef: listRef,
    itemCount,
    enabled: itemCount > VIRTUAL_GRID_MIN_ITEMS,
    maxItems: ENTITY_MAX_RENDERED,
  });
  const start = gridWindow?.startIndex ?? 0;
  const end = gridWindow?.endIndex ?? Math.min(itemCount, LIBRARY_ENTITY_PAGE);

  useEffect(() => {
    if (end <= start) return;
    const first = Math.floor(start / LIBRARY_ENTITY_PAGE);
    const last = Math.floor((end - 1) / LIBRARY_ENTITY_PAGE);
    for (let pageIndex = first; pageIndex <= last; pageIndex += 1) loadPage(pageIndex);
  }, [start, end, loadPage]);

  const itemAt = (index: number) =>
    pagesRef.current.get(Math.floor(index / LIBRARY_ENTITY_PAGE))?.[index % LIBRARY_ENTITY_PAGE];
  const showLetterRail =
    state.sort === "title" && letters.length > 0 && itemCount > LETTER_RAIL_MIN_ITEMS;
  const { jumpTo, currentLetter } = useLetterJump({
    listRef,
    letters,
    enabled: showLetterRail,
    titleAt: (index) => itemAt(index)?.title,
    loadedTick,
    resultKey: `${stateKey}:${retryTick}`,
  });

  function update(patch: Partial<LibraryBrowseState>) {
    const params = browseStateParams({ ...state, ...patch });
    const view = searchParams.get(LIBRARY_VIEW_PARAM);
    if (view) params.set(LIBRARY_VIEW_PARAM, view);
    setSearchParams(params, { replace: true });
  }

  const count = (mediaCount: number) => t("libraryBrowseCount", { count: mediaCount });
  const cells = [];
  for (let index = start; index < end; index += 1) {
    const item = itemAt(index);
    cells.push(
      item ? (
        <MediaItemCard
          key={item.id}
          item={{ ...item, summary: entityCardLine(item, childKinds, displayLocale, count) }}
          posInSet={index + 1}
          setSize={itemCount}
        />
      ) : (
        <PendingCard key={`pending-${index}`} />
      ),
    );
  }

  const status =
    total === null ? (failed ? "" : t("loading")) : t("libraryBrowseCount", { count: total });
  return (
    <EntityPageFrame nodeID={nodeID} libraryID={libraryID} heading={heading} status={status}>
      <LibraryBrowseToolbar
        state={state}
        facets={facets.facets}
        audioOnly={facets.audioOnly}
        onUpdate={update}
        libraryName={heading}
        leading={switcher}
        trailing={
          <>
            <PosterSizeControl />
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
          aria-label={label}
          aria-busy={total === null}
          className="bf-grid min-w-0 flex-1"
        >
          {gridWindow && gridWindow.topPad > 0 && <Spacer height={gridWindow.topPad} />}
          {cells}
          {gridWindow && gridWindow.bottomPad > 0 && <Spacer height={gridWindow.bottomPad} />}
        </ul>
        {showLetterRail && (
          <LibraryLetterRail letters={letters} current={currentLetter} onJump={jumpTo} />
        )}
      </div>
    </EntityPageFrame>
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

// Holds the place of a card whose page has not arrived yet; never focusable
// and hidden from assistive tech.
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
