import { useCallback, useLayoutEffect, useMemo, useRef, useState } from "react";
import { getIdentityEpochSignal, graphql } from "@/lib/api";
import { useSession, useViewerIdentity } from "@/lib/session";

export const MUSIC_QUEUE_PARAM = "playQueue";

export interface MusicQueueRow {
  id: string;
  title: string;
  mediaClass: string;
  locked?: boolean;
}

const PAGE_SIZE = 200;
const QUEUE_QUERY = `query MusicQueue($query: String, $limit: Int, $offset: Int) {
  sourceItems(kind: "queue", query: $query, limit: $limit, offset: $offset) {
    id title mediaClass locked
  }
}`;

async function readMusicQueue(signal: AbortSignal): Promise<MusicQueueRow[]> {
  const rows = new Map<string, MusicQueueRow>();
  for (let offset = 0; ; offset += PAGE_SIZE) {
    signal.throwIfAborted();
    // An explicit read includes muted rows; playback still skips locked and non-audio rows.
    const { sourceItems } = await graphql<{ sourceItems: MusicQueueRow[] }>(
      QUEUE_QUERY,
      { query: "queue", limit: PAGE_SIZE, offset },
      signal,
    );
    signal.throwIfAborted();
    for (const row of sourceItems) rows.set(row.id, row);
    if (sourceItems.length < PAGE_SIZE) return [...rows.values()];
  }
}

export function playableMusic(row: MusicQueueRow): boolean {
  return row.mediaClass === "audio" && !row.locked;
}

type QueueScope = { epoch: AbortSignal | undefined };
type QueueRead = {
  scope: QueueScope;
  signal: AbortSignal;
  pending?: Promise<MusicQueueRow[] | undefined>;
};

export function useMusicQueue(enabled: boolean, cursor?: string | null) {
  const { session, revealed } = useSession();
  const identity = useViewerIdentity();
  const profileID = session?.profileID;
  const epoch = getIdentityEpochSignal();
  // A route change or identity change discards the previous read immediately.
  const scope = useMemo(
    () => (enabled && profileID ? { epoch, profileID, identity, revealed, cursor } : null),
    [enabled, epoch, profileID, identity, revealed, cursor],
  );
  const active = useRef<QueueRead | null>(null);
  const [result, setResult] = useState<{ scope: QueueScope; rows?: MusicQueueRow[] }>();
  const isCurrent = useCallback(
    () =>
      !!scope &&
      active.current?.scope === scope &&
      !active.current.signal.aborted &&
      scope.epoch === getIdentityEpochSignal(),
    [scope],
  );
  const refresh = useCallback(() => {
    const read = active.current;
    if (!read || !isCurrent()) return Promise.resolve(undefined);
    if (!read.pending) {
      const settle = (rows?: MusicQueueRow[]) => {
        read.pending = undefined;
        if (!isCurrent() || active.current !== read) return undefined;
        setResult({ scope: read.scope, rows });
        return rows;
      };
      read.pending = readMusicQueue(read.signal).then(settle, () => settle());
    }
    return read.pending;
  }, [isCurrent]);

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
  return { rows: scoped?.rows, settled: !!scoped, refresh, isCurrent };
}
