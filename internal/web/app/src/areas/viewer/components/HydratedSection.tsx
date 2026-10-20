import { useEffect, useState } from "react";
import { Skeleton } from "@/components/ui/skeleton";
import type { FieldFilters, Section, SourceItem } from "@/lib/graphql/types";
import { fetchSectionItems } from "../lib/sections";
import { Hero } from "./Hero";
import { SectionView, type SeeAllTarget } from "./SectionView";

export const SECTION_LOAD_TIMEOUT_MS = 15000;

// Hydrates one Section into a slot, ported from the vanilla hydrateSection
// (app.js): skeleton first (never focusable), content on arrival, collapse to
// nothing when empty (ADR-0014). A re-fetch (a filter-bar chip flip changing
// extraFilters) supersedes any in-flight older fetch — the stale resolution is
// dropped, so the freshest selection always wins.
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
  onHydrationStateChange?: (state: "loading" | "empty" | "visible") => void;
  navigationID?: string;
}) {
  const [items, setItems] = useState<SourceItem[] | null>(null);

  useEffect(() => {
    let alive = true;
    setItems(null);
    onHydrationStateChange?.("loading");
    const timeout = window.setTimeout(() => {
      if (!alive) return;
      alive = false;
      onConnectionLost?.();
      setItems([]);
      onHydrationStateChange?.("empty");
    }, SECTION_LOAD_TIMEOUT_MS);
    fetchSectionItems(section, { libraryID, entityID, extraFilters })
      .then((next) => {
        if (!alive) return;
        window.clearTimeout(timeout);
        setItems(next);
        onHydrationStateChange?.(next.length ? "visible" : "empty");
      })
      .catch(() => {
        if (!alive) return;
        window.clearTimeout(timeout);
        onConnectionLost?.();
        setItems([]);
        onHydrationStateChange?.("empty");
      });
    return () => {
      alive = false;
      window.clearTimeout(timeout);
    };
    // extraFilters is a fresh object per selection; serialize for a stable dep.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [section, libraryID, entityID, JSON.stringify(extraFilters || null)]);

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
// aria-hidden. The shimmer (bf-shimmer) lives in CSS and reduce-motion drops it,
// leaving the static tone — matching the design system's "skeletons fade to
// content, never focusable" rule. Geometry mirrors a shelf rail so the layout
// does not shift when real content arrives.
function SectionSkeleton({ hero }: { hero: boolean }) {
  if (hero) {
    return (
      <div aria-hidden className="bf-section">
        <Skeleton className="bf-shimmer aspect-[21/9] max-h-[440px] w-full rounded-[var(--radius-card)]" />
      </div>
    );
  }
  return (
    <div aria-hidden className="bf-section flex flex-col gap-3">
      <Skeleton className="bf-shimmer h-[26px] w-52" />
      <div className="flex gap-[14px] overflow-hidden p-1">
        {Array.from({ length: 7 }).map((_, i) => (
          <Skeleton
            key={i}
            className="bf-shimmer aspect-[2/3] w-[150px] shrink-0 rounded-[var(--radius-card)]"
          />
        ))}
      </div>
    </div>
  );
}
