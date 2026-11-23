import type { SourceItem } from "@/lib/graphql/types";
import { viewerIdentityRevealed } from "@/lib/viewerCache";

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
  /**
   * The item's plugin declares it plays on its own (CORE-3147, ADR-0055 §2.4):
   * no sibling plays after it, and it never runs on from another item.
   */
  playsAlone?: boolean;
}

/**
 * The audio and caption tracks picked on a detail page before playback
 * (CORE-2442), for the Media they were picked for. Unset fields leave the
 * server's choice; captionID null starts with subtitles off.
 */
export interface StartTracks {
  mediaID: string;
  /**
   * The Version picked with them: the tracks name streams of that file, so it
   * travels with them rather than only through browser storage. "" is Auto
   * (the server's choice); undefined leaves the remembered Version.
   */
  versionID?: string;
  audioIndex?: number;
  captionID?: string | null;
}

/** The nav state the watch route reads. */
export interface PlayerNavState {
  title?: string;
  queue?: PlayQueueItem[];
  /** The viewer identity the queue was listed for; a queue without it is ignored. */
  queueIdentity?: string;
  tracks?: StartTracks;
  /**
   * The identity of one explicit playback start (Play or Start over on a
   * detail page): a new one starts the Media afresh with the picks it carries,
   * even when the player already plays that Media from the same position.
   * Opening the full player from the mini player carries it unchanged.
   */
  startID?: string;
}

/**
 * The title a watch navigation may write into history state. A Revealed session
 * may be playing restricted Media, and a history entry outlives the session: it
 * carries no title then, and the player names the item from its media read.
 * `identity` is the viewer identity the navigation is made under.
 */
export function navTitleFor(identity: string | null, title: string): string | undefined {
  return viewerIdentityRevealed(identity) ? undefined : title;
}

/** A new playback-start identity for {@link PlayerNavState.startID}. */
export function newStartID(): string {
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}

// Nav state is serialised into history; a huge section must not bloat it, so
// the queue handed to the player is a window of at most this many entries
// centred on the item being played.
export const MAX_QUEUE = 500;

/** Source kinds that list one item per container: their items are not siblings. */
const PER_CONTAINER_SOURCES = new Set(["continue-watching", "next-up"]);

/**
 * The queue a list's cards hand the player: its playable items, or undefined
 * for a Source that lists each container's own next or resume point, whose
 * items belong to unrelated containers, so playing one never runs on into
 * another container's item (ADR-0055 §2.4). Each card then queues only
 * itself, which also ends an earlier Play all queue.
 */
export function sectionQueue(
  sourceKind: string | undefined,
  items: readonly (SourceItem & { mediaClass?: string })[],
): PlayQueueItem[] | undefined {
  return PER_CONTAINER_SOURCES.has(sourceKind ?? "") ? undefined : playQueueOf(items);
}

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
    const entry: PlayQueueItem = { id: item.id, title: item.title };
    if (item.mediaClass) entry.mediaClass = item.mediaClass;
    if (item.playsAlone) entry.playsAlone = true;
    queue.push(entry);
  }
  return queue;
}

/**
 * True when a listed Media item plays on its own (CORE-3147): the listing is
 * then no track list, and each of its cards queues only itself.
 */
export function listsPlaysAlone(items: readonly SourceItem[]): boolean {
  return items.some((item) => item.kind === "media" && !!item.playsAlone);
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
 * Keeps items that play on their own out of a shared queue (CORE-3147): when
 * the playing item plays alone the queue is that item only, so Next and
 * auto-advance never run on into a sibling; otherwise the plays-alone entries
 * are dropped, so nothing runs on into one. The queue is returned unchanged
 * when no entry plays alone.
 */
export function queueForPlaying(
  queue: readonly PlayQueueItem[],
  playingId: string,
): readonly PlayQueueItem[] {
  if (!queue.some((item) => item.playsAlone)) return queue;
  const playing = queue.find((item) => item.id === playingId);
  if (playing?.playsAlone) return [playing];
  return queue.filter((item) => !item.playsAlone);
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
  const raw = state as {
    title?: unknown;
    queue?: unknown;
    queueIdentity?: unknown;
    tracks?: unknown;
    startID?: unknown;
  };
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
          .map(({ id, title, mediaClass, playsAlone }) => {
            const entry: PlayQueueItem = { id, title };
            if (typeof mediaClass === "string" && mediaClass) entry.mediaClass = mediaClass;
            if (playsAlone === true) entry.playsAlone = true;
            return entry;
          })
      : undefined;
  const tracks = readStartTracks(raw.tracks);
  return {
    title,
    queue,
    ...(queue ? { queueIdentity: identity as string } : {}),
    ...(tracks ? { tracks } : {}),
    ...(typeof raw.startID === "string" && raw.startID ? { startID: raw.startID } : {}),
  };
}

function readStartTracks(raw: unknown): StartTracks | undefined {
  if (!raw || typeof raw !== "object") return undefined;
  const { mediaID, versionID, audioIndex, captionID } = raw as Record<string, unknown>;
  if (typeof mediaID !== "string" || !mediaID) return undefined;
  const tracks: StartTracks = { mediaID };
  if (typeof versionID === "string") tracks.versionID = versionID;
  if (typeof audioIndex === "number" && Number.isInteger(audioIndex) && audioIndex >= 0) {
    tracks.audioIndex = audioIndex;
  }
  if (captionID === null || (typeof captionID === "string" && captionID)) {
    tracks.captionID = captionID;
  }
  return tracks.versionID === undefined &&
    tracks.audioIndex === undefined &&
    tracks.captionID === undefined
    ? undefined
    : tracks;
}
