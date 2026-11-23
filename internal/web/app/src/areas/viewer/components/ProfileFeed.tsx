import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { fetchSourceItems } from "@/lib/graphql";
import type { SourceItem } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { useViewerIdentity } from "@/lib/session";
import { useEngagementEpoch } from "../actions/engagementSync";
import { playQueueOf } from "../lib/watchQueue";
import { MediaItemCard } from "./MediaItemCard";

export const PROFILE_FEED_LIMIT = 100;

/** Server-side narrowing of a played feed (History search, CORE-3333). */
export interface FeedFilters {
  /** Matches title, artist or album; empty matches all. */
  query: string;
  /** One Library; empty means every visible Library. */
  libraryID: string;
  /** RFC 3339 lower bound on the last play; empty means any time. */
  playedAfter: string;
}

const NO_FILTERS: FeedFilters = { query: "", libraryID: "", playedAfter: "" };

function feedPage(
  kind: string,
  filters: FeedFilters,
  offset: number,
  signal: AbortSignal,
): Promise<SourceItem[]> {
  return fetchSourceItems(
    {
      kind,
      entityKind: "",
      libraryID: filters.libraryID,
      ...(filters.query ? { query: filters.query } : {}),
      ...(filters.playedAfter ? { playedAfter: filters.playedAfter } : {}),
      entityID: "",
      fieldFilters: null,
      entityFilter: null,
      limit: PROFILE_FEED_LIMIT,
      offset,
    },
    signal,
  );
}

/** A bound on the server pages one read walks past pages shortened by muting. */
const MAX_FEED_READS = 10;
/**
 * How many empty pages in a row end a feed. A whole page can be muted, so one
 * empty page does not prove the end; a run of this many does. A longer run of
 * muted rows still hides what follows until the server applies Mute before
 * paging (CORE-1859).
 */
const FEED_END_EMPTY_PAGES = 3;

// One read of a feed from `offset`. The server drops muted rows from a page
// after paging, so a short or even empty page is not the end: the read steps
// on by the requested size until a page comes back full (more may follow) or
// FEED_END_EMPTY_PAGES pages in a row come back empty (the end). It returns the
// rows and the offset of the next page, or null at the end. A read that hits
// MAX_FEED_READS first leaves Load more to step on.
async function readFeed(
  kind: string,
  filters: FeedFilters,
  offset: number,
  signal: AbortSignal,
): Promise<{ items: SourceItem[]; next: number | null }> {
  const items: SourceItem[] = [];
  let at = offset;
  let emptyRun = 0;
  for (let reads = 0; reads < MAX_FEED_READS; reads++) {
    const page = await feedPage(kind, filters, at, signal);
    items.push(...page);
    at += PROFILE_FEED_LIMIT;
    if (page.length === PROFILE_FEED_LIMIT) return { items, next: at };
    emptyRun = page.length === 0 ? emptyRun + 1 : 0;
    if (emptyRun === FEED_END_EMPTY_PAGES) return { items, next: null };
  }
  return { items, next: at };
}

// One of the acting Profile's own feeds (favorites, recently played, most
// played; CORE-1635) as a poster grid in server order. It reloads when the
// identity or the Profile's engagement changes. The feed reads one page at a
// time, and Load more reads the next one until the feed ends (CORE-1878).
export function ProfileFeed({
  kind,
  heading,
  empty,
  filters = NO_FILTERS,
}: {
  /** The sourceItems kind ("favorites", "recently-played", "most-played"). */
  kind: string;
  /** A section heading; omit when the page title already names the feed. */
  heading?: string;
  empty: string;
  /** Narrows the feed on the server, before paging. */
  filters?: FeedFilters;
}) {
  const t = useT();
  const identity = useViewerIdentity();
  const epoch = useEngagementEpoch();
  const [items, setItems] = useState<SourceItem[] | null>(null);
  // The offset of the next page, or null when the feed has ended.
  const [nextOffset, setNextOffset] = useState<number | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  const controllerRef = useRef<AbortController | null>(null);
  // Key the read on the filter values, not the object, so a re-render with
  // equal filters does not re-read.
  const { query, libraryID, playedAfter } = filters;
  const active = useMemo(
    () => ({ query, libraryID, playedAfter }),
    [query, libraryID, playedAfter],
  );

  // The identity and engagement epoch only re-run the read; they are not inputs to it.
  useEffect(() => {
    const controller = new AbortController();
    controllerRef.current = controller;
    setItems(null);
    setNextOffset(null);
    setLoadingMore(false);
    readFeed(kind, active, 0, controller.signal)
      .then(({ items: first, next }) => {
        setItems(first);
        setNextOffset(next);
      })
      .catch(() => {
        if (!controller.signal.aborted) setItems([]);
      });
    return () => controller.abort();
  }, [kind, active, identity, epoch]);

  const loadMore = useCallback(() => {
    const controller = controllerRef.current;
    if (!controller || nextOffset === null) return;
    setLoadingMore(true);
    readFeed(kind, active, nextOffset, controller.signal)
      .then(({ items: more, next }) => {
        if (controller.signal.aborted) return;
        setItems((prev) => [...(prev ?? []), ...more]);
        setNextOffset(next);
      })
      .catch(() => {})
      .finally(() => {
        if (!controller.signal.aborted) setLoadingMore(false);
      });
  }, [kind, active, nextOffset]);

  const queue = useMemo(() => playQueueOf(items ?? []), [items]);

  return (
    <section className="bf-section flex flex-col gap-3">
      {heading && (
        <h2 className="bf-section-title">
          <span>{heading}</span>
        </h2>
      )}
      {items === null && <Skeleton aria-hidden className="bf-shimmer h-40 w-full" />}
      {items?.length === 0 && <p className="text-sm text-muted-foreground">{empty}</p>}
      {items && items.length > 0 && (
        <ul className="bf-grid">
          {items.map((item, index) => (
            <MediaItemCard
              key={`${item.id}-${index}`}
              item={item}
              posInSet={index + 1}
              setSize={items.length}
              queue={queue}
            />
          ))}
        </ul>
      )}
      {items && nextOffset !== null && (
        <Button variant="outline" className="self-center" disabled={loadingMore} onClick={loadMore}>
          {t("loadMore")}
        </Button>
      )}
    </section>
  );
}
