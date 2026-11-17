import { graphql } from "@/lib/api";
import { type CollectionInfo, fetchCollections } from "@/lib/graphql";
import type { LibrarySummary, SourceItem } from "@/lib/graphql/types";

// Ordered Collections in the web viewer (CORE-1684). An ordered Collection is
// the playlist: a household-shared, library-scoped list of Media in a curated
// order (glossary: "Collection"). A box set is the same thing with its own
// page. The list API stays domain-agnostic; nothing here knows what kind of
// Media a Collection holds.

// The entries come through the Section feed path, so they carry the same
// artwork, engagement and reveal filtering as every other card in the viewer.
const COLLECTION_ITEMS_QUERY =
  'query CollectionItems($value: String, $limit: Int, $offset: Int) { sourceItems(kind: "collection", value: $value, limit: $limit, offset: $offset) { id kind title summary year artwork { poster } artworkSlots engagement { positionSeconds durationSeconds completed totalCount remainingCount } } }';

/** The server's per-request cap on a Source feed page. */
export const COLLECTION_PAGE = 200;
/** A bound on how many entries one page view reads, so a runaway list stays finite. */
export const MAX_COLLECTION_ENTRIES = 2000;

/** The visible entries of one Collection, in curated order. */
export async function fetchCollectionItems(
  collectionID: string,
  signal?: AbortSignal,
): Promise<SourceItem[]> {
  const items: SourceItem[] = [];
  while (items.length < MAX_COLLECTION_ENTRIES) {
    const data = await graphql<{ sourceItems: SourceItem[] | null }>(
      COLLECTION_ITEMS_QUERY,
      { value: collectionID, limit: COLLECTION_PAGE, offset: items.length },
      signal,
    );
    const page = data.sourceItems ?? [];
    items.push(...page);
    if (page.length < COLLECTION_PAGE) break;
  }
  return items;
}

// Only the lock state of each entry (CORE-1876). A member read through
// media() carries no certification-ceiling flag, so the playlist page asks the
// same Collection feed which entries are locked for the acting Profile.
const COLLECTION_LOCKS_QUERY =
  'query CollectionLocks($value: String, $limit: Int, $offset: Int) { sourceItems(kind: "collection", value: $value, limit: $limit, offset: $offset) { id locked } }';

/**
 * The lock state of a Collection's entries for the acting Profile: true when
 * the Profile cannot play the entry, false when it can. It reads every page
 * up to `count` entries, because the server drops muted entries after paging
 * and a short page does not mean the list has ended. A muted entry is absent
 * from the map: the feed does not say whether it is locked.
 */
export async function fetchEntryLocks(
  collectionID: string,
  count: number,
  signal?: AbortSignal,
): Promise<Map<string, boolean>> {
  const locks = new Map<string, boolean>();
  for (let offset = 0; offset < count; offset += COLLECTION_PAGE) {
    const data = await graphql<{ sourceItems: Pick<SourceItem, "id" | "locked">[] | null }>(
      COLLECTION_LOCKS_QUERY,
      { value: collectionID, limit: COLLECTION_PAGE, offset },
      signal,
    );
    for (const item of data.sourceItems ?? []) locks.set(item.id, item.locked === true);
  }
  return locks;
}

/** One Library's Collections, for the list page. */
export interface LibraryCollections {
  library: LibrarySummary;
  collections: CollectionInfo[];
}

/** Collections in display order: the curated sort order, then name. */
export function sortCollections(collections: readonly CollectionInfo[]): CollectionInfo[] {
  return [...collections].sort(
    (a, b) => a.sortOrder - b.sortOrder || a.name.localeCompare(b.name) || a.id.localeCompare(b.id),
  );
}

/**
 * Every Collection the session can see, grouped by the Libraries the manifest
 * lists (the libraries query is a Console read; the manifest is the viewer's).
 */
export async function fetchViewerCollections(
  libraries: readonly LibrarySummary[],
): Promise<LibraryCollections[]> {
  return Promise.all(
    libraries.map(async (library) => ({
      library,
      collections: sortCollections(await fetchCollections(library.id)),
    })),
  );
}

/** One Collection by id, or null when the session cannot see it. */
export async function fetchCollection(
  libraryID: string,
  collectionID: string,
): Promise<CollectionInfo | null> {
  const collections = await fetchCollections(libraryID);
  return collections.find((collection) => collection.id === collectionID) ?? null;
}

/** The member ids of a Collection in curated order. */
export function memberOrder(collection: CollectionInfo): string[] {
  return [...collection.members]
    .sort((a, b) => a.sortOrder - b.sortOrder || a.mediaID.localeCompare(b.mediaID))
    .map((member) => member.mediaID);
}

/**
 * The full member order after moving one visible entry a step up (-1) or down
 * (+1) past its visible neighbour. Members the session cannot see keep their
 * places, so a reorder from a narrower view never reshuffles them. Returns null
 * when the move is not possible (first entry up, last entry down).
 */
export function moveEntry(
  members: readonly string[],
  visible: readonly string[],
  id: string,
  delta: -1 | 1,
): string[] | null {
  const at = visible.indexOf(id);
  const neighbour = visible[at + delta];
  if (at < 0 || neighbour === undefined) return null;
  const from = members.indexOf(id);
  const to = members.indexOf(neighbour);
  if (from < 0 || to < 0) return null;
  const next = [...members];
  next[from] = neighbour;
  next[to] = id;
  return next;
}
