import { useEffect, useMemo, useState } from "react";
import { Skeleton } from "@/components/ui/skeleton";
import { fetchSourceItems } from "@/lib/graphql";
import type { MediaRow } from "@/lib/graphql/browse";
import type { EntityInfo, SourceItem } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { filmographyOrder } from "../lib/people";
import {
  appearsOn,
  audioQueue,
  containerTrackRows,
  isAudioListing,
  type TrackRow,
  topTracks,
  trackGroups,
  trackRows,
} from "../lib/trackList";
import { ContainerChildren } from "./ContainerChildren";
import { MediaItemCard } from "./MediaItemCard";
import { TrackList } from "./TrackList";
import type { ListingRows } from "./useListingRows";

// The Media an Entity page lists when its Kind has no detail Layout. The
// layout follows the generic rules in lib/trackList (CORE-1635): an audio
// "container" lists numbered tracks, grouped under a heading per stored
// sequence when it has several (discs), an audio "person" lists top tracks,
// then all its tracks when there are more (CORE-2362), and the containers it
// appears on; any other "person" lists its Media as a poster grid, newest year
// first (CORE-2406); anything else lists its children grouped by
// kind (ContainerChildren, CORE-1786). The page reads the per-Media rows
// (useListingRows) and passes them in, so its Play action and this list use
// one track order (containerTrackRows).
export function EntityMediaBrowse({
  entity,
  items,
  label,
  listing,
}: {
  entity: EntityInfo | null;
  items: SourceItem[];
  /** Accessible name of the child list the non-audio layout renders. */
  label: string;
  /** The page's per-Media rows (useListingRows), shared with its Play action. */
  listing: ListingRows;
}) {
  const { wanted, rows } = listing;
  if (wanted && rows === null) {
    return (
      <div aria-hidden className="flex flex-col gap-2">
        <Skeleton className="bf-shimmer h-12 w-full" />
        <Skeleton className="bf-shimmer h-12 w-full" />
        <Skeleton className="bf-shimmer h-12 w-full" />
      </div>
    );
  }
  const tracks = containerTrackRows(entity, items, rows);
  if (tracks) return <ContainerTracks tracks={tracks} />;
  if (entity && rows && entity.archetype === "person" && isAudioListing(items, rows))
    return <PersonListening entity={entity} items={items} rows={rows} />;
  if (entity?.archetype === "person") return <PersonFilmography items={items} />;
  return <ContainerChildren items={items} label={label} />;
}

function PersonFilmography({ items }: { items: SourceItem[] }) {
  const t = useT();
  const ordered = useMemo(
    () => filmographyOrder(items.filter((item) => item.kind === "media")),
    [items],
  );
  if (ordered.length === 0) return null;
  return (
    <section className="bf-section flex flex-col gap-3">
      <h2 className="bf-section-title">
        <span>{t("personAppearsIn")}</span>
      </h2>
      <ul className="bf-grid" aria-label={t("personAppearsIn")}>
        {ordered.map((item, index) => (
          <MediaItemCard
            key={item.id}
            item={item}
            posInSet={index + 1}
            setSize={ordered.length}
            showYear
            nameContainer={false}
          />
        ))}
      </ul>
    </section>
  );
}

function ContainerTracks({ tracks }: { tracks: TrackRow[] }) {
  const t = useT();
  // One queue in display order, so playback runs on across groups.
  const queue = audioQueue(tracks.map((track) => track.item));
  return (
    <section className="bf-section flex flex-col gap-3">
      <h2 className="bf-section-title">
        <span>{t("trackListLabel")}</span>
      </h2>
      {trackGroups(tracks).map(({ group, rows: groupRows }) => {
        if (group === undefined)
          return <TrackList key="all" rows={groupRows} queue={queue} label={t("trackListLabel")} />;
        const heading = t("trackGroupLabel", { number: group });
        return (
          <div key={group} className="bf-track-group">
            <h3 className="bf-track-group-title">{heading}</h3>
            <TrackList rows={groupRows} queue={queue} label={heading} />
          </div>
        );
      })}
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
  rows: Record<string, MediaRow>;
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
  // Every track in server order, when the top list leaves some out.
  const all = items.filter((item) => item.kind === "media");
  const allRows = all.length > top.length ? trackRows(all, rows, new Set([entity.id])) : null;
  const elsewhere = appearsOn(entity.relatedEntities, items, rows);
  return (
    <>
      <section className="bf-section flex flex-col gap-3">
        <h2 className="bf-section-title">
          <span>{t("personTopTracks")}</span>
        </h2>
        <TrackList rows={topRows} queue={audioQueue(top)} label={t("personTopTracks")} />
      </section>
      {allRows && (
        <section className="bf-section flex flex-col gap-3">
          <h2 className="bf-section-title">
            <span>{t("personAllTracks")}</span>
          </h2>
          <TrackList rows={allRows} queue={audioQueue(all)} label={t("personAllTracks")} />
        </section>
      )}
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
                nameContainer={false}
              />
            ))}
          </ul>
        </section>
      )}
    </>
  );
}
