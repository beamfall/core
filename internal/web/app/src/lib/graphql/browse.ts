// Listening browse reads (CORE-1635): the per-Media row a track list draws
// (title, runtime, class, credits). Domain-neutral: the server reports the
// Media class and each credit's archetype; nothing here names a domain.

import { graphql } from "@/lib/api";
import type { GenericMediaInfo } from "./types";

const MEDIA_ROW_QUERY =
  "query MediaRow($id: String!) { media(id: $id) { id title durationSeconds mediaClass credits { entityID title kind archetype relation } } }";

/** One Media item's row fields; null when the item is missing or not visible. */
export async function fetchMediaRow(
  id: string,
  signal?: AbortSignal,
): Promise<GenericMediaInfo | null> {
  const data = await graphql<{ media: GenericMediaInfo | null }>(MEDIA_ROW_QUERY, { id }, signal);
  return data.media ?? null;
}

const ROW_CONCURRENCY = 4;

/**
 * Rows for many Media items, keyed by id, fetched a few at a time. An item
 * whose read fails is simply absent; an abort rejects the whole call.
 */
export async function fetchMediaRows(
  ids: readonly string[],
  signal?: AbortSignal,
): Promise<Record<string, GenericMediaInfo>> {
  const rows: Record<string, GenericMediaInfo> = {};
  let next = 0;
  async function worker() {
    while (next < ids.length) {
      const id = ids[next++];
      try {
        const row = await fetchMediaRow(id, signal);
        if (row) rows[id] = row;
      } catch (err) {
        if (signal?.aborted) throw err;
      }
    }
  }
  await Promise.all(Array.from({ length: Math.min(ROW_CONCURRENCY, ids.length) }, worker));
  return rows;
}
