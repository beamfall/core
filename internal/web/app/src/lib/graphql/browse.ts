// Listening browse reads (CORE-1635): the per-Media row a track list draws
// (title, runtime, class, credits and, for a container's listing, the scan's
// ordinal positions). Domain-neutral: the server reports the Media class and
// each credit's archetype, and the positions are core's own generic fields;
// nothing here names a domain or a plugin field.

import { graphql } from "@/lib/api";
import type { GenericMediaInfo } from "./types";

const MEDIA_ROW_FIELDS =
  "media(id: $id) { id title durationSeconds mediaClass credits { entityID title kind archetype relation } }";
const MEDIA_ROW_QUERY = `query MediaRow($id: String!) { ${MEDIA_ROW_FIELDS} }`;
// The same row plus the Media's stored fields, read for core's scan positions.
const MEDIA_TRACK_ROW_QUERY = `query MediaTrackRow($id: String!) { ${MEDIA_ROW_FIELDS} mediaFields(mediaID: $id) { name value } }`;

/** Core's scan writes a Media item's parsed ordinals as these stored fields (sdk ParsedMedia). */
export const SEQUENCE_FIELD = "sequence";
export const INDEX_FIELD = "index";

/**
 * A Media row, plus, when read with positions, the generic ordinals core's
 * scan stored for it: `sequence` (the outer ordinal) and `index` (the inner
 * one). Each is absent when the scan stored none.
 */
export interface MediaRow extends GenericMediaInfo {
  sequence?: number;
  index?: number;
}

function positiveNumber(value: string | undefined): number | undefined {
  const number = Number(value);
  return value && Number.isFinite(number) && number > 0 ? number : undefined;
}

/** One Media item's row fields; null when the item is missing or not visible. */
export async function fetchMediaRow(
  id: string,
  signal?: AbortSignal,
  options: { positions?: boolean; strict?: boolean } = {},
): Promise<MediaRow | null> {
  if (!options.positions) {
    const data = await graphql<{ media: GenericMediaInfo | null }>(MEDIA_ROW_QUERY, { id }, signal);
    if (options.strict && data.media === undefined) throw new Error("Missing Media row response");
    return data.media ?? null;
  }
  const data = await graphql<{
    media: GenericMediaInfo | null;
    mediaFields: { name: string; value: string }[] | null;
  }>(MEDIA_TRACK_ROW_QUERY, { id }, signal);
  if (options.strict && data.media === undefined) throw new Error("Missing Media row response");
  if (!data.media) return null;
  const row: MediaRow = { ...data.media };
  const fields = data.mediaFields ?? [];
  const sequence = positiveNumber(fields.find((field) => field.name === SEQUENCE_FIELD)?.value);
  const index = positiveNumber(fields.find((field) => field.name === INDEX_FIELD)?.value);
  if (sequence !== undefined) row.sequence = sequence;
  if (index !== undefined) row.index = index;
  return row;
}

const ROW_CONCURRENCY = 4;

/**
 * Rows for many Media items, keyed by id, fetched a few at a time. An item
 * whose read fails is absent by default. Pass strict when a caller must
 * distinguish a failed read from a confirmed missing or invisible item.
 */
export async function fetchMediaRows(
  ids: readonly string[],
  signal?: AbortSignal,
  options: { positions?: boolean; strict?: boolean } = {},
): Promise<Record<string, MediaRow>> {
  const rows: Record<string, MediaRow> = {};
  let next = 0;
  async function worker() {
    while (next < ids.length) {
      const id = ids[next++];
      try {
        const row = await fetchMediaRow(id, signal, options);
        if (row) rows[id] = row;
      } catch (err) {
        if (signal?.aborted || options.strict) throw err;
      }
    }
  }
  await Promise.all(Array.from({ length: Math.min(ROW_CONCURRENCY, ids.length) }, worker));
  return rows;
}
