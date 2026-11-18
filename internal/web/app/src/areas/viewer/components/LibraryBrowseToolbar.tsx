import { ArrowDown, ArrowUp, X } from "lucide-react";
import { type ReactNode, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { displayString } from "@/lib/display";
import { useDisplayLocale, useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";
import {
  browseYear,
  LIBRARY_MAX_YEAR,
  LIBRARY_MIN_RATINGS,
  LIBRARY_MIN_YEAR,
  LIBRARY_SORTS,
  LIBRARY_WATCH_STATES,
  LIBRARY_WATCHING_SORTS,
  type LibraryBrowseState,
  type LibraryItemsFacets,
  type LibrarySort,
} from "../lib/libraryBrowse";

const ALL = "all";
// Grouping values are free text from the library, so their option values are
// prefixed to stay distinct from the "Any" option.
const GROUP_VALUE = "value:";

const SORT_LABELS: Record<LibrarySort, StringKey> = {
  title: "libraryBrowseSortTitle",
  year: "libraryBrowseSortYear",
  added: "libraryBrowseSortAdded",
  duration: "libraryBrowseSortDuration",
  "last-watched": "libraryBrowseSortLastWatched",
  rating: "libraryBrowseSortRating",
  "release-date": "libraryBrowseSortReleaseDate",
  "audience-score": "libraryBrowseSortAudienceScore",
  plays: "libraryBrowseSortPlays",
};
const WATCH_LABELS: Record<string, StringKey> = {
  unwatched: "libraryBrowseWatchUnwatched",
  "in-progress": "libraryBrowseWatchInProgress",
  watched: "libraryBrowseWatchWatched",
};
const QUALITY_LABELS: Record<string, StringKey> = {
  uhd: "libraryBrowseQualityUhd",
  hd1080: "libraryBrowseQualityHd1080",
  hd720: "libraryBrowseQualityHd720",
  sd: "libraryBrowseQualitySd",
};

/** The browse state with every filter cleared, keeping the order. */
export const CLEARED_BROWSE_FILTERS = {
  watch: "",
  groups: {},
  decade: 0,
  yearFrom: 0,
  yearTo: 0,
  quality: "",
  minRating: 0,
} as const satisfies Partial<LibraryBrowseState>;

/** Whether a browse state narrows the result. */
export function hasBrowseFilters(state: LibraryBrowseState): boolean {
  return (
    !!state.watch ||
    Object.keys(state.groups).length > 0 ||
    state.decade > 0 ||
    state.yearFrom > 0 ||
    state.yearTo > 0 ||
    !!state.quality ||
    state.minRating > 0
  );
}

// The sort, direction and filter controls of a Library browse, and the chips
// that name and clear each active filter (CORE-1190). The Media view and each
// Entity kind's view share it (CORE-2440), so one URL state reads the same in
// both. `audioOnly` drops the controls that only fit watching (CORE-2327); a
// value already selected stays offered so a shared URL still reads back.
export function LibraryBrowseToolbar({
  state,
  facets,
  audioOnly,
  onUpdate,
  trailing,
}: {
  state: LibraryBrowseState;
  facets: LibraryItemsFacets;
  audioOnly: boolean | null;
  onUpdate: (patch: Partial<LibraryBrowseState>) => void;
  /** Placed at the end of the controls row, such as the display toggle. */
  trailing?: ReactNode;
}) {
  const t = useT();
  const displayLocale = useDisplayLocale();

  const decadeLabel = (decade: number) => t("libraryBrowseDecade", { decade: String(decade) });
  const qualityLabel = (quality: string) =>
    QUALITY_LABELS[quality] ? t(QUALITY_LABELS[quality]) : quality;
  const ratingLabel = (minRating: number) => {
    if (minRating <= 1) return t("libraryBrowseRatingAny");
    const key = minRating >= 10 ? "libraryBrowseRatingTop" : "libraryBrowseRatingAtLeast";
    return t(key, { stars: String(minRating / 2) });
  };
  // A grouping selected in the URL is offered even before (or without) the
  // server naming it, so the choice can always be seen and cleared.
  const groupings = [
    ...facets.groupings,
    ...Object.keys(state.groups)
      .filter((kind) => !facets.groupings.some((grouping) => grouping.kind === kind))
      .map((kind) => ({ kind, title: undefined, values: [] })),
  ].map((grouping) => ({
    kind: grouping.kind,
    label: displayString(grouping.title, displayLocale) || grouping.kind,
    values: withSelected(grouping.values, state.groups[grouping.kind]),
  }));
  const withGroup = (kind: string, value: string) => {
    const groups = { ...state.groups };
    if (value) groups[kind] = value;
    else delete groups[kind];
    return groups;
  };
  const decades = withSelected(facets.decades, state.decade || undefined);
  const qualities =
    audioOnly && !state.quality ? [] : withSelected(facets.qualities, state.quality);
  const sorts = LIBRARY_SORTS.filter(
    (sort) => !audioOnly || sort === state.sort || !LIBRARY_WATCHING_SORTS.includes(sort),
  );
  const showWatch = !audioOnly || !!state.watch;

  const activeFilters: { label: string; clear: Partial<LibraryBrowseState> }[] = [];
  if (state.watch) {
    activeFilters.push({ label: t(WATCH_LABELS[state.watch]), clear: { watch: "" } });
  }
  for (const grouping of groupings) {
    const selected = state.groups[grouping.kind];
    if (selected) {
      activeFilters.push({
        label: `${grouping.label}: ${selected}`,
        clear: { groups: withGroup(grouping.kind, "") },
      });
    }
  }
  if (state.decade) activeFilters.push({ label: decadeLabel(state.decade), clear: { decade: 0 } });
  const yearRange = yearRangeLabel(state.yearFrom, state.yearTo, t);
  if (yearRange) activeFilters.push({ label: yearRange, clear: { yearFrom: 0, yearTo: 0 } });
  if (state.quality) {
    activeFilters.push({ label: qualityLabel(state.quality), clear: { quality: "" } });
  }
  if (state.minRating) {
    activeFilters.push({ label: ratingLabel(state.minRating), clear: { minRating: 0 } });
  }

  return (
    <>
      <div className="flex flex-wrap items-end gap-3">
        <BrowseSelect
          id="library-browse-sort"
          label={t("libraryBrowseSortLabel")}
          value={state.sort}
          onChange={(value) => onUpdate({ sort: value as LibrarySort })}
          options={sorts.map((sort) => ({ value: sort, label: t(SORT_LABELS[sort]) }))}
        />
        <Button
          variant="outline"
          size="sm"
          aria-label={t("libraryBrowseDirectionLabel", {
            direction: t(
              state.direction === "asc" ? "libraryBrowseAscending" : "libraryBrowseDescending",
            ),
          })}
          onClick={() => onUpdate({ direction: state.direction === "asc" ? "desc" : "asc" })}
        >
          {state.direction === "asc" ? <ArrowUp aria-hidden /> : <ArrowDown aria-hidden />}
          {t(state.direction === "asc" ? "libraryBrowseAscending" : "libraryBrowseDescending")}
        </Button>
        {showWatch && (
          <BrowseSelect
            id="library-browse-watch"
            label={t("libraryBrowseWatchLabel")}
            value={state.watch || ALL}
            onChange={(value) =>
              onUpdate({ watch: value === ALL ? "" : (value as LibraryBrowseState["watch"]) })
            }
            options={[
              { value: ALL, label: t("libraryBrowseAny") },
              ...LIBRARY_WATCH_STATES.map((watch) => ({
                value: watch,
                label: t(WATCH_LABELS[watch]),
              })),
            ]}
          />
        )}
        <BrowseSelect
          id="library-browse-rating"
          label={t("libraryBrowseRatingLabel")}
          value={state.minRating ? String(state.minRating) : ALL}
          onChange={(value) => onUpdate({ minRating: value === ALL ? 0 : Number(value) })}
          options={[
            { value: ALL, label: t("libraryBrowseAny") },
            ...LIBRARY_MIN_RATINGS.map((minRating) => ({
              value: String(minRating),
              label: ratingLabel(minRating),
            })),
          ]}
        />
        {groupings.map((grouping) => (
          <BrowseSelect
            key={grouping.kind}
            id={`library-browse-group-${grouping.kind}`}
            label={grouping.label}
            value={state.groups[grouping.kind] ? GROUP_VALUE + state.groups[grouping.kind] : ALL}
            onChange={(value) =>
              onUpdate({
                groups: withGroup(
                  grouping.kind,
                  value === ALL ? "" : value.slice(GROUP_VALUE.length),
                ),
              })
            }
            options={[
              { value: ALL, label: t("libraryBrowseAny") },
              ...grouping.values.map((value) => ({ value: GROUP_VALUE + value, label: value })),
            ]}
          />
        ))}
        {decades.length > 0 && (
          <BrowseSelect
            id="library-browse-decade"
            label={t("libraryBrowseDecadeLabel")}
            value={state.decade ? String(state.decade) : ALL}
            onChange={(value) =>
              onUpdate({ decade: value === ALL ? 0 : Number(value), yearFrom: 0, yearTo: 0 })
            }
            options={[
              { value: ALL, label: t("libraryBrowseAny") },
              ...decades.map((decade) => ({ value: String(decade), label: decadeLabel(decade) })),
            ]}
          />
        )}
        <YearInput
          id="library-browse-year-from"
          label={t("libraryBrowseYearFromLabel")}
          value={state.yearFrom}
          onCommit={(year) => onUpdate(yearRangePatch(year, state.yearTo))}
        />
        <YearInput
          id="library-browse-year-to"
          label={t("libraryBrowseYearToLabel")}
          value={state.yearTo}
          onCommit={(year) => onUpdate(yearRangePatch(state.yearFrom, year))}
        />
        {qualities.length > 0 && (
          <BrowseSelect
            id="library-browse-quality"
            label={t("libraryBrowseQualityLabel")}
            value={state.quality || ALL}
            onChange={(value) =>
              onUpdate({ quality: value === ALL ? "" : (value as LibraryBrowseState["quality"]) })
            }
            options={[
              { value: ALL, label: t("libraryBrowseAny") },
              ...qualities.map((quality) => ({ value: quality, label: qualityLabel(quality) })),
            ]}
          />
        )}
        {trailing}
      </div>

      {activeFilters.length > 0 && (
        <ul aria-label={t("libraryBrowseActiveFilters")} className="bf-filter-chips list-none">
          {activeFilters.map((filter) => (
            <li key={filter.label}>
              <button
                type="button"
                className="bf-chip"
                aria-label={t("libraryBrowseRemoveFilter", { filter: filter.label })}
                onClick={() => onUpdate(filter.clear)}
              >
                {filter.label}
                <X aria-hidden className="size-3.5" />
              </button>
            </li>
          ))}
          {activeFilters.length > 1 && (
            <li>
              <button
                type="button"
                className="bf-chip"
                onClick={() => onUpdate(CLEARED_BROWSE_FILTERS)}
              >
                {t("libraryBrowseClearFilters")}
              </button>
            </li>
          )}
        </ul>
      )}
    </>
  );
}

function BrowseSelect({
  id,
  label,
  value,
  onChange,
  options,
}: {
  id: string;
  label: string;
  value: string;
  onChange: (value: string) => void;
  options: { value: string; label: string }[];
}) {
  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={id} className="bf-field-label">
        {label}
      </label>
      <Select value={value} onValueChange={onChange}>
        <SelectTrigger id={id} size="sm" className="min-w-32">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {options.map((option) => (
            <SelectItem key={option.value} value={option.value}>
              {option.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  );
}

// A year range edit: in order, and clearing the decade it stands in for.
function yearRangePatch(from: number, to: number): Partial<LibraryBrowseState> {
  const [yearFrom, yearTo] = from && to && from > to ? [to, from] : [from, to];
  return yearFrom || yearTo ? { yearFrom, yearTo, decade: 0 } : { yearFrom, yearTo };
}

function yearRangeLabel(from: number, to: number, t: ReturnType<typeof useT>): string {
  if (from && to) return t("libraryBrowseYearRange", { from: String(from), to: String(to) });
  if (from) return t("libraryBrowseYearFrom", { year: String(from) });
  if (to) return t("libraryBrowseYearTo", { year: String(to) });
  return "";
}

// One side of the year range. The draft is free text while typing and is
// committed on blur or Enter: a year in range sets the side, an empty field
// clears it, and anything else reverts to the committed value.
function YearInput({
  id,
  label,
  value,
  onCommit,
}: {
  id: string;
  label: string;
  value: number;
  onCommit: (year: number) => void;
}) {
  const committed = value ? String(value) : "";
  const [draft, setDraft] = useState<{ base: string; text: string }>({
    base: committed,
    text: committed,
  });
  // A new committed value (a chip, a link, the other side swapping) resets the draft.
  const text = draft.base === committed ? draft.text : committed;
  const commit = () => {
    const trimmed = text.trim();
    const year = browseYear(trimmed);
    if ((year || !trimmed) && year !== value) onCommit(year);
    else setDraft({ base: committed, text: committed });
  };
  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={id} className="bf-field-label">
        {label}
      </label>
      <Input
        id={id}
        type="number"
        inputMode="numeric"
        min={LIBRARY_MIN_YEAR}
        max={LIBRARY_MAX_YEAR}
        step={1}
        value={text}
        onChange={(event) => setDraft({ base: committed, text: event.target.value })}
        onBlur={commit}
        onKeyDown={(event) => {
          if (event.key === "Enter") commit();
        }}
        className="h-8 w-24"
      />
    </div>
  );
}

function withSelected<T>(values: T[], selected: T | "" | undefined): T[] {
  if (selected === "" || selected === undefined || values.includes(selected)) return values;
  return [selected, ...values];
}
