import { useEffect, useState } from "react";
import { fetchMediaRows, type MediaRow } from "@/lib/graphql/browse";
import type { EntityInfo, SourceItem } from "@/lib/graphql/types";
import { hasKnownNonAudio } from "../lib/trackList";

/** The per-Media rows an Entity's listing reads; `rows` is null while they load. */
export interface ListingRows {
  wanted: boolean;
  rows: Record<string, MediaRow> | null;
}

// The rows behind the listening layout (CORE-1635), read once per Entity page
// so the page's Play action and the track list share one order. Only a
// "container" or "person" Entity with Media reads them, only when no listed
// item is already known to be something other than audio, and only a
// container reads the stored positions.
export function useListingRows(entity: EntityInfo | null, items: readonly SourceItem[]) {
  const archetype = entity?.archetype;
  const wanted =
    (archetype === "container" || archetype === "person") &&
    items.some((item) => item.kind === "media") &&
    !hasKnownNonAudio(items);
  const positions = archetype === "container";
  const [rows, setRows] = useState<Record<string, MediaRow> | null>(null);
  const ids = items
    .filter((item) => item.kind === "media")
    .map((item) => item.id)
    .join("\n");

  useEffect(() => {
    setRows(null);
    if (!wanted || !ids) return;
    const controller = new AbortController();
    fetchMediaRows(ids.split("\n"), controller.signal, { positions })
      .then(setRows)
      .catch(() => {
        if (!controller.signal.aborted) setRows({});
      });
    return () => controller.abort();
  }, [wanted, ids, positions]);

  return { wanted, rows } satisfies ListingRows;
}
