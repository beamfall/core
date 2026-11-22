import { Check, Play, RotateCcw } from "lucide-react";
import { useEffect, useId, useMemo, useState } from "react";
import { useNavigate } from "react-router";
import { Badge } from "@/components/ui/badge";
import { type SubscriptionEntry, setWatched } from "@/lib/graphql";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { useViewerIdentity } from "@/lib/session";
import type { StringKey } from "@/strings";
import {
  CATALOGUE_FILTERS,
  CATALOGUE_SORTS,
  type CatalogueFilter,
  type CatalogueSort,
  catalogueView,
  entryAvailabilityKey,
  formatBytes,
  parseWireTime,
} from "../../lib/feeds";
import { mediaPath } from "../../lib/mediaAddress";
import { playState } from "../../lib/trackList";
import type { PlayQueueItem } from "../../lib/watchQueue";
import { preloadPlayer } from "../../player/preload";

const SELECT_CLASS =
  "h-8 rounded-[var(--radius-control)] border border-input bg-transparent px-2 text-sm";

const FILTER_KEYS: Record<CatalogueFilter, StringKey> = {
  all: "feedsFilterAll",
  unplayed: "feedsFilterUnplayed",
  downloaded: "feedsFilterDownloaded",
};
const SORT_KEYS: Record<CatalogueSort, StringKey> = {
  newest: "feedsSortNewest",
  oldest: "feedsSortOldest",
};

type Notice = { kind: "status" | "alert"; text: string } | null;

// A followed feed's catalogue (CORE-2957, CORE-2959): every entry with its
// date, size, availability and whether this Profile played it, filtered and
// sorted. Play goes through the ordinary watch path for the entry's Media id,
// downloaded or not (a not-downloaded entry streams), with the visible list as
// the queue; Mark played toggles the Profile's played state.
export function FeedCatalogue({ entries }: { entries: readonly SubscriptionEntry[] }) {
  const t = useT();
  const locale = useDisplayLocale();
  const identity = useViewerIdentity();
  const navigate = useNavigate();
  const filterID = useId();
  const sortID = useId();
  const [items, setItems] = useState<readonly SubscriptionEntry[]>(entries);
  const [filter, setFilter] = useState<CatalogueFilter>("all");
  const [sort, setSort] = useState<CatalogueSort>("newest");
  const [pending, setPending] = useState<ReadonlySet<string>>(() => new Set());
  const [notice, setNotice] = useState<Notice>(null);
  useEffect(() => setItems(entries), [entries]);

  const shown = useMemo(() => catalogueView(items, filter, sort), [items, filter, sort]);
  const queue = useMemo<PlayQueueItem[]>(
    () => shown.map((entry) => ({ id: entry.mediaID, title: entry.title || t("feedsUntitled") })),
    [shown, t],
  );
  const dates = useMemo(() => new Intl.DateTimeFormat(locale, { dateStyle: "medium" }), [locale]);

  function play(item: PlayQueueItem) {
    navigate(mediaPath("watch", item.id), { state: playState(identity, queue, item) });
  }

  // Each row tracks its own request, so acting on another row never
  // re-enables one still in flight.
  function settle(id: string, on: boolean) {
    setPending((prev) => {
      const next = new Set(prev);
      if (on) next.add(id);
      else next.delete(id);
      return next;
    });
  }

  async function togglePlayed(entry: SubscriptionEntry) {
    if (pending.has(entry.mediaID)) return;
    const played = !entry.played;
    settle(entry.mediaID, true);
    try {
      await setWatched({ mediaID: entry.mediaID }, played);
      setItems((prev) =>
        prev.map((item) => (item.mediaID === entry.mediaID ? { ...item, played } : item)),
      );
      setNotice(null);
    } catch {
      setNotice({ kind: "alert", text: t("feedsActionFailed") });
    } finally {
      settle(entry.mediaID, false);
    }
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-3">
        <label htmlFor={filterID} className="flex items-center gap-2 text-sm">
          {t("feedsFilterLabel")}
          <select
            id={filterID}
            className={SELECT_CLASS}
            value={filter}
            onChange={(event) => setFilter(event.target.value as CatalogueFilter)}
          >
            {CATALOGUE_FILTERS.map((value) => (
              <option key={value} value={value}>
                {t(FILTER_KEYS[value])}
              </option>
            ))}
          </select>
        </label>
        <label htmlFor={sortID} className="flex items-center gap-2 text-sm">
          {t("feedsSortLabel")}
          <select
            id={sortID}
            className={SELECT_CLASS}
            value={sort}
            onChange={(event) => setSort(event.target.value as CatalogueSort)}
          >
            {CATALOGUE_SORTS.map((value) => (
              <option key={value} value={value}>
                {t(SORT_KEYS[value])}
              </option>
            ))}
          </select>
        </label>
      </div>
      {notice && (
        <p
          role={notice.kind}
          className="m-0 text-sm text-[var(--text-secondary)]"
          data-notice={notice.kind}
        >
          {notice.text}
        </p>
      )}
      {shown.length === 0 ? (
        <p className="bf-empty m-0 text-sm text-[var(--text-muted)]">{t("feedsFilterEmpty")}</p>
      ) : (
        <ol className="m-0 flex list-none flex-col p-0" aria-label={t("feedsListHeading")}>
          {shown.map((entry, index) => {
            const item = queue[index];
            const published = parseWireTime(entry.publishedAt);
            const size = formatBytes(entry.enclosureLength, locale);
            const availability = entryAvailabilityKey(entry);
            const busy = pending.has(entry.mediaID);
            return (
              <li
                key={entry.mediaID}
                data-entry={entry.mediaID}
                data-played={entry.played ? "true" : undefined}
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
                <div className={`flex min-w-0 flex-1 flex-col ${entry.played ? "opacity-60" : ""}`}>
                  <span className="break-words font-medium">{item.title}</span>
                  <span className="text-sm text-[var(--text-muted)]">
                    {[published && dates.format(published), size, entry.played && t("feedsPlayed")]
                      .filter(Boolean)
                      .join(" · ")}
                  </span>
                </div>
                <Badge
                  variant={availability === "feedsDownloaded" ? "secondary" : "outline"}
                  data-availability={entry.availability}
                >
                  {t(availability)}
                </Badge>
                <button
                  type="button"
                  className="bf-row-play"
                  aria-label={t(entry.played ? "feedsMarkUnplayed" : "feedsMarkPlayed", {
                    title: item.title,
                  })}
                  aria-pressed={entry.played}
                  disabled={busy}
                  onClick={() => togglePlayed(entry)}
                >
                  {entry.played ? <RotateCcw aria-hidden /> : <Check aria-hidden />}
                </button>
              </li>
            );
          })}
        </ol>
      )}
    </div>
  );
}
