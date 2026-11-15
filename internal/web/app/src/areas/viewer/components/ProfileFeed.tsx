import { useEffect, useMemo, useState } from "react";
import { Skeleton } from "@/components/ui/skeleton";
import { fetchSourceItems } from "@/lib/graphql";
import type { SourceItem } from "@/lib/graphql/types";
import { useViewerIdentity } from "@/lib/session";
import { useEngagementEpoch } from "../actions/engagementSync";
import { playQueueOf } from "../lib/watchQueue";
import { MediaItemCard } from "./MediaItemCard";

export const PROFILE_FEED_LIMIT = 100;

// One of the acting Profile's own feeds (favorites, recently played, most
// played; CORE-1635) as a poster grid in server order. It reloads when the
// identity or the Profile's engagement changes.
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
  const identity = useViewerIdentity();
  const epoch = useEngagementEpoch();
  const [items, setItems] = useState<SourceItem[] | null>(null);

  // The identity and engagement epoch only re-run the read; they are not inputs to it.
  useEffect(() => {
    const controller = new AbortController();
    fetchSourceItems(
      {
        kind,
        entityKind: "",
        libraryID: "",
        entityID: "",
        fieldFilters: null,
        entityFilter: null,
        limit: PROFILE_FEED_LIMIT,
        offset: 0,
      },
      controller.signal,
    )
      .then(setItems)
      .catch(() => {
        if (!controller.signal.aborted) setItems([]);
      });
    return () => controller.abort();
  }, [kind, identity, epoch]);

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
    </section>
  );
}
