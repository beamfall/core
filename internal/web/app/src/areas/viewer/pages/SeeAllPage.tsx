import { ChevronLeft, LayoutGrid } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router";
import { IconHeading } from "@/components/beamfall/workbench";
import { AppShell } from "@/components/chrome/AppShell";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { getIdentityEpochSignal, isAuthRejection } from "@/lib/api";
import { displayString } from "@/lib/display";
import type { SourceItem } from "@/lib/graphql/types";
import { fetchHomeLibraries, type HomeLibrary } from "@/lib/graphql/viewer";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { useSession, useViewerIdentity } from "@/lib/session";
import { useEngagementEpoch } from "../actions";
import { MediaItemCard } from "../components/MediaItemCard";
import { useVirtualGrid, VIRTUAL_GRID_MIN_ITEMS } from "../components/useVirtualGrid";
import { ViewerActions } from "../components/ViewerActions";
import { repeatedTitles, showsYear } from "../lib/cardTitles";
import {
  fetchSectionItems,
  findHomeRecentSection,
  MAX_RENDERED_SECTIONS,
  SECTION_KINDS,
  SEEALL_PAGE,
} from "../lib/sections";
import type { HomeLibrarySeeAllState, SeeAllContext } from "../lib/seeall";
import { playQueueOf } from "../lib/watchQueue";

type ResolvedSeeAllContext = SeeAllContext & { homeLibraryShelf?: true };

// See All (/seeall/:sourceId), ported from the vanilla openSeeAll /
// loadSeeAllPage (app.js). Renders one Source as a full grid, paged 60 at a
// time through the same Section card path as browse (so a result activates
// identically — Media plays, entities open detail). Load More disappears when a
// page comes back short (the Source is exhausted). The Source context rides
// react-router navigation state; a direct visit without it degrades to a Back
// affordance (the vanilla view is reachable only from a shelf).
export function SeeAllPage() {
  const location = useLocation();
  const identity = useViewerIdentity();
  const { manifest } = useSession();
  const state = location.state as SeeAllContext | HomeLibrarySeeAllState | null;
  const ctx = useMemo<ResolvedSeeAllContext | null>(() => {
    if (!state) return null;
    if ("section" in state) {
      // Older generated library shelves put their synthetic Section in history.
      const legacyLibraryShelf =
        state.home &&
        state.libraryID &&
        state.section.source?.kind === "recently-added" &&
        state.section.id.endsWith(`:${state.libraryID}`);
      if (!legacyLibraryShelf) return state;
    }
    const home = manifest?.navigation.find(({ view }) => view === "layout");
    const layout = manifest?.layouts.find(({ id }) => id === home?.layoutID);
    const sections = layout?.sections.filter(({ kind }) => SECTION_KINDS.has(kind)) ?? [];
    const section = findHomeRecentSection(sections.slice(0, MAX_RENDERED_SECTIONS));
    return section
      ? {
          section,
          libraryID: state.libraryID,
          entityID: "",
          returnTo: "/",
          home: true,
          homeLibraryShelf: true,
        }
      : null;
  }, [manifest, state]);
  return <SeeAllResults key={JSON.stringify([location.key, identity, Boolean(ctx)])} ctx={ctx} />;
}

async function fetchPage(ctx: ResolvedSeeAllContext, offset: number, signal?: AbortSignal) {
  let libraryTitle: HomeLibrary["title"] | undefined;
  if (ctx.homeLibraryShelf) {
    // Core applies Home visibility to aggregate reads; a scoped feed can
    // return a library that was hidden after the viewer opened its shelf.
    const libraries = await fetchHomeLibraries(signal);
    const library = libraries?.find(
      ({ id, showOnHome }) => id === ctx.libraryID && showOnHome !== false,
    );
    if (!library) return null;
    libraryTitle = library.title;
  }
  signal?.throwIfAborted();
  const items = await fetchSectionItems(
    ctx.section,
    {
      libraryID: ctx.libraryID,
      entityID: ctx.entityID,
      home: ctx.home,
      paging: { limit: SEEALL_PAGE, offset },
    },
    signal,
  );
  return { items, libraryTitle };
}

function SeeAllResults({ ctx }: { ctx: ResolvedSeeAllContext | null }) {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const navigate = useNavigate();

  const [items, setItems] = useState<SourceItem[]>([]);
  const [libraryTitle, setLibraryTitle] = useState<HomeLibrary["title"]>();
  const [hasMore, setHasMore] = useState(true);
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);
  const offsetRef = useRef(0);
  // Bumped by every reset or reload, so a response from before it is dropped.
  const generationRef = useRef(0);
  const engagementEpoch = useEngagementEpoch();
  const seenEpochRef = useRef(engagementEpoch);
  const gridRef = useRef<HTMLUListElement | null>(null);
  const gridWindow = useVirtualGrid({
    gridRef,
    itemCount: items.length,
    enabled: items.length > VIRTUAL_GRID_MIN_ITEMS,
    variableRows: ctx?.home ? items : undefined,
  });
  const start = gridWindow?.startIndex ?? 0;
  const end = gridWindow?.endIndex ?? items.length;
  const visibleItems = gridWindow ? items.slice(start, end) : items;
  const queue = useMemo(() => playQueueOf(items), [items]);
  const repeated = useMemo(() => repeatedTitles(items), [items]);

  const clearResults = useCallback(() => {
    setItems([]);
    setLibraryTitle(undefined);
    offsetRef.current = 0;
    setHasMore(false);
  }, []);

  const loadPage = useCallback(async () => {
    if (!ctx) return;
    const generation = generationRef.current;
    const signal = getIdentityEpochSignal();
    setBusy(true);
    try {
      const page = await fetchPage(ctx, offsetRef.current, signal);
      if (generationRef.current !== generation || signal?.aborted) return;
      if (page) {
        setItems((prev) => [...prev, ...page.items]);
        setLibraryTitle(page.libraryTitle);
        offsetRef.current += page.items.length;
        setHasMore(page.items.length >= SEEALL_PAGE);
      } else {
        clearResults();
      }
      setFailed(false);
    } catch (error) {
      if (generationRef.current === generation && !signal?.aborted) {
        if (isAuthRejection(error)) clearResults();
        setFailed(true);
      }
    } finally {
      if (generationRef.current === generation) setBusy(false);
    }
  }, [ctx, clearResults]);

  // Reads the span already shown again and swaps it in whole, so a list that
  // changed underneath (an item left the watchlist) neither keeps the old rows
  // nor pages on from a stale offset.
  const reloadShown = useCallback(async () => {
    if (!ctx) return;
    const generation = ++generationRef.current;
    const signal = getIdentityEpochSignal();
    const want = Math.max(offsetRef.current, SEEALL_PAGE);
    const next: SourceItem[] = [];
    let nextLibraryTitle: HomeLibrary["title"] | undefined;
    let more = true;
    setBusy(true);
    try {
      while (more && next.length < want) {
        const page = await fetchPage(ctx, next.length, signal);
        if (generationRef.current !== generation || signal?.aborted) return;
        if (!page) {
          clearResults();
          setFailed(false);
          return;
        }
        next.push(...page.items);
        nextLibraryTitle = page.libraryTitle;
        more = page.items.length >= SEEALL_PAGE;
      }
      setItems(next);
      setLibraryTitle(nextLibraryTitle);
      offsetRef.current = next.length;
      setHasMore(more);
      setFailed(false);
    } catch (error) {
      if (generationRef.current === generation && !signal?.aborted && isAuthRejection(error)) {
        clearResults();
        setFailed(true);
      }
      // Keep what is shown; the next change or Load More tries again.
    } finally {
      if (generationRef.current === generation) setBusy(false);
    }
  }, [ctx, clearResults]);

  useEffect(() => {
    if (offsetRef.current > 0) void reloadShown();
    else void loadPage();
    return () => {
      generationRef.current += 1;
    };
  }, [loadPage, reloadShown]);

  useEffect(() => {
    if (seenEpochRef.current === engagementEpoch) return;
    seenEpochRef.current = engagementEpoch;
    void reloadShown();
  }, [engagementEpoch, reloadShown]);

  const firstPageLoading = busy && items.length === 0;
  const loadFailed = !busy && failed && items.length === 0;
  const empty = !busy && !failed && !hasMore && items.length === 0;
  const title = ctx?.homeLibraryShelf ? libraryTitle : ctx?.section.title;

  function goBack() {
    navigate(ctx?.returnTo || "/");
  }

  if (!ctx) {
    return (
      <AppShell actions={<ViewerActions />}>
        <div className="flex flex-col gap-3">
          <Button
            variant="ghost"
            size="sm"
            className="-ml-2 self-start"
            onClick={() => navigate("/")}
          >
            <ChevronLeft aria-hidden />
            {t("back")}
          </Button>
          <div className="bf-empty flex flex-col gap-2" role="status">
            <p className="text-sm text-[var(--text-secondary)]">{t("libraryEmptyLead")}</p>
          </div>
        </div>
      </AppShell>
    );
  }

  return (
    <AppShell actions={<ViewerActions />}>
      <div className="flex flex-col gap-6">
        <div className="flex flex-col items-start gap-1">
          <Button variant="ghost" size="sm" className="-ml-2" onClick={goBack}>
            <ChevronLeft aria-hidden />
            {t("back")}
          </Button>
          {title && <IconHeading icon={LayoutGrid} title={displayString(title, displayLocale)} />}
        </div>
        {firstPageLoading && (
          <div aria-busy="true" className="bf-grid">
            <span className="sr-only">{t("loading")}</span>
            {Array.from({ length: 12 }).map((_, i) => (
              <Skeleton key={i} aria-hidden className="bf-shimmer aspect-[2/3] w-full" />
            ))}
          </div>
        )}
        {loadFailed && (
          <div role="alert" className="bf-empty flex flex-col gap-3">
            <p className="text-sm text-[var(--text-secondary)]">{t("seeAllLoadFailed")}</p>
            <Button variant="outline" className="self-start" onClick={loadPage}>
              {t("jobRetryAction")}
            </Button>
          </div>
        )}
        {empty && (
          <div className="bf-empty flex flex-col gap-2" role="status">
            <p className="text-sm text-[var(--text-secondary)]">{t("libraryEmptyTitle")}</p>
          </div>
        )}
        <ul
          ref={gridRef}
          className="bf-grid"
          style={items.length === 0 ? { display: "none" } : undefined}
        >
          {gridWindow && gridWindow.topPad > 0 && (
            <li
              aria-hidden
              role="presentation"
              className="bf-grid-spacer"
              style={{ gridColumn: "1 / -1", height: `${gridWindow.topPad}px` }}
            />
          )}
          {visibleItems.map((item, offset) => {
            const index = start + offset;
            return (
              <MediaItemCard
                key={`${item.id}-${index}`}
                item={item}
                posInSet={index + 1}
                setSize={items.length}
                queue={queue}
                showYear={showsYear(item, repeated)}
                intrinsicArtwork={ctx.home}
              />
            );
          })}
          {gridWindow && gridWindow.bottomPad > 0 && (
            <li
              aria-hidden
              role="presentation"
              className="bf-grid-spacer"
              style={{ gridColumn: "1 / -1", height: `${gridWindow.bottomPad}px` }}
            />
          )}
        </ul>
        {hasMore && items.length > 0 && (
          <div className="flex justify-center">
            <Button onClick={loadPage} disabled={busy}>
              {t("loadMore")}
            </Button>
          </div>
        )}
      </div>
    </AppShell>
  );
}
