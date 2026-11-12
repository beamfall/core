import { getIdentityEpochSignal } from "@/lib/api";
import { fetchLibraryItems, type LibraryItemsRequest } from "./libraryBrowse";

// The client-owned play queue behind Play all / Shuffle (CORE-1190). The queue
// is the library query that produced it plus the IDs read so far; later pages
// load as playback approaches them. playback.ts asks for the item after the
// one being played and passes it to the server as nextMediaID, so the player's
// existing Up Next advance walks the queue. It is held in memory and belongs
// to the identity epoch that built it: a profile switch, relock or sign-out
// starts a new epoch, and the next identity never inherits the queue.

const QUEUE_PAGE = 200;

interface PlayQueue {
  request: Omit<LibraryItemsRequest, "limit" | "offset">;
  epoch: AbortSignal | undefined;
  ids: string[];
  /** Index of the item last handed to the player. */
  cursor: number;
  /** Size of the set, and how far into it the server was read, at the last page. */
  total: number;
  fetched: number;
  done: boolean;
}

let current: PlayQueue | null = null;

function activeQueue(): PlayQueue | null {
  if (current && current.epoch !== getIdentityEpochSignal()) current = null;
  return current;
}

/**
 * Starts a queue over one library query and resolves its first Media ID
 * ("" when the filtered set is empty).
 */
export async function startPlayQueue(request: PlayQueue["request"]): Promise<string> {
  current = null;
  const epoch = getIdentityEpochSignal();
  const page = await fetchLibraryItems({ ...request, limit: QUEUE_PAGE, offset: 0 });
  const ids = page.items.filter((item) => item.kind === "media").map((item) => item.id);
  if (!ids.length || epoch !== getIdentityEpochSignal()) return "";
  current = {
    request,
    epoch,
    ids,
    cursor: 0,
    total: page.total,
    fetched: page.items.length,
    done: page.items.length >= page.total,
  };
  return ids[0];
}

/**
 * Appends the next unseen IDs. Playing the queue changes the set it walks: a
 * watch filter drops each finished item and a last-watched order moves it, so
 * the items not yet queued sit at a lower offset than the count already read.
 * The read steps back by that drift, then scans forward past IDs it already
 * holds, so nothing is skipped and nothing repeats.
 */
async function loadMore(queue: PlayQueue, played: number): Promise<void> {
  const reorders = !queue.request.shuffleSeed && queue.request.state.sort === "last-watched";
  let offset = reorders ? Math.max(0, queue.fetched - played) : queue.fetched;
  let probed = reorders;
  for (;;) {
    const page = await fetchLibraryItems({ ...queue.request, limit: QUEUE_PAGE, offset });
    const shrunk = queue.total - page.total;
    queue.total = page.total;
    if (!probed) {
      probed = true;
      if (shrunk > 0 && offset > 0) {
        offset = Math.max(0, offset - shrunk);
        continue;
      }
    }
    queue.fetched = offset + page.items.length;
    const seen = new Set(queue.ids);
    const more = page.items
      .filter((item) => item.kind === "media" && !seen.has(item.id))
      .map((item) => item.id);
    if (more.length) {
      queue.ids.push(...more);
      return;
    }
    if (!page.items.length || queue.fetched >= page.total) {
      queue.done = true;
      return;
    }
    offset = queue.fetched;
  }
}

/**
 * The queue item that follows mediaID, or "" when mediaID is not the queue's
 * current or next item (playback started elsewhere) or the queue is finished.
 */
export async function nextInPlayQueue(mediaID: string): Promise<string> {
  const queue = activeQueue();
  if (!queue) return "";
  let index = -1;
  if (queue.ids[queue.cursor] === mediaID) index = queue.cursor;
  else if (queue.ids[queue.cursor + 1] === mediaID) index = queue.cursor + 1;
  if (index < 0) return "";
  if (index + 1 >= queue.ids.length && !queue.done) {
    try {
      await loadMore(queue, index + 1);
    } catch {
      return "";
    }
    if (activeQueue() !== queue) return "";
  }
  queue.cursor = index;
  return queue.ids[index + 1] ?? "";
}
