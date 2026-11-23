import type { Section, SourceItem } from "@/lib/graphql/types";

/** Whether a Section lists newly added items, which group by container (CORE-2524). */
/** Cards a folded new-items shelf shows (CORE-2823). */
export const NEW_SHELF_CARDS = 20;

/**
 * Items a folded new-items shelf reads (CORE-2823). Folding shrinks the window,
 * so a shelf reading only NEW_SHELF_CARDS items collapses to a handful of cards
 * when one show adds a season; reading deeper refills it after the fold and
 * lets each container's count cover more than the first page.
 */
export const NEW_SHELF_FETCH = 100;

export function groupsNewByContainer(section: Section): boolean {
  return section.kind === "shelf" && section.source?.kind === "recently-added";
}

/**
 * Folds items that share a container into one card for the container (CORE-2524):
 * three new episodes of a show read as the show with "3 new" instead of three
 * near-identical cards. The card opens the container's page and keeps the
 * position of the container's first item. A lone new video folds too, so a
 * single new episode shows its show's poster and opens the show rather than the
 * episode; any other item alone in its container (a lone album track), or held
 * by none, stays as it is. The count is the card's subtitle, so it leads
 * under the title and is part of the card's accessible name (CORE-2553). It
 * counts the items the Profile has not finished (CORE-2823), and falls back to
 * all of them when every one is finished.
 */
export function groupByContainer(
  items: readonly SourceItem[],
  countLabel: (count: number) => string,
): SourceItem[] {
  const counts = new Map<string, number>();
  const unfinished = new Map<string, number>();
  for (const item of items) {
    const id = item.container?.id;
    if (!id) continue;
    counts.set(id, (counts.get(id) ?? 0) + 1);
    if (!item.engagement?.completed) unfinished.set(id, (unfinished.get(id) ?? 0) + 1);
  }
  const placed = new Set<string>();
  const grouped: SourceItem[] = [];
  for (const item of items) {
    const container = item.container;
    const count = container ? (counts.get(container.id) ?? 0) : 0;
    if (!container || (count < 2 && item.mediaClass !== "video")) {
      grouped.push(item);
      continue;
    }
    if (placed.has(container.id)) continue;
    placed.add(container.id);
    const left = unfinished.get(container.id);
    grouped.push({
      id: container.id,
      kind: "entity",
      title: container.title,
      // Nothing left to watch is nothing new: a fully finished group names no count.
      subtitle: left ? countLabel(left) : undefined,
      artwork: container.artwork,
      mediaClass: item.mediaClass,
    });
  }
  return grouped;
}

/** Folds a new-items shelf and keeps the cards it shows (CORE-2823). */
export function foldNewShelf(
  items: readonly SourceItem[],
  countLabel: (count: number) => string,
): SourceItem[] {
  return groupByContainer(items, countLabel).slice(0, NEW_SHELF_CARDS);
}

/**
 * Draws an item with its container's poster when it has one: a Continue
 * Watching episode reads as its show, not as a frame from the episode.
 */
export function withContainerPoster(item: SourceItem): SourceItem {
  const poster = item.container?.artwork?.poster;
  if (!poster) return item;
  return { ...item, artworkSlots: { ...item.artworkSlots, poster } };
}
