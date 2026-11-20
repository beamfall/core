import { ChevronRight } from "lucide-react";
import { useMemo, useRef } from "react";
import { useNavigate } from "react-router";
import { displayString } from "@/lib/display";
import type { Section, SourceItem } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { repeatedTitles, showsYear } from "../lib/cardTitles";
import { isRankedShelf, rankShelfItems } from "../lib/containerPage";
import { SECTION_GRID_CAP } from "../lib/sections";
import { playQueueOf } from "../lib/watchQueue";
import { MediaItemCard } from "./MediaItemCard";
import { useVirtualGrid, VIRTUAL_GRID_MIN_ITEMS } from "./useVirtualGrid";

/** A See-All target: the route plus the navigation state carrying the Source context. */
export interface SeeAllTarget {
  href: string;
  state: unknown;
}

// One rendered Section (shelf / grid / list / info), ported from the vanilla
// buildSection (app.js). Hero and filter-bar are handled elsewhere (Hero.tsx /
// FilterBar.tsx). A heading plus a row/grid of item cards; shelves carry a
// trailing "See all" affordance that opens the full Source as a paged grid.
//
// A large `grid` Section is windowed (useVirtualGrid) — only the rows
// intersecting the viewport mount, padded above/below with spacer rows so the
// scroll height stays correct. Windowing engages only past VIRTUAL_GRID_MIN_ITEMS
// (the vanilla threshold); smaller grids and shelves/stacks render plainly. No
// virtualization dependency is added — the hook measures the live grid geometry.
export function SectionView({
  section,
  items: served,
  seeAll,
  navigationID,
  playOnActivate = section.kind === "list",
  showPlayButton = false,
  nameContainer = true,
  intrinsicArtwork = false,
  headingLevel = "h2",
}: {
  section: Section;
  items: SourceItem[];
  /** When set (shelves), renders the See-All affordance to this target. */
  seeAll?: SeeAllTarget;
  navigationID?: string;
  /** Cards start playback on activation, which is the default for list Sections. */
  playOnActivate?: boolean;
  /** Child Media open detail and have a separate Play button. */
  showPlayButton?: boolean;
  /** Cards name the container holding their item; false inside a container's page (CORE-2524). */
  nameContainer?: boolean;
  intrinsicArtwork?: boolean;
  headingLevel?: "h2" | "h3";
}) {
  const displayLocale = useDisplayLocale();
  const navigate = useNavigate();
  const gridRef = useRef<HTMLUListElement | null>(null);
  // A catalog shelf reads in progress first, never in plain title order
  // (CORE-2512); grids and See All keep the server's order.
  const ranked = isRankedShelf(section);
  const items = useMemo(() => (ranked ? rankShelfItems(served) : served), [ranked, served]);

  // Layout per kind (the design-system Section renderers): shelf is a horizontal
  // scroll rail; grid is a responsive poster grid; list/info are dense vertical
  // stacked rows. The treatments live in chrome.css under [data-register].
  const isGrid = section.kind !== "shelf" && section.kind !== "list" && section.kind !== "info";
  const containerClass =
    section.kind === "shelf"
      ? "bf-shelf"
      : section.kind === "list" || section.kind === "info"
        ? "bf-stack"
        : "bf-grid";

  const virtualize = isGrid && items.length > VIRTUAL_GRID_MIN_ITEMS;
  const window = useVirtualGrid({
    gridRef,
    itemCount: items.length,
    enabled: virtualize,
  });

  // The visible slice: the whole set unless windowing produced a range.
  const start = window ? window.startIndex : 0;
  const end = window ? window.endIndex : items.length;
  const visible = window ? items.slice(start, end) : items;
  const queue = useMemo(() => playQueueOf(items), [items]);
  const repeated = useMemo(() => repeatedTitles(items), [items]);
  const title = displayString(section.title, displayLocale);

  return (
    <section className="bf-section flex flex-col gap-3">
      <SectionHeading
        title={title}
        level={headingLevel}
        onSeeAll={
          seeAll && (section.kind === "shelf" || items.length >= SECTION_GRID_CAP)
            ? () => navigate(seeAll.href, { state: seeAll.state })
            : undefined
        }
      />
      <ul ref={gridRef} className={containerClass} aria-label={title}>
        {window && window.topPad > 0 && (
          <li
            aria-hidden
            role="presentation"
            className="bf-grid-spacer"
            style={{ gridColumn: "1 / -1", height: `${window.topPad}px` }}
          />
        )}
        {visible.map((item, offset) => {
          const index = start + offset;
          return (
            <MediaItemCard
              key={`${item.id}-${index}`}
              item={item}
              posInSet={index + 1}
              setSize={items.length}
              queue={queue}
              playOnActivate={!showPlayButton && playOnActivate}
              showPlayButton={showPlayButton}
              showYear={showsYear(item, repeated)}
              nameContainer={nameContainer}
              intrinsicArtwork={intrinsicArtwork}
              focusAnchor={
                navigationID
                  ? {
                      navigation: navigationID,
                      section: section.id,
                      item: item.id,
                    }
                  : undefined
              }
            />
          );
        })}
        {window && window.bottomPad > 0 && (
          <li
            aria-hidden
            role="presentation"
            className="bf-grid-spacer"
            style={{ gridColumn: "1 / -1", height: `${window.bottomPad}px` }}
          />
        )}
      </ul>
    </section>
  );
}

export function SectionHeading({
  title,
  level: Heading = "h2",
  onSeeAll,
}: {
  title: string;
  level?: "h2" | "h3";
  onSeeAll?: () => void;
}) {
  const t = useT();
  return (
    <Heading className="bf-section-title">
      <span>{title}</span>
      {onSeeAll && (
        <button type="button" onClick={onSeeAll} className="bf-see-all">
          {t("seeAll")}
          <ChevronRight aria-hidden className="size-4" />
        </button>
      )}
    </Heading>
  );
}
