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

function feedPage(kind: string, offset: number, signal: AbortSignal): Promise<SourceItem[]> {
  return fetchSourceItems(
    {
      kind,
      entityKind: "",
      libraryID: "",
      entityID: "",
      fieldFilters: null,
      entityFilter: null,
      limit: PROFILE_FEED_LIMIT,
      offset,
    },
    signal,
  );
}

// One of the acting Profile's own feeds (favorites, recently played, most
// played; CORE-1635) as a poster grid in server order. It reloads when the
// identity or the Profile's engagement changes. The feed reads one page at a
// time, and a full page offers Load more for the next one (CORE-1878).
export function ProfileFeed({
  kind,
  heading,
  empty,
}: {
  /** The sourceItems kind ("favorites", "recently-played", "most-played"). */
  kind: string;
  /** A section heading; omit when the page title already names the feed. */
  heading?: string;
  empty: string;
}) {
  const t = useT();
  const identity = useViewerIdentity();
  const epoch = useEngagementEpoch();
  const [items, setItems] = useState<SourceItem[] | null>(null);
  // The offset of the next page, or null when the last page read was short.
  const [nextOffset, setNextOffset] = useState<number | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  const controllerRef = useRef<AbortController | null>(null);

  // The identity and engagement epoch only re-run the read; they are not inputs to it.
  useEffect(() => {
    const controller = new AbortController();
    controllerRef.current = controller;
    setItems(null);
    setNextOffset(null);
    setLoadingMore(false);
    feedPage(kind, 0, controller.signal)
      .then((page) => {
        setItems(page);
        setNextOffset(page.length === PROFILE_FEED_LIMIT ? PROFILE_FEED_LIMIT : null);
      })
      .catch(() => {
        if (!controller.signal.aborted) setItems([]);
      });
    return () => controller.abort();
  }, [kind, identity, epoch]);

  const loadMore = useCallback(() => {
    const controller = controllerRef.current;
    if (!controller || nextOffset === null) return;
    setLoadingMore(true);
    // Paging steps by the requested size, not the rows returned: the server can
    // drop muted rows from a page after paging.
    feedPage(kind, nextOffset, controller.signal)
      .then((page) => {
        if (controller.signal.aborted) return;
        setItems((prev) => [...(prev ?? []), ...page]);
        setNextOffset(page.length === PROFILE_FEED_LIMIT ? nextOffset + PROFILE_FEED_LIMIT : null);
      })
      .catch(() => {})
      .finally(() => {
        if (!controller.signal.aborted) setLoadingMore(false);
      });
  }, [kind, nextOffset]);

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
