import { useEffect, useState } from "react";
import { Skeleton } from "@/components/ui/skeleton";
import { isAuthRejection } from "@/lib/api";
import type { FieldFilters, Section, SourceItem } from "@/lib/graphql/types";
import { useViewerIdentity } from "@/lib/session";
import {
  clearViewerCache,
  readViewerCache,
  viewerCacheEntries,
  writeViewerCache,
} from "@/lib/viewerCache";
import { useEngagementEpoch } from "../actions/engagementSync";
import { fetchSectionItems } from "../lib/sections";
import { Hero } from "./Hero";
import { SectionView, type SeeAllTarget } from "./SectionView";

export const SECTION_LOAD_TIMEOUT_MS = 15000;

/** "failed" is a network/timeout failure with nothing cached: hidden like
 * "empty", but reported separately so the Layout never mistakes an unreachable
 * server for an empty library. */
export type SectionHydrationState = "loading" | "empty" | "failed" | "visible";

const SECTION_CACHE_PREFIX = "section:";

/** The viewer-cache key for one hydrated slot (scoped by identity in viewerCache). */
export function sectionCacheKey(
  section: Section,
  libraryID: string,
  entityID: string,
  extraFilters: FieldFilters | null | undefined,
): string {
  return (
    SECTION_CACHE_PREFIX + JSON.stringify([section, libraryID, entityID, extraFilters || null])
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
  extraFilters,
  seeAll,
  onConnectionLost,
  onHydrationStateChange,
  navigationID,
}: {
  section: Section;
  libraryID: string;
  entityID: string;
  /** A filter-bar selection routed to this (target) section; null when none. */
  extraFilters?: FieldFilters | null;
  seeAll?: SeeAllTarget;
  onConnectionLost?: () => void;
  onHydrationStateChange?: (state: SectionHydrationState) => void;
  navigationID?: string;
}) {
  const identity = useViewerIdentity();
  // Watched, watchlist and rating changes (here or on another client) revalidate
  // the section behind the items already shown.
  const engagementEpoch = useEngagementEpoch();
  const cacheKey = sectionCacheKey(section, libraryID, entityID, extraFilters);
  const [items, setItems] = useState<SourceItem[] | null>(
    () => readViewerCache<SourceItem[]>(identity, cacheKey) ?? null,
  );

  useEffect(() => {
    let alive = true;
    const controller = new AbortController();
    const cached = readViewerCache<SourceItem[]>(identity, cacheKey);
    setItems(cached ?? null);
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
    fetchSectionItems(section, { libraryID, entityID, extraFilters }, controller.signal)
      .then((next) => {
        if (!alive) return;
        window.clearTimeout(timeout);
        writeViewerCache(identity, cacheKey, next);
        setItems(next);
        onHydrationStateChange?.(next.length ? "visible" : "empty");
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
    // cacheKey serializes libraryID, entityID and extraFilters (a fresh object
    // per selection) into a stable dependency. The Section object itself stays a
    // dependency: a reloaded Layout must re-report hydration state (LayoutView
    // tracks it per Layout object) and revalidates, served from the cache.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [identity, section, cacheKey, engagementEpoch]);

  if (items === null) {
    return <SectionSkeleton hero={section.kind === "hero"} />;
  }
  if (!items.length) {
    return null; // empty sections are hidden (ADR-0014)
  }
  if (section.kind === "hero") {
    return <Hero section={section} item={items[0]} navigationID={navigationID} />;
  }
  return (
    <SectionView section={section} items={items} seeAll={seeAll} navigationID={navigationID} />
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
