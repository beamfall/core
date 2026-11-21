import { useEffect, useMemo, useState } from "react";
import { fetchMediaRows, type MediaRow } from "@/lib/graphql/browse";
import type { EntityInfo, SourceItem } from "@/lib/graphql/types";
import { hasKnownNonAudio, listedRow } from "../lib/trackList";

/** The per-Media rows an Entity's listing reads; `rows` is null while they load. */
export interface ListingRows {
  wanted: boolean;
  rows: Record<string, MediaRow> | null;
}

// The rows behind the listening layout (CORE-1635), read once per Entity page
// so the page's Play action and the track list share one order. Only a
// "container" or "person" Entity with Media reads them, only when no listed
// item is already known to be something other than audio, and only a
// container reads the stored positions. A row the listing item already
// carries is built from it (CORE-2922); only the rest are read through
// media(id), so an artist or album of audio the server classed opens with
// no per-track request.
export function useListingRows(entity: EntityInfo | null, items: readonly SourceItem[]) {
  const archetype = entity?.archetype;
  const wanted =
    (archetype === "container" || archetype === "person") &&
    items.some((item) => item.kind === "media") &&
    !hasKnownNonAudio(items);
  const positions = archetype === "container";
  const listed = useMemo(() => {
    const rows: Record<string, MediaRow> = {};
    const unread: string[] = [];
    for (const item of items) {
      if (item.kind !== "media") continue;
      const row = listedRow(item);
      if (row) rows[item.id] = row;
      else unread.push(item.id);
    }
    return { rows, unread: unread.join("\n") };
  }, [items]);
  const readKey = `${positions}\n${listed.unread}`;
  const [read, setRead] = useState<{ key: string; rows: Record<string, MediaRow> } | null>(null);

  useEffect(() => {
    if (!wanted || !listed.unread) return;
    const controller = new AbortController();
    fetchMediaRows(listed.unread.split("\n"), controller.signal, { positions })
      .then((rows) => setRead({ key: readKey, rows }))
      .catch(() => {
        if (!controller.signal.aborted) setRead({ key: readKey, rows: {} });
      });
    return () => controller.abort();
  }, [wanted, listed.unread, positions, readKey]);

  const readRows = read?.key === readKey ? read.rows : null;
  const rows = useMemo(() => {
    if (!wanted) return null;
    if (!listed.unread) return listed.rows;
    return readRows ? { ...listed.rows, ...readRows } : null;
  }, [wanted, listed, readRows]);

  return { wanted, rows } satisfies ListingRows;
}
