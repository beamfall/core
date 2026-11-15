import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { addCollectionMedia, fetchCollections, fetchTagAssignments } from "@/lib/graphql";
import type { CollectionInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { AddToPlaylistDialog } from "./AddToPlaylistDialog";

const session = vi.hoisted(() => ({ isManager: true }));

vi.mock("@/lib/graphql", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/graphql")>()),
  addCollectionMedia: vi.fn(),
  fetchCollections: vi.fn(),
  fetchTagAssignments: vi.fn(),
}));
vi.mock("@/lib/session", () => ({
  useSession: () => ({
    manifest: { libraries: [{ id: "lib-a" }, { id: "lib-b" }] },
    isManager: session.isManager,
  }),
}));

function playlist(id: string, libraryID: string, name: string): CollectionInfo {
  return { id, libraryID, name, sortOrder: 0, memberCount: 0, members: [] };
}

function renderDialog() {
  render(
    <I18nProvider initialLocale="en">
      <AddToPlaylistDialog mediaID="m-1" title="Test tone" onClose={() => {}} />
    </I18nProvider>,
  );
}

beforeEach(() => {
  session.isManager = true;
  vi.mocked(fetchCollections).mockImplementation(async (libraryID) =>
    libraryID === "lib-a"
      ? [playlist("p-a", "lib-a", "Library A list")]
      : [playlist("p-b", "lib-b", "Library B list")],
  );
  vi.mocked(fetchTagAssignments).mockResolvedValue({
    targetType: "media",
    targetID: "m-1",
    libraryID: "lib-b",
    tags: [],
  });
  vi.mocked(addCollectionMedia).mockResolvedValue(undefined as never);
});

afterEach(() => {
  cleanup();
  vi.mocked(fetchCollections).mockReset();
  vi.mocked(fetchTagAssignments).mockReset();
  vi.mocked(addCollectionMedia).mockReset();
});

describe("AddToPlaylistDialog destinations (CORE-1877)", () => {
  it("offers only the playlists in the item's own library", async () => {
    renderDialog();

    fireEvent.click(await screen.findByRole("button", { name: /Library B list/ }));
    expect(screen.queryByRole("button", { name: /Library A list/ })).toBeNull();
    expect(fetchTagAssignments).toHaveBeenCalledWith("media", "m-1");
    await vi.waitFor(() => expect(addCollectionMedia).toHaveBeenCalledWith("p-b", ["m-1"]));
  });

  it("says so when every playlist is in another library", async () => {
    vi.mocked(fetchCollections).mockImplementation(async (libraryID) =>
      libraryID === "lib-a" ? [playlist("p-a", "lib-a", "Library A list")] : [],
    );
    renderDialog();

    expect(await screen.findByText(/No playlists in this item's library yet/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Library A list/ })).toBeNull();
  });

  it("offers nothing when the item's library cannot be read", async () => {
    vi.mocked(fetchTagAssignments).mockRejectedValue(new Error("unknown media"));
    renderDialog();

    expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't load the playlists.");
    expect(screen.queryByRole("button", { name: /list/ })).toBeNull();
  });

  it("loads nothing on a device that cannot change playlists", async () => {
    session.isManager = false;
    renderDialog();

    expect(
      await screen.findByText("Playlists can be changed from a manager device."),
    ).toBeInTheDocument();
    expect(fetchTagAssignments).not.toHaveBeenCalled();
    expect(fetchCollections).not.toHaveBeenCalled();
  });
});
