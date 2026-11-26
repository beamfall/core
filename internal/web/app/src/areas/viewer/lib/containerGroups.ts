import { useEffect, useMemo, useState } from "react";
import { fetchMultiItemContainers } from "@/lib/graphql";
import type { Section, SourceItem } from "@/lib/graphql/types";

/** Whether a Section lists newly added items, which group by container (CORE-2524). */
/** Cards a folded new-items shelf shows (CORE-2823). */
export const NEW_SHELF_CARDS = 20;

/**
 * Items a folded discovery shelf reads (CORE-2823). Folding shrinks the window,
 * so a shelf reading only NEW_SHELF_CARDS items collapses to a handful of cards
 * when one container adds or plays a run of items; reading deeper refills it
 * after the fold and lets each container's count cover more than the first page.
 */
export const NEW_SHELF_FETCH = 100;

export function groupsNewByContainer(section: Section): boolean {
  return section.kind === "shelf" && section.source?.kind === "recently-added";
}

const SHELF_CONTAINER_SOURCES = new Set(["recently-added", "recently-played", "most-played"]);

/**
 * Whether a browse Section folds sibling items into their container card on
 * discovery shelves (`recently-added`, `recently-played`, `most-played`).
 */
export function groupsShelfByContainer(section: Section): boolean {
  return (
    section.kind === "shelf" &&
    !!section.source?.kind &&
    SHELF_CONTAINER_SOURCES.has(section.source.kind)
  );
}

/**
 * Audio container IDs that only appeared once inside `items`, so the client
 * cannot tell from the slice alone whether they belong to a multi-track
 * container (>= 2 items in catalog) or a 1-track single.
 */
export function ambiguousAudioContainers(items: readonly SourceItem[]): string[] {
  const counts = new Map<string, number>();
  for (const item of items) {
    const id = item.container?.id;
    if (id) counts.set(id, (counts.get(id) ?? 0) + 1);
  }
  const probe: string[] = [];
  const seen = new Set<string>();
  for (const item of items) {
    const id = item.container?.id;
    if (!id || seen.has(id)) continue;
    if (item.kind === "media" && item.mediaClass === "audio" && (counts.get(id) ?? 0) === 1) {
      seen.add(id);
      probe.push(id);
    }
  }
  return probe;
}

/**
 * Resolves ambiguous single-in-window audio containers via batched GraphQL
 * child probes so multi-track albums fold even when only one track appeared
 * in the fetched shelf window, while true 1-track singles remain single songs.
 */
export function useMultiItemContainers(
  items: readonly SourceItem[] | null | undefined,
  enabled: boolean,
): ReadonlySet<string> {
  const ids = useMemo(
    () => (enabled && items?.length ? ambiguousAudioContainers(items) : []),
    [items, enabled],
  );
  const key = ids.join("\u0000");
  const [resolved, setResolved] = useState<{ key: string; ids: Set<string> }>(() => ({
    key: "",
    ids: new Set(),
  }));

  useEffect(() => {
    if (!key) return;
    const controller = new AbortController();
    fetchMultiItemContainers(key.split("\u0000"), controller.signal)
      .then((found) => {
        if (!controller.signal.aborted) {
          setResolved({ key, ids: found });
        }
      })
      .catch(() => {
        // Fail-soft so offline/mocked callers keep slice-based folding.
      });
    return () => controller.abort();
  }, [key]);

  return resolved.key === key ? resolved.ids : EMPTY_CONTAINERS;
}

const EMPTY_CONTAINERS: ReadonlySet<string> = new Set();

export interface GroupByContainerOptions {
  /** Container IDs confirmed to hold >= 2 visible items in catalog. */
  multiItemContainers?: ReadonlySet<string>;
}

/**
 * Folds items that share a container into one card for the container (CORE-2524):
 * three new episodes of a show read as the show with "3 new" instead of three
 * near-identical cards. The card opens the container's page and keeps the
 * position of the container's first item. A lone new video folds too, so a
 * single new episode shows its show's poster and opens the show rather than the
 * episode; an audio item folds when the slice holds at least two siblings or
 * its container is in `multiItemContainers` (a multi-track album), while a
 * standalone 1-track single or containerless item stays as it is. The count is
 * the card's subtitle when non-empty, so it leads under the title and is part
 * of the card's accessible name (CORE-2553).
 */
export function groupByContainer(
  items: readonly SourceItem[],
  countLabel: (count: number) => string,
  options?: GroupByContainerOptions,
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
    const isMulti =
      !!container && (count >= 2 || Boolean(options?.multiItemContainers?.has(container.id)));
    if (!container || (!isMulti && item.mediaClass !== "video")) {
      grouped.push(item);
      continue;
    }
    if (placed.has(container.id)) continue;
    placed.add(container.id);
    const left = unfinished.get(container.id);
    const label = left ? countLabel(left) : "";
    grouped.push({
      id: container.id,
      kind: "entity",
      title: container.title,
      // Nothing left to watch is nothing new: a fully finished group names no count.
      subtitle: label || undefined,
      artwork: container.artwork,
      mediaClass: item.mediaClass,
    });
  }
  return grouped;
}

/** Folds a discovery shelf and keeps the cards it shows (CORE-2823). */
export function foldNewShelf(
  items: readonly SourceItem[],
  countLabel: (count: number) => string,
  options?: GroupByContainerOptions,
): SourceItem[] {
  return groupByContainer(items, countLabel, options).slice(0, NEW_SHELF_CARDS);
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
