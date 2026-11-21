import { useCallback, useLayoutEffect, useMemo, useRef, useState } from "react";
import { flushSync } from "react-dom";
import { getIdentityEpochSignal, graphql } from "@/lib/api";
import type { GenericMediaInfo, SessionInfo } from "@/lib/graphql/types";
import { openIdentityChanges } from "@/lib/identityChanges";
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

type QueueScope = {
  epoch: AbortSignal | undefined;
  profileID: string;
  revealed: boolean;
  deviceRole: string | undefined;
  profile: string | undefined;
};
// Undefined retires a superseded request. Null reports a completed, failed read.
type RefreshResult = MusicQueueRow[] | null | undefined;
type QueueRead = {
  scope: QueueScope;
  validation: typeof INITIAL_VALIDATION;
  controller: AbortController;
  signal: AbortSignal;
  invalid?: boolean;
  pending?: { controller: AbortController; promise: Promise<RefreshResult> };
  complete?: (rows: MusicQueueRow[] | null) => void;
};
type QueueResult = {
  scope: QueueScope;
  checked: boolean;
  failed?: boolean;
  invalid?: boolean;
  rows?: MusicQueueRow[];
};
const INITIAL_VALIDATION = { hidden: false };

export function useMusicQueue(enabled: boolean) {
  const { session, revealed, refresh: refreshIdentity } = useSession();
  const identity = useViewerIdentity();
  const { profileID, deviceRole, profile } = session ?? {};
  const epoch = getIdentityEpochSignal();
  // Only the live identity can reuse a snapshot across routes in the run.
  const scope = useMemo(
    () =>
      enabled && profileID ? { epoch, profileID, identity, revealed, deviceRole, profile } : null,
    [enabled, epoch, profileID, identity, revealed, deviceRole, profile],
  );
  const active = useRef<QueueRead | null>(null);
  const [result, setResult] = useState<QueueResult>();
  const [validation, setValidation] = useState(INITIAL_VALIDATION);
  const isCurrent = useCallback(
    () =>
      !!scope &&
      active.current?.scope === scope &&
      active.current.validation === validation &&
      !active.current.invalid &&
      !active.current.signal.aborted &&
      scope.epoch === getIdentityEpochSignal(),
    [scope, validation],
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

  const refresh = useCallback(
    (fresh = false, complete?: QueueRead["complete"]) => {
      const read = active.current;
      if (!read || !isCurrent()) return Promise.resolve(undefined);
      if (!fresh && read.pending) return read.pending.promise;
      // An explicit Play or advance starts a read at the time of the action.
      if (fresh) read.complete = complete;
      read.pending?.controller.abort();
      const request = new AbortController();
      const signal = AbortSignal.any([read.signal, request.signal]);
      const promise = readMusicQueue((offset) =>
        readScoped<Page>(QUEUE_QUERY, { query: "queue", limit: PAGE_SIZE, offset }, signal),
      )
        .catch(() => null)
        .then((rows) => {
          if (signal.aborted || !isCurrent() || active.current !== read) return undefined;
          read.pending = undefined;
          setResult((previous) => ({
            scope: read.scope,
            checked: rows !== null || (previous?.scope === read.scope && previous.checked),
            failed: rows === null,
            rows: rows ?? (previous?.scope === read.scope ? previous.rows : undefined),
          }));
          const finish = read.complete;
          read.complete = undefined;
          finish?.(rows);
          return rows;
        });
      read.pending = { controller: request, promise };
      return promise;
    },
    [isCurrent, readScoped],
  );

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
    if (!scope || validation.hidden) return;
    const controller = new AbortController();
    const signal = scope.epoch
      ? AbortSignal.any([controller.signal, scope.epoch])
      : controller.signal;
    // Revalidation can replace the read while an advance is waiting for it.
    const previous = active.current;
    const complete = previous?.scope === scope && !previous.invalid ? previous.complete : undefined;
    active.current = { scope, validation, signal, controller, complete };
    void refresh();
    return () => controller.abort();
  }, [scope, refresh, validation]);

  useLayoutEffect(() => {
    if (!scope) return;
    const invalidate = (hidden?: boolean) => {
      active.current?.controller.abort();
      // Hide old titles before a restored document can be painted.
      flushSync(() => {
        setResult((previous) =>
          previous?.scope === scope && !previous.invalid
            ? { scope, checked: previous.checked }
            : previous,
        );
        setValidation((previous) => ({ hidden: hidden ?? previous.hidden }));
      });
    };
    const foreground = () => {
      if (document.visibilityState !== "hidden") invalidate(false);
    };
    const pagehide = () => invalidate(true);
    const restore = (event: PageTransitionEvent) => {
      if (event.persisted) foreground();
    };
    const channel = openIdentityChanges();
    const identityChanged = () => invalidate();
    channel?.addEventListener("message", identityChanged);
    window.addEventListener("focus", foreground);
    window.addEventListener("pagehide", pagehide);
    window.addEventListener("pageshow", restore);
    document.addEventListener("visibilitychange", foreground);
    return () => {
      channel?.close();
      window.removeEventListener("focus", foreground);
      window.removeEventListener("pagehide", pagehide);
      window.removeEventListener("pageshow", restore);
      document.removeEventListener("visibilitychange", foreground);
    };
  }, [scope]);

  const scoped = scope && !scope.epoch?.aborted && result?.scope === scope ? result : undefined;
  return {
    rows: scoped?.rows,
    failed: scoped?.failed ?? false,
    checked: scoped?.checked ?? false,
    validated: scoped?.rows !== undefined,
    invalid: scoped?.invalid ?? false,
    refresh,
    isCurrent,
    readMedia,
  };
}
