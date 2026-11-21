import { useCallback, useLayoutEffect, useMemo, useRef, useState } from "react";
import { getIdentityEpochSignal, graphql } from "@/lib/api";
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
  }
}`;

type Page = { sourceItems: MusicQueueRow[] };
type SessionResult = { currentSession: SessionInfo | null };

async function readMusicQueue(
  readPage: (offset: number) => Promise<Page>,
): Promise<MusicQueueRow[]> {
  const rows = new Map<string, MusicQueueRow>();
  for (let offset = 0; ; offset += PAGE_SIZE) {
    // An explicit read includes muted rows; playback still skips locked and non-audio rows.
    const { sourceItems } = await readPage(offset);
    for (const row of sourceItems) rows.set(row.id, row);
    if (sourceItems.length < PAGE_SIZE) return [...rows.values()];
  }
}

export function playableMusic(row: MusicQueueRow): boolean {
  return row.mediaClass === "audio" && !row.locked;
}

type QueueScope = {
  epoch: AbortSignal | undefined;
  profileID: string;
  revealed: boolean;
  deviceRole: string | undefined;
  profile: string | undefined;
};
type QueueRead = {
  scope: QueueScope;
  signal: AbortSignal;
  invalid?: boolean;
  pending?: AbortController;
};
type QueueResult = {
  scope: QueueScope;
  checked: boolean;
  invalid?: boolean;
  rows?: MusicQueueRow[];
};

export function useMusicQueue(enabled: boolean, cursor?: string | null) {
  const { session, revealed, refresh: refreshIdentity } = useSession();
  const identity = useViewerIdentity();
  const { profileID, deviceRole, profile } = session ?? {};
  const epoch = getIdentityEpochSignal();
  // A route change or identity change discards the previous read immediately.
  const scope = useMemo(
    () =>
      enabled && profileID
        ? { epoch, profileID, identity, revealed, cursor, deviceRole, profile }
        : null,
    [enabled, epoch, profileID, identity, revealed, cursor, deviceRole, profile],
  );
  const active = useRef<QueueRead | null>(null);
  const [result, setResult] = useState<QueueResult>();
  const isCurrent = useCallback(
    () =>
      !!scope &&
      active.current?.scope === scope &&
      !active.current.invalid &&
      !active.current.signal.aborted &&
      scope.epoch === getIdentityEpochSignal(),
    [scope],
  );

  const readScoped = useCallback(
    async <T>(query: string, vars: Record<string, unknown>, signal: AbortSignal) => {
      signal.throwIfAborted();
      const data = await graphql<T & SessionResult>(query, vars, signal);
      signal.throwIfAborted();
      const read = active.current;
      if (!read || !isCurrent()) throw new Error("Queue scope retired");
      const live = data.currentSession;
      const expected = read.scope;
      if (
        live?.profileID !== expected.profileID ||
        live.revealed !== expected.revealed ||
        live.deviceRole !== expected.deviceRole ||
        live.profile !== expected.profile
      ) {
        read.invalid = true;
        setResult({ scope: expected, checked: false, invalid: true });
        // A different tab can change the shared cookie without retiring this tab's epoch.
        void refreshIdentity();
        throw new Error("Queue scope changed");
      }
      return data;
    },
    [isCurrent, refreshIdentity],
  );

  const refresh = useCallback(async () => {
    const read = active.current;
    if (!read || !isCurrent()) return undefined;
    // A Play or advance must start its own read even if verification is already in flight.
    read.pending?.abort();
    const request = new AbortController();
    read.pending = request;
    const signal = AbortSignal.any([read.signal, request.signal]);
    let rows: MusicQueueRow[] | undefined;
    try {
      rows = await readMusicQueue((offset) =>
        readScoped<Page>(QUEUE_QUERY, { query: "queue", limit: PAGE_SIZE, offset }, signal),
      );
    } catch {
      // An incomplete or failed read has no authoritative order.
    }
    if (signal.aborted || !isCurrent() || active.current !== read) return undefined;
    read.pending = undefined;
    setResult((previous) => ({
      scope: read.scope,
      checked: rows !== undefined || (previous?.scope === read.scope && previous.checked),
      rows,
    }));
    return rows;
  }, [isCurrent, readScoped]);

  const readMedia = useCallback(
    async (id: string, signal: AbortSignal) => {
      const read = active.current;
      if (!read || !isCurrent()) return null;
      try {
        const { media } = await readScoped<{ media: GenericMediaInfo | null }>(
          MEDIA_QUERY,
          { id },
          AbortSignal.any([read.signal, signal]),
        );
        return isCurrent() ? media : null;
      } catch {
        return null;
      }
    },
    [isCurrent, readScoped],
  );

  useLayoutEffect(() => {
    if (!scope) return;
    const controller = new AbortController();
    const signal = scope.epoch
      ? AbortSignal.any([controller.signal, scope.epoch])
      : controller.signal;
    active.current = { scope, signal };
    void refresh();
    return () => controller.abort();
  }, [scope, refresh]);

  const scoped = scope && !scope.epoch?.aborted && result?.scope === scope ? result : undefined;
  return {
    rows: scoped?.rows,
    settled: !!scoped,
    checked: scoped?.checked ?? false,
    invalid: scoped?.invalid ?? false,
    refresh,
    isCurrent,
    readMedia,
  };
}
