import {
  useCallback,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { ApiError, getIdentityEpochSignal, graphql } from "@/lib/api";
import { queueAddManyEnd, queueAddMedia, queuePlayNextMedia } from "@/lib/graphql";
import type { GenericMediaInfo, SessionInfo } from "@/lib/graphql/types";
import { useSession, useViewerIdentity } from "@/lib/session";
import { isUnknownMedia } from "./mediaDetail";

export const MUSIC_QUEUE_PARAM = "playQueue";
export const QUEUE_BATCH_SIZE = 500;

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
  currentSession?: SessionInfo | null;
  sourceItems: MusicQueueRow[];
  playQueue?: {
    currentEntryID?: string | null;
    entries?: PlayQueueEntryRef[];
  } | null;
};
type SessionResult = { currentSession?: SessionInfo | null };

export interface QueueSnapshot {
  rows: MusicQueueRow[];
  currentEntryID: string | null;
  entries: PlayQueueEntryRef[];
  entryMediaIDs: readonly string[];
  session?: SessionInfo | null;
}

const queueEntryMediaIDs = new WeakMap<readonly MusicQueueRow[], readonly string[]>();

async function readMusicQueue(
  readPage: (offset: number) => Promise<Page>,
  onSnapshotMeta?: (meta: {
    currentEntryID: string | null;
    entries: PlayQueueEntryRef[];
    session?: SessionInfo | null;
  }) => void,
): Promise<MusicQueueRow[]> {
  // The first row of each later page must still be the preceding page's last
  // row. If rows shifted across that boundary, start over from the server.
  attempts: for (let attempt = 0; attempt < 3; attempt++) {
    const rows: MusicQueueRow[] = [];
    const priorPageIDs = new Set<string>();
    let entryRefs: PlayQueueEntryRef[] | undefined;
    let currentEntryID: string | null = null;
    let session: SessionInfo | null | undefined;
    for (let offset = 0; ; offset += PAGE_SIZE - 1) {
      // An explicit read includes muted rows; playback skips locked and non-audio rows.
      const page = await readPage(offset);
      const { sourceItems } = page;
      if (offset === 0) {
        session = page.currentSession;
        currentEntryID = page.playQueue?.currentEntryID ?? null;
        if (Array.isArray(page.playQueue?.entries)) {
          entryRefs = page.playQueue.entries;
        }
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
        onSnapshotMeta?.({ currentEntryID, entries: entryRefs ?? [], session });
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

type SnapshotListener = (profileID: string | undefined, snapshot: QueueSnapshot) => void;
const snapshotListeners = new Set<SnapshotListener>();

/** Profile mismatch refused on read or mutation: drops queue edits cleanly (CORE-2845). */
export class QueueProfileMismatchError extends Error {
  constructor(message = "Profile mismatch") {
    super(message);
    this.name = "QueueProfileMismatchError";
  }
}

export function isProfileMismatchError(err: unknown): boolean {
  if (err instanceof QueueProfileMismatchError) return true;
  if (!(err instanceof ApiError)) return false;
  const payload = err.payload as
    | { reason?: unknown; errors?: { extensions?: { reason?: unknown } }[] }
    | undefined;
  if (!payload || typeof payload !== "object") return false;
  if (payload.reason === "PROFILE_MISMATCH") return true;
  return (
    Array.isArray(payload.errors) &&
    payload.errors.some((error) => error?.extensions?.reason === "PROFILE_MISMATCH")
  );
}

// Coordinated serializer for server queue edits & player advances (CORE-2845):
// edits run strictly FIFO; advances wait on `hasPendingQueueEdits()` BEFORE
// calling `beginQueueAdvance()`, and callers MUST NEVER await `waitForPendingQueueEdits()`
// while holding `beginQueueAdvance()` (prevents circular waits).
let editChain: Promise<void> = Promise.resolve();
let pendingEditCount = 0;
let editSeq = 0;
let activeAdvanceCount = 0;
const editWaiters = new Set<() => void>();
const advanceWaiters = new Set<() => void>();
const editListeners = new Set<() => void>();

function drainWaiters(waiters: Set<() => void>): void {
  for (const resolve of waiters) resolve();
  waiters.clear();
}

function notifyEditListeners(): void {
  for (const listener of editListeners) listener();
}

function subscribeQueueEdits(listener: () => void): () => void {
  editListeners.add(listener);
  return () => editListeners.delete(listener);
}

export function hasPendingQueueEdits(): boolean {
  return pendingEditCount > 0;
}

export function useHasPendingQueueEdits(): boolean {
  return useSyncExternalStore(subscribeQueueEdits, hasPendingQueueEdits, hasPendingQueueEdits);
}

export function currentEditSeq(): number {
  return editSeq;
}

export function waitForPendingQueueEdits(): Promise<void> {
  if (pendingEditCount === 0) return Promise.resolve();
  return new Promise<void>((resolve) => {
    editWaiters.add(resolve);
  });
}

export function beginQueueAdvance(): () => void {
  activeAdvanceCount++;
  let ended = false;
  return () => {
    if (ended) return;
    ended = true;
    activeAdvanceCount = Math.max(0, activeAdvanceCount - 1);
    if (activeAdvanceCount === 0) drainWaiters(advanceWaiters);
  };
}

export async function enqueueQueueEdit<T>(work: () => Promise<T>): Promise<T> {
  pendingEditCount++;
  notifyEditListeners();
  let result!: T;
  let failure: unknown;
  let failed = false;
  const run = editChain.then(async () => {
    while (activeAdvanceCount > 0) {
      await new Promise<void>((resolve) => {
        advanceWaiters.add(resolve);
      });
    }
    editSeq++;
    try {
      result = await work();
    } catch (err) {
      failed = true;
      failure = err;
    } finally {
      editSeq++;
      pendingEditCount = Math.max(0, pendingEditCount - 1);
      notifyEditListeners();
      if (pendingEditCount === 0) drainWaiters(editWaiters);
    }
    if (failed) throw failure;
    return result;
  });
  editChain = run.then(
    () => undefined,
    () => undefined,
  );
  return run;
}

export function resetQueueCoordinatorForTest(): void {
  editChain = Promise.resolve();
  pendingEditCount = 0;
  editSeq = 0;
  activeAdvanceCount = 0;
  drainWaiters(editWaiters);
  drainWaiters(advanceWaiters);
  editListeners.clear();
  snapshotListeners.clear();
}

export function publishMusicQueueSnapshot(
  profileID: string | undefined,
  snapshot: QueueSnapshot,
): void {
  for (const listener of snapshotListeners) {
    listener(profileID, snapshot);
  }
}

function assertExpectedSession(
  session: SessionInfo | null | undefined,
  expectedProfileID?: string,
): void {
  if (!expectedProfileID) return;
  if (session === null || (session !== undefined && session.profileID !== expectedProfileID)) {
    throw new QueueProfileMismatchError();
  }
}

export async function readMusicQueueSnapshot(
  options: { expectedProfileID?: string; signal?: AbortSignal } = {},
): Promise<QueueSnapshot> {
  const { expectedProfileID, signal } = options;
  signal?.throwIfAborted();
  let meta: {
    currentEntryID: string | null;
    entries: PlayQueueEntryRef[];
    session?: SessionInfo | null;
  } = { currentEntryID: null, entries: [] };
  const rows = await readMusicQueue(
    (offset) =>
      graphql<Page>(QUEUE_QUERY, { query: "queue", limit: PAGE_SIZE, offset }, signal, {
        adminUnlock: false,
      }),
    (collected) => {
      meta = collected;
    },
  );
  signal?.throwIfAborted();
  assertExpectedSession(meta.session, expectedProfileID);
  const entryMediaIDs = meta.entries.map((entry) => entry.mediaID);
  if (meta.entries.length > 0) queueEntryMediaIDs.set(rows, entryMediaIDs);
  return {
    rows,
    currentEntryID: meta.currentEntryID,
    entries: meta.entries,
    entryMediaIDs,
    session: meta.session,
  };
}

function sameQueueOrder(a: readonly MusicQueueRow[], b: readonly MusicQueueRow[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (a[i]?.id !== b[i]?.id) return false;
  }
  return true;
}

async function rereadSnapshotSafely(
  profileID: string | undefined,
  signal: AbortSignal | undefined,
  applyConfirmed?: (snapshot: QueueSnapshot) => void,
): Promise<QueueSnapshot | null> {
  if (signal?.aborted) return null;
  try {
    const reread = await readMusicQueueSnapshot({ expectedProfileID: profileID, signal });
    applyConfirmed?.(reread);
    publishMusicQueueSnapshot(profileID ?? reread.session?.profileID ?? undefined, reread);
    return reread;
  } catch {
    return null;
  }
}

/**
 * Serialized Play next / Add to queue against the server queue (CORE-2845).
 * Always checks expectedProfileID, rereads server state on confirmation or
 * post-write failure (no automatic write replay), and never requeues the active track.
 */
export async function editServerQueue({
  mediaID,
  placement,
  expectedProfileID,
  currentPlayingMediaID,
  signal,
  applyConfirmed,
}: {
  mediaID: string;
  placement: "next" | "end";
  expectedProfileID?: string;
  currentPlayingMediaID?: string | null | (() => string | null | undefined);
  signal?: AbortSignal;
  applyConfirmed?: (snapshot: QueueSnapshot) => void;
}): Promise<QueueSnapshot> {
  return enqueueQueueEdit(async () => {
    signal?.throwIfAborted();
    // Resolve dynamic playing-anchor after waiting on any in-flight advance.
    const livePlayingMediaID =
      typeof currentPlayingMediaID === "function" ? currentPlayingMediaID() : currentPlayingMediaID;
    if (livePlayingMediaID && mediaID === livePlayingMediaID) {
      throw new Error("Cannot requeue the currently playing track");
    }
    const before = await readMusicQueueSnapshot({ expectedProfileID, signal });
    const effectiveProfile = expectedProfileID ?? before.session?.profileID ?? undefined;
    const serverCurrentMediaID = before.currentEntryID
      ? before.entries.find((entry) => entry.id === before.currentEntryID)?.mediaID
      : undefined;
    const anchorID = livePlayingMediaID ?? serverCurrentMediaID ?? "";
    if (anchorID && anchorID === mediaID && livePlayingMediaID === mediaID) {
      throw new Error("Cannot requeue the currently playing track");
    }
    let writeStarted = false;
    try {
      writeStarted = true;
      if (placement === "next") {
        const currentIndex = anchorID ? before.rows.findIndex((row) => row.id === anchorID) : -1;
        const existingIndex = before.rows.findIndex((row) => row.id === mediaID);
        const position =
          currentIndex < 0
            ? 0
            : existingIndex >= 0 && existingIndex < currentIndex
              ? currentIndex
              : currentIndex + 1;
        await queuePlayNextMedia(mediaID, position, effectiveProfile);
      } else {
        let duplicateCount = 0;
        for (const entry of before.entries) {
          if (entry.mediaID === mediaID) duplicateCount++;
        }
        await queueAddMedia(
          mediaID,
          duplicateCount > 1 ? before.rows.length : undefined,
          effectiveProfile,
        );
      }
      const after = await readMusicQueueSnapshot({
        expectedProfileID: effectiveProfile,
        signal,
      });
      applyConfirmed?.(after);
      publishMusicQueueSnapshot(effectiveProfile ?? after.session?.profileID ?? undefined, after);
      return after;
    } catch (err) {
      if (isProfileMismatchError(err)) throw err;
      if (writeStarted) {
        await rereadSnapshotSafely(effectiveProfile, signal, applyConfirmed);
      }
      throw err;
    }
  });
}

function orderedCatalogAfterCurrent(
  catalogIDs: readonly string[],
  currentMediaID: string,
): string[] {
  const unique: string[] = [];
  const seen = new Set<string>();
  for (const id of catalogIDs) {
    if (!id || seen.has(id)) continue;
    seen.add(id);
    unique.push(id);
  }
  const currentIndex = unique.indexOf(currentMediaID);
  if (currentIndex < 0) return unique.filter((id) => id !== currentMediaID);
  return [...unique.slice(currentIndex + 1), ...unique.slice(0, currentIndex)];
}

/**
 * Converts an active album / playlist / Play all run into the server Play Queue
 * on the first explicit edit (Play next or Add to queue): complete source
 * listing before queueWindow truncation, current track first, existing server
 * rows kept in order, then missing playable catalog tracks (CORE-2845).
 */
export async function convertCatalogAndApplyEdit({
  resolveCatalogIDs,
  currentMediaID,
  editMediaID,
  placement,
  expectedProfileID,
  isStillCurrent,
  applyConfirmed,
  onConverted,
  signal,
}: {
  resolveCatalogIDs: () => Promise<readonly string[]>;
  currentMediaID: string;
  editMediaID: string;
  placement: "next" | "end";
  expectedProfileID?: string;
  isStillCurrent?: () => boolean;
  applyConfirmed?: (snapshot: QueueSnapshot) => void;
  onConverted: (snapshot: QueueSnapshot, profileID: string) => void;
  signal?: AbortSignal;
}): Promise<QueueSnapshot> {
  return enqueueQueueEdit(async () => {
    signal?.throwIfAborted();
    if (!currentMediaID || editMediaID === currentMediaID) {
      throw new Error("Cannot requeue the currently playing track");
    }
    const rawCatalog = await resolveCatalogIDs();
    signal?.throwIfAborted();
    if (isStillCurrent && !isStillCurrent()) {
      throw new Error("Queue conversion superseded");
    }
    const orderedCatalog = orderedCatalogAfterCurrent(rawCatalog, currentMediaID);
    const before = await readMusicQueueSnapshot({ expectedProfileID, signal });
    const effectiveProfile = expectedProfileID ?? before.session?.profileID ?? undefined;
    if (!effectiveProfile) {
      throw new Error("Missing profile for server queue conversion");
    }
    if (isStillCurrent && !isStillCurrent()) {
      throw new Error("Queue conversion superseded");
    }

    let writeStarted = false;
    try {
      if (before.rows.length === 0) {
        const seen = new Set<string>([currentMediaID, editMediaID]);
        const rest: string[] = [];
        for (const id of orderedCatalog) {
          if (!seen.has(id)) {
            seen.add(id);
            rest.push(id);
          }
        }
        const batchIDs =
          placement === "next"
            ? [currentMediaID, editMediaID, ...rest]
            : [currentMediaID, ...rest, editMediaID];
        writeStarted = true;
        for (let i = 0; i < batchIDs.length; i += QUEUE_BATCH_SIZE) {
          await queueAddManyEnd(batchIDs.slice(i, i + QUEUE_BATCH_SIZE), effectiveProfile);
        }
      } else {
        const existingSet = new Set<string>(before.rows.map((row) => row.id));
        existingSet.add(currentMediaID);
        writeStarted = true;
        if (before.rows[0]?.id !== currentMediaID) {
          await queueAddMedia(currentMediaID, 0, effectiveProfile);
        }
        if (placement === "next") {
          await queueAddMedia(editMediaID, 1, effectiveProfile);
          const missing: string[] = [];
          for (const id of orderedCatalog) {
            if (!existingSet.has(id) && id !== editMediaID) {
              missing.push(id);
            }
          }
          for (let i = 0; i < missing.length; i += QUEUE_BATCH_SIZE) {
            await queueAddManyEnd(missing.slice(i, i + QUEUE_BATCH_SIZE), effectiveProfile);
          }
        } else {
          const alreadyExisting = existingSet.has(editMediaID);
          const missing: string[] = [];
          for (const id of orderedCatalog) {
            if (!existingSet.has(id) && id !== editMediaID) {
              missing.push(id);
            }
          }
          const appendIDs = alreadyExisting ? missing : [...missing, editMediaID];
          for (let i = 0; i < appendIDs.length; i += QUEUE_BATCH_SIZE) {
            await queueAddManyEnd(appendIDs.slice(i, i + QUEUE_BATCH_SIZE), effectiveProfile);
          }
          if (alreadyExisting) {
            let duplicateCount = 0;
            for (const entry of before.entries) {
              if (entry.mediaID === editMediaID) duplicateCount++;
            }
            await queueAddMedia(
              editMediaID,
              duplicateCount > 1 ? before.rows.length + missing.length : undefined,
              effectiveProfile,
            );
          }
        }
      }

      const confirmed = await readMusicQueueSnapshot({
        expectedProfileID: effectiveProfile,
        signal,
      });
      if (!isStillCurrent || isStillCurrent()) {
        applyConfirmed?.(confirmed);
        publishMusicQueueSnapshot(effectiveProfile, confirmed);
        onConverted(confirmed, effectiveProfile);
      }
      return confirmed;
    } catch (err) {
      if (isProfileMismatchError(err)) throw err;
      if (writeStarted) {
        const reread = await rereadSnapshotSafely(effectiveProfile, signal, applyConfirmed);
        if (
          reread &&
          (!isStillCurrent || isStillCurrent()) &&
          !sameQueueOrder(reread.rows, before.rows) &&
          reread.rows.some((row) => row.id === currentMediaID)
        ) {
          onConverted(reread, effectiveProfile);
        }
      }
      throw err;
    }
  });
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
    gen: number;
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
    active.current = { scope, signal, gen: 0 };
    const listener: SnapshotListener = (targetProfileID, snapshot) => {
      if (targetProfileID && targetProfileID !== scope.profileID) return;
      const read = active.current;
      if (!read || read.scope !== scope || read.signal.aborted || read.invalid) return;
      read.gen++;
      read.pending?.controller.abort();
      read.pending = undefined;
      if (snapshot.entries.length > 0) {
        queueEntryMediaIDs.set(snapshot.rows, snapshot.entryMediaIDs);
      }
      read.rows = snapshot.rows;
      setResult({ scope, pending: false, failed: false, rows: snapshot.rows });
    };
    snapshotListeners.add(listener);
    return () => {
      snapshotListeners.delete(listener);
      controller.abort();
    };
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
      read.gen++;
      const startGen = read.gen;
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
          if (signal.aborted || current() !== read || read.gen !== startGen) return undefined;
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

  const applyConfirmedSnapshot = useCallback(
    (snapshot: QueueSnapshot) => {
      const read = current();
      if (!scope || !read) return;
      read.gen++;
      read.pending?.controller.abort();
      read.pending = undefined;
      if (snapshot.entries.length > 0) {
        queueEntryMediaIDs.set(snapshot.rows, snapshot.entryMediaIDs);
      }
      read.rows = snapshot.rows;
      setResult({ scope, pending: false, failed: false, rows: snapshot.rows });
    },
    [scope, current],
  );

  const readSnapshot = useCallback(async (): Promise<QueueSnapshot | null> => {
    const read = current();
    if (!scope || !read) return null;
    try {
      const snap = await readMusicQueueSnapshot({
        expectedProfileID: scope.profileID,
        signal: read.signal,
      });
      if (current() !== read) return null;
      return snap;
    } catch (err) {
      if (
        (err instanceof QueueIdentityChanged || isProfileMismatchError(err)) &&
        !read.signal.aborted &&
        current() === read
      ) {
        invalidate(read);
      }
      throw err;
    }
  }, [scope, current, invalidate]);

  /** The rows the latest read returned, even before they render (CORE-3361). */
  const latestRows = useCallback(() => current()?.rows, [current]);

  const refreshFresh = useCallback(() => refresh(true), [refresh]);
  const view = useMemo<MusicQueueView | undefined>(() => {
    if (!scope || scope.epoch?.aborted || !result || result.scope !== scope) return undefined;
    return { ...result, refresh: refreshFresh };
  }, [scope, result, refreshFresh]);
  return {
    view,
    refresh,
    readSnapshot,
    applyConfirmedSnapshot,
    readMedia,
    isCurrent: current,
    latestRows,
  };
}
