import { fetchLibraryItems, type LibraryItemsRequest } from "./libraryBrowse";

// The client-owned play queue behind Play all / Shuffle (CORE-1190). The queue
// is the library query that produced it plus the IDs read so far; later pages
// load as playback approaches them. playback.ts asks for the item after the
// one being played and passes it to the server as nextMediaID, so the player's
// existing Up Next advance walks the queue. It lives in sessionStorage so a
// reload mid-queue keeps going and a new tab starts with none.

const QUEUE_KEY = "beamfall.playQueue";
const QUEUE_PAGE = 200;

interface PlayQueue {
  request: Omit<LibraryItemsRequest, "limit" | "offset">;
  total: number;
  ids: string[];
  /** Index of the item last handed to the player. */
  cursor: number;
}

function readQueue(): PlayQueue | null {
  try {
    const raw = window.sessionStorage.getItem(QUEUE_KEY);
    return raw ? (JSON.parse(raw) as PlayQueue) : null;
  } catch {
    return null;
  }
}

function writeQueue(queue: PlayQueue | null): void {
  try {
    if (queue) window.sessionStorage.setItem(QUEUE_KEY, JSON.stringify(queue));
    else window.sessionStorage.removeItem(QUEUE_KEY);
  } catch {
    // Without storage the first item still plays; it just has no successor.
  }
}

/**
 * Starts a queue over one library query and resolves its first Media ID
 * ("" when the filtered set is empty).
 */
export async function startPlayQueue(request: PlayQueue["request"]): Promise<string> {
  const page = await fetchLibraryItems({ ...request, limit: QUEUE_PAGE, offset: 0 });
  const ids = page.items.filter((item) => item.kind === "media").map((item) => item.id);
  if (!ids.length) {
    writeQueue(null);
    return "";
  }
  writeQueue({ request, total: page.total, ids, cursor: 0 });
  return ids[0];
}

/**
 * The queue item that follows mediaID, or "" when mediaID is not the queue's
 * current or next item (playback started elsewhere) or the queue is finished.
 */
export async function nextInPlayQueue(mediaID: string): Promise<string> {
  const queue = readQueue();
  if (!queue) return "";
  let index = -1;
  if (queue.ids[queue.cursor] === mediaID) index = queue.cursor;
  else if (queue.ids[queue.cursor + 1] === mediaID) index = queue.cursor + 1;
  if (index < 0) return "";
  if (index + 1 >= queue.ids.length && queue.ids.length < queue.total) {
    try {
      const page = await fetchLibraryItems({
        ...queue.request,
        limit: QUEUE_PAGE,
        offset: queue.ids.length,
      });
      const more = page.items.filter((item) => item.kind === "media").map((item) => item.id);
      queue.ids.push(...more);
      // A short page means the set shrank; stop asking for more.
      if (!more.length) queue.total = queue.ids.length;
    } catch {
      return "";
    }
  }
  queue.cursor = index;
  writeQueue(queue);
  return queue.ids[index + 1] ?? "";
}
