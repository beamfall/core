import { ArrowDown, ArrowUp, ArrowUpDown, Check, Search, SlidersHorizontal, X } from "lucide-react";
import { type ReactNode, useEffect, useId, useRef, useState } from "react";
import { Menu, Popover } from "@/components/ui/astryx-primitives";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
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
  browseQuery,
  browseYear,
  LIBRARY_MAX_QUERY,
  LIBRARY_MAX_YEAR,
  LIBRARY_MIN_RATINGS,
  LIBRARY_MIN_YEAR,
  LIBRARY_SORTS,
  LIBRARY_WATCH_STATES,
  LIBRARY_WATCHING_SORTS,
  type LibraryBrowseState,
  type LibraryDirection,
  type LibraryItemsFacets,
  type LibrarySort,
} from "../lib/libraryBrowse";

const ALL = "all";
// Grouping values are free text from the library, so their option values are
// prefixed to stay distinct from the "Any" option.
const GROUP_VALUE = "value:";
// How long typing pauses before the search reaches the URL and the server.
export const BROWSE_SEARCH_DEBOUNCE_MS = 300;
// Below this width the Filters panel is a bottom sheet rather than a popover.
const SHEET_QUERY = "(max-width: 640px)";

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
const DIRECTION_LABELS: Record<LibraryDirection, StringKey> = {
  asc: "libraryBrowseAscending",
  desc: "libraryBrowseDescending",
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

/** The browse state with the search and every filter cleared, keeping the order. */
export const CLEARED_BROWSE_FILTERS = {
  query: "",
  watch: "",
  groups: {},
  decade: 0,
  yearFrom: 0,
  yearTo: 0,
  quality: "",
  minRating: 0,
} as const satisfies Partial<LibraryBrowseState>;

/** Whether a browse state narrows the result by a filter, not counting the search. */
function hasFilters(state: LibraryBrowseState): boolean {
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

/** Whether a browse state narrows the result, by the search or a filter. */
export function hasBrowseFilters(state: LibraryBrowseState): boolean {
  return !!browseQuery(state.query) || hasFilters(state);
}

// The browse toolbar of a Library (CORE-1190, one row since CORE-2568): the
// title search, the view switcher, a Filters button that opens the filters in
// a popover (a bottom sheet on a phone), one Sort menu for the field and the
// direction, and the display controls. Under it, a chip names and clears each
// active filter. The Media view and each Entity kind's view share it
// (CORE-2440), so one URL state reads the same in both. `audioOnly` drops the
// controls that only fit watching (CORE-2327); a value already selected stays
// offered so a shared URL still reads back.
export function LibraryBrowseToolbar({
  state,
  facets,
  audioOnly,
  onUpdate,
  libraryName,
  leading,
  trailing,
}: {
  state: LibraryBrowseState;
  facets: LibraryItemsFacets;
  audioOnly: boolean | null;
  onUpdate: (patch: Partial<LibraryBrowseState>) => void;
  /** The Library's name, for the search box's label. */
  libraryName: string;
  /** Placed after the search, such as the view switcher. */
  leading?: ReactNode;
  /** Placed at the end of the row, such as the poster size and display toggle. */
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
  const query = browseQuery(state.query);
  // The panel's Clear all leaves the search, which has its own clear button;
  // the chip row's clears both.
  const clearFilters = () => onUpdate({ ...CLEARED_BROWSE_FILTERS, query: state.query });

  const fields = (
    <>
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
      <div className="bf-filter-year">
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
      </div>
    </>
  );

  return (
    <>
      <div className="bf-browse-toolbar">
        <BrowseSearch
          value={query}
          label={t("libraryBrowseSearchLabel", { library: libraryName })}
          onCommit={(next) => onUpdate({ query: next })}
        />
        {leading}
        <div className="bf-browse-toolbar-end">
          <BrowseFilters count={activeFilters.length} onClear={clearFilters}>
            {fields}
          </BrowseFilters>
          <SortMenu
            sorts={sorts}
            sort={state.sort}
            direction={state.direction}
            onUpdate={onUpdate}
          />
          {trailing}
        </div>
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
          {activeFilters.length + (query ? 1 : 0) > 1 && (
            <li>
              <button
                type="button"
                className="bf-chip bf-chip-quiet"
                onClick={() => onUpdate(CLEARED_BROWSE_FILTERS)}
              >
                {t("libraryBrowseClearAll")}
              </button>
            </li>
          )}
        </ul>
      )}
    </>
  );
}

/**
 * What an empty browse result says: that the search or the filters matched
 * nothing, with the controls that widen it, or that the Library is empty.
 */
export function LibraryBrowseEmpty({
  state,
  onUpdate,
}: {
  state: LibraryBrowseState;
  onUpdate: (patch: Partial<LibraryBrowseState>) => void;
}) {
  const t = useT();
  const query = browseQuery(state.query);
  const filtered = hasFilters(state);
  let message = t("libraryEmptyLead");
  if (query) message = t("libraryBrowseNoSearchMatches", { query });
  else if (filtered) message = t("libraryBrowseNoMatches");
  return (
    <div role="status" className="bf-empty flex flex-col gap-3">
      <p className="text-sm text-[var(--text-secondary)]">{message}</p>
      {(query || filtered) && (
        <div className="flex flex-wrap justify-center gap-2">
          {query && (
            <Button variant="outline" onClick={() => onUpdate({ query: "" })}>
              {t("libraryBrowseSearchClear")}
            </Button>
          )}
          {filtered && (
            <Button variant="outline" onClick={() => onUpdate(CLEARED_BROWSE_FILTERS)}>
              {t(query ? "libraryBrowseClearAll" : "libraryBrowseClearFilters")}
            </Button>
          )}
        </div>
      )}
    </div>
  );
}

// The title search. The draft is the field's own while typing and reaches the
// URL once typing pauses (or on Enter), trimmed; a new value from elsewhere
// (a chip, Clear all, Back) replaces the draft.
function BrowseSearch({
  value,
  label,
  onCommit,
}: {
  value: string;
  label: string;
  onCommit: (query: string) => void;
}) {
  const t = useT();
  const id = useId();
  const inputRef = useRef<HTMLInputElement>(null);
  const [text, setText] = useState(value);
  const [shown, setShown] = useState(value);
  if (shown !== value) {
    setShown(value);
    // The draft's own commit comes back trimmed; keep what is being typed.
    if (browseQuery(text) !== value) setText(value);
  }
  // The latest commit, so a pending timer never writes an older browse state.
  const commitRef = useRef(onCommit);
  useEffect(() => {
    commitRef.current = onCommit;
  });
  useEffect(() => {
    if (browseQuery(text) === value) return;
    const timer = setTimeout(() => commitRef.current(browseQuery(text)), BROWSE_SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [text, value]);

  return (
    <div className="bf-browse-search">
      <label htmlFor={id} className="sr-only">
        {label}
      </label>
      <Search aria-hidden className="bf-browse-search-icon" />
      <Input
        ref={inputRef}
        id={id}
        type="search"
        enterKeyHint="search"
        autoComplete="off"
        spellCheck={false}
        maxLength={LIBRARY_MAX_QUERY}
        placeholder={label}
        value={text}
        onChange={(event) => setText(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Enter") commitRef.current(browseQuery(text));
        }}
        className="h-8 pr-8 pl-8"
      />
      {text && (
        <button
          type="button"
          className="bf-browse-search-clear"
          aria-label={t("libraryBrowseSearchClear")}
          onClick={() => {
            setText("");
            commitRef.current("");
            inputRef.current?.focus();
          }}
        >
          <X aria-hidden className="size-4" />
        </button>
      )}
    </div>
  );
}

function useSheetLayout(): boolean {
  const [sheet, setSheet] = useState(
    () => typeof window.matchMedia === "function" && window.matchMedia(SHEET_QUERY).matches,
  );
  useEffect(() => {
    if (typeof window.matchMedia !== "function") return;
    const query = window.matchMedia(SHEET_QUERY);
    const onChange = () => setSheet(query.matches);
    query.addEventListener("change", onChange);
    return () => query.removeEventListener("change", onChange);
  }, []);
  return sheet;
}

// The Filters button and its panel: a popover under the button on a wide
// screen, a modal bottom sheet on a phone (CORE-1787). Both come from the
// Base UI primitives, so aria-expanded, Escape, outside clicks and focus
// return are theirs.
function BrowseFilters({
  count,
  onClear,
  children,
}: {
  count: number;
  onClear: () => void;
  children: ReactNode;
}) {
  const t = useT();
  const sheet = useSheetLayout();
  const triggerProps = {
    className: "bf-chip",
    "aria-label": t("libraryBrowseFiltersButton", { count }),
    children: (
      <>
        <SlidersHorizontal aria-hidden className="size-4" />
        <span className="bf-chip-label">{t("libraryBrowseFilters")}</span>
        {count > 0 && (
          <span aria-hidden className="bf-chip-count">
            {count}
          </span>
        )}
      </>
    ),
  };
  const clear = (
    <Button variant="ghost" size="sm" disabled={count === 0} onClick={onClear}>
      {t("libraryBrowseClearAll")}
    </Button>
  );

  if (sheet) {
    return (
      <Dialog>
        <DialogTrigger {...triggerProps} />
        <DialogContent
          className="bf-filter-sheet top-auto bottom-0 left-0 max-h-[85dvh] max-w-none translate-x-0 translate-y-0 overflow-y-auto rounded-t-[var(--radius-card)] rounded-b-none border-x-0 border-b-0 p-4 pb-[max(16px,env(safe-area-inset-bottom))] sm:max-w-none"
          data-register="viewer"
        >
          <DialogHeader>
            <DialogTitle>{t("libraryBrowseFilters")}</DialogTitle>
          </DialogHeader>
          <div className="bf-filter-fields">{children}</div>
          <DialogFooter className="flex-row justify-between">
            {clear}
            <DialogClose render={<Button size="sm" />}>{t("libraryBrowseFiltersDone")}</DialogClose>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    );
  }
  return (
    <Popover.Root>
      <Popover.Trigger {...triggerProps} />
      <Popover.Portal>
        <Popover.Positioner side="bottom" align="end" sideOffset={6} className="bf-card-menu-layer">
          <Popover.Popup className="bf-filter-popover" data-register="viewer">
            <Popover.Title className="bf-filter-popover-title">
              {t("libraryBrowseFilters")}
            </Popover.Title>
            <div className="bf-filter-fields">{children}</div>
            <div className="bf-filter-popover-footer">{clear}</div>
          </Popover.Popup>
        </Popover.Positioner>
      </Popover.Portal>
    </Popover.Root>
  );
}

// One menu for the order: the sort field and the direction, each a radio
// group, behind a button that names both.
function SortMenu({
  sorts,
  sort,
  direction,
  onUpdate,
}: {
  sorts: readonly LibrarySort[];
  sort: LibrarySort;
  direction: LibraryDirection;
  onUpdate: (patch: Partial<LibraryBrowseState>) => void;
}) {
  const t = useT();
  const field = t(SORT_LABELS[sort]);
  const DirectionIcon = direction === "asc" ? ArrowUp : ArrowDown;
  const item = (value: string, label: string) => (
    <Menu.RadioItem key={value} value={value} closeOnClick className="bf-card-menu-item">
      <span className="bf-menu-check">
        <Menu.RadioItemIndicator>
          <Check aria-hidden size={15} />
        </Menu.RadioItemIndicator>
      </span>
      {label}
    </Menu.RadioItem>
  );
  return (
    <Menu.Root>
      <Menu.Trigger
        className="bf-chip"
        aria-label={t("libraryBrowseSortButton", {
          field,
          direction: t(DIRECTION_LABELS[direction]),
        })}
      >
        <ArrowUpDown aria-hidden className="size-4" />
        {field}
        <DirectionIcon aria-hidden className="size-3.5" />
      </Menu.Trigger>
      <Menu.Portal>
        <Menu.Positioner side="bottom" align="end" sideOffset={6} className="bf-card-menu-layer">
          <Menu.Popup className="bf-card-menu" data-register="viewer">
            <Menu.Group>
              <Menu.GroupLabel className="bf-menu-group-label">
                {t("libraryBrowseSortLabel")}
              </Menu.GroupLabel>
              <Menu.RadioGroup
                value={sort}
                onValueChange={(value: LibrarySort) => onUpdate({ sort: value })}
              >
                {sorts.map((option) => item(option, t(SORT_LABELS[option])))}
              </Menu.RadioGroup>
            </Menu.Group>
            <Menu.Separator className="bf-menu-separator" />
            <Menu.Group>
              <Menu.GroupLabel className="bf-menu-group-label">
                {t("libraryBrowseOrderLabel")}
              </Menu.GroupLabel>
              <Menu.RadioGroup
                value={direction}
                onValueChange={(value: LibraryDirection) => onUpdate({ direction: value })}
              >
                {(["asc", "desc"] as const).map((option) =>
                  item(option, t(DIRECTION_LABELS[option])),
                )}
              </Menu.RadioGroup>
            </Menu.Group>
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
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
  const [text, setText] = useState(committed);
  // Every new committed value (this field's own commit, a chip, a link, the
  // other side swapping) replaces the draft, so a cleared year stays cleared.
  const [shown, setShown] = useState(committed);
  if (shown !== committed) {
    setShown(committed);
    setText(committed);
  }
  const commit = () => {
    const trimmed = text.trim();
    const year = browseYear(trimmed);
    if ((year || !trimmed) && year !== value) onCommit(year);
    // The draft always ends as the committed value: a commit the range
    // normalizes back onto this side's value (a swap) changes nothing above.
    setText(committed);
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
        onChange={(event) => setText(event.target.value)}
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
