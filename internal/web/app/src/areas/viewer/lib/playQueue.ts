import type { SourceItem } from "@/lib/graphql/types";

// The play queue a catalog surface hands the player (CORE-1143). A card that
// starts playback carries the playable siblings it was shown with — an album's
// tracks, a section's items — in the watch route's nav state, so the player can
// offer previous/next and declare the next item to playback-info (which then
// pre-mints its stream for a gapless handoff). The queue is a convenience the
// client owns: a deep link to /watch/:id simply has none.

/** One playable entry in the queue. */
export interface PlayQueueItem {
  id: string;
  title: string;
}

/** The nav state the watch route reads. */
export interface PlayerNavState {
  title?: string;
  queue?: PlayQueueItem[];
}

// Nav state is serialised into history; a huge section must not bloat it.
const MAX_QUEUE = 500;

/** The playable (kind "media") items of a list, in order, as a queue. */
export function playQueueOf(items: readonly SourceItem[]): PlayQueueItem[] {
  const queue: PlayQueueItem[] = [];
  for (const item of items) {
    if (item.kind !== "media") continue;
    queue.push({ id: item.id, title: item.title });
    if (queue.length >= MAX_QUEUE) break;
  }
  return queue;
}

/** The queue entries either side of `id`, when it is in the queue. */
export function queueNeighbours(
  queue: readonly PlayQueueItem[] | undefined,
  id: string,
): { previous?: PlayQueueItem; next?: PlayQueueItem } {
  if (!queue) return {};
  const index = queue.findIndex((item) => item.id === id);
  if (index < 0) return {};
  return { previous: queue[index - 1], next: queue[index + 1] };
}

/** Reads the watch route's nav state defensively (history state is untyped). */
export function readPlayerNavState(state: unknown): PlayerNavState {
  if (!state || typeof state !== "object") return {};
  const raw = state as { title?: unknown; queue?: unknown };
  const title = typeof raw.title === "string" ? raw.title : undefined;
  const queue = Array.isArray(raw.queue)
    ? raw.queue.filter(
        (item): item is PlayQueueItem =>
          !!item &&
          typeof item === "object" &&
          typeof (item as PlayQueueItem).id === "string" &&
          typeof (item as PlayQueueItem).title === "string",
      )
    : undefined;
  return { title, queue };
}
