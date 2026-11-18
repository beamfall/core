import type { Section, SourceItem } from "@/lib/graphql/types";

/** Whether a Section lists newly added items, which group by container (CORE-2524). */
export function groupsNewByContainer(section: Section): boolean {
  return section.kind === "shelf" && section.source?.kind === "recently-added";
}

/**
 * Folds items that share a container into one card for the container (CORE-2524):
 * three new episodes of a show read as the show with "3 new" instead of three
 * near-identical cards. The card opens the container's page and keeps the
 * position of the container's first item; an item alone in its container, or
 * held by none, stays as it is.
 */
export function groupByContainer(
  items: readonly SourceItem[],
  countLabel: (count: number) => string,
): SourceItem[] {
  const counts = new Map<string, number>();
  for (const item of items) {
    const id = item.container?.id;
    if (id) counts.set(id, (counts.get(id) ?? 0) + 1);
  }
  const placed = new Set<string>();
  const grouped: SourceItem[] = [];
  for (const item of items) {
    const container = item.container;
    const count = container ? (counts.get(container.id) ?? 0) : 0;
    if (!container || count < 2) {
      grouped.push(item);
      continue;
    }
    if (placed.has(container.id)) continue;
    placed.add(container.id);
    grouped.push({
      id: container.id,
      kind: "entity",
      title: container.title,
      summary: countLabel(count),
      artwork: container.artwork,
      mediaClass: item.mediaClass,
    });
  }
  return grouped;
}
