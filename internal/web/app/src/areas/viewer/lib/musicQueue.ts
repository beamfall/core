import { useCallback, useLayoutEffect, useMemo, useRef, useState } from "react";
import { getIdentityEpochSignal, graphql } from "@/lib/api";
import type { GenericMediaInfo, SessionInfo } from "@/lib/graphql/types";
import { useSession, useViewerIdentity } from "@/lib/session";
import { isUnknownMedia } from "./mediaDetail";

export const MUSIC_QUEUE_PARAM = "playQueue";

export interface MusicQueueRow {
  id: string;
  title: string;
  mediaClass: string;
  locked?: boolean;
  libraryID?: string;
}

const PAGE_SIZE = 200;
const SESSION_FIELDS = "currentSession { profileID revealed deviceRole profile }";
const QUEUE_QUERY = `query MusicQueue($query: String, $limit: Int, $offset: Int) {
  ${SESSION_FIELDS}
  sourceItems(kind: "queue", query: $query, limit: $limit, offset: $offset) {
    id title mediaClass locked libraryID
  }
  playQueue {
    currentEntryID
    entries { id mediaID }
  }
}`;
const MEDIA_QUERY = `query MusicQueueMedia($id: String!) {
  ${SESSION_FIELDS}
  media(id: $id) {
    id title mediaClass libraryID artworkSlots
    credits { entityID title kind kindDisplayName archetype relation attributes artworkSlots }
    metadataAttribution { providerName text url }
  }
}`;
const QUEUE_MOVE_MUTATION = `mutation MusicQueueMove($mediaID: String!, $position: Int!, $expectedProfileID: String) {
  queueMove(mediaID: $mediaID, position: $position, expectedProfileID: $expectedProfileID) { id }
}`;
const QUEUE_REMOVE_MUTATION = `mutation MusicQueueRemove($mediaID: String!, $expectedProfileID: String) {
  queueRemove(mediaID: $mediaID, expectedProfileID: $expectedProfileID) { id }
}`;

type PlayQueueEntryRef = { id: string; mediaID: string };
type Page = {
  sourceItems: MusicQueueRow[];
  playQueue?: {
    currentEntryID?: string | null;
    entries?: PlayQueueEntryRef[];
  } | null;
};
type SessionResult = { currentSession: SessionInfo | null };

const queueEntryMediaIDs = new WeakMap<readonly MusicQueueRow[], readonly string[]>();

async function readMusicQueue(
  readPage: (offset: number) => Promise<Page>,
): Promise<MusicQueueRow[]> {
  // The first row of each later page must still be the preceding page's last
  // row. If rows shifted across that boundary, start over from the server.
  attempts: for (let attempt = 0; attempt < 3; attempt++) {
    const rows: MusicQueueRow[] = [];
    const priorPageIDs = new Set<string>();
    let entryRefs: PlayQueueEntryRef[] | undefined;
    for (let offset = 0; ; offset += PAGE_SIZE - 1) {
      // An explicit read includes muted rows; playback skips locked and non-audio rows.
      const page = await readPage(offset);
      const { sourceItems } = page;
      if (offset === 0 && Array.isArray(page.playQueue?.entries)) {
        entryRefs = page.playQueue.entries;
      }
      if (offset > 0) {
        const overlap = sourceItems[0];
        if (!overlap || overlap.id !== rows.at(-1)?.id) continue attempts;
        rows[rows.length - 1] = overlap;
      }
      const additions = offset === 0 ? sourceItems : sourceItems.slice(1);
      for (const row of additions) {
        if (offset > 0 && priorPageIDs.has(row.id)) continue attempts;
        rows.push(row);
      }
      for (const row of additions) {
        priorPageIDs.add(row.id);
      }
      if (sourceItems.length < PAGE_SIZE) {
        if (entryRefs) {
          queueEntryMediaIDs.set(
            rows,
            entryRefs.map((entry) => entry.mediaID),
          );
        }
        return rows;
      }
    }
  }
  throw new Error("Queue changed during pagination");
}

export function playableMusic(row: MusicQueueRow): boolean {
  return row.mediaClass === "audio" && !row.locked;
}

export function musicQueueNeighbours(rows: MusicQueueRow[] | undefined, cursor: string) {
  if (!rows) return {};
  const index = rows.findIndex((row) => row.id === cursor);
  if (index < 0) return {};
  return {
    previous: rows.slice(0, index).findLast(playableMusic),
    next: rows.slice(index + 1).find(playableMusic),
  };
}

/**
 * Playable audio rows after `currentID` (or all playable rows when `currentID`
 * is not in `rows`), in server order (CORE-2846). Includes duplicate entries
 * from `playQueue.entries` when present so Clear upcoming removes every queued
 * occurrence of a repeated track.
 */
export function upcomingPlayableIDs(
  rows: readonly MusicQueueRow[] | undefined,
  currentID: string,
): string[] {
  if (!rows) return [];
  const entryIDs = queueEntryMediaIDs.get(rows);
  if (entryIDs && entryIDs.length > 0) {
    const playableIDs = new Set(rows.filter(playableMusic).map((row) => row.id));
    const index = currentID ? entryIDs.indexOf(currentID) : -1;
    const slice = index >= 0 ? entryIDs.slice(index + 1) : entryIDs;
    return slice.filter((id) => playableIDs.has(id));
  }
  const index = currentID ? rows.findIndex((row) => row.id === currentID) : -1;
  const slice = index >= 0 ? rows.slice(index + 1) : rows;
  return slice.filter(playableMusic).map((row) => row.id);
}

/**
 * Current plus upcoming playable audio rows from a complete queue snapshot,
 * frozen before creating a playlist (CORE-2847). Uses the un-deduplicated
 * `playQueue.entries` list when present so duplicate queued Media IDs are
 * preserved rather than collapsed by the legacy `sourceItems(kind: "queue")`
 * projection.
 */
export function currentAndUpcomingPlayable(
  rows: readonly MusicQueueRow[] | undefined,
  currentID: string,
): MusicQueueRow[] {
  if (!rows) return [];
  const entryIDs = queueEntryMediaIDs.get(rows);
  if (entryIDs && entryIDs.length > 0) {
    const playableByID = new Map(rows.filter(playableMusic).map((row) => [row.id, row]));
    const startIndex = currentID ? entryIDs.indexOf(currentID) : -1;
    const sliceIDs = startIndex >= 0 ? entryIDs.slice(startIndex) : entryIDs;
    return sliceIDs
      .map((id) => playableByID.get(id))
      .filter((row): row is MusicQueueRow => row !== undefined);
  }
  const index = currentID ? rows.findIndex((row) => row.id === currentID) : -1;
  const slice = index >= 0 ? rows.slice(index) : rows;
  return slice.filter(playableMusic);
}

/**
 * True when `rows[index]` is an upcoming playable audio track after `currentID`
 * (or any playable audio track when `currentID` is not in `rows`) (CORE-2846).
 */
export function isUpcomingPlayableIndex(
  rows: readonly MusicQueueRow[] | undefined,
  index: number,
  currentID: string,
): boolean {
  if (!rows || index < 0 || index >= rows.length) return false;
  const row = rows[index]!;
  if (!playableMusic(row) || row.id === currentID) return false;
  const currentIndex = currentID ? rows.findIndex((entry) => entry.id === currentID) : -1;
  return currentIndex < 0 || index > currentIndex;
}

/**
 * Adjacent movable positions among all visible server rows for the upcoming
 * row at `index` (CORE-2846). The current, played-history, video and locked
 * rows are not movable, and upcoming rows after `currentID` stay after
 * `currentID`.
 */
export function movableNeighbours(
  rows: readonly MusicQueueRow[] | undefined,
  index: number,
  currentID: string,
): { upPosition?: number; downPosition?: number } {
  if (!rows || !isUpcomingPlayableIndex(rows, index, currentID)) return {};
  const currentIndex = currentID ? rows.findIndex((entry) => entry.id === currentID) : -1;
  const minIndex = currentIndex >= 0 ? currentIndex + 1 : 0;
  const maxIndex = rows.length - 1;
  let upPosition: number | undefined;
  for (let i = index - 1; i >= minIndex; i--) {
    const candidate = rows[i]!;
    if (playableMusic(candidate) && candidate.id !== currentID) {
      upPosition = i;
      break;
    }
  }
  let downPosition: number | undefined;
  for (let i = index + 1; i <= maxIndex; i++) {
    const candidate = rows[i]!;
    if (playableMusic(candidate) && candidate.id !== currentID) {
      downPosition = i;
      break;
    }
  }
  return {
    ...(upPosition !== undefined ? { upPosition } : {}),
    ...(downPosition !== undefined ? { downPosition } : {}),
  };
}

export async function moveMusicQueueTrack(
  mediaID: string,
  position: number,
  expectedProfileID?: string,
): Promise<void> {
  await graphql(
    QUEUE_MOVE_MUTATION,
    expectedProfileID ? { mediaID, position, expectedProfileID } : { mediaID, position },
    undefined,
    { adminUnlock: false },
  );
}

export async function removeMusicQueueTrack(
  mediaID: string,
  expectedProfileID?: string,
): Promise<void> {
  await graphql(
    QUEUE_REMOVE_MUTATION,
    expectedProfileID ? { mediaID, expectedProfileID } : { mediaID },
    undefined,
    { adminUnlock: false },
  );
}

type QueueIdentity = Pick<SessionInfo, "profileID" | "revealed"> &
  Partial<Pick<SessionInfo, "deviceRole" | "profile">>;
class QueueIdentityChanged extends Error {}

async function readScoped<T>(
  query: string,
  variables: Record<string, unknown>,
  expected: QueueIdentity,
  signal: AbortSignal,
): Promise<T> {
  signal.throwIfAborted();
  const data = await graphql<T & SessionResult>(query, variables, signal, { adminUnlock: false });
  signal.throwIfAborted();
  const live = data.currentSession;
  if (
    !live ||
    live.profileID !== expected.profileID ||
    live.revealed !== expected.revealed ||
    live.deviceRole !== expected.deviceRole ||
    live.profile !== expected.profile
  ) {
    throw new QueueIdentityChanged();
  }
  return data;
}

export interface MusicQueueView {
  rows?: MusicQueueRow[];
  failed?: boolean;
  invalid?: boolean;
  pending?: boolean;
  refresh?: () => Promise<MusicQueueRow[] | null | undefined>;
}

type RefreshResult = MusicQueueRow[] | null | undefined;

/** The hosted player keeps this reader alive while its transport is docked. */
export function useMusicQueue() {
  const { session, revealed } = useSession();
  const identity = useViewerIdentity();
  const { profileID, deviceRole, profile } = session ?? {};
  const epoch = getIdentityEpochSignal();
  const scope = useMemo(
    () => (profileID ? { epoch, identity, profileID, revealed, deviceRole, profile } : null),
    [epoch, identity, profileID, revealed, deviceRole, profile],
  );
  const active = useRef<{
    scope: typeof scope;
    signal: AbortSignal;
    invalid?: boolean;
    pending?: { controller: AbortController; promise: Promise<RefreshResult> };
    // The last rows read, before React renders them.
    rows?: MusicQueueRow[];
  }>(null);
  const [result, setResult] = useState<MusicQueueView & { scope: typeof scope }>();

  useLayoutEffect(() => {
    setResult(undefined);
    if (!scope) return;
    const controller = new AbortController();
    const signal = epoch ? AbortSignal.any([epoch, controller.signal]) : controller.signal;
    active.current = { scope, signal };
    return () => controller.abort();
  }, [scope, epoch]);

  const current = useCallback(() => {
    const read = active.current;
    return scope && read?.scope === scope && !read.signal.aborted && !read.invalid ? read : null;
  }, [scope]);

  const invalidate = useCallback(
    (read: NonNullable<typeof active.current>) => {
      read.invalid = true;
      read.pending?.controller.abort();
      setResult({ scope, invalid: true });
    },
    [scope],
  );

  const refresh = useCallback(
    (fresh = false): Promise<RefreshResult> => {
      const read = current();
      if (!scope || !read) return Promise.resolve(undefined);
      if (!fresh && read.pending) return read.pending.promise;
      read.pending?.controller.abort();
      const controller = new AbortController();
      const signal = AbortSignal.any([read.signal, controller.signal]);
      setResult((previous) =>
        previous?.scope === scope ? { ...previous, pending: true } : { scope, pending: true },
      );
      const promise = readMusicQueue((offset) =>
        readScoped<Page>(QUEUE_QUERY, { query: "queue", limit: PAGE_SIZE, offset }, scope, signal),
      )
        .catch((error: unknown) => {
          if (error instanceof QueueIdentityChanged && !signal.aborted && current() === read) {
            invalidate(read);
          }
          return null;
        })
        .then((rows) => {
          if (signal.aborted || current() !== read) return undefined;
          read.pending = undefined;
          if (rows) read.rows = rows;
          setResult((previous) => ({
            scope,
            pending: false,
            failed: rows === null,
            rows: rows ?? (previous?.scope === scope ? previous.rows : undefined),
          }));
          return rows;
        });
      read.pending = { controller, promise };
      return promise;
    },
    [scope, current, invalidate],
  );

  const readMedia = useCallback(
    async (id: string, signal: AbortSignal) => {
      const read = current();
      if (!scope || !read) return null;
      const request = AbortSignal.any([read.signal, signal]);
      try {
        const { media } = await readScoped<{ media: GenericMediaInfo | null }>(
          MEDIA_QUERY,
          { id },
          scope,
          request,
        );
        if (current() !== read) throw new Error("media read superseded");
        return media;
      } catch (error) {
        if (error instanceof QueueIdentityChanged && !request.aborted && current() === read) {
          invalidate(read);
        }
        // Only "unknown media" means hidden or absent; any other failure is
        // unknown to the caller, which must not read it as a restriction.
        if (isUnknownMedia(error)) return null;
        throw error;
      }
    },
    [scope, current, invalidate],
  );

  /** The rows the latest read returned, even before they render (CORE-3361). */
  const latestRows = useCallback(() => current()?.rows, [current]);

  const refreshFresh = useCallback(() => refresh(true), [refresh]);
  const view = useMemo<MusicQueueView | undefined>(() => {
    if (!scope || scope.epoch?.aborted || !result || result.scope !== scope) return undefined;
    return { ...result, refresh: refreshFresh };
  }, [scope, result, refreshFresh]);
  return { view, refresh, readMedia, isCurrent: current, latestRows };
}
