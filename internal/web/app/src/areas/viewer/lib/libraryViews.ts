import { graphql } from "@/lib/api";
import { displayString } from "@/lib/display";
import type { DisplayString, SourceItem } from "@/lib/graphql/types";

// Library views (CORE-2362). A Library page shows one view at a time: one per
// Entity kind the Library's plugin browses it by, in the server's order
// (parent kinds first), then the Media view, the flat libraryItems grid. The
// kinds and their labels come from the server (graph libraryEntities), so core
// names none. The view travels in the URL as `view=<id>`, and no view means
// the first one. A further view slots in as one more entry.

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

/** One Entity card with its counts and the Entities it sits under. */
export interface LibraryEntityItem extends SourceItem {
  childCount: number;
  mediaCount: number;
  parents: LibraryEntityParent[];
}

export interface LibraryEntitiesPage {
  kinds: LibraryEntityKind[];
  /** The plugin's title for its unfiltered Media grid. */
  allLabel?: DisplayString;
  /** The kind listed: the requested one, else the first browse kind. */
  kind: string;
  total: number;
  items: LibraryEntityItem[];
}

export interface LibraryEntitiesRequest {
  libraryID: string;
  kind?: string;
  limit: number;
  offset: number;
}

const LIBRARY_ENTITIES_QUERY =
  "query LibraryEntities($libraryID: String!, $kind: String, $limit: Int, $offset: Int) { libraryEntities(libraryID: $libraryID, kind: $kind, limit: $limit, offset: $offset) { kinds { kind label kindDisplayName archetype total } allLabel kind total items { id kind title summary artwork { poster } artworkSlots locked mediaClass year engagement { positionSeconds durationSeconds completed totalCount remainingCount } childCount mediaCount parents { id title } } } }";

export async function fetchLibraryEntities(
  request: LibraryEntitiesRequest,
  signal?: AbortSignal,
): Promise<LibraryEntitiesPage> {
  const vars: Record<string, unknown> = {
    libraryID: request.libraryID,
    limit: request.limit,
    offset: request.offset,
  };
  if (request.kind) vars.kind = request.kind;
  const data = await graphql<{ libraryEntities: LibraryEntitiesPage | null }>(
    LIBRARY_ENTITIES_QUERY,
    vars,
    signal,
  );
  const page = data.libraryEntities;
  return {
    kinds: page?.kinds ?? [],
    allLabel: page?.allLabel,
    kind: page?.kind ?? "",
    total: page?.total ?? 0,
    items: (page?.items ?? []).map((item) => ({ ...item, parents: item.parents ?? [] })),
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
 * the given fallback.
 */
export function libraryViews(
  kinds: readonly LibraryEntityKind[],
  allLabel: DisplayString | undefined,
  locale: string,
  mediaFallback: string,
): LibraryView[] {
  const views: LibraryView[] = kinds.map((kind) => ({
    id: kind.kind,
    kind: kind.kind,
    label:
      displayString(kind.label, locale) || displayString(kind.kindDisplayName, locale) || kind.kind,
  }));
  if (views.length === 0) return views;
  views.push({ id: MEDIA_VIEW, label: displayString(allLabel, locale) || mediaFallback });
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

/** The line under an Entity card's title: the Entities it sits under, else how much it holds. */
export function entityCardLine(
  item: LibraryEntityItem,
  count: (mediaCount: number) => string,
): string {
  const parents = item.parents.map((parent) => parent.title).filter(Boolean);
  if (parents.length > 0) return parents.join(", ");
  return item.mediaCount > 0 ? count(item.mediaCount) : "";
}
