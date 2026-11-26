import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigationType } from "react-router";
import { ErrorState } from "@/components/beamfall/workbench";
import { sectionMatchesLens, useExperienceLens } from "@/components/chrome/experienceLens";
import { Button, buttonVariants } from "@/components/ui/button";
import type { FieldFilters, Layout, Section } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import { HiddenAwareEmpty } from "../actions/HiddenControls";
import { applyRenderFocus } from "../lib/renderFocus";
import { findHomeRecentSection, MAX_RENDERED_SECTIONS, SECTION_KINDS } from "../lib/sections";
import { FilterBar } from "./FilterBar";
import { FirstScanButton } from "./FirstScanButton";
import { Hero } from "./Hero";
import { HomeRecentShelves, useHomeArtwork, useHomeRecent } from "./HomeRecent";
import { HydratedSection, type SectionHydrationState, SectionSkeleton } from "./HydratedSection";
import type { SeeAllTarget } from "./SectionView";

const HOME_ARTWORK_FOCUS_WAIT_MS = 1000;

// Renders one Layout's Sections in declared order, ported from the vanilla
// renderLayout (app.js). Unknown Section kinds are silently dropped and the
// known set is capped at MAX_RENDERED_SECTIONS (ADR-0014). A filter-bar narrows
// the next item-bearing Section below it (not another filter-bar or a hero) by
// merging its one-per-field selection into that Section's fieldFilters. The
// selected Library's accent (manifest LibrarySummary.accent, validated #RRGGBB)
// is applied as a CSS custom property the card/hero fallback gradients tint off.
export function LayoutView({
  layout,
  libraryID,
  entityID = "",
  home = false,
  buildSeeAll,
  navigationID,
  onEmptyChange,
}: {
  layout: Layout | null;
  libraryID: string;
  /** The context entity for a detail Layout; "" on browse. */
  entityID?: string;
  /** Rendering Home: aggregate Sections leave out Libraries hidden from Home (CORE-2360). */
  home?: boolean;
  /** Maps a (shelf) Section to its See-All target; omit to hide See-All. */
  buildSeeAll?: (section: Section, homeShelfLibraryID?: string) => SeeAllTarget | undefined;
  /** Stable navigation identity for initial focus and Reveal/relock restore. */
  navigationID?: string;
  /** Told whether the Layout settled with nothing to show (not a load failure). */
  onEmptyChange?: (empty: boolean) => void;
}) {
  const t = useT();
  const { manifest, isManager, refresh } = useSession();
  const { activeLens, libraryIDFilter } = useExperienceLens();
  const homeLensLibraryIDs = home && !libraryID ? libraryIDFilter : undefined;
  const navigationType = useNavigationType();
  const [connectionLost, setConnectionLost] = useState(false);
  // Bumped by the failure state's retry: remounts every Section so each
  // re-fetches, without reloading the whole session.
  const [attempt, setAttempt] = useState(0);
  const rootRef = useRef<HTMLDivElement | null>(null);
  const focusAppliedRef = useRef(false);
  const interactedDuringHydrationRef = useRef(false);
  const previousLayoutRef = useRef(layout);
  if (previousLayoutRef.current !== layout) {
    previousLayoutRef.current = layout;
    focusAppliedRef.current = false;
    interactedDuringHydrationRef.current = false;
  }
  const [hydration, setHydration] = useState<{
    layout: Layout | null;
    states: Record<string, SectionHydrationState>;
  }>({ layout, states: {} });
  // field → value selections, keyed by the filter-bar Section id and scoped to
  // the active Layout and navigation context so selections never leak across nodes.
  const filterScope = `${layout?.id ?? ""}\0${libraryID}\0${entityID}\0${navigationID ?? ""}`;
  const [filterState, setFilterState] = useState<{
    scope: string;
    selections: Record<string, FieldFilters>;
  }>({ scope: filterScope, selections: {} });
  // A selection belongs to the node it was made on: leaving the scope drops it,
  // so returning later does not resurrect it.
  const filterInScope = filterState.scope === filterScope;
  if (!filterInScope) setFilterState({ scope: filterScope, selections: {} });
  const filterSelections = filterInScope ? filterState.selections : {};

  const { sections, homeRecent, homeHero, homeContinue } = useMemo(() => {
    const all = layout?.sections || [];
    const known = all
      .filter((s) => SECTION_KINDS.has(s.kind))
      .filter((s) => !home || libraryID || sectionMatchesLens(s, activeLens));
    // Unknown kinds are still dropped, but each distinct one is named once
    // (CORE-0203), like Apple's unknownSectionKind. The kind is plugin
    // vocabulary; the diagnostic carries no Section ids, titles or Reveal state.
    for (const kind of new Set(all.map((s) => s.kind).filter((k) => !SECTION_KINDS.has(k)))) {
      console.warn(`Beamfall rendering diagnostic: UnknownSectionKind ${kind}`);
    }
    const sections = known.slice(0, MAX_RENDERED_SECTIONS);
    const homeRecent = home && !libraryID ? findHomeRecentSection(sections) : undefined;
    const homeHero: Section | undefined =
      !layout?.suppressAutoHero &&
      homeRecent &&
      !sections.some((section) => section.kind === "hero")
        ? { ...homeRecent, id: `${homeRecent.id}:featured`, kind: "hero", title: t("homeFeatured") }
        : undefined;
    const homeContinue = homeHero
      ? sections.find((section) => section.source?.kind === "continue-watching")
      : undefined;
    if (known.length > MAX_RENDERED_SECTIONS) {
      // The diagnostic omits Section ids, titles, counts, and Reveal state.
      console.warn("Beamfall rendering diagnostic: SectionLimitExceeded");
    }
    return { sections, homeRecent, homeHero, homeContinue };
  }, [layout, home, libraryID, activeLens, t]);

  // Map each filter-bar to the index of the section it narrows: the next
  // item-bearing section that is neither a filter-bar nor a hero (buildFilterBar).
  const filterTargets = useMemo<Map<number, number>>(() => {
    const map = new Map<number, number>();
    sections.forEach((section, index) => {
      if (section.kind !== "filter-bar") return;
      for (let i = index + 1; i < sections.length; i++) {
        const kind = sections[i].kind;
        if (kind !== "filter-bar" && kind !== "hero") {
          map.set(index, i);
          return;
        }
      }
    });
    return map;
  }, [sections]);

  // Which extraFilters a given section index receives, if it is a filter-bar target.
  const extraFiltersForIndex = (index: number): FieldFilters | null => {
    for (const [barIndex, targetIndex] of filterTargets) {
      if (targetIndex === index) {
        const barID = sections[barIndex]?.id;
        return (barID ? filterSelections[barID] : undefined) || null;
      }
    }
    return null;
  };

  const reportConnectionLost = useCallback(() => setConnectionLost(true), []);
  const recentFilters = homeRecent ? extraFiltersForIndex(sections.indexOf(homeRecent)) : null;
  const recentFeed = useHomeRecent(
    homeRecent,
    recentFilters,
    attempt,
    reportConnectionLost,
    homeLensLibraryIDs,
  );
  const hydrationStates = hydration.layout === layout ? hydration.states : {};
  // A Profile with something to resume sees Continue Watching first, above the
  // fold, not a newest-arrival feature (CORE-2825). The feature waits for that
  // shelf to settle, so it never appears and then vanishes.
  const continueState = homeContinue ? hydrationStates[homeContinue.id] : "empty";
  const featured = continueState === "empty" || continueState === "failed" ? homeHero : undefined;
  const feature = useHomeArtwork(featured ? recentFeed.heroItems : [], recentFeed.epoch);
  // Replace the manifest's Recent slot with one slot per visible library.
  const maxRecentShelves = MAX_RENDERED_SECTIONS - sections.length + 1;
  const [artworkWaitExpired, setArtworkWaitExpired] = useState<string>();
  const waitingForArtwork = Boolean(
    featured && (feature ? !feature.artwork.ready : recentFeed.pending),
  );
  const waitToFocus = waitingForArtwork && artworkWaitExpired !== recentFeed.epoch;
  useEffect(() => {
    if (!waitToFocus || !navigationID || focusAppliedRef.current) return;
    // Give the hero first focus when it loads promptly. A stalled image leaves
    // the focus target in the manifest layout once this grace period expires.
    const timeout = window.setTimeout(
      () => setArtworkWaitExpired(recentFeed.epoch),
      HOME_ARTWORK_FOCUS_WAIT_MS,
    );
    return () => window.clearTimeout(timeout);
  }, [navigationID, recentFeed.epoch, waitToFocus]);

  const hydratable = useMemo(
    () => sections.filter((section) => section.kind !== "filter-bar"),
    [sections],
  );
  const stateFor = (section: Section) =>
    section === homeRecent ? recentFeed.state : hydrationStates[section.id];
  const allHydrated = hydratable.every((section) => {
    const state = stateFor(section);
    return state !== undefined && state !== "loading";
  });
  const hasVisibleSection = hydratable.some((section) => stateFor(section) === "visible");
  const aggregateEmpty = hydratable.length === 0 || (allHydrated && !hasVisibleSection);
  // Nothing visible and at least one Section could not be reached: the page is
  // a load failure, not an empty library (no owner "first scan" CTA).
  const aggregateFailed =
    aggregateEmpty && hydratable.some((section) => stateFor(section) === "failed");

  const settledEmpty = aggregateEmpty && !aggregateFailed;
  // Sections render in manifest order. The server leads a default Home with
  // Continue Watching (CORE-2825) and applies the Profile's own order when it
  // has saved one (CORE-1972), so the client never reorders.
  const renderOrder = sections.map((section, index) => ({ section, index }));
  useEffect(() => {
    onEmptyChange?.(settledEmpty);
  }, [onEmptyChange, settledEmpty]);

  const updateHydrationState = useCallback(
    (sectionID: string, state: SectionHydrationState) => {
      setHydration((current) => ({
        layout,
        states: {
          ...(current.layout === layout ? current.states : {}),
          [sectionID]: state,
        },
      }));
    },
    [layout],
  );

  useEffect(() => {
    const markInteraction = () => {
      interactedDuringHydrationRef.current = true;
    };
    document.addEventListener("pointerdown", markInteraction, { passive: true });
    document.addEventListener("keydown", markInteraction);
    return () => {
      document.removeEventListener("pointerdown", markInteraction);
      document.removeEventListener("keydown", markInteraction);
    };
  }, []);

  useEffect(() => {
    if (
      focusAppliedRef.current ||
      !navigationID ||
      !allHydrated ||
      waitToFocus ||
      !hasVisibleSection ||
      !rootRef.current
    ) {
      return;
    }
    // Never yank focus back into a late-hydrating Layout after the user has
    // already chosen another target. Their interaction itself satisfies the
    // no-focus-less-screen guarantee.
    if (interactedDuringHydrationRef.current) {
      focusAppliedRef.current = true;
      return;
    }
    focusAppliedRef.current = Boolean(
      applyRenderFocus(rootRef.current, navigationID, {
        preventScroll: navigationType === "POP",
      }),
    );
  }, [allHydrated, hasVisibleSection, navigationID, navigationType, waitToFocus]);

  // The library accent (validated literal #RRGGBB) tints the fallback gradients.
  const accentVar = useMemo<Record<string, string>>(() => {
    const library = (manifest?.libraries || []).find((l) => l.id === libraryID);
    const accent = library?.accent || "";
    return /^#[0-9a-fA-F]{6}$/.test(accent) ? { ["--library-accent" as string]: accent } : {};
  }, [manifest, libraryID]);

  function toggleFilter(barID: string, field: string, value: string) {
    setFilterState((prev) => {
      const base = prev.scope === filterScope ? prev.selections : {};
      const current = { ...(base[barID] || {}) };
      if (current[field] === value) {
        delete current[field];
      } else {
        current[field] = value;
      }
      return { scope: filterScope, selections: { ...base, [barID]: current } };
    });
  }

  // No manifest at all once the session settled means it failed to load (the
  // session keeps a session even when the manifest fetch fails).
  if (!layout && !manifest) {
    return <LoadFailed onRetry={() => void refresh()} />;
  }
  // A detail Layout with nothing to show renders nothing: the Entity page
  // around it already has its header, and the library onboarding empty state
  // (first scan, migrate) does not belong inside an Entity (CORE-1786).
  if (!layout || !sections.length) {
    return entityID ? null : <LibraryEmpty isManager={isManager} />;
  }

  function retrySections() {
    setConnectionLost(false);
    setHydration({ layout, states: {} });
    setAttempt((n) => n + 1);
  }

  return (
    <div ref={rootRef} style={accentVar} className="flex flex-col gap-10">
      {connectionLost && (
        <div
          role="status"
          className="rounded-[var(--radius-field)] border border-[color-mix(in_oklab,var(--state-warning)_40%,transparent)] bg-[color-mix(in_oklab,var(--state-warning)_12%,transparent)] px-3 py-2 text-sm text-[var(--text-secondary)]"
        >
          {t("connectionLost")}
        </div>
      )}
      {featured &&
        !aggregateEmpty &&
        (feature ? (
          <Hero
            key={JSON.stringify([recentFeed.epoch, feature.item.id, feature.artwork.url])}
            section={featured}
            item={feature.item}
            artwork={feature.artwork}
            navigationID={feature.artwork.ready ? navigationID : undefined}
          />
        ) : recentFeed.state === "loading" && sections.length < MAX_RENDERED_SECTIONS ? (
          <SectionSkeleton hero />
        ) : null)}
      {aggregateFailed ? (
        <LoadFailed onRetry={retrySections} />
      ) : aggregateEmpty ? (
        entityID ? null : (
          <HiddenAwareEmpty fallback={<LibraryEmpty isManager={isManager} />} />
        )
      ) : (
        renderOrder.map(({ section, index }) => {
          if (section.kind === "filter-bar") {
            // A filter-bar with no options or no adjacent target collapses (ADR-0014).
            const target = filterTargets.get(index);
            if (!(section.filters || []).length || target === undefined) {
              return null;
            }
            return (
              <FilterBar
                key={section.id}
                section={section}
                selected={filterSelections[section.id] || {}}
                onToggle={(field, value) => toggleFilter(section.id, field, value)}
              />
            );
          }
          if (section === homeRecent) {
            return (
              <HomeRecentShelves
                key={recentFeed.epoch}
                section={section}
                libraries={recentFeed.libraries}
                shelves={recentFeed.shelves}
                fallbackItems={recentFeed.fallbackItems}
                pending={recentFeed.pending}
                maxShelves={maxRecentShelves}
                buildSeeAll={(shelf, libraryID) =>
                  buildSeeAll?.(
                    !libraryID && recentFilters && shelf.source
                      ? {
                          ...shelf,
                          source: {
                            ...shelf.source,
                            fieldFilters: { ...shelf.source.fieldFilters, ...recentFilters },
                          },
                        }
                      : shelf,
                    libraryID,
                  )
                }
                navigationID={navigationID}
              />
            );
          }
          return (
            <HydratedSection
              key={`${section.id}:${attempt}`}
              section={section}
              libraryID={libraryID}
              libraryIDs={homeLensLibraryIDs}
              entityID={entityID}
              home={home}
              extraFilters={extraFiltersForIndex(index)}
              seeAll={section.kind === "hero" ? undefined : buildSeeAll?.(section)}
              onConnectionLost={reportConnectionLost}
              onHydrationStateChange={(state) => updateHydrationState(section.id, state)}
              navigationID={navigationID}
            />
          );
        })
      )}
    </div>
  );
}

function LoadFailed({ onRetry }: { onRetry: () => void }) {
  const t = useT();
  return (
    <div className="flex flex-col items-start gap-3">
      <ErrorState message="couldNotReachServer" />
      <Button type="button" variant="outline" onClick={onRetry}>
        {t("tryAgain")}
      </Button>
    </div>
  );
}

// Empty-as-activation (onboarding-funnel §6). For the OWNER/manager an empty
// browse is an activation surface: a headline + a forward action (start the
// first scan / migrate) so a fresh install never sits inert. For every other
// surface — a managed/locked Profile, or any Reveal-filtered no-access empty —
// it stays the EXACT inert empty, byte-indistinguishable from genuine-empty
// (no count, no "restricted", no activation CTA that could betray hidden
// content). The activation treatment is gated STRICTLY behind isManager, per
// ADR-0101 §2 / ADR-0004: no-access ≡ empty/absent.
function LibraryEmpty({ isManager }: { isManager: boolean }) {
  const t = useT();
  if (!isManager) {
    return (
      <div className="bf-empty flex flex-col gap-2" role="status">
        <p className="text-lg font-semibold text-[var(--text-primary)]">{t("libraryEmptyTitle")}</p>
        <p className="text-sm text-[var(--text-secondary)]">{t("libraryEmptyLead")}</p>
      </div>
    );
  }
  return (
    <div className="bf-empty flex flex-col gap-3" role="status">
      <p className="text-lg font-semibold text-[var(--text-primary)]">
        {t("libraryEmptyOwnerTitle")}
      </p>
      <p className="text-sm text-[var(--text-secondary)]">{t("libraryEmptyOwnerLead")}</p>
      <div className="mt-2 flex flex-wrap items-start justify-center gap-2">
        <FirstScanButton />
        <Link to="/console/migrate" className={buttonVariants({ variant: "outline" })}>
          {t("libraryEmptyMigrate")}
        </Link>
      </div>
    </div>
  );
}
