import { useCallback, useLayoutEffect, useMemo, useRef, useState } from "react";
import { type GraphQLResult, getIdentityEpochSignal, graphql, postJSON } from "@/lib/api";
import type { GenericMediaInfo, SessionInfo } from "@/lib/graphql/types";
import { useSession, useViewerIdentity } from "@/lib/session";

export const MUSIC_QUEUE_PARAM = "playQueue";

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
  if (!live || !sameIdentity(live, expected)) {
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
  /** Set only for a server-queue route belonging to this profile. */
  server?: {
    canEdit: () => boolean;
    invalidateIdentity: () => void;
    receive: (rows: MusicQueueRow[] | null) => void;
  };
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

function sameIdentity(a: QueueIdentity, b: QueueIdentity): boolean {
  return (
    a.profileID === b.profileID &&
    a.revealed === b.revealed &&
    a.deviceRole === b.deviceRole &&
    a.profile === b.profile
  );
}

export function musicQueueMenuState(scope: QueueIdentity | undefined, id: string) {
  if (!scope?.profileID || getIdentityEpochSignal()?.aborted) return "hidden";
  if (!player) return "enabled";
  if (!sameIdentity(player.scope, scope) || !player.server?.canEdit()) return "hidden";
  return player.id === id ? "disabled" : "enabled";
}

export type MusicQueueEdit = "next" | "tail";
export type QueueEditResult = "confirmed" | "failed" | "dropped";

export function editMusicQueue(
  scope: QueueIdentity,
  id: string,
  edit: MusicQueueEdit,
  isTargetUnlocked: () => boolean = () => true,
) {
  if (musicQueueMenuState(scope, id) !== "enabled")
    return Promise.resolve<QueueEditResult>("dropped");
  const active = player;
  const epoch = getIdentityEpochSignal();
  const signal = epoch ?? new AbortController().signal;
  const sameEpoch = () => !signal.aborted && epoch === getIdentityEpochSignal();
  const canEdit = () =>
    sameEpoch() && player === active && musicQueueMenuState(scope, id) === "enabled";
  const identityWasLost = (error: unknown) => {
    if (!sameEpoch()) return true;
    if (!(error instanceof QueueIdentityChanged)) return false;
    if (player && sameIdentity(player.scope, scope)) player.server?.invalidateIdentity();
    return true;
  };

  return serializeMusicQueue(async (): Promise<QueueEditResult> => {
    if (!canEdit() || !isTargetUnlocked()) return "dropped";
    let before: MusicQueueRow[];
    try {
      before = await readQueue(scope, signal);
    } catch (error) {
      if (identityWasLost(error) || !canEdit()) return "dropped";
      active?.server?.receive(null);
      return "failed";
    }
    if (!canEdit()) return "dropped";
    active?.server?.receive(before);
    if (!isTargetUnlocked() || before.some((row) => row.id === id && !playableMusic(row))) {
      return "dropped";
    }

    const cursor = active?.id;
    const withoutTarget = before.filter((row) => row.id !== id);
    const index = withoutTarget.findIndex((row) => row.id === cursor);
    if (cursor !== undefined && index < 0) return "dropped";
    try {
      await addTrack(id, edit === "next" ? index + 1 : undefined, scope.profileID, signal);
    } catch (error) {
      if (identityWasLost(error)) return "dropped";
      // The write can commit even when its response is lost. Only read it back.
    }

    if (!sameEpoch()) return "dropped";
    let after: MusicQueueRow[] | null;
    try {
      after = await readQueue(scope, signal);
    } catch (error) {
      if (identityWasLost(error)) return "dropped";
      after = null;
    }
    if (!canEdit()) return "dropped";
    active?.server?.receive(after);
    if (!after) return "failed";

    const current = after.findIndex((row) => row.id === cursor);
    if (cursor !== undefined && current < 0) return "failed";
    const placed = edit === "tail" ? after.at(-1) : after[current + 1];
    return placed?.id === id && playableMusic(placed) ? "confirmed" : "failed";
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
