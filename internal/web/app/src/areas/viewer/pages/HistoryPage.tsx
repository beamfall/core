import { useMemo, useState } from "react";
import { AppShell } from "@/components/chrome/AppShell";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import type { StringKey } from "@/strings";
import { BrowseSearch } from "../components/LibraryBrowseToolbar";
import { type FeedFilters, ProfileFeed } from "../components/ProfileFeed";
import { ViewerActions } from "../components/ViewerActions";

/** How far back the History page looks at last plays. */
export type HistoryRange = "any" | "today" | "week" | "month" | "year";

const RANGES: { value: HistoryRange; label: StringKey }[] = [
  { value: "any", label: "historyRangeAny" },
  { value: "today", label: "historyRangeToday" },
  { value: "week", label: "historyRangeWeek" },
  { value: "month", label: "historyRangeMonth" },
  { value: "year", label: "historyRangeYear" },
];

const DAY_MS = 24 * 60 * 60 * 1000;

/**
 * The RFC 3339 lower bound for a range, from `now`: today starts at local
 * midnight, the others count whole days back. "any" is unbounded ("").
 */
export function playedAfterFor(range: HistoryRange, now: Date): string {
  switch (range) {
    case "any":
      return "";
    case "today":
      return new Date(now.getFullYear(), now.getMonth(), now.getDate()).toISOString();
    case "week":
      return new Date(now.getTime() - 7 * DAY_MS).toISOString();
    case "month":
      return new Date(now.getTime() - 30 * DAY_MS).toISOString();
    case "year":
      return new Date(now.getTime() - 365 * DAY_MS).toISOString();
  }
}

// The acting Profile's play history: recently played, then most played
// (CORE-1635), searchable by title, artist or album and narrowed by Library and
// last-played range on the server, before paging (CORE-3333).
export function HistoryPage() {
  const t = useT();
  const { manifest } = useSession();
  const libraries = manifest?.libraries ?? [];
  const [query, setQuery] = useState("");
  const [libraryID, setLibraryID] = useState("");
  const [range, setRange] = useState<HistoryRange>("any");
  // The bound is fixed when a range, Library or search is chosen, so the
  // pages of one read stay consistent.
  const [playedAfter, setPlayedAfter] = useState("");
  const filters = useMemo<FeedFilters>(
    () => ({ query, libraryID, playedAfter }),
    [query, libraryID, playedAfter],
  );
  const filtered = query !== "" || libraryID !== "" || playedAfter !== "";
  const empty = filtered ? t("historyNoMatch") : t("historyEmpty");

  return (
    <AppShell actions={<ViewerActions />}>
      <div className="flex flex-col gap-8">
        <h1 className="bf-page-title">{t("historyTitle")}</h1>
        <div className="flex flex-col gap-3">
          <BrowseSearch
            value={query}
            label={t("historySearch")}
            onCommit={(next) => {
              setQuery(next);
              // A fresh search re-anchors the range, so "Today" follows midnight.
              setPlayedAfter(playedAfterFor(range, new Date()));
            }}
          />
          {libraries.length > 1 && (
            <div className="bf-filter-chips" role="group" aria-label={t("historyFilterLibrary")}>
              {[{ id: "", title: t("historyFilterAll") }, ...libraries].map((library) => (
                <button
                  key={library.id || "all"}
                  type="button"
                  aria-pressed={libraryID === library.id}
                  onClick={() => {
                    setLibraryID(library.id);
                    setPlayedAfter(playedAfterFor(range, new Date()));
                  }}
                  className="bf-chip"
                >
                  {library.title}
                </button>
              ))}
            </div>
          )}
          <div className="bf-filter-chips" role="group" aria-label={t("historyRange")}>
            {RANGES.map((option) => (
              <button
                key={option.value}
                type="button"
                aria-pressed={range === option.value}
                onClick={() => {
                  setRange(option.value);
                  setPlayedAfter(playedAfterFor(option.value, new Date()));
                }}
                className="bf-chip"
              >
                {t(option.label)}
              </button>
            ))}
          </div>
        </div>
        <ProfileFeed
          kind="recently-played"
          heading={t("historyRecent")}
          empty={empty}
          filters={filters}
        />
        <ProfileFeed
          kind="most-played"
          heading={t("historyMost")}
          empty={empty}
          filters={filters}
        />
      </div>
    </AppShell>
  );
}
