import { useEffect, useMemo, useState } from "react";
import { Skeleton } from "@/components/ui/skeleton";
import { fetchSourceItems } from "@/lib/graphql";
import type { MediaRow } from "@/lib/graphql/browse";
import type { EntityInfo, SourceItem } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { useViewerIdentity } from "@/lib/session";
import { useEngagementEpoch } from "../actions/engagementSync";
import { filmographyOrder } from "../lib/people";
import {
  appearsOn,
  audioQueue,
  containerTrackRows,
  isAudioListing,
  listedTrackRows,
  TOP_TRACKS_LIMIT,
  type TrackRow,
  topTracks,
  trackGroups,
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

async function readPlayedTracks(items: readonly SourceItem[], signal: AbortSignal) {
  const remaining = new Set(items.filter((item) => item.kind === "media").map((item) => item.id));
  const played: SourceItem[] = [];
  const limit = 200;
  const maxPages = 5;
  let emptyPages = 0;
  // Bound the profile-wide scan when the person has few plays, allowing for
  // pages emptied by the server's post-page mute filter.
  for (
    let pageIndex = 0;
    pageIndex < maxPages && remaining.size > 0 && played.length < TOP_TRACKS_LIMIT;
    pageIndex++
  ) {
    const page = await fetchSourceItems(
      {
        kind: "most-played",
        entityKind: "",
        libraryID: "",
        entityID: "",
        fieldFilters: null,
        entityFilter: null,
        limit,
        offset: pageIndex * limit,
      },
      signal,
    );
    signal.throwIfAborted();
    for (const item of page) {
      if (remaining.delete(item.id)) played.push(item);
    }
    emptyPages = page.length === 0 ? emptyPages + 1 : 0;
    if (emptyPages === 3) break;
  }
  return played;
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
  const identity = useViewerIdentity();
  const epoch = useEngagementEpoch();
  const [ranking, setRanking] = useState<{ identity: string; items: SourceItem[] } | null>(null);

  useEffect(() => {
    if (!identity) return;
    const controller = new AbortController();
    readPlayedTracks(items, controller.signal)
      .then((played) => {
        if (!controller.signal.aborted) setRanking({ identity, items: played });
      })
      .catch(() => {
        if (!controller.signal.aborted) setRanking(null);
      });
    return () => controller.abort();
  }, [identity, items, epoch]);

  const { tracks: top, hasPlays } = topTracks(
    items,
    ranking?.identity === identity ? (ranking?.items ?? []) : [],
  );
  const topLabel = t(hasPlays ? "personTopTracks" : "personTracks");
  const owners = new Set([entity.title]);
  // Numbered by rank; the person's own credit is never repeated on its rows.
  const topRows = listedTrackRows(top, rows, owners);
  // Every track in server order, when the top list leaves some out.
  const all = items.filter((item) => item.kind === "media");
  const allRows = all.length > top.length ? listedTrackRows(all, rows, owners) : null;
  const elsewhere = appearsOn(entity.relatedEntities, items, rows);
  return (
    <>
      <section className="bf-section flex flex-col gap-3">
        <h2 className="bf-section-title">
          <span>{topLabel}</span>
        </h2>
        <TrackList rows={topRows} queue={audioQueue(top)} label={topLabel} />
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
