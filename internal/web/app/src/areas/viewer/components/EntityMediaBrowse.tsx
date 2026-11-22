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
  topTracks,
} from "../lib/trackList";
import { ContainerChildren } from "./ContainerChildren";
import { MediaItemCard } from "./MediaItemCard";
import { ContainerTracks, TrackList } from "./TrackList";
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
  guardContainerID,
}: {
  entity: EntityInfo | null;
  items: SourceItem[];
  /** The outermost container the spoiler guard keys on (CORE-3057). */
  guardContainerID?: string;
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
  if (entity?.archetype === "person") return <PosterGrid items={items} byYear />;
  // A rolled-up list stands its containers' Media in as container cards
  // (CORE-3058), which open their Entity page rather than play.
  if (items.some((item) => item.kind === "entity"))
    return <PosterGrid items={items} label={label} />;
  const container = entity?.archetype === "container" ? entity : null;
  return (
    <ContainerChildren
      items={items}
      label={label}
      containerID={guardContainerID}
      containerArt={
        container?.artworkSlots?.backdrop || container?.artworkSlots?.poster || undefined
      }
    />
  );
}

/**
 * The listed Media and container cards as a poster grid: a person's, newest
 * year first under "Appears in", or anything else's in the server's order.
 */
function PosterGrid({
  items,
  byYear = false,
  label,
}: {
  items: SourceItem[];
  byYear?: boolean;
  label?: string;
}) {
  const t = useT();
  const ordered = useMemo(() => (byYear ? filmographyOrder(items) : items), [items, byYear]);
  if (ordered.length === 0) return null;
  const heading = byYear ? t("personAppearsIn") : "";
  return (
    <section className="bf-section flex flex-col gap-3">
      {heading && (
        <h2 className="bf-section-title">
          <span>{heading}</span>
        </h2>
      )}
      <ul className="bf-grid" aria-label={heading || label}>
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
