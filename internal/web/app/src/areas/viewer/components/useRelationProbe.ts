import { useEffect, useMemo, useState } from "react";
import { fetchMediaRows } from "@/lib/graphql/browse";
import type { EntityInfo, SourceItem } from "@/lib/graphql/types";
import {
  creditAttribute,
  creditRoles,
  ROLE_ATTRIBUTE,
  SEQUENCE_ATTRIBUTE,
} from "../lib/relationOrder";
import { AUDIO_CLASS, hasKnownNonAudio } from "../lib/trackList";

/**
 * What an Entity page's listing relation carries on its attributes (CORE-3014).
 * `sequenced`: a container whose relation places each Media in a sequence, so
 * the listing is already in that order and lists as a poster grid with no
 * shared play queue. `roles`: a person's comma-separated roles per Media id,
 * so the listing groups by role.
 */
export interface RelationProbe {
  pending: boolean;
  sequenced: boolean;
  roles: Record<string, string[]> | null;
}

const NONE: RelationProbe = { pending: false, sequenced: false, roles: null };

// Read only for an audio "container" or "person" listing, the layout these
// attributes reshape: every listed Media's row is read, and any credit from
// this Entity that carries a sequence or a role reshapes the page, so a first
// book without one (or whose read failed) does not hide the rest. A row that
// fails to read is skipped; with none read the page keeps its usual layout.
export function useRelationProbe(
  entity: EntityInfo | null,
  items: readonly SourceItem[],
): RelationProbe {
  const archetype = entity?.archetype;
  const entityID = entity?.id ?? "";
  const ids = useMemo(
    () => items.filter((item) => item.kind === "media").map((item) => item.id),
    [items],
  );
  const wanted =
    (archetype === "container" || archetype === "person") &&
    ids.length > 0 &&
    !hasKnownNonAudio(items);
  const key = `${entityID}\n${ids.join("\n")}`;
  const [probe, setProbe] = useState<{ key: string; probe: RelationProbe } | null>(null);

  useEffect(() => {
    if (!wanted) return;
    const controller = new AbortController();
    const { signal } = controller;
    const settle = (next: RelationProbe) => {
      if (!signal.aborted) setProbe({ key, probe: next });
    };
    (async () => {
      const rows = await fetchMediaRows(ids, signal);
      const read = ids.map((id) => rows[id]).filter((row) => row !== undefined);
      const attribute = archetype === "container" ? SEQUENCE_ATTRIBUTE : ROLE_ATTRIBUTE;
      // One audio row carrying the attribute: a video row carrying it, beside
      // an audio row without it, does not reshape a mixed listing.
      const carried = read.some(
        (row) =>
          row.mediaClass === AUDIO_CLASS &&
          creditAttribute(row.credits, entityID, attribute) !== "",
      );
      if (!carried) return settle(NONE);
      if (archetype === "container") return settle({ ...NONE, sequenced: true });
      const roles: Record<string, string[]> = {};
      for (const id of ids) {
        roles[id] = creditRoles(creditAttribute(rows[id]?.credits, entityID, ROLE_ATTRIBUTE));
      }
      settle({ ...NONE, roles });
    })().catch(() => settle(NONE));
    return () => controller.abort();
  }, [wanted, key, ids, archetype, entityID]);

  if (!wanted) return NONE;
  return probe?.key === key ? probe.probe : { ...NONE, pending: true };
}
