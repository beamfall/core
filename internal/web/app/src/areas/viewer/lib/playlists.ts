import {
  addCollectionMedia,
  createCollection,
  deleteCollection,
  fetchCollections,
  removeCollectionMedia,
  reorderCollectionMedia,
  updateCollection,
} from "@/lib/graphql";
import type { CollectionInfo } from "@/lib/graphql/types";

// Playlists in the viewer (CORE-1635) are the server's Collections (ADR-0098):
// one list per Library, member order kept by the server. These wrap the
// Collection reads and mutations the viewer uses; the server enforces that a
// member belongs to the playlist's Library and that the caller may change it.

/** Every live playlist across the given Libraries, Library by Library, in server order. */
export async function loadPlaylists(libraryIDs: readonly string[]): Promise<CollectionInfo[]> {
  const lists = await Promise.all(
    libraryIDs.map((id) => fetchCollections(id).catch(() => [] as CollectionInfo[])),
  );
  return lists.flat().filter((playlist) => !playlist.trashedAt);
}

/** The playlist's member ids in its stored order. */
export function memberOrder(playlist: CollectionInfo): string[] {
  return [...playlist.members]
    .sort((a, b) => a.sortOrder - b.sortOrder)
    .map((member) => member.mediaID);
}

/** `order` with the entry at `index` moved by `delta`; unchanged when it would leave the list. */
export function moved(order: readonly string[], index: number, delta: number): string[] {
  const target = index + delta;
  if (index < 0 || index >= order.length || target < 0 || target >= order.length) {
    return [...order];
  }
  const next = [...order];
  const [entry] = next.splice(index, 1);
  next.splice(target, 0, entry);
  return next;
}

export function createPlaylist(libraryID: string, name: string): Promise<CollectionInfo> {
  return createCollection({ libraryID, name: name.trim() });
}

export function renamePlaylist(id: string, name: string): Promise<CollectionInfo> {
  return updateCollection({ id, name: name.trim() });
}

/** Moves the playlist to its Library's trash (restorable from Manage). */
export function deletePlaylist(id: string): Promise<CollectionInfo> {
  return deleteCollection(id);
}

export function addMediaToPlaylist(id: string, mediaID: string): Promise<CollectionInfo> {
  return addCollectionMedia(id, [mediaID]);
}

export function removeMediaFromPlaylist(id: string, mediaID: string): Promise<CollectionInfo> {
  return removeCollectionMedia(id, [mediaID]);
}

/** Moves one member up or down and stores the whole new order. */
export function moveInPlaylist(
  playlist: CollectionInfo,
  index: number,
  delta: number,
): Promise<CollectionInfo> {
  return reorderCollectionMedia(playlist.id, moved(memberOrder(playlist), index, delta));
}
