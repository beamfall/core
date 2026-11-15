import { useEffect, useRef, useState } from "react";
import { fetchEntity, fetchMediaForEntity } from "@/lib/graphql";
import type { DisplayString, GenericMediaInfo, SourceItem } from "@/lib/graphql/types";

// Where one Media item sits among its containers (CORE-1785, CORE-1786).
//
// The vocabulary is the archetype's, never a domain's: a credit whose kind has
// the "container" archetype is a parent, its mediaForEntity list is the
// ordered set of siblings (sort_order first), and a related Entity that is an
// ordered container holding at least as many items as the parent is the
// parent's own parent (a grouping above it). Nothing here names a plugin kind.

const CONTAINER_ARCHETYPE = "container";

/** One link up the hierarchy, outermost first when there are several. */
export interface ContainerLink {
  id: string;
  title: string;
  kindLabel?: DisplayString;
}

export interface ContainerContext {
  /** Links up the hierarchy, outermost first. Empty when the item has no container. */
  parents: ContainerLink[];
  /** The next item in the nearest container's order, when there is one. */
  next: SourceItem | null;
  /** This item as the nearest container lists it (carries engagement such as duration). */
  current: SourceItem | null;
}

const EMPTY: ContainerContext = { parents: [], next: null, current: null };

/** The item's container credits, de-duplicated, in the server's order. */
export function containerCredits(media: GenericMediaInfo | null | undefined): ContainerLink[] {
  const seen = new Set<string>();
  const links: ContainerLink[] = [];
  for (const credit of media?.credits ?? []) {
    if (credit.archetype !== CONTAINER_ARCHETYPE || !credit.entityID || !credit.title) continue;
    if (seen.has(credit.entityID)) continue;
    seen.add(credit.entityID);
    links.push({
      id: credit.entityID,
      title: credit.title,
      kindLabel: credit.kindDisplayName ?? undefined,
    });
  }
  return links;
}

/** The item after `id` in an ordered list, or null when `id` is last or absent. */
export function nextAfter(items: readonly SourceItem[], id: string): SourceItem | null {
  const at = items.findIndex((item) => item.id === id);
  return at >= 0 && at + 1 < items.length ? items[at + 1] : null;
}

/**
 * Of several containers' ordered lists, the smallest that holds the item: the
 * nearest container (a grouping inside a larger set lists fewer items).
 */
export function nearestContainer(
  lists: ReadonlyArray<{ link: ContainerLink; items: SourceItem[] }>,
  id: string,
): { link: ContainerLink; items: SourceItem[] } | null {
  let best: { link: ContainerLink; items: SourceItem[] } | null = null;
  for (const entry of lists) {
    if (!entry.items.some((item) => item.id === id)) continue;
    if (!best || entry.items.length < best.items.length) best = entry;
  }
  return best;
}

/**
 * The related Entities that are ordered containers above `container`: of a
 * different kind, holding at least as many items as the container does.
 */
export function ancestorItems(
  related: ReadonlyArray<{ items: SourceItem[] }> | undefined,
  container: { id: string; kind: string },
  childCount: number,
): SourceItem[] {
  const out: SourceItem[] = [];
  for (const group of related ?? []) {
    for (const item of group.items) {
      if (item.id === container.id || item.kind === container.kind) continue;
      const total = item.engagement?.totalCount ?? 0;
      if (total > 0 && total >= childCount && !out.some((seen) => seen.id === item.id)) {
        out.push(item);
      }
    }
  }
  return out;
}

/**
 * Loads the item's container context: its parents (credited containers plus
 * the grouping above the nearest one) and the next item. The credited parents
 * show at once; the rest arrives after two reads and fails silently, since the
 * page is complete without it.
 */
export function useContainerContext(
  media: GenericMediaInfo | null | undefined,
  mediaID: string,
): ContainerContext {
  const [context, setContext] = useState<ContainerContext>(EMPTY);
  // Reload only when the credited containers change, not on every media object.
  const mediaRef = useRef(media);
  mediaRef.current = media;
  const creditKey = containerCredits(media)
    .map((link) => link.id)
    .join(" ");

  useEffect(() => {
    void creditKey;
    const links = containerCredits(mediaRef.current);
    setContext({ ...EMPTY, parents: links });
    if (links.length === 0) return;
    const controller = new AbortController();
    void (async () => {
      try {
        const lists = await Promise.all(
          links.map(async (link) => ({
            link,
            items: await fetchMediaForEntity(link.id, controller.signal),
          })),
        );
        const nearest = nearestContainer(lists, mediaID);
        if (!nearest || controller.signal.aborted) return;
        const entity = await fetchEntity(nearest.link.id, controller.signal);
        if (controller.signal.aborted) return;
        const above = entity
          ? ancestorItems(entity.relatedEntities, entity, nearest.items.length)
          : [];
        const others = links.filter((link) => link.id !== nearest.link.id);
        const parents: ContainerLink[] = [
          ...above
            .filter((item) => !links.some((link) => link.id === item.id))
            .map((item) => ({ id: item.id, title: item.title })),
          ...others,
          nearest.link,
        ];
        setContext({
          parents,
          next: nextAfter(nearest.items, mediaID),
          current: nearest.items.find((item) => item.id === mediaID) ?? null,
        });
      } catch {
        // Context is an enhancement: the credited parents already show.
      }
    })();
    return () => controller.abort();
  }, [creditKey, mediaID]);

  return context;
}
