import { Play } from "lucide-react";
import { useMemo } from "react";
import { useNavigate } from "react-router";
import { Badge } from "@/components/ui/badge";
import type { SubscriptionEntry } from "@/lib/graphql";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { useViewerIdentity } from "@/lib/session";
import { entryAvailabilityKey, formatBytes, inFeedOrder, parseWireTime } from "../../lib/feeds";
import { mediaPath } from "../../lib/mediaAddress";
import { playState } from "../../lib/trackList";
import type { PlayQueueItem } from "../../lib/watchQueue";
import { preloadPlayer } from "../../player/preload";

// A followed feed's catalogue (CORE-2957): every entry in feed order with its
// date, size and whether it is downloaded. Play goes through the ordinary
// watch path for the entry's Media id, downloaded or not (a not-downloaded
// entry streams); the queue is the catalogue.
export function FeedCatalogue({ entries }: { entries: readonly SubscriptionEntry[] }) {
  const t = useT();
  const locale = useDisplayLocale();
  const identity = useViewerIdentity();
  const navigate = useNavigate();
  const ordered = useMemo(() => inFeedOrder(entries), [entries]);
  const queue = useMemo<PlayQueueItem[]>(
    () => ordered.map((entry) => ({ id: entry.mediaID, title: entry.title || t("feedsUntitled") })),
    [ordered, t],
  );
  const dates = useMemo(() => new Intl.DateTimeFormat(locale, { dateStyle: "medium" }), [locale]);

  function play(item: PlayQueueItem) {
    navigate(mediaPath("watch", item.id), { state: playState(identity, queue, item) });
  }

  return (
    <ol className="m-0 flex list-none flex-col p-0" aria-label={t("feedsListHeading")}>
      {ordered.map((entry, index) => {
        const item = queue[index];
        const published = parseWireTime(entry.publishedAt);
        const size = formatBytes(entry.enclosureLength, locale);
        const availability = entryAvailabilityKey(entry);
        return (
          <li
            key={entry.mediaID}
            data-entry={entry.mediaID}
            className="flex items-center gap-3 border-b border-[var(--border-subtle)] py-2"
          >
            <button
              type="button"
              className="bf-row-play"
              aria-label={t("feedsPlayEntry", { title: item.title })}
              onClick={() => play(item)}
              onPointerEnter={preloadPlayer}
              onFocus={preloadPlayer}
            >
              <Play aria-hidden />
            </button>
            <div className="flex min-w-0 flex-1 flex-col">
              <span className="break-words font-medium">{item.title}</span>
              <span className="text-sm text-[var(--text-muted)]">
                {[published && dates.format(published), size].filter(Boolean).join(" · ")}
              </span>
            </div>
            <Badge
              variant={availability === "feedsDownloaded" ? "secondary" : "outline"}
              data-availability={entry.availability}
            >
              {t(availability)}
            </Badge>
          </li>
        );
      })}
    </ol>
  );
}
