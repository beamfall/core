import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  addCollectionMedia,
  createCollection,
  deleteCollection,
  deleteSmartCollection,
  fetchCollections,
  fetchSmartCollections,
  reorderCollectionMedia,
  updateCollection,
  updateSmartCollection,
} from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { CollectionsPage } from "./CollectionsPage";

vi.mock("@/lib/graphql", () => ({
  fetchCollections: vi.fn(),
  createCollection: vi.fn(),
  updateCollection: vi.fn(),
  deleteCollection: vi.fn(),
  restoreCollection: vi.fn(),
  addCollectionMedia: vi.fn(),
  removeCollectionMedia: vi.fn(),
  reorderCollectionMedia: vi.fn(),
  fetchSmartCollections: vi.fn(),
  createSmartCollection: vi.fn(),
  updateSmartCollection: vi.fn(),
  deleteSmartCollection: vi.fn(),
}));

vi.mock("@/lib/session", () => ({
  useSession: () => ({
    manifest: {
      libraries: [{ id: "lib-1", title: "Films" }],
    },
  }),
}));

const awardWinners = {
  id: "col-1",
  libraryID: "lib-1",
  name: "Award winners",
  sortOrder: 0,
  memberCount: 2,
  members: [
    { mediaID: "media-7f3a", sortOrder: 0, addedAt: "2024-05-01" },
    { mediaID: "media-9b2c", sortOrder: 1, addedAt: "2024-05-03" },
  ],
};

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(fetchCollections).mockResolvedValue([awardWinners]);
  vi.mocked(fetchSmartCollections).mockResolvedValue([]);
  vi.mocked(createCollection).mockResolvedValue(awardWinners);
  vi.mocked(deleteCollection).mockResolvedValue(awardWinners);
  vi.mocked(addCollectionMedia).mockResolvedValue(awardWinners);
  vi.mocked(reorderCollectionMedia).mockResolvedValue(awardWinners);
  vi.mocked(updateSmartCollection).mockResolvedValue({
    id: "smart-1",
    libraryID: "lib-1",
    name: "Recently added",
    sourceExpr: "{}",
    sortExpr: "{}",
  });
});

function renderPage() {
  return render(
    <I18nProvider>
      <CollectionsPage />
    </I18nProvider>,
  );
}

describe("CollectionsPage", () => {
  it("lists Collections for the default Library", async () => {
    renderPage();
    expect(await screen.findByText("Award winners")).toBeVisible();
    expect(fetchCollections).toHaveBeenCalledWith("lib-1", false);
  });

  it("creates a top-level Collection with a null parentID", async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findByText("Award winners");

    await user.type(screen.getByLabelText("New Collection name"), "New picks");
    await user.click(screen.getByRole("button", { name: "Create" }));

    expect(createCollection).toHaveBeenCalledWith({
      libraryID: "lib-1",
      name: "New picks",
      parentID: null,
    });
  });

  it("shows the member editor with reorder for the selected Collection", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole("button", { name: "Award winners" }));

    const firstMemberRow = (await screen.findByText("media-7f3a")).closest("tr")!;
    // Move the first member down — writes the whole order back.
    await user.click(within(firstMemberRow).getByRole("button", { name: "Move down" }));

    await waitFor(() =>
      expect(reorderCollectionMedia).toHaveBeenCalledWith("col-1", ["media-9b2c", "media-7f3a"]),
    );
  });

  // A rejected reorder write must not leave the optimistic order on screen:
  // the members are restored to the server's order and reloaded.
  it("restores the server's member order when the reorder write fails", async () => {
    const user = userEvent.setup();
    vi.mocked(reorderCollectionMedia).mockRejectedValue(new Error("refused"));
    renderPage();
    await user.click(await screen.findByRole("button", { name: "Award winners" }));

    const firstMemberRow = (await screen.findByText("media-7f3a")).closest("tr")!;
    await user.click(within(firstMemberRow).getByRole("button", { name: "Move down" }));

    await waitFor(() => expect(fetchCollections).toHaveBeenCalledTimes(2));
    await waitFor(() => {
      const ids = screen.getAllByText(/^media-/).map((cell) => cell.textContent);
      expect(ids).toEqual(["media-7f3a", "media-9b2c"]);
    });
    expect(screen.getByText("Could not update the members.")).toBeVisible();
  });

  // Swapping two Collections is two writes. When the second is refused the
  // first is rolled back and the list is reloaded, so the displayed order is
  // the server's rather than a half-applied swap.
  it("rolls back the first sortOrder write when the second fails", async () => {
    const user = userEvent.setup();
    const second = { ...awardWinners, id: "col-2", name: "Brunch picks", sortOrder: 1 };
    vi.mocked(fetchCollections).mockResolvedValue([awardWinners, second]);
    vi.mocked(updateCollection)
      .mockResolvedValueOnce(awardWinners)
      .mockRejectedValueOnce(new Error("refused"))
      .mockResolvedValue(awardWinners);
    renderPage();

    const firstRow = (await screen.findByText("Award winners")).closest("tr")!;
    await user.click(within(firstRow).getByRole("button", { name: "Move down" }));

    await waitFor(() => expect(updateCollection).toHaveBeenCalledTimes(3));
    expect(vi.mocked(updateCollection).mock.calls.map(([input]) => input)).toEqual([
      { id: "col-1", sortOrder: 1 },
      { id: "col-2", sortOrder: 0 },
      { id: "col-1", sortOrder: 0 },
    ]);
    await waitFor(() => expect(fetchCollections).toHaveBeenCalledTimes(2));
    const names = screen
      .getAllByRole("button", { name: /picks|winners/ })
      .map((b) => b.textContent);
    expect(names).toEqual(["Award winners", "Brunch picks"]);
    expect(screen.getByText("Could not update the Collection.")).toBeVisible();
  });

  it("bulk-adds members from a separated ID list", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole("button", { name: "Award winners" }));
    await screen.findByText("media-7f3a");

    await user.type(
      screen.getByLabelText("Add Media IDs (comma or space separated)"),
      "media-aaa, media-bbb",
    );
    await user.click(screen.getByRole("button", { name: "Add members" }));

    expect(addCollectionMedia).toHaveBeenCalledWith("col-1", ["media-aaa", "media-bbb"]);
  });

  it("deletes a Collection through the confirm dialog (recoverable)", async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findByText("Award winners");

    await user.click(screen.getByRole("button", { name: "Delete" }));
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));

    await waitFor(() => expect(deleteCollection).toHaveBeenCalledWith("col-1"));
  });

  it("edits the Smart Collection Source and sort expressions separately", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchSmartCollections).mockResolvedValue([
      {
        id: "smart-1",
        libraryID: "lib-1",
        name: "Recently added",
        sourceExpr: '{"all":[]}',
        sortExpr: '{"by":"addedAt"}',
      },
    ]);
    renderPage();

    // Switch to the Smart tab.
    await user.click(screen.getByRole("tab", { name: "Smart" }));
    await user.click(await screen.findByRole("button", { name: "Recently added" }));

    const sort = await screen.findByLabelText("Sort expression");
    await user.clear(sort);
    await user.type(sort, '{{"by":"title"}');
    await user.click(screen.getByRole("button", { name: "Save Source" }));

    await waitFor(() =>
      expect(updateSmartCollection).toHaveBeenCalledWith(
        expect.objectContaining({
          id: "smart-1",
          sourceExpr: '{"all":[]}',
          sortExpr: '{"by":"title"}',
        }),
      ),
    );
  });

  it("deletes a Smart Collection only through the confirm dialog", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchSmartCollections).mockResolvedValue([
      {
        id: "smart-1",
        libraryID: "lib-1",
        name: "Recently added",
        sourceExpr: "{}",
        sortExpr: "{}",
      },
    ]);
    renderPage();
    await user.click(screen.getByRole("tab", { name: "Smart" }));
    await screen.findByRole("button", { name: "Recently added" });

    await user.click(screen.getByRole("button", { name: "Remove" }));
    expect(deleteSmartCollection).not.toHaveBeenCalled();
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Remove" }));

    await waitFor(() => expect(deleteSmartCollection).toHaveBeenCalledWith("smart-1"));
  });

  it("surfaces a fixed error when the Collections query fails", async () => {
    vi.mocked(fetchCollections).mockRejectedValue(new Error("boom"));
    renderPage();
    expect(await screen.findByText("Collections are unavailable right now.")).toBeVisible();
  });
});
