import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router";
import { Skeleton } from "@/components/ui/skeleton";
import { isAuthRejection } from "@/lib/api";
import { displayString } from "@/lib/display";
import type { FieldFilters, LibrarySummary, Section, SourceItem } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { useViewerIdentity } from "@/lib/session";
import {
  clearViewerCache,
  readViewerCache,
  viewerCacheEntries,
  writeViewerCache,
} from "@/lib/viewerCache";
import { useEngagementEpoch } from "../actions/engagementSync";
import { artworkSlotURL } from "../lib/artwork";
import { groupByContainer, groupsNewByContainer } from "../lib/containerGroups";
import { fetchSectionItems, type SectionFetchContext } from "../lib/sections";
import { Hero } from "./Hero";
import { SectionHeading, SectionView, type SeeAllTarget } from "./SectionView";

export const SECTION_LOAD_TIMEOUT_MS = 15000;

/** "failed" is a network/timeout failure with nothing cached: hidden like
 * "empty", but reported separately so the Layout never mistakes an unreachable
 * server for an empty library. */
export type SectionHydrationState = "loading" | "empty" | "failed" | "visible";

const SECTION_CACHE_PREFIX = "section:";
// The SourceItems endpoint serves at most 200 rows in one request.
const HOME_RECENT_WINDOW = { limit: 200, offset: 0 };

type HydratedItem = SourceItem & { homeLibraryID?: string };

async function fetchHomeRecent(
  section: Section,
  context: SectionFetchContext,
  libraries: readonly LibrarySummary[],
  signal: AbortSignal,
): Promise<HydratedItem[]> {
  if (!libraries.length) return [];
  // Scoped reads ignore the Home flag, and the Viewer cannot read showOnHome.
  // The aggregate feed is the authority for which of the scoped rows may show.
  const visible = await fetchSectionItems(
    section,
    { ...context, paging: HOME_RECENT_WINDOW },
    signal,
  );
  if (!visible.length || signal.aborted) return [];
  const visibleByID = new Map(visible.map((item) => [JSON.stringify([item.kind, item.id]), item]));
  const shelves = await Promise.all(
    libraries.map(async ({ id: libraryID }) => {
      const items = await fetchSectionItems(section, { ...context, libraryID }, signal);
      return items.flatMap(({ kind, id }) => {
        const confirmed = visibleByID.get(JSON.stringify([kind, id]));
        return confirmed ? [{ ...confirmed, homeLibraryID: libraryID }] : [];
      });
    }),
  );
  return shelves.flat();
}

function featuredArtwork(items: readonly SourceItem[], failed: ReadonlySet<string>) {
  for (const item of items) {
    if (item.locked) continue;
    for (const slot of ["backdrop", "poster", "thumb"]) {
      const url = artworkSlotURL(item, slot);
      if (url && !failed.has(url)) return { item, url };
    }
  }
}

/** The viewer-cache key for one hydrated slot (scoped by identity in viewerCache). */
export function sectionCacheKey(
  section: Section,
  libraryID: string,
  entityID: string,
  extraFilters: FieldFilters | null | undefined,
  home = false,
): string {
  return (
    SECTION_CACHE_PREFIX +
    JSON.stringify([section, libraryID, entityID, extraFilters || null, home])
  );
}

/** An item already hydrated into some section for this identity, if any. */
export function findCachedSectionItem(identity: string | null, id: string): SourceItem | undefined {
  for (const [key, value] of viewerCacheEntries(identity)) {
    if (!key.startsWith(SECTION_CACHE_PREFIX)) continue;
    const match = (value as SourceItem[]).find((item) => item.id === id);
    if (match) return match;
  }
  return undefined;
}

// Hydrates one Section into a slot, ported from the vanilla hydrateSection
// (app.js): skeleton first (never focusable), content on arrival, collapse to
// nothing when empty (ADR-0014). A re-fetch (a filter-bar chip flip changing
// extraFilters) supersedes and aborts any in-flight older fetch, so the
// freshest selection always wins.
//
// Items already loaded for the bound identity render straight from the
// identity-scoped viewer cache (no skeleton) and are revalidated behind them.
// A failed revalidation keeps the cached items and raises the connection-lost
// notice; an authorization rejection drops the cache instead.
export function HydratedSection({
  section,
  libraryID,
  entityID,
  home = false,
  homeLibraries,
  extraFilters,
  seeAll,
  onConnectionLost,
  onHydrationStateChange,
  navigationID,
}: {
  section: Section;
  libraryID: string;
  entityID: string;
  /** Hydrating Home: Libraries hidden from Home stay out (CORE-2360). */
  home?: boolean;
  /** Libraries eligible to supply sub-shelves of the aggregate Home recent feed. */
  homeLibraries?: readonly LibrarySummary[];
  /** A filter-bar selection routed to this (target) section; null when none. */
  extraFilters?: FieldFilters | null;
  seeAll?: SeeAllTarget;
  onConnectionLost?: () => void;
  onHydrationStateChange?: (state: SectionHydrationState) => void;
  navigationID?: string;
}) {
  const identity = useViewerIdentity();
  const t = useT();
  const displayLocale = useDisplayLocale();
  const navigate = useNavigate();
  // Watched, watchlist and rating changes (here or on another client) revalidate
  // the section behind the items already shown.
  const engagementEpoch = useEngagementEpoch();
  const cacheKey = sectionCacheKey(section, libraryID, entityID, extraFilters, home);
  const homeHero = home && section.kind === "hero";
  // Require a fresh Home visibility check before showing the new shelves or hero.
  const useCache = !homeLibraries && !homeHero;
  const [items, setItems] = useState<HydratedItem[] | null>(
    () => (useCache ? readViewerCache<HydratedItem[]>(identity, cacheKey) : undefined) ?? null,
  );
  const [failedArtwork, setFailedArtwork] = useState<ReadonlySet<string>>(() => new Set());
  const [loadedArtwork, setLoadedArtwork] = useState("");

  useEffect(() => {
    let alive = true;
    const controller = new AbortController();
    const cached = useCache ? readViewerCache<HydratedItem[]>(identity, cacheKey) : undefined;
    setItems(cached ?? null);
    setFailedArtwork(new Set());
    setLoadedArtwork("");
    onHydrationStateChange?.(cached ? (cached.length ? "visible" : "empty") : "loading");
    const abandon = () => {
      alive = false;
      window.clearTimeout(timeout);
      controller.abort();
    };
    const fail = () => {
      onConnectionLost?.();
      if (cached) return;
      setItems([]);
      onHydrationStateChange?.("failed");
    };
    const timeout = window.setTimeout(() => {
      if (!alive) return;
      abandon();
      fail();
    }, SECTION_LOAD_TIMEOUT_MS);
    const context = { libraryID, entityID, extraFilters, home };
    const request = homeLibraries
      ? fetchHomeRecent(section, context, homeLibraries, controller.signal)
      : fetchSectionItems(
          section,
          homeHero ? { ...context, paging: HOME_RECENT_WINDOW } : context,
          controller.signal,
        );
    request
      .then((next) => {
        if (!alive) return;
        window.clearTimeout(timeout);
        if (useCache) writeViewerCache(identity, cacheKey, next);
        setItems(next);
        if (homeHero) {
          onHydrationStateChange?.(featuredArtwork(next, new Set()) ? "loading" : "empty");
        } else {
          onHydrationStateChange?.(next.length ? "visible" : "empty");
        }
      })
      .catch((err: unknown) => {
        if (!alive) return;
        window.clearTimeout(timeout);
        if (isAuthRejection(err)) {
          clearViewerCache();
          setItems([]);
          onHydrationStateChange?.("empty");
          onConnectionLost?.();
          return;
        }
        fail();
      });
    return abandon;
    // cacheKey serializes libraryID, entityID, home and extraFilters (a fresh object
    // per selection) into a stable dependency. The Section object itself stays a
    // dependency: a reloaded Layout must re-report hydration state (LayoutView
    // tracks it per Layout object) and revalidates, served from the cache.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [identity, section, cacheKey, engagementEpoch, homeLibraries, homeHero, useCache]);

  // Newly added items fold under their container (CORE-2524); a container's own
  // page lists its items as they are.
  const shown = useMemo<HydratedItem[] | null>(
    () =>
      items && !homeLibraries && !entityID && groupsNewByContainer(section)
        ? groupByContainer(items, (count) => t("cardNewCount", { count }))
        : items,
    [items, entityID, homeLibraries, section, t],
  );

  if (shown === null) {
    return <SectionSkeleton hero={section.kind === "hero"} />;
  }
  if (!shown.length) {
    return null; // empty sections are hidden (ADR-0014)
  }
  if (homeHero) {
    const feature = featuredArtwork(shown, failedArtwork);
    if (!feature) return null;
    return (
      <Hero
        key={feature.url}
        section={section}
        item={feature.item}
        navigationID={navigationID}
        artwork={{
          url: feature.url,
          ready: loadedArtwork === feature.url,
          onLoad: () => {
            setLoadedArtwork(feature.url);
            onHydrationStateChange?.("visible");
          },
          onError: () => {
            const failed = new Set(failedArtwork).add(feature.url);
            setFailedArtwork(failed);
            if (!featuredArtwork(shown, failed)) onHydrationStateChange?.("empty");
          },
        }}
      />
    );
  }
  if (section.kind === "hero") {
    return <Hero section={section} item={shown[0]} navigationID={navigationID} />;
  }
  if (homeLibraries) {
    return (
      <section className="flex flex-col gap-6">
        <SectionHeading
          title={displayString(section.title, displayLocale)}
          onSeeAll={seeAll ? () => navigate(seeAll.href, { state: seeAll.state }) : undefined}
        />
        {homeLibraries.map((library) => {
          const recent = shown.filter((item) => item.homeLibraryID === library.id);
          if (!recent.length) return null;
          return (
            <SectionView
              key={library.id}
              section={{ ...section, id: `${section.id}:${library.id}`, title: library.title }}
              items={groupByContainer(recent, (count) => t("cardNewCount", { count }))}
              navigationID={navigationID}
              headingLevel="h3"
              intrinsicArtwork
            />
          );
        })}
      </section>
    );
  }
  return (
    <SectionView
      section={section}
      items={shown}
      seeAll={seeAll}
      navigationID={navigationID}
      showPlayButton={Boolean(entityID) && section.source?.kind === "entity-media"}
      nameContainer={!entityID}
      intrinsicArtwork={home}
    />
  );
}

// Skeleton placeholder, ported from buildSkeleton (app.js): never focusable,
// aria-hidden. The placeholder tone (bf-shimmer) is a static fill in CSS,
// matching the design system's "skeletons fade to content, never focusable"
// rule. It reuses the real hero and shelf classes, so the frame, card width,
// gap and copy height match and the layout does not shift when content arrives.
export function SectionSkeleton({ hero }: { hero: boolean }) {
  if (hero) {
    return (
      <div aria-hidden className="bf-section">
        <Skeleton className="bf-hero bf-shimmer" />
      </div>
    );
  }
  return (
    <div aria-hidden className="bf-section flex flex-col gap-3">
      <Skeleton className="bf-shimmer mb-1 h-7 w-44" />
      <div className="bf-shelf overflow-hidden">
        {Array.from({ length: 7 }).map((_, i) => (
          <div key={i} className="bf-card shrink-0">
            <Skeleton className="bf-shimmer aspect-[2/3] w-full" />
            <div className="bf-card-copy">
              <Skeleton className="h-[19px] w-3/4 bg-[var(--bg-surface)]" />
              <Skeleton className="h-[18px] w-1/3 bg-[var(--bg-surface)]" />
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
