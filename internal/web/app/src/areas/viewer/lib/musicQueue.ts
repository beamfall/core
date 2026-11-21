import { useCallback, useLayoutEffect, useMemo, useRef, useState } from "react";
import { type GraphQLResult, getIdentityEpochSignal, graphql, postJSON } from "@/lib/api";
import type { GenericMediaInfo, SessionInfo } from "@/lib/graphql/types";
import { useSession, useViewerIdentity } from "@/lib/session";

export const MUSIC_QUEUE_PARAM = "playQueue";

const CONVERTED_QUEUE_KEY = "beamfall.musicQueue.conversion.";

/** The current tab can return to an earlier watch entry after a docked edit. */
export function convertedQueueProfile(playbackKey: string): string | null {
  try {
    return window.sessionStorage.getItem(CONVERTED_QUEUE_KEY + playbackKey);
  } catch {
    return null;
  }
}

/** Let storage errors stop conversion before a mutation can reach the server. */
export function rememberQueueConversion(playbackKey: string, profileID: string): void {
  window.sessionStorage.setItem(CONVERTED_QUEUE_KEY + playbackKey, profileID);
}

export interface MusicQueueRow {
  id: string;
  title: string;
  mediaClass: string;
  locked?: boolean;
}

const PAGE_SIZE = 200;
const SESSION_FIELDS = "currentSession { profileID revealed deviceRole profile }";
const QUEUE_QUERY = `query MusicQueue($query: String, $limit: Int, $offset: Int) {
  ${SESSION_FIELDS}
  sourceItems(kind: "queue", query: $query, limit: $limit, offset: $offset) {
    id title mediaClass locked
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

type Page = { sourceItems: MusicQueueRow[] };
type SessionResult = { currentSession: SessionInfo | null };

async function readMusicQueue(
  readPage: (offset: number) => Promise<Page>,
): Promise<MusicQueueRow[]> {
  // The first row of each later page must still be the preceding page's last
  // row. If rows shifted across that boundary, start over from the server.
  attempts: for (let attempt = 0; attempt < 3; attempt++) {
    const rows: MusicQueueRow[] = [];
    const ids = new Set<string>();
    for (let offset = 0; ; offset += PAGE_SIZE - 1) {
      // An explicit read includes muted rows; playback skips locked and non-audio rows.
      const { sourceItems } = await readPage(offset);
      if (offset > 0) {
        const overlap = sourceItems[0];
        if (!overlap || overlap.id !== rows.at(-1)?.id) continue attempts;
        rows[rows.length - 1] = overlap;
      }
      const additions = offset === 0 ? sourceItems : sourceItems.slice(1);
      for (const row of additions) {
        if (ids.has(row.id)) continue attempts;
        ids.add(row.id);
        rows.push(row);
      }
      if (sourceItems.length < PAGE_SIZE) return rows;
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

function readQueue(scope: QueueIdentity, signal: AbortSignal): Promise<MusicQueueRow[]> {
  return readMusicQueue((offset) =>
    readScoped<Page>(QUEUE_QUERY, { query: "queue", limit: PAGE_SIZE, offset }, scope, signal),
  );
}

const ADD_TRACK = `mutation AddMusicQueueTrack(
  $mediaID: String!, $position: Int, $expectedProfileID: String!
) {
  queueAdd(mediaID: $mediaID, position: $position, expectedProfileID: $expectedProfileID) { id }
}`;

async function addTrack(
  mediaID: string,
  position: number | undefined,
  expectedProfileID: string,
  signal: AbortSignal,
) {
  signal.throwIfAborted();
  const result = await postJSON<
    Omit<GraphQLResult<{ queueAdd: { id: string }[] }>, "errors"> & {
      errors?: { message: string; extensions?: { reason?: string } }[];
    }
  >(
    "/api/graphql",
    { query: ADD_TRACK, variables: { mediaID, position, expectedProfileID } },
    signal,
  );
  signal.throwIfAborted();
  if (result.errors?.some((error) => error.extensions?.reason === "PROFILE_MISMATCH")) {
    throw new QueueIdentityChanged();
  }
  if (result.errors?.length || !result.data?.queueAdd) throw new Error("Queue write failed");
}

let pending: Promise<void> | undefined;

/** Writes and cursor advances share a turn, including the confirmation read. */
export function serializeMusicQueue<T>(work: () => T | Promise<T>): Promise<T> {
  const result = pending ? pending.then(work) : (async () => work())();
  const turn = result.then(
    () => {},
    () => {},
  );
  pending = turn;
  void turn.then(() => {
    if (pending === turn) pending = undefined;
  });
  return result;
}

interface QueuePlayer {
  scope: QueueIdentity;
  id: string;
  /** Stays stable if this cursor re-registers during a conversion. */
  cursor?: object;
  /** False while a navigation is replacing the cursor. */
  canEdit?: () => boolean;
  /** Present until this catalog run starts using the server. */
  readCatalog?: (signal: AbortSignal) => Promise<readonly string[] | undefined>;
  beginEdit?: () => () => void;
  beginConversion?: () => void;
  invalidateIdentity?: () => void;
  receive: (rows: MusicQueueRow[] | null) => void;
}

let player: QueuePlayer | undefined;
const playerListeners = new Set<() => void>();

export function registerMusicQueuePlayer(next: QueuePlayer) {
  player = next;
  for (const listener of playerListeners) listener();
  return () => {
    if (player !== next) return;
    player = undefined;
    for (const listener of playerListeners) listener();
  };
}

export function subscribeMusicQueuePlayer(listener: () => void) {
  playerListeners.add(listener);
  return () => {
    playerListeners.delete(listener);
  };
}

export function isCurrentMusicTrack(id: string): boolean {
  return player?.id === id;
}

export type MusicQueueEdit = "next" | "tail";
export type QueueEditResult = "confirmed" | "failed" | "dropped";

export function editMusicQueue(scope: QueueIdentity, id: string, edit: MusicQueueEdit) {
  const epoch = getIdentityEpochSignal();
  const signal = epoch ?? new AbortController().signal;
  return serializeMusicQueue(async (): Promise<QueueEditResult> => {
    if (signal.aborted || epoch !== getIdentityEpochSignal()) return "dropped";
    const forProfile = () =>
      player?.scope.profileID === scope.profileID &&
      player.scope.revealed === scope.revealed &&
      player.scope.deviceRole === scope.deviceRole &&
      player.scope.profile === scope.profile
        ? player
        : undefined;
    const active = forProfile();
    if (active?.id === id) return "dropped";
    const endEdit = active?.beginEdit?.();
    const sameCursor = (live: QueuePlayer | undefined) =>
      live === active || (active?.cursor !== undefined && live?.cursor === active.cursor);
    const identityWasLost = (error: unknown) => {
      if (signal.aborted || epoch !== getIdentityEpochSignal()) return true;
      if (!(error instanceof QueueIdentityChanged)) return false;
      forProfile()?.invalidateIdentity?.();
      return true;
    };
    try {
      let convert = false;
      let wrote = false;
      let requested = false;
      let anchor: string | undefined;
      try {
        const rows = await readQueue(scope, signal);
        const current = active?.id;
        const reader = forProfile();
        if (!sameCursor(reader) || reader?.canEdit?.() === false) return "failed";

        const catalog = await reader?.readCatalog?.(signal);
        const withoutTarget = rows.filter((row) => row.id !== id);
        const write = (mediaID: string, position?: number, placement = false) => {
          signal.throwIfAborted();
          const live = forProfile();
          if (!sameCursor(live) || live?.canEdit?.() === false) {
            throw new Error("Playback cursor changed");
          }
          if (!wrote && convert) live?.beginConversion?.();
          wrote = true;
          if (placement) requested = true;
          return addTrack(mediaID, position, scope.profileID, signal);
        };

        if (current && catalog) {
          convert = true;
          if (rows[0]?.id !== current) await write(current, 0);
          const queued = new Set(rows.map((row) => row.id));
          queued.add(current);
          for (const mediaID of catalog) {
            if (queued.has(mediaID)) continue;
            await write(mediaID);
            queued.add(mediaID);
          }
        }

        const cursorIndex = withoutTarget.findIndex((row) => row.id === current);
        const position = edit === "tail" ? undefined : convert ? 1 : cursorIndex + 1;
        if (edit === "next" && (convert || cursorIndex >= 0)) anchor = current;
        await write(id, position, true);
      } catch (error) {
        if (identityWasLost(error)) return "dropped";
        if (!wrote) return "failed";
        // A rejected response may follow a committed write. The only recovery
        // after a write starts is a fresh read of the server's actual order.
      }

      let rows: MusicQueueRow[] | null;
      try {
        rows = await readQueue(scope, signal);
      } catch (error) {
        if (identityWasLost(error)) return "dropped";
        rows = null;
      }
      const live = forProfile();
      if (live && sameCursor(live) && live.canEdit?.() !== false) {
        live.receive(rows);
      }
      if (!rows || !requested) return "failed";

      const cursor = rows.findIndex((row) => row.id === anchor);
      if ((anchor && cursor < 0) || (convert && rows[0]?.id !== active?.id)) return "failed";
      const placed = edit === "tail" ? rows.at(-1) : rows[cursor + 1];
      if (placed?.id !== id || !playableMusic(placed)) return "failed";
      return "confirmed";
    } finally {
      endEdit?.();
    }
  });
}

export interface MusicQueueView {
  rows?: MusicQueueRow[];
  failed?: boolean;
  invalid?: boolean;
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

  const invalidateIdentity = useCallback(() => {
    const read = current();
    if (read) invalidate(read);
  }, [current, invalidate]);

  const refresh = useCallback(
    (fresh = false): Promise<RefreshResult> => {
      const read = current();
      if (!scope || !read) return Promise.resolve(undefined);
      if (!fresh && read.pending) return read.pending.promise;
      read.pending?.controller.abort();
      const controller = new AbortController();
      const signal = AbortSignal.any([read.signal, controller.signal]);
      const promise = readQueue(scope, signal)
        .catch((error: unknown) => {
          if (error instanceof QueueIdentityChanged && !signal.aborted && current() === read) {
            invalidate(read);
          }
          return null;
        })
        .then((rows) => {
          if (signal.aborted || current() !== read) return undefined;
          read.pending = undefined;
          setResult((previous) => ({
            scope,
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

  const acceptRows = useCallback(
    (rows: MusicQueueRow[] | null) => {
      const read = current();
      if (!read) return;
      read.pending?.controller.abort();
      read.pending = undefined;
      setResult((previous) => ({
        scope,
        failed: rows === null,
        rows: rows ?? (previous?.scope === scope ? previous.rows : undefined),
      }));
    },
    [scope, current],
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
        return current() === read ? media : null;
      } catch (error) {
        if (error instanceof QueueIdentityChanged && !request.aborted && current() === read) {
          invalidate(read);
        }
        return null;
      }
    },
    [scope, current, invalidate],
  );

  const view = scope && !scope.epoch?.aborted && result?.scope === scope ? result : undefined;
  return { view, refresh, acceptRows, invalidateIdentity, readMedia, isCurrent: current };
}
