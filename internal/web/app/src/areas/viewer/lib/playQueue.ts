import { getIdentityEpochSignal } from "@/lib/api";
import type { SourceItem } from "@/lib/graphql/types";
import { fetchLibraryItems, type LibraryItemsRequest } from "./libraryBrowse";

// The client-owned play queue behind Play all / Shuffle (CORE-1190). The queue
// is the library query that produced it plus the items read so far; later
// pages load as playback approaches them. It holds only items the profile can
// play: one a locked certification ceiling blocks stays on the page but is
// left out here, because the server will not issue it as a next item. playback.ts asks for the item after the
// one being played and passes it to the server as nextMediaID, so the player's
// existing Up Next advance walks the queue. It is held in memory and belongs
// to the identity epoch that built it: a profile switch, relock or sign-out
// starts a new epoch, and the next identity never inherits the queue.

const QUEUE_PAGE = 200;
const MAX_STEPS_BACK = 8;

interface PlayQueue {
  request: Omit<LibraryItemsRequest, "limit" | "offset">;
  epoch: AbortSignal | undefined;
  ids: string[];
  /** The titles the listing gave, by ID: the player's heading for each item. */
  titles: Map<string, string>;
  /** Index of the item last handed to the player. */
  cursor: number;
  /** Size of the set, and how far into it the server was read, at the last page. */
  total: number;
  fetched: number;
  done: boolean;
}

let current: PlayQueue | null = null;

function playable(item: SourceItem): boolean {
  return item.kind === "media" && !item.locked;
}

function activeQueue(): PlayQueue | null {
  if (current && current.epoch !== getIdentityEpochSignal()) current = null;
  return current;
}

/** The item a new queue opens on; the title is the player's heading for it. */
export interface PlayQueueStart {
  id: string;
  title: string;
}

/**
 * Starts a queue over one library query and resolves its first Media (null
 * when the filtered set is empty). A caller that goes away while the set is
 * being read aborts leaving: nothing starts and no queue is left behind.
 */
export async function startPlayQueue(
  request: PlayQueue["request"],
  leaving?: AbortSignal,
): Promise<PlayQueueStart | null> {
  current = null;
  const epoch = getIdentityEpochSignal();
  const queue: PlayQueue = {
    request,
    epoch,
    ids: [],
    titles: new Map(),
    cursor: 0,
    total: 0,
    fetched: 0,
    done: false,
  };
  // Reads on until a page holds something playable, so a set that opens on
  // locked items still starts at its first playable one.
  await loadMore(queue);
  // Playing a last-watched set reorders it, one progress write at a time and
  // on no schedule the queue can see, so no later read could tell which items
  // it had yet to queue. That order is read whole here, before anything
  // plays, and the queue walks the set as it stood: one read per
  // QUEUE_PAGE items, paid once at Play all.
  const reorders = !request.shuffleSeed && request.state.sort === "last-watched";
  const live = () => epoch === getIdentityEpochSignal() && !leaving?.aborted;
  while (reorders && !queue.done && live()) await loadMore(queue);
  const first = queue.ids[0];
  if (first === undefined || !live()) return null;
  current = queue;
  return { id: first, title: queue.titles.get(first) ?? "" };
}

/**
 * Appends the next unseen IDs. Playing the queue can shrink the set it walks:
 * a watch filter drops each finished item, so the items not yet queued sit at
 * a lower offset than the count already read. A read that finds the set
 * smaller steps back by the difference, again for each retry that finds it
 * smaller still, then scans forward past IDs it already holds, so nothing is
 * skipped and nothing repeats.
 */
async function loadMore(queue: PlayQueue): Promise<void> {
  let offset = queue.fetched;
  // Progress writes land independently of these reads, so the set can shrink
  // again between a read and its retry: step back every time it does. The
  // bound only stops a set that keeps changing from holding the player.
  let stepsBack = MAX_STEPS_BACK;
  for (;;) {
    const page = await fetchLibraryItems({ ...queue.request, limit: QUEUE_PAGE, offset });
    const shrunk = queue.total - page.total;
    queue.total = page.total;
    if (stepsBack > 0 && shrunk > 0 && offset > 0) {
      stepsBack--;
      offset = Math.max(0, offset - shrunk);
      continue;
    }
    queue.fetched = offset + page.items.length;
    const seen = new Set(queue.ids);
    const more = page.items.filter((item) => playable(item) && !seen.has(item.id));
    for (const item of more) {
      queue.ids.push(item.id);
      queue.titles.set(item.id, item.title || "");
    }
    queue.done = !page.items.length || queue.fetched >= page.total;
    if (more.length || queue.done) return;
    offset = queue.fetched;
  }
}

/**
 * An opaque handle on the queue active right now. A playback request takes it
 * before its first await and hands it back to {@link nextInPlayQueue}.
 */
export function playQueueHandle(): unknown {
  return activeQueue();
}

/** The listed title of an item the active queue holds, or "". */
export function playQueueTitle(mediaID: string): string {
  return activeQueue()?.titles.get(mediaID) ?? "";
}

/**
 * Ends the queue the handle names, if it is still the active one: playback
 * that another queue drives has left it. A newer queue is left alone.
 */
export function leavePlayQueue(issuedFor: unknown): void {
  if (issuedFor && activeQueue() === issuedFor) current = null;
}

/**
 * The queue item that follows mediaID, or "" when the queue is finished or
 * mediaID is not its current or next item. Playback started elsewhere ends
 * the queue, so a later visit to one of its items does not resume it.
 * issuedFor is the handle taken when the playback request began: a request
 * older than the active queue says nothing about it and leaves it alone.
 */
export async function nextInPlayQueue(
  mediaID: string,
  issuedFor: unknown = activeQueue(),
): Promise<string> {
  const queue = activeQueue();
  if (!queue || issuedFor !== queue) return "";
  let index = -1;
  if (queue.ids[queue.cursor] === mediaID) index = queue.cursor;
  else if (queue.ids[queue.cursor + 1] === mediaID) index = queue.cursor + 1;
  if (index < 0) {
    // Playback started elsewhere: the viewer left the queue, so it ends here.
    current = null;
    return "";
  }
  if (index + 1 >= queue.ids.length && !queue.done) {
    try {
      await loadMore(queue);
    } catch {
      return "";
    }
    if (activeQueue() !== queue) return "";
  }
  queue.cursor = index;
  return queue.ids[index + 1] ?? "";
}
