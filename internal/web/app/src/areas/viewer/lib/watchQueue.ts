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
  libraryID?: string;
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
  /** Identifies an advance within the hosted music player. */
  musicRun?: string;
  /** The server queue supplies the continuation of this playback start. */
  musicAdopted?: boolean;
  /** Playback-info may pre-mint this candidate for a gapless handoff. */
  musicNextID?: string;
  musicCursor?: MusicQueueCursor;
}

export type RepeatMode = "off" | "all" | "one";

/** A music queue's playback position and local modes. Titles stay out of history. */
export interface MusicQueueCursor {
  currentID: string;
  playedIDs: string[];
  requeuedIDs: string[];
  repeat?: RepeatMode;
  shuffledIDs?: string[];
  catalogAnchor?: string;
  catalogCycleIDs?: string[];
}

/** A new playback-start identity for {@link PlayerNavState.startID}. */
export function newStartID(): string {
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
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
export function readPlayerNavState(
  state: unknown,
  identity: string | null,
  profileID?: string,
): PlayerNavState {
  if (!state || typeof state !== "object") return {};
  const raw = state as {
    title?: unknown;
    queue?: unknown;
    queueIdentity?: unknown;
    tracks?: unknown;
    startID?: unknown;
    musicRun?: unknown;
    musicAdopted?: unknown;
    musicNextID?: unknown;
    musicIdentity?: unknown;
    musicProfileID?: unknown;
    musicCursor?: unknown;
  };
  const title =
    typeof raw.title === "string" && !raw.musicRun && raw.musicAdopted !== true
      ? raw.title
      : undefined;
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
  const tracks = readStartTracks(raw.tracks);
  const sameMusicViewer = !!identity && raw.musicIdentity === identity;
  // The cursor contains only IDs, so the same profile can keep its position
  // across relock. Titles and pre-minted next candidates stay identity-scoped.
  const sameMusicProfile = !!identity && !!profileID && raw.musicProfileID === profileID;
  const legacyMusicViewer = sameMusicViewer && raw.musicProfileID === undefined;
  const musicAdopted = raw.musicAdopted === true && (sameMusicProfile || legacyMusicViewer);
  const musicCursor =
    sameMusicProfile || (musicAdopted && legacyMusicViewer)
      ? readMusicCursor(raw.musicCursor)
      : undefined;
  return {
    title,
    queue,
    ...(sameMusicViewer && typeof raw.musicRun === "string" ? { musicRun: raw.musicRun } : {}),
    ...(musicAdopted ? { musicAdopted } : {}),
    ...(musicCursor ? { musicCursor } : {}),
    ...(sameMusicViewer && typeof raw.musicNextID === "string"
      ? { musicNextID: raw.musicNextID }
      : {}),
    ...(queue ? { queueIdentity: identity as string } : {}),
    ...(tracks ? { tracks } : {}),
    ...(typeof raw.startID === "string" && raw.startID ? { startID: raw.startID } : {}),
  };
}

function readMusicCursor(raw: unknown): MusicQueueCursor | undefined {
  if (!raw || typeof raw !== "object") return undefined;
  const { currentID, playedIDs, requeuedIDs, repeat, shuffledIDs, catalogAnchor, catalogCycleIDs } =
    raw as Partial<MusicQueueCursor>;
  if (typeof currentID !== "string" || !Array.isArray(playedIDs) || !Array.isArray(requeuedIDs))
    return undefined;
  return {
    currentID,
    playedIDs: playedIDs.filter((id) => typeof id === "string"),
    requeuedIDs: requeuedIDs.filter((id) => typeof id === "string"),
    ...(repeat === "off" || repeat === "all" || repeat === "one" ? { repeat } : {}),
    ...(Array.isArray(shuffledIDs)
      ? { shuffledIDs: shuffledIDs.filter((id) => typeof id === "string") }
      : {}),
    ...(typeof catalogAnchor === "string" ? { catalogAnchor } : {}),
    ...(Array.isArray(catalogCycleIDs)
      ? { catalogCycleIDs: catalogCycleIDs.filter((id) => typeof id === "string") }
      : {}),
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
