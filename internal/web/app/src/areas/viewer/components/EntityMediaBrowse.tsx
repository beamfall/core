import { useEffect, useState } from "react";
import { Skeleton } from "@/components/ui/skeleton";
import { fetchSourceItems } from "@/lib/graphql";
import { fetchMediaRows } from "@/lib/graphql/browse";
import type { EntityInfo, GenericMediaInfo, SourceItem } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import {
  AUDIO_CLASS,
  appearsOn,
  hasKnownNonAudio,
  isAudioListing,
  ownerPeople,
  topTracks,
  trackRows,
} from "../lib/trackList";
import { playQueueOf } from "../lib/watchQueue";
import { ContainerChildren } from "./ContainerChildren";
import { MediaItemCard } from "./MediaItemCard";
import { TrackList } from "./TrackList";

// The Media an Entity page lists when its Kind has no detail Layout. The
// layout follows the generic rules in lib/trackList (CORE-1635): an audio
// "container" lists numbered tracks, an audio "person" lists top tracks and
// the containers it appears on; anything else lists its children grouped by
// kind (ContainerChildren, CORE-1786). Only those two archetypes read
// per-Media rows, and only when no listed item is already known to be
// something other than audio.
export function EntityMediaBrowse({
  entity,
  items,
  label,
}: {
  entity: EntityInfo | null;
  items: SourceItem[];
  /** Accessible name of the child list the non-audio layout renders. */
  label: string;
}) {
  const archetype = entity?.archetype;
  const wantsRows =
    (archetype === "container" || archetype === "person") &&
    items.some((item) => item.kind === "media") &&
    !hasKnownNonAudio(items);
  const [rows, setRows] = useState<Record<string, GenericMediaInfo> | null>(null);
  const ids = items
    .filter((item) => item.kind === "media")
    .map((item) => item.id)
    .join("\n");

  useEffect(() => {
    setRows(null);
    if (!wantsRows || !ids) return;
    const controller = new AbortController();
    fetchMediaRows(ids.split("\n"), controller.signal)
      .then(setRows)
      .catch(() => {
        if (!controller.signal.aborted) setRows({});
      });
    return () => controller.abort();
  }, [wantsRows, ids]);

  if (wantsRows && rows === null) {
    return (
      <div aria-hidden className="flex flex-col gap-2">
        <Skeleton className="bf-shimmer h-12 w-full" />
        <Skeleton className="bf-shimmer h-12 w-full" />
        <Skeleton className="bf-shimmer h-12 w-full" />
      </div>
    );
  }
  if (entity && rows && isAudioListing(items, rows)) {
    if (archetype === "container")
      return <ContainerTracks entity={entity} items={items} rows={rows} />;
    if (archetype === "person")
      return <PersonListening entity={entity} items={items} rows={rows} />;
  }
  return <ContainerChildren items={items} label={label} />;
}

/** Marks rows the page verified as audio, so the player's queue knows their class. */
function audioQueue(items: readonly SourceItem[]) {
  return playQueueOf(items.map((item) => ({ ...item, mediaClass: AUDIO_CLASS })));
}

function ContainerTracks({
  entity,
  items,
  rows,
}: {
  entity: EntityInfo;
  items: SourceItem[];
  rows: Record<string, GenericMediaInfo>;
}) {
  const t = useT();
  const tracks = trackRows(items, rows, ownerPeople(entity.relatedEntities, items, rows));
  return (
    <section className="bf-section flex flex-col gap-3">
      <h2 className="bf-section-title">
        <span>{t("trackListLabel")}</span>
      </h2>
      <TrackList rows={tracks} queue={audioQueue(items)} label={t("trackListLabel")} />
    </section>
  );
}

function PersonListening({
  entity,
  items,
  rows,
}: {
  entity: EntityInfo;
  items: SourceItem[];
  rows: Record<string, GenericMediaInfo>;
}) {
  const t = useT();
  const [profileTop, setProfileTop] = useState<SourceItem[]>([]);

  useEffect(() => {
    const controller = new AbortController();
    fetchSourceItems(
      {
        kind: "top-tracks",
        entityKind: "",
        libraryID: "",
        entityID: "",
        fieldFilters: null,
        entityFilter: null,
        limit: 200,
        offset: 0,
      },
      controller.signal,
    )
      .then(setProfileTop)
      .catch(() => {
        if (!controller.signal.aborted) setProfileTop([]);
      });
    return () => controller.abort();
  }, []);

  const top = topTracks(items, profileTop);
  // Numbered by rank; the person's own credit is never repeated on its rows.
  const topRows = trackRows(top, rows, new Set([entity.id]));
  const elsewhere = appearsOn(entity.relatedEntities, items, rows);
  return (
    <>
      <section className="bf-section flex flex-col gap-3">
        <h2 className="bf-section-title">
          <span>{t("personTopTracks")}</span>
        </h2>
        <TrackList rows={topRows} queue={audioQueue(top)} label={t("personTopTracks")} />
      </section>
      {elsewhere.length > 0 && (
        <section className="bf-section flex flex-col gap-3">
          <h2 className="bf-section-title">
            <span>{t("personAppearsOn")}</span>
          </h2>
          <ul className="bf-grid">
            {elsewhere.map((item, index) => (
              <MediaItemCard
                key={item.id}
                item={item}
                posInSet={index + 1}
                setSize={elsewhere.length}
              />
            ))}
          </ul>
        </section>
      )}
    </>
  );
}
