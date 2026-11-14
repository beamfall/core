import type { SourceItem } from "@/lib/graphql/types";

// The play queue a catalog surface hands the player (CORE-1143). A card that
// starts playback carries the playable siblings it was shown with — an album's
// tracks, a section's items — in the watch route's nav state, so the player can
// offer previous/next and declare the next item to playback-info (which then
// pre-mints its stream for a gapless handoff). The queue is a convenience the
// client owns: a deep link to /watch/:id simply has none. History keeps the
// queue's titles, so it is bound to the viewer identity (profile and Reveal
// state) that listed them and dropped once that identity changes, as entity
// previews are.

/** One playable entry in the queue. */
export interface PlayQueueItem {
  id: string;
  title: string;
  /**
   * The Media's class ("audio", "video", ...) when known: from the row that
   * listed it (search results report it) or learned from playback-info.
   */
  mediaClass?: string;
}

/** The nav state the watch route reads. */
export interface PlayerNavState {
  title?: string;
  queue?: PlayQueueItem[];
  /** The viewer identity the queue was listed for; a queue without it is ignored. */
  queueIdentity?: string;
}

// Nav state is serialised into history; a huge section must not bloat it, so
// the queue handed to the player is a window of at most this many entries
// centred on the item being played.
export const MAX_QUEUE = 500;

/**
 * The playable items of a list, in order, as a queue: kind "media" and not
 * locked. A locked item (a certification ceiling blocks it) stays on the page
 * but Next and auto-advance step over it, as the Play all queue does.
 */
export function playQueueOf(
  items: readonly (SourceItem & { mediaClass?: string })[],
): PlayQueueItem[] {
  const queue: PlayQueueItem[] = [];
  for (const item of items) {
    if (item.kind !== "media" || item.locked) continue;
    queue.push(
      item.mediaClass
        ? { id: item.id, title: item.title, mediaClass: item.mediaClass }
        : { id: item.id, title: item.title },
    );
  }
  return queue;
}

/**
 * Records learned classes on the queue entries they name (CORE-1490). A class
 * the queue already knows is kept; the queue is returned unchanged when
 * nothing new was learned, so memoised consumers keep their identity.
 */
export function withQueueClasses(
  queue: readonly PlayQueueItem[],
  classes: Readonly<Record<string, string>>,
): readonly PlayQueueItem[] {
  let changed = false;
  const next = queue.map((item) => {
    const learned = classes[item.id];
    if (!learned || item.mediaClass) return item;
    changed = true;
    return { ...item, mediaClass: learned };
  });
  return changed ? next : queue;
}

/**
 * Narrows a queue to the class of the item being played (CORE-1490): a track
 * started from a shelf that mixes tracks and videos queues only the tracks.
 * An entry whose class is not known stays (the server has not said), and the
 * playing item always stays. With no known class for the playing item the
 * queue is returned unchanged.
 */
export function queueOfClass(
  queue: readonly PlayQueueItem[],
  mediaClass: string | undefined,
  playingId: string,
): readonly PlayQueueItem[] {
  if (!mediaClass) return queue;
  const narrowed = queue.filter(
    (item) => item.id === playingId || !item.mediaClass || item.mediaClass === mediaClass,
  );
  return narrowed.length === queue.length ? queue : narrowed;
}

/**
 * At most `max` queue entries around `id`, so an item deep in a long list
 * still gets its real neighbours. A queue without `id` is returned from its
 * start.
 */
export function queueWindow(
  queue: readonly PlayQueueItem[],
  id: string,
  max = MAX_QUEUE,
): PlayQueueItem[] {
  if (queue.length <= max) return [...queue];
  const index = Math.max(
    0,
    queue.findIndex((item) => item.id === id),
  );
  const start = Math.min(Math.max(0, index - Math.floor(max / 2)), queue.length - max);
  return queue.slice(start, start + max);
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

/**
 * Reads the watch route's nav state defensively (history state is untyped).
 * The queue is kept only when it was listed for the current viewer identity.
 */
export function readPlayerNavState(state: unknown, identity: string | null): PlayerNavState {
  if (!state || typeof state !== "object") return {};
  const raw = state as { title?: unknown; queue?: unknown; queueIdentity?: unknown };
  const title = typeof raw.title === "string" ? raw.title : undefined;
  const sameViewer = !!identity && raw.queueIdentity === identity;
  const queue =
    sameViewer && Array.isArray(raw.queue)
      ? raw.queue
          .filter(
            (item): item is PlayQueueItem =>
              !!item &&
              typeof item === "object" &&
              typeof (item as PlayQueueItem).id === "string" &&
              typeof (item as PlayQueueItem).title === "string",
          )
          .map(({ id, title, mediaClass }) =>
            typeof mediaClass === "string" && mediaClass
              ? { id, title, mediaClass }
              : { id, title },
          )
      : undefined;
  return { title, queue, ...(queue ? { queueIdentity: identity as string } : {}) };
}
