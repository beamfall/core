import { beforeEach, describe, expect, it, vi } from "vitest";
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
import {
  addMediaToPlaylist,
  createPlaylist,
  deletePlaylist,
  loadPlaylists,
  memberOrder,
  moved,
  moveInPlaylist,
  removeMediaFromPlaylist,
  renamePlaylist,
} from "./playlists";

vi.mock("@/lib/graphql", () => ({
  addCollectionMedia: vi.fn(),
  createCollection: vi.fn(),
  deleteCollection: vi.fn(),
  fetchCollections: vi.fn(),
  removeCollectionMedia: vi.fn(),
  reorderCollectionMedia: vi.fn(),
  updateCollection: vi.fn(),
}));

function playlist(
  id: string,
  members: string[],
  extra: Partial<CollectionInfo> = {},
): CollectionInfo {
  return {
    id,
    libraryID: "lib-1",
    name: `List ${id}`,
    sortOrder: 0,
    memberCount: members.length,
    // Stored out of order: memberOrder sorts by sortOrder.
    members: members.map((mediaID, index) => ({ mediaID, sortOrder: index })).reverse(),
    ...extra,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("playlist mutations", () => {
  it("loads live playlists across Libraries and tolerates one failing", async () => {
    vi.mocked(fetchCollections).mockImplementation(async (libraryID) => {
      if (libraryID === "lib-bad") throw new Error("denied");
      return [
        playlist(`${libraryID}-a`, []),
        playlist(`${libraryID}-old`, [], { trashedAt: "2026-01-01" }),
      ];
    });
    const list = await loadPlaylists(["lib-1", "lib-bad", "lib-2"]);
    expect(list.map((entry) => entry.id)).toEqual(["lib-1-a", "lib-2-a"]);
  });

  it("creates and renames with trimmed names", async () => {
    await createPlaylist("lib-1", "  Test List ");
    expect(createCollection).toHaveBeenCalledWith({ libraryID: "lib-1", name: "Test List" });
    await renamePlaylist("p1", " Renamed ");
    expect(updateCollection).toHaveBeenCalledWith({ id: "p1", name: "Renamed" });
  });

  it("deletes, adds and removes through the Collection mutations", async () => {
    await deletePlaylist("p1");
    expect(deleteCollection).toHaveBeenCalledWith("p1");
    await addMediaToPlaylist("p1", "t1");
    expect(addCollectionMedia).toHaveBeenCalledWith("p1", ["t1"]);
    await removeMediaFromPlaylist("p1", "t1");
    expect(removeCollectionMedia).toHaveBeenCalledWith("p1", ["t1"]);
  });

  it("reorders by storing the whole moved order", async () => {
    const list = playlist("p1", ["t1", "t2", "t3"]);
    expect(memberOrder(list)).toEqual(["t1", "t2", "t3"]);
    await moveInPlaylist(list, 2, -1);
    expect(reorderCollectionMedia).toHaveBeenCalledWith("p1", ["t1", "t3", "t2"]);
    await moveInPlaylist(list, 0, 1);
    expect(reorderCollectionMedia).toHaveBeenLastCalledWith("p1", ["t2", "t1", "t3"]);
  });

  it("keeps the order when a move would leave the list", () => {
    expect(moved(["t1", "t2"], 0, -1)).toEqual(["t1", "t2"]);
    expect(moved(["t1", "t2"], 1, 1)).toEqual(["t1", "t2"]);
  });
});
