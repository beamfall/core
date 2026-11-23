import { graphql } from "@/lib/api";
import type { DisplayString, SourceItem } from "@/lib/graphql/types";

// Library browse (CORE-1190): the whole-library grid behind /n/:nodeId/all.
// The server owns visibility, counting, sorting and filtering (graph
// libraryItems); this module is the request shape, the URL <-> query mapping
// and the per-library view-mode memory.

export const LIBRARY_BROWSE_PAGE = 100;

export const LIBRARY_SORTS = [
  "title",
  "year",
  "added",
  "duration",
  "last-watched",
  "rating",
  "release-date",
  "audience-score",
  "plays",
] as const;
export type LibrarySort = (typeof LIBRARY_SORTS)[number];
/** Sorts that only mean something for items a viewer watches (CORE-2327). */
export const LIBRARY_WATCHING_SORTS: readonly LibrarySort[] = ["last-watched", "plays"];
export const LIBRARY_WATCH_STATES = ["unwatched", "in-progress", "watched"] as const;
export type LibraryWatch = (typeof LIBRARY_WATCH_STATES)[number];
export const LIBRARY_QUALITIES = ["uhd", "hd1080", "hd720", "sd"] as const;
export type LibraryQuality = (typeof LIBRARY_QUALITIES)[number];
// "My rating" filter steps, as the minimum personal rating on the 1-10 wire
// scale: any rating, then 3, 4 and 5 stars.
export const LIBRARY_MIN_RATINGS = [1, 6, 8, 10] as const;
export type LibraryDirection = "asc" | "desc";
// How a Library's items are drawn (CORE-2386): a poster grid, a landscape
// thumbnail grid, a compact list of rows, or a dense table of rows without
// artwork (CORE-2443).
export type LibraryViewMode = "poster" | "thumbnail" | "list" | "table";
export const LIBRARY_VIEW_MODES: readonly LibraryViewMode[] = [
  "poster",
  "thumbnail",
  "list",
  "table",
];
/** The release years a year range accepts (CORE-2443). */
export const LIBRARY_MIN_YEAR = 1800;
export const LIBRARY_MAX_YEAR = 2200;

// A grouping selection travels in the URL as `group.<kind>=<value>`. The kinds
// and their labels are the Library plugin's, reported by the server.
const GROUP_PARAM = "group.";

/** The longest title search the server accepts, in characters (CORE-2568). */
export const LIBRARY_MAX_QUERY = 200;

/** The browse state the URL carries (defaults are omitted from the URL). */
export interface LibraryBrowseState {
  /** Title text the result must hold, word by word (CORE-2568); "" for none. */
  query: string;
  sort: LibrarySort;
  direction: LibraryDirection;
  watch: LibraryWatch | "";
  /** The value selected for each plugin-declared grouping, keyed by its kind. */
  groups: Record<string, string>;
  /** A decade start year (1990 = the 1990s), or 0 for none. */
  decade: number;
  /**
   * A release-year range, inclusive, with 0 leaving a side open (CORE-2443).
   * It and the decade exclude each other.
   */
  yearFrom: number;
  yearTo: number;
  quality: LibraryQuality | "";
  /** The lowest personal rating to keep (1-10), or 0 for none. */
  minRating: number;
}

export const DEFAULT_BROWSE_STATE: LibraryBrowseState = {
  query: "",
  sort: "title",
  direction: "asc",
  watch: "",
  groups: {},
  decade: 0,
  yearFrom: 0,
  yearTo: 0,
  quality: "",
  minRating: 0,
};

/**
 * Whether a library's loaded items are all audio, so the browse bar drops the
 * controls that only fit watching: the watch-state and quality filters and the
 * last-watched sort (CORE-2327). A page with no Media decides nothing.
 */
export function isAudioOnly(items: readonly SourceItem[]): boolean {
  const media = items.filter((item) => item.kind === "media");
  return media.length > 0 && media.every((item) => item.mediaClass === "audio");
}

/** A release year in range, or 0 for anything else. */
export function browseYear(raw: string | null | undefined): number {
  const year = /^\s*\d{4}\s*$/.test(raw ?? "") ? Number(raw) : 0;
  return year >= LIBRARY_MIN_YEAR && year <= LIBRARY_MAX_YEAR ? year : 0;
}

function oneOf<T extends string>(values: readonly T[], value: string | null): T | "" {
  return (values as readonly string[]).includes(value ?? "") ? (value as T) : "";
}

/** Cap a browse search at the server's Unicode code-point limit. */
export function limitBrowseQuery(text: string): string {
  return Array.from(text).slice(0, LIBRARY_MAX_QUERY).join("");
}

/** A title search as the server takes it: trimmed and cut to its length limit. */
export function browseQuery(raw: string | null | undefined): string {
  return limitBrowseQuery((raw ?? "").trim()).trim();
}

/**
 * Whether a watched-state or rating change can reorder or refilter a read in
 * this state, so the read starts over instead of refreshing its cards. With
 * the Profile's Hide watched on, a read that names no watch filter drops what
 * the Profile finishes, so it follows engagement too.
 */
export function browseFollowsEngagement(state: LibraryBrowseState, hideWatched = false): boolean {
  return (
    state.watch !== "" ||
    hideWatched ||
    state.minRating > 0 ||
    state.sort === "rating" ||
    LIBRARY_WATCHING_SORTS.includes(state.sort)
  );
}

/** Reads browse state from the URL; unknown values fall back to defaults. */
export function parseBrowseState(params: URLSearchParams): LibraryBrowseState {
  const decade = Number.parseInt(params.get("decade") ?? "", 10);
  const groups: Record<string, string> = {};
  for (const [name, raw] of params) {
    const kind = name.startsWith(GROUP_PARAM) ? name.slice(GROUP_PARAM.length).trim() : "";
    const value = raw.trim();
    if (kind && value) groups[kind] = value;
  }
  const rated = Number.parseInt(params.get("rated") ?? "", 10);
  const decadeYear = Number.isFinite(decade) && decade > 0 ? Math.floor(decade / 10) * 10 : 0;
  // A decade wins over a range, which a hand-written URL could carry both of;
  // a reversed range reads in order.
  let yearFrom = decadeYear ? 0 : browseYear(params.get("from"));
  let yearTo = decadeYear ? 0 : browseYear(params.get("to"));
  if (yearFrom && yearTo && yearFrom > yearTo) [yearFrom, yearTo] = [yearTo, yearFrom];
  return {
    query: browseQuery(params.get("q")),
    sort: oneOf(LIBRARY_SORTS, params.get("sort")) || DEFAULT_BROWSE_STATE.sort,
    direction: params.get("dir") === "desc" ? "desc" : "asc",
    watch: oneOf(LIBRARY_WATCH_STATES, params.get("watch")),
    groups,
    decade: decadeYear,
    yearFrom,
    yearTo,
    quality: oneOf(LIBRARY_QUALITIES, params.get("quality")),
    minRating: (LIBRARY_MIN_RATINGS as readonly number[]).includes(rated) ? rated : 0,
  };
}

/** Writes browse state as URL params, leaving defaults out. */
export function browseStateParams(state: LibraryBrowseState): URLSearchParams {
  const params = new URLSearchParams();
  const query = browseQuery(state.query);
  if (query) params.set("q", query);
  if (state.sort !== DEFAULT_BROWSE_STATE.sort) params.set("sort", state.sort);
  if (state.direction !== DEFAULT_BROWSE_STATE.direction) params.set("dir", state.direction);
  if (state.watch) params.set("watch", state.watch);
  for (const kind of Object.keys(state.groups).sort()) {
    params.set(GROUP_PARAM + kind, state.groups[kind]);
  }
  if (state.decade) params.set("decade", String(state.decade));
  if (state.yearFrom) params.set("from", String(state.yearFrom));
  if (state.yearTo) params.set("to", String(state.yearTo));
  if (state.quality) params.set("quality", state.quality);
  if (state.minRating) params.set("rated", String(state.minRating));
  return params;
}

/** One way the Library's plugin groups its items, with the values in reach. */
export interface LibraryItemsGrouping {
  kind: string;
  title?: DisplayString;
  values: string[];
  /** Items per value, parallel to values (CORE-2505); absent from an older server. */
  counts?: number[];
  browseCounts?: number[];
  browseLabels?: (DisplayString | null)[];
}

export interface LibraryItemsFacets {
  groupings: LibraryItemsGrouping[];
  decades: number[];
  qualities: string[];
  /** The values of each requested plugin field with their item counts, counted before any field filter (CORE-3369). */
  fields?: LibraryItemsField[];
}

/** One plugin Filterable text field's values in the Library, most items first. */
export interface LibraryItemsField {
  field: string;
  values: string[];
  counts: number[];
}

/** Where one title initial ("A", or "#" for a digit or symbol) starts in the result, and how many items share it. */
export interface LibraryItemsLetter {
  letter: string;
  offset: number;
  count: number;
}

export interface LibraryItemsPage {
  total: number;
  items: SourceItem[];
  facets: LibraryItemsFacets;
  /** The title initials across the whole result, on a title sort only. */
  letters: LibraryItemsLetter[];
}

/** One libraryItems request: the browse state plus the order and page. */
export interface LibraryItemsRequest {
  /** Resolve category browse units only for the Categories pivot. */
  categoryCounts?: boolean;
  libraryID: string;
  state: LibraryBrowseState;
  /** "random" replaces the state's sort for Shuffle; seed keeps paging stable. */
  shuffleSeed?: string;
  /**
   * A plugin field to order by (CORE-3362), sent as "field:<name>": the karaoke
   * songbook's artist order. It replaces the state's sort, not its direction.
   */
  fieldSort?: string;
  /** Plugin Filterable text fields to match exactly, one value each (CORE-3369). */
  fieldFilters?: Record<string, string>;
  /** Plugin Filterable text fields to report under facets.fields. */
  facetFields?: string[];
  limit: number;
  offset: number;
}

const LIBRARY_ITEMS_QUERY =
  "query LibraryItems($libraryID: String!, $sort: String, $direction: String, $seed: String, $watch: String, $quality: String, $minRating: Int, $yearFrom: Int, $yearTo: Int, $groupFilters: Map, $query: String, $limit: Int, $offset: Int, $categoryCounts: Boolean, $fieldFilters: Map, $facetFields: [String!]) { libraryItems(libraryID: $libraryID, sort: $sort, direction: $direction, seed: $seed, watch: $watch, quality: $quality, minRating: $minRating, yearFrom: $yearFrom, yearTo: $yearTo, groupFilters: $groupFilters, query: $query, limit: $limit, offset: $offset, categoryCounts: $categoryCounts, fieldFilters: $fieldFilters, facetFields: $facetFields) { total items { id kind title summary artwork { poster } artworkSlots locked mediaClass year certification audienceScore audienceScoreVotes playsAlone placeholderIcon byline subtitle sequence index durationSeconds container { id title artwork { poster } } engagement { positionSeconds durationSeconds completed totalCount remainingCount } } facets { groupings { kind title values counts browseCounts browseLabels } decades qualities fields { field values counts } } letters { letter offset count } } }";

export function libraryItemsVariables(request: LibraryItemsRequest): Record<string, unknown> {
  const { state } = request;
  const vars: Record<string, unknown> = {
    libraryID: request.libraryID,
    sort: request.shuffleSeed
      ? "random"
      : request.fieldSort
        ? `field:${request.fieldSort}`
        : state.sort,
    direction: request.shuffleSeed ? "asc" : state.direction,
    limit: request.limit,
    offset: request.offset,
  };
  if (request.categoryCounts) vars.categoryCounts = true;
  if (request.fieldFilters && Object.keys(request.fieldFilters).length)
    vars.fieldFilters = request.fieldFilters;
  if (request.facetFields?.length) vars.facetFields = request.facetFields;
  if (request.shuffleSeed) vars.seed = request.shuffleSeed;
  if (state.watch) vars.watch = state.watch;
  if (state.quality) vars.quality = state.quality;
  if (state.minRating) vars.minRating = state.minRating;
  if (state.decade) {
    vars.yearFrom = state.decade;
    vars.yearTo = state.decade + 9;
  } else {
    if (state.yearFrom) vars.yearFrom = state.yearFrom;
    if (state.yearTo) vars.yearTo = state.yearTo;
  }
  if (Object.keys(state.groups).length) vars.groupFilters = state.groups;
  const query = browseQuery(state.query);
  if (query) vars.query = query;
  return vars;
}

export async function fetchLibraryItems(
  request: LibraryItemsRequest,
  signal?: AbortSignal,
): Promise<LibraryItemsPage> {
  const data = await graphql<{ libraryItems: LibraryItemsPage | null }>(
    LIBRARY_ITEMS_QUERY,
    libraryItemsVariables(request),
    signal,
  );
  const page = data.libraryItems;
  return {
    total: page?.total ?? 0,
    items: page?.items ?? [],
    facets: {
      groupings: page?.facets?.groupings ?? [],
      decades: page?.facets?.decades ?? [],
      qualities: page?.facets?.qualities ?? [],
      fields: page?.facets?.fields ?? [],
    },
    letters: page?.letters ?? [],
  };
}

// View mode is a per-viewer, per-library convenience, so it lives in this
// browser only; a blocked or cleared store just means the default poster grid.
// The earlier "grid" choice (CORE-2362) reads as the poster grid it drew.
const VIEW_MODE_KEY = "beamfall.libraryView.";

export function readLibraryViewMode(libraryID: string): LibraryViewMode {
  let stored: string | null = null;
  try {
    stored = window.localStorage.getItem(VIEW_MODE_KEY + libraryID);
  } catch {
    // A blocked store falls back to the default.
  }
  return LIBRARY_VIEW_MODES.find((mode) => mode === stored) ?? "poster";
}

/** The class of the list that draws a Library's items in a view mode. */
export function libraryViewClass(mode: LibraryViewMode): string {
  if (mode === "list") return "bf-stack bf-rows";
  if (mode === "table") return "bf-stack bf-rows bf-table-rows";
  return mode === "thumbnail" ? "bf-grid bf-grid-wide" : "bf-grid";
}

export function writeLibraryViewMode(libraryID: string, mode: LibraryViewMode): void {
  try {
    window.localStorage.setItem(VIEW_MODE_KEY + libraryID, mode);
  } catch {
    // Remembering the mode is optional.
  }
}
