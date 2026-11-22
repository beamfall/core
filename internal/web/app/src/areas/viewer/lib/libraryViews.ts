import { graphql } from "@/lib/api";
import { displayString } from "@/lib/display";
import type { DisplayString, SourceItem } from "@/lib/graphql/types";
import { readEngagementEpoch } from "../actions/engagementSync";
import {
  browseStateParams,
  DEFAULT_BROWSE_STATE,
  type LibraryBrowseState,
  type LibraryItemsLetter,
  libraryItemsVariables,
} from "./libraryBrowse";
import { FOLDER_VIEW } from "./libraryFolders";

// Library views (CORE-2362). A Library page shows one view at a time: one per
// Entity kind the Library's plugin browses it by, in the server's order
// (parent kinds first), then the Media view, the flat libraryItems grid, then
// the Folders view (CORE-2359). The kinds and their labels come from the
// server (graph libraryEntities), so core names none. The view travels in the
// URL as `view=<id>`, and no view means the first one. A further view slots in
// as one more entry.

export const LIBRARY_VIEW_PARAM = "view";
export const MEDIA_VIEW = "media";
export const LIBRARY_ENTITY_PAGE = 100;

/** One Entity kind the Library can be browsed by, and how many it holds. */
export interface LibraryEntityKind {
  kind: string;
  /** The plugin's browse Section title for the kind. */
  label?: DisplayString;
  kindDisplayName?: DisplayString;
  archetype?: string;
  total: number;
}

export interface LibraryEntityParent {
  id: string;
  title: string;
}

/** A kind whose Entities count as the listed kind's children, in the plugin's words (CORE-2891). */
export interface LibraryEntityChildKind {
  kind: string;
  kindDisplayName?: DisplayString;
  kindPluralDisplayName?: DisplayString;
}

export interface LibraryEntityChildCount {
  kind: string;
  count: number;
}

/** One Entity card with its counts and the Entities it sits under. */
export interface LibraryEntityItem extends SourceItem {
  childCount: number;
  /** The children by kind, in childKinds order, kinds it has none of left out. */
  childCounts: LibraryEntityChildCount[];
  mediaCount: number;
  parents: LibraryEntityParent[];
}

export interface LibraryEntitiesPage {
  kinds: LibraryEntityKind[];
  /** The listed kind's child kinds, with their plugin display names. */
  childKinds: LibraryEntityChildKind[];
  /** The plugin's title for its unfiltered Media grid. */
  allLabel?: DisplayString;
  /** The kind listed: the requested one, else the first browse kind. */
  kind: string;
  total: number;
  items: LibraryEntityItem[];
  /** The title initials across the listed kind's result, on a title sort only (CORE-2440). */
  letters: LibraryItemsLetter[];
}

export interface LibraryEntitiesRequest {
  libraryID: string;
  kind?: string;
  /**
   * The Media view's sort and filters, applied to the listed kind by the Media
   * each Entity reaches (CORE-2440); absent means title order, unfiltered.
   */
  state?: LibraryBrowseState;
  limit: number;
  offset: number;
}

const LIBRARY_ENTITIES_QUERY =
  "query LibraryEntities($libraryID: String!, $kind: String, $sort: String, $direction: String, $watch: String, $quality: String, $minRating: Int, $yearFrom: Int, $yearTo: Int, $groupFilters: Map, $query: String, $limit: Int, $offset: Int) { libraryEntities(libraryID: $libraryID, kind: $kind, sort: $sort, direction: $direction, watch: $watch, quality: $quality, minRating: $minRating, yearFrom: $yearFrom, yearTo: $yearTo, groupFilters: $groupFilters, query: $query, limit: $limit, offset: $offset) { kinds { kind label kindDisplayName archetype total } childKinds { kind kindDisplayName kindPluralDisplayName } allLabel kind total items { id kind title summary artwork { poster } artworkSlots locked mediaClass year byline engagement { positionSeconds durationSeconds completed totalCount remainingCount } childCount childCounts { kind count } mediaCount parents { id title } } letters { letter offset count } } }";

/** The libraryEntities variables: the browse state's, as the Media view sends them, plus the kind. */
export function libraryEntitiesVariables(request: LibraryEntitiesRequest): Record<string, unknown> {
  const vars = libraryItemsVariables({
    libraryID: request.libraryID,
    state: request.state ?? DEFAULT_BROWSE_STATE,
    limit: request.limit,
    offset: request.offset,
  });
  if (request.kind) vars.kind = request.kind;
  return vars;
}

// Recent libraryEntities reads, newest last, so a Library page opened again
// paints at once while it reads afresh (stale-while-revalidate, CORE-3064).
// A read made before an engagement change (watched state, ratings, hides,
// Hide watched) is never served: those change what a query returns.
const ENTITY_CACHE_LIMIT = 24;
const entityCache = new Map<string, { epoch: number; page: LibraryEntitiesPage }>();
const entityReads = new Map<string, Promise<LibraryEntitiesPage>>();

function entityCacheKey(request: LibraryEntitiesRequest): string {
  return JSON.stringify(libraryEntitiesVariables(request));
}

function rememberLibraryEntities(
  key: string,
  epoch: number,
  page: LibraryEntitiesPage,
  alias = false,
) {
  // A read from before an engagement change never replaces a newer one, and
  // a page cached under the kind the server chose never replaces a read that
  // named that kind itself in the same epoch.
  const held = entityCache.get(key);
  if (held && (held.epoch > epoch || (alias && held.epoch === epoch))) return;
  entityCache.delete(key);
  entityCache.set(key, { epoch, page });
  for (const oldest of entityCache.keys()) {
    if (entityCache.size <= ENTITY_CACHE_LIMIT) break;
    entityCache.delete(oldest);
  }
}

/** The last read of this request, unless engagement has changed since it was made. */
export function cachedLibraryEntities(
  request: LibraryEntitiesRequest,
): LibraryEntitiesPage | undefined {
  const entry = entityCache.get(entityCacheKey(request));
  return entry && entry.epoch === readEngagementEpoch() ? entry.page : undefined;
}

/** Drops every cached libraryEntities read. */
export function clearLibraryEntitiesCache(): void {
  entityCache.clear();
  entityReads.clear();
}

/**
 * Reads one libraryEntities page and caches it. Callers asking for the same
 * page at once, in the same engagement epoch, share one request: a caller
 * after an engagement change reads afresh rather than joining a read whose
 * answer may predate it. A caller's abort rejects its own promise
 * and leaves the shared read to finish into the cache. A read that names no
 * kind is also cached under the kind the server chose.
 */
export function fetchLibraryEntities(
  request: LibraryEntitiesRequest,
  signal?: AbortSignal,
): Promise<LibraryEntitiesPage> {
  const key = entityCacheKey(request);
  const epoch = readEngagementEpoch();
  const readKey = `${epoch}:${key}`;
  let read = entityReads.get(readKey);
  if (!read) {
    read = readLibraryEntities(request)
      .then((page) => {
        rememberLibraryEntities(key, epoch, page);
        if (!request.kind && page.kind) {
          rememberLibraryEntities(
            entityCacheKey({ ...request, kind: page.kind }),
            epoch,
            page,
            true,
          );
        }
        return page;
      })
      .finally(() => {
        if (entityReads.get(readKey) === read) entityReads.delete(readKey);
      });
    entityReads.set(readKey, read);
  }
  if (!signal) return read;
  if (signal.aborted) return Promise.reject(signal.reason);
  const shared = read;
  return new Promise((resolve, reject) => {
    const abort = () => reject(signal.reason);
    signal.addEventListener("abort", abort, { once: true });
    shared.then(resolve, reject).finally(() => signal.removeEventListener("abort", abort));
  });
}

async function readLibraryEntities(request: LibraryEntitiesRequest): Promise<LibraryEntitiesPage> {
  const data = await graphql<{ libraryEntities: LibraryEntitiesPage | null }>(
    LIBRARY_ENTITIES_QUERY,
    libraryEntitiesVariables(request),
  );
  const page = data.libraryEntities;
  return {
    kinds: page?.kinds ?? [],
    childKinds: page?.childKinds ?? [],
    allLabel: page?.allLabel,
    kind: page?.kind ?? "",
    total: page?.total ?? 0,
    items: (page?.items ?? []).map((item) => ({
      ...item,
      childCounts: item.childCounts ?? [],
      parents: item.parents ?? [],
    })),
    letters: page?.letters ?? [],
  };
}

/** One entry of the view switcher. */
export interface LibraryView {
  id: string;
  label: string;
  /** The Entity kind the view lists; absent for the Media view. */
  kind?: string;
}

/**
 * The Library's views: one per browse kind, labelled by its Section title (else
 * its display name), then the Media view under the plugin's title for it, else
 * the given fallback, then the Folders view. A Library with no browse kinds
 * still offers the Media and Folders views.
 */
export function libraryViews(
  kinds: readonly LibraryEntityKind[],
  allLabel: DisplayString | undefined,
  locale: string,
  mediaFallback: string,
  foldersLabel: string,
): LibraryView[] {
  const views: LibraryView[] = kinds.map((kind) => ({
    id: kind.kind,
    kind: kind.kind,
    label:
      displayString(kind.label, locale) || displayString(kind.kindDisplayName, locale) || kind.kind,
  }));
  views.push({ id: MEDIA_VIEW, label: displayString(allLabel, locale) || mediaFallback });
  views.push({ id: FOLDER_VIEW, label: foldersLabel });
  return views;
}

/** The view a `view` value names, else the first. */
export function selectedView(
  views: readonly LibraryView[],
  id: string | null,
): LibraryView | undefined {
  return views.find((view) => view.id === id) ?? views[0];
}

/** The search parameters that open a view, keeping the rest of the current ones. */
export function viewSearchParams(current: URLSearchParams, id: string): URLSearchParams {
  const params = new URLSearchParams(current);
  params.set(LIBRARY_VIEW_PARAM, id);
  return params;
}

/**
 * The line under an Entity card's title: the Entities it sits under, else its
 * children counted by kind in the plugin's words ("4 Albums"), else how much
 * Media it holds. A kind with no plural name falls back to its singular one,
 * then to its name.
 */
export function entityCardLine(
  item: LibraryEntityItem,
  childKinds: readonly LibraryEntityChildKind[],
  locale: string,
  count: (mediaCount: number) => string,
): string {
  const parents = item.parents.map((parent) => parent.title).filter(Boolean);
  if (parents.length > 0) return parents.join(", ");
  const plural = pluralRules(locale);
  const children = (item.childCounts ?? []).flatMap(({ kind, count: n }) => {
    const child = childKinds.find((entry) => entry.kind === kind);
    if (!child || n <= 0) return [];
    const singular = displayString(child.kindDisplayName, locale) || kind;
    const name =
      plural.select(n) === "one"
        ? singular
        : displayString(child.kindPluralDisplayName, locale) || singular;
    return [`${n.toLocaleString(locale)} ${name}`];
  });
  if (children.length > 0) return children.join(" · ");
  return item.mediaCount > 0 ? count(item.mediaCount) : "";
}

function pluralRules(locale: string): Intl.PluralRules {
  try {
    return new Intl.PluralRules(locale);
  } catch {
    return new Intl.PluralRules("en");
  }
}

/**
 * The Library pivot address for one grouping value (CORE-2437): the Media view
 * of `/all`, filtered to that value, so a Category tile opens what it names.
 */
export function libraryCategorySearch(kind: string, value: string): string {
  const params = browseStateParams({ ...DEFAULT_BROWSE_STATE, groups: { [kind]: value } });
  params.set(LIBRARY_VIEW_PARAM, MEDIA_VIEW);
  return `?${params.toString()}`;
}
