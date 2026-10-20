import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router";
import type { FieldFilters, Layout, Section } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import { applyRenderFocus } from "../lib/renderFocus";
import { MAX_RENDERED_SECTIONS, SECTION_KINDS } from "../lib/sections";
import { FilterBar } from "./FilterBar";
import { HydratedSection } from "./HydratedSection";
import type { SeeAllTarget } from "./SectionView";

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
  buildSeeAll,
  navigationID,
}: {
  layout: Layout | null;
  libraryID: string;
  /** The context entity for a detail Layout; "" on browse. */
  entityID?: string;
  /** Maps a (shelf) Section to its See-All target; omit to hide See-All. */
  buildSeeAll?: (section: Section) => SeeAllTarget | undefined;
  /** Stable navigation identity for initial focus and Reveal/relock restore. */
  navigationID?: string;
}) {
  const t = useT();
  const { manifest, isManager } = useSession();
  const [connectionLost, setConnectionLost] = useState(false);
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
    states: Record<string, "loading" | "empty" | "visible">;
  }>({ layout, states: {} });
  // field → value selections, keyed by the filter-bar that owns them.
  const [filterSelections, setFilterSelections] = useState<Record<string, FieldFilters>>({});

  const renderable = useMemo<Section[]>(() => {
    const known = (layout?.sections || []).filter((s) => SECTION_KINDS.has(s.kind));
    if (known.length > MAX_RENDERED_SECTIONS) {
      // Intentionally omits Section ids, titles, counts, and Reveal state. The
      // diagnostic is actionable without leaking filtered-layout contents.
      console.warn("Beamfall rendering diagnostic: SectionLimitExceeded");
    }
    return known.slice(0, MAX_RENDERED_SECTIONS);
  }, [layout]);

  const hydratable = useMemo(
    () => renderable.filter((section) => section.kind !== "filter-bar"),
    [renderable],
  );
  const hydrationStates = hydration.layout === layout ? hydration.states : {};
  const allHydrated = hydratable.every((section) => {
    const state = hydrationStates[section.id];
    return state === "empty" || state === "visible";
  });
  const hasVisibleSection = hydratable.some((section) => hydrationStates[section.id] === "visible");
  const aggregateEmpty = hydratable.length === 0 || (allHydrated && !hasVisibleSection);

  const updateHydrationState = useCallback(
    (sectionID: string, state: "loading" | "empty" | "visible") => {
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
    focusAppliedRef.current = Boolean(applyRenderFocus(rootRef.current, navigationID));
  }, [allHydrated, hasVisibleSection, navigationID]);

  // Map each filter-bar to the index of the section it narrows: the next
  // item-bearing section that is neither a filter-bar nor a hero (buildFilterBar).
  const filterTargets = useMemo<Map<number, number>>(() => {
    const map = new Map<number, number>();
    renderable.forEach((section, index) => {
      if (section.kind !== "filter-bar") return;
      for (let i = index + 1; i < renderable.length; i++) {
        const kind = renderable[i].kind;
        if (kind !== "filter-bar" && kind !== "hero") {
          map.set(index, i);
          return;
        }
      }
    });
    return map;
  }, [renderable]);

  // The library accent (validated literal #RRGGBB) tints the fallback gradients.
  const accentVar = useMemo<Record<string, string>>(() => {
    const library = (manifest?.libraries || []).find((l) => l.id === libraryID);
    const accent = library?.accent || "";
    return /^#[0-9a-fA-F]{6}$/.test(accent) ? { ["--library-accent" as string]: accent } : {};
  }, [manifest, libraryID]);

  // Which extraFilters a given section index receives, if it is a filter-bar target.
  const extraFiltersForIndex = (index: number): FieldFilters | null => {
    for (const [barIndex, targetIndex] of filterTargets) {
      if (targetIndex === index) {
        return filterSelections[barIndex] || null;
      }
    }
    return null;
  };

  function toggleFilter(barIndex: number, field: string, value: string) {
    setFilterSelections((prev) => {
      const current = { ...(prev[barIndex] || {}) };
      if (current[field] === value) {
        delete current[field];
      } else {
        current[field] = value;
      }
      return { ...prev, [barIndex]: current };
    });
  }

  if (!layout || !renderable.length) {
    return <LibraryEmpty />;
  }

  return (
    <div ref={rootRef} style={accentVar} className="flex flex-col gap-8">
      {connectionLost && (
        <div
          role="status"
          className="rounded-[var(--radius-card)] border border-[oklch(78%_0.16_78_/_40%)] bg-[oklch(78%_0.16_78_/_12%)] px-3 py-2 text-sm text-[var(--text-secondary)]"
        >
          {t("connectionLost")}
        </div>
      )}
      {aggregateEmpty ? (
        <LibraryEmpty />
      ) : (
        renderable.map((section, index) => {
          if (section.kind === "filter-bar") {
            // A filter-bar with no options or no adjacent target collapses (ADR-0014).
            if (!(section.filters || []).length || !filterTargets.has(index)) {
              return null;
            }
            return (
              <FilterBar
                key={section.id}
                section={section}
                selected={filterSelections[index] || {}}
                onToggle={(field, value) => toggleFilter(index, field, value)}
              />
            );
          }
          return (
            <HydratedSection
              key={section.id}
              section={section}
              libraryID={libraryID}
              entityID={entityID}
              extraFilters={extraFiltersForIndex(index)}
              seeAll={section.kind === "shelf" ? buildSeeAll?.(section) : undefined}
              onConnectionLost={() => setConnectionLost(true)}
              onHydrationStateChange={(state) => updateHydrationState(section.id, state)}
              navigationID={navigationID}
            />
          );
        })
      )}
    </div>
  );

  // Empty-as-activation (onboarding-funnel §6). For the OWNER/manager an empty
  // browse is an activation surface: a headline + a forward action (start the
  // first scan / migrate) so a fresh install never sits inert. For every other
  // surface — a managed/locked Profile, or any Reveal-filtered no-access empty —
  // it stays the EXACT inert empty, byte-indistinguishable from genuine-empty
  // (no count, no "restricted", no activation CTA that could betray hidden
  // content). The activation treatment is gated STRICTLY behind isManager, per
  // ADR-0101 §2 / ADR-0004: no-access ≡ empty/absent.
  function LibraryEmpty() {
    if (!isManager) {
      return (
        <div className="bf-empty flex flex-col gap-2" role="status">
          <p className="text-lg font-semibold text-[var(--text-primary)]">
            {t("libraryEmptyTitle")}
          </p>
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
        <div className="flex flex-wrap gap-2">
          <Link to="/console/libraries" className="bf-action-link">
            {t("libraryEmptyStartScan")}
          </Link>
          <Link to="/console/remote" className="bf-action-link">
            {t("libraryEmptyMigrate")}
          </Link>
        </div>
      </div>
    );
  }
}
