import { graphql } from "@/lib/api";
import type { DisplayString } from "@/lib/display";
import type { SourceItem } from "@/lib/graphql/types";

// Library browse (CORE-1190): the whole-library grid behind /n/:nodeId/all.
// The server owns visibility, counting, sorting and filtering (graph
// libraryItems); this module is the request shape, the URL <-> query mapping
// and the per-library view-mode memory.

export const LIBRARY_BROWSE_PAGE = 100;

export const LIBRARY_SORTS = ["title", "year", "added", "duration", "last-watched"] as const;
export type LibrarySort = (typeof LIBRARY_SORTS)[number];
export const LIBRARY_WATCH_STATES = ["unwatched", "in-progress", "watched"] as const;
export type LibraryWatch = (typeof LIBRARY_WATCH_STATES)[number];
export const LIBRARY_QUALITIES = ["uhd", "hd1080", "hd720", "sd"] as const;
export type LibraryQuality = (typeof LIBRARY_QUALITIES)[number];
export type LibraryDirection = "asc" | "desc";
export type LibraryViewMode = "grid" | "list";

// A grouping selection travels in the URL as `group.<kind>=<value>`. The kinds
// and their labels are the Library plugin's, reported by the server.
const GROUP_PARAM = "group.";

/** The browse state the URL carries (defaults are omitted from the URL). */
export interface LibraryBrowseState {
  sort: LibrarySort;
  direction: LibraryDirection;
  watch: LibraryWatch | "";
  /** The value selected for each plugin-declared grouping, keyed by its kind. */
  groups: Record<string, string>;
  /** A decade start year (1990 = the 1990s), or 0 for none. */
  decade: number;
  quality: LibraryQuality | "";
}

export const DEFAULT_BROWSE_STATE: LibraryBrowseState = {
  sort: "title",
  direction: "asc",
  watch: "",
  groups: {},
  decade: 0,
  quality: "",
};

function oneOf<T extends string>(values: readonly T[], value: string | null): T | "" {
  return (values as readonly string[]).includes(value ?? "") ? (value as T) : "";
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
  return {
    sort: oneOf(LIBRARY_SORTS, params.get("sort")) || DEFAULT_BROWSE_STATE.sort,
    direction: params.get("dir") === "desc" ? "desc" : "asc",
    watch: oneOf(LIBRARY_WATCH_STATES, params.get("watch")),
    groups,
    decade: Number.isFinite(decade) && decade > 0 ? Math.floor(decade / 10) * 10 : 0,
    quality: oneOf(LIBRARY_QUALITIES, params.get("quality")),
  };
}

/** Writes browse state as URL params, leaving defaults out. */
export function browseStateParams(state: LibraryBrowseState): URLSearchParams {
  const params = new URLSearchParams();
  if (state.sort !== DEFAULT_BROWSE_STATE.sort) params.set("sort", state.sort);
  if (state.direction !== DEFAULT_BROWSE_STATE.direction) params.set("dir", state.direction);
  if (state.watch) params.set("watch", state.watch);
  for (const kind of Object.keys(state.groups).sort()) {
    params.set(GROUP_PARAM + kind, state.groups[kind]);
  }
  if (state.decade) params.set("decade", String(state.decade));
  if (state.quality) params.set("quality", state.quality);
  return params;
}

/** One way the Library's plugin groups its items, with the values in reach. */
export interface LibraryItemsGrouping {
  kind: string;
  title?: DisplayString;
  values: string[];
}

export interface LibraryItemsFacets {
  groupings: LibraryItemsGrouping[];
  decades: number[];
  qualities: string[];
}

export interface LibraryItemsPage {
  total: number;
  items: SourceItem[];
  facets: LibraryItemsFacets;
}

/** One libraryItems request: the browse state plus the order and page. */
export interface LibraryItemsRequest {
  libraryID: string;
  state: LibraryBrowseState;
  /** "random" replaces the state's sort for Shuffle; seed keeps paging stable. */
  shuffleSeed?: string;
  limit: number;
  offset: number;
}

const LIBRARY_ITEMS_QUERY =
  "query LibraryItems($libraryID: String!, $sort: String, $direction: String, $seed: String, $watch: String, $quality: String, $yearFrom: Int, $yearTo: Int, $groupFilters: Map, $limit: Int, $offset: Int) { libraryItems(libraryID: $libraryID, sort: $sort, direction: $direction, seed: $seed, watch: $watch, quality: $quality, yearFrom: $yearFrom, yearTo: $yearTo, groupFilters: $groupFilters, limit: $limit, offset: $offset) { total items { id kind title summary artwork { poster } artworkSlots } facets { groupings { kind title values } decades qualities } } }";

export function libraryItemsVariables(request: LibraryItemsRequest): Record<string, unknown> {
  const { state } = request;
  const vars: Record<string, unknown> = {
    libraryID: request.libraryID,
    sort: request.shuffleSeed ? "random" : state.sort,
    direction: request.shuffleSeed ? "asc" : state.direction,
    limit: request.limit,
    offset: request.offset,
  };
  if (request.shuffleSeed) vars.seed = request.shuffleSeed;
  if (state.watch) vars.watch = state.watch;
  if (state.quality) vars.quality = state.quality;
  if (state.decade) {
    vars.yearFrom = state.decade;
    vars.yearTo = state.decade + 9;
  }
  if (Object.keys(state.groups).length) vars.groupFilters = state.groups;
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
    },
  };
}

// View mode is a per-viewer, per-library convenience, so it lives in this
// browser only; a blocked or cleared store just means the default grid.
const VIEW_MODE_KEY = "beamfall.libraryView.";

export function readLibraryViewMode(libraryID: string): LibraryViewMode {
  try {
    return window.localStorage.getItem(VIEW_MODE_KEY + libraryID) === "list" ? "list" : "grid";
  } catch {
    return "grid";
  }
}

export function writeLibraryViewMode(libraryID: string, mode: LibraryViewMode): void {
  try {
    window.localStorage.setItem(VIEW_MODE_KEY + libraryID, mode);
  } catch {
    // Remembering the mode is optional.
  }
}
