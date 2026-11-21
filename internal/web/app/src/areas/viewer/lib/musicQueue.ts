import { useCallback, useLayoutEffect, useMemo, useRef, useState } from "react";
import {
  ApiError,
  type GraphQLResult,
  getIdentityEpochSignal,
  graphql,
  postJSON,
  resetIdentityEpoch,
} from "@/lib/api";
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

type AddTrackResult = Omit<GraphQLResult<{ queueAdd: { id: string }[] }>, "errors"> & {
  errors?: { message: string; extensions?: { reason?: string } }[];
};

async function addTrack(
  mediaID: string,
  position: number | undefined,
  scope: QueueIdentity,
  signal: AbortSignal,
) {
  signal.throwIfAborted();
  const result = await postJSON<AddTrackResult>(
    "/api/graphql",
    {
      query: ADD_TRACK,
      variables: {
        mediaID,
        position,
        expectedProfileID: scope.profileID,
      },
    },
    signal,
  ).catch((error: unknown): AddTrackResult => {
    const payload = error instanceof ApiError ? error.payload : undefined;
    // GraphQL can refuse the request with a non-2xx status as well.
    if (
      payload &&
      typeof payload === "object" &&
      "errors" in payload &&
      Array.isArray(payload.errors) &&
      payload.errors.length
    ) {
      return { errors: payload.errors };
    }
    throw error;
  });
  signal.throwIfAborted();
  if (result.errors?.some(({ extensions }) => extensions?.reason === "PROFILE_MISMATCH")) {
    throw new QueueIdentityChanged();
  }
  if (result.errors?.length) return false;
  if (!result.data?.queueAdd) throw new Error("Queue write failed");
  return true;
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
  /** Set only for a server-queue route belonging to this profile. */
  server?: {
    run: { cursor: string };
    canEdit: () => boolean;
    invalidateIdentity: () => void;
    receive: (rows: MusicQueueRow[] | null) => void;
  };
}

let player: QueuePlayer | undefined;
let invalidEditEpoch: AbortSignal | undefined | null = null;
const menuListeners = new Set<() => void>();

function notifyMusicQueueMenu() {
  for (const listener of menuListeners) listener();
}

export function registerMusicQueuePlayer(next: QueuePlayer) {
  player = next;
  notifyMusicQueueMenu();
  return () => {
    if (player !== next) return;
    player = undefined;
    notifyMusicQueueMenu();
  };
}

export function subscribeMusicQueueMenu(listener: () => void) {
  menuListeners.add(listener);
  return () => {
    menuListeners.delete(listener);
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
  const epoch = getIdentityEpochSignal();
  if (!scope?.profileID || epoch?.aborted || epoch === invalidEditEpoch) return "hidden";
  if (!player) return "enabled";
  if (!sameIdentity(player.scope, scope) || !player.server?.canEdit()) return "hidden";
  return player.server.run.cursor === id ? "disabled" : "enabled";
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
  const run = player?.server?.run;
  const epoch = getIdentityEpochSignal();
  const signal = epoch ?? new AbortController().signal;
  const sameEpoch = () => !signal.aborted && epoch === getIdentityEpochSignal();
  const canEdit = () =>
    sameEpoch() && player?.server?.run === run && musicQueueMenuState(scope, id) === "enabled";
  const identityWasLost = (error: unknown) => {
    if (!sameEpoch()) return true;
    if (!(error instanceof QueueIdentityChanged)) return false;
    // Pending edits share this session even when no reader is mounted.
    invalidEditEpoch = epoch;
    if (player && sameIdentity(player.scope, scope)) player.server?.invalidateIdentity();
    notifyMusicQueueMenu();
    if (!run) resetIdentityEpoch();
    return true;
  };

  return serializeMusicQueue(async (): Promise<QueueEditResult> => {
    if (!canEdit() || !isTargetUnlocked()) return "dropped";
    const cursor = run?.cursor;
    const stillAtCursor = () => canEdit() && run?.cursor === cursor;
    let before: MusicQueueRow[];
    try {
      before = await readQueue(scope, signal);
    } catch (error) {
      if (identityWasLost(error) || !stillAtCursor()) return "dropped";
      player?.server?.receive(null);
      return "failed";
    }
    if (!stillAtCursor()) return "dropped";
    player?.server?.receive(before);
    if (!isTargetUnlocked() || before.some((row) => row.id === id && !playableMusic(row))) {
      return "dropped";
    }

    const withoutTarget = before.filter((row) => row.id !== id);
    const index = withoutTarget.findIndex((row) => row.id === cursor);
    if (cursor !== undefined && index < 0) return "dropped";
    try {
      const accepted = await addTrack(id, edit === "next" ? index + 1 : undefined, scope, signal);
      if (!accepted) return "failed";
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
    if (!stillAtCursor()) return "dropped";
    player?.server?.receive(after);
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
