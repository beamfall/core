import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  fetchActivityRecords,
  fetchDuplicateCopies,
  fetchTrashList,
  purgeTrash,
  restoreMedia,
  undoVersionMerge,
} from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { datetimeLocalDaysAgo, splitTrash, TrashPage } from "./TrashPage";

vi.mock("@/lib/graphql", () => ({
  fetchTrashList: vi.fn(),
  restoreMedia: vi.fn(),
  purgeTrash: vi.fn(),
  fetchActivityRecords: vi.fn(),
  fetchDuplicateCopies: vi.fn(),
  undoVersionMerge: vi.fn(),
}));

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(fetchTrashList).mockResolvedValue([
    { id: "media-9c08", title: "Untitled clip 9c08", trashedAt: "2026-06-14T09:12:00Z" },
  ]);
  vi.mocked(restoreMedia).mockResolvedValue([]);
  vi.mocked(fetchDuplicateCopies).mockResolvedValue([]);
  vi.mocked(undoVersionMerge).mockResolvedValue();
  vi.mocked(purgeTrash).mockResolvedValue({ affected: 3 });
  vi.mocked(fetchActivityRecords).mockResolvedValue([
    {
      id: "1",
      occurredAt: "2026-06-14T14:02:00Z",
      action: "media.trash",
      reversible: true,
      targetType: "media",
      targetID: "media-9c08",
    },
    {
      id: "2",
      occurredAt: "2026-06-14T11:20:00Z",
      action: "trash.purge",
      reversible: false,
      targetType: "trash",
    },
  ]);
});

function renderPage() {
  return render(
    <MemoryRouter>
      <I18nProvider>
        <TrashPage />
      </I18nProvider>
    </MemoryRouter>,
  );
}

describe("TrashPage", () => {
  it("lists retained Media and restores a row", async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findByText("Untitled clip 9c08");

    await user.click(screen.getByRole("button", { name: "Restore" }));
    await waitFor(() => expect(restoreMedia).toHaveBeenCalledWith("media-9c08"));
  });

  it("does not let an older refresh replace the latest trash list", async () => {
    let resolveOld: ((value: Awaited<ReturnType<typeof fetchTrashList>>) => void) | undefined;
    vi.mocked(fetchTrashList)
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveOld = resolve;
          }),
      )
      .mockResolvedValueOnce([]);
    const user = userEvent.setup();
    renderPage();
    await waitFor(() => expect(fetchTrashList).toHaveBeenCalledTimes(1));

    await user.click(screen.getAllByRole("button", { name: "Refresh" })[0]);
    expect(await screen.findByText(/Trash is empty/)).toBeInTheDocument();
    resolveOld?.([{ id: "stale", title: "Stale trash row", trashedAt: "2026-06-14T09:12:00Z" }]);

    await waitFor(() => expect(screen.queryByText("Stale trash row")).not.toBeInTheDocument());
    expect(screen.getByText(/Trash is empty/)).toBeInTheDocument();
  });

  it("confirm-gates the irreversible purge — button disabled until acknowledged", async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findByText("Untitled clip 9c08");

    const purgeBtn = screen.getByRole("button", { name: "Purge before cutoff" });
    expect(purgeBtn).toBeDisabled();

    // Pick a cutoff and acknowledge → button enables.
    const cutoff = document.getElementById("purge-before") as HTMLInputElement;
    await user.clear(cutoff);
    await user.type(cutoff, "2026-06-01T00:00");
    await user.click(screen.getByRole("checkbox"));
    expect(purgeBtn).toBeEnabled();

    // Trigger opens the confirm dialog; confirming calls purgeTrash with the
    // explicit confirm flag.
    await user.click(purgeBtn);
    await user.click(
      await screen.findByRole("button", { name: "Purge before cutoff", hidden: false }),
    );
    await waitFor(() => expect(purgeTrash).toHaveBeenCalledTimes(1));
    expect(vi.mocked(purgeTrash).mock.calls[0][1]).toBe(true);
  });

  it("fills the cutoff from a quick pick, and the purge stays gated", async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findByText("Untitled clip 9c08");
    expect(
      screen.getByRole("button", { name: "Help: Purge items trashed before" }),
    ).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Older than 30 days" }));
    const cutoff = document.getElementById("purge-before") as HTMLInputElement;
    expect(cutoff.value).toBe(datetimeLocalDaysAgo(30));
    expect(screen.getByRole("button", { name: "Purge before cutoff" })).toBeDisabled();
  });

  it("formats a local datetime N days back", () => {
    expect(datetimeLocalDaysAgo(30, new Date(2026, 2, 15, 9, 5))).toBe("2026-02-13T09:05");
  });

  it("surfaces a reversible-action undo and flags the irreversible purge", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByText("Activity"));
    // The row reads as a sentence; the raw event key stays on the cell for hover.
    await screen.findByTitle("media.trash");
    // media.trash row carries an Undo control that restores the item; trash.purge row is flagged.
    const undoBtn = screen.getByRole("button", { name: "Undo" });
    expect(undoBtn).toBeInTheDocument();
    expect(screen.getByText("No undo")).toBeInTheDocument();
    await user.click(undoBtn);
    await waitFor(() => expect(restoreMedia).toHaveBeenCalledWith("media-9c08"));
  });

  it("lists merged copies apart from deletions, by batch, with Undo merge", async () => {
    const merge = {
      batchID: "batch-1",
      survivorID: "media-keep",
      survivorTitle: "Harbour Lights",
      mergedAt: "2026-11-20T10:00:00Z",
    };
    vi.mocked(fetchTrashList).mockResolvedValue([
      { id: "media-9c08", title: "Untitled clip 9c08", trashedAt: "2026-06-14T09:12:00Z" },
      { id: "m-a", title: "Harbour Lights copy", trashedAt: merge.mergedAt, mergedInto: merge },
      { id: "m-b", title: "Harbour Lights (1)", trashedAt: merge.mergedAt, mergedInto: merge },
    ]);
    const user = userEvent.setup();
    renderPage();

    await screen.findByText("Harbour Lights copy, Harbour Lights (1)");
    // Deletions keep Restore; merged rows offer Undo merge instead.
    expect(screen.getAllByRole("button", { name: "Restore" })).toHaveLength(1);
    expect(screen.getByText("1 retained")).toBeInTheDocument();
    expect(screen.getByText("As a Version (automatic, scan)")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Harbour Lights" })).toHaveAttribute(
      "href",
      "/manage/items/media-keep",
    );

    await user.click(screen.getByRole("button", { name: "Undo merge into Harbour Lights" }));
    await waitFor(() => expect(undoVersionMerge).toHaveBeenCalledWith("batch-1"));
    await waitFor(() => expect(fetchTrashList).toHaveBeenCalledTimes(2));
    // The undone merge moved files back, so the duplicate report reloads.
    await waitFor(() => expect(fetchDuplicateCopies).toHaveBeenCalledTimes(2));
  });

  it("keeps a merge with a redacted survivor as a merge, without a link or Undo", async () => {
    const merge = {
      batchID: "batch-2",
      survivorID: "",
      survivorTitle: "",
      mergedAt: "2026-11-20T10:00:00Z",
    };
    vi.mocked(fetchTrashList).mockResolvedValue([
      { id: "m-a", title: "Folded copy", trashedAt: merge.mergedAt, mergedInto: merge },
    ]);
    renderPage();

    expect(await screen.findByText("Another item")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Restore" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Undo merge/ })).not.toBeInTheDocument();
    expect(screen.getByText("Another item").closest("a")).toBeNull();
  });

  it("keeps the latest duplicate report when an older request resolves last", async () => {
    const merge = {
      batchID: "batch-1",
      survivorID: "media-keep",
      survivorTitle: "Harbour Lights",
      mergedAt: "2026-11-20T10:00:00Z",
    };
    vi.mocked(fetchTrashList).mockResolvedValue([
      { id: "m-a", title: "Harbour Lights copy", trashedAt: merge.mergedAt, mergedInto: merge },
    ]);
    const set = (path: string) => ({
      mediaID: "media-keep",
      title: "Harbour Lights",
      libraryID: "lib-1",
      copies: [
        { path, sizeBytes: 1, label: "" },
        { path: `${path}.2`, sizeBytes: 1, label: "" },
      ],
    });
    let resolveStale: (value: ReturnType<typeof set>[]) => void = () => {};
    vi.mocked(fetchDuplicateCopies)
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveStale = resolve;
          }),
      )
      .mockResolvedValueOnce([set("/media/after-undo.mkv")]);
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: "Undo merge into Harbour Lights" }));
    await waitFor(() => expect(fetchDuplicateCopies).toHaveBeenCalledTimes(2));
    resolveStale([set("/media/before-undo.mkv")]);
    await user.click(screen.getByText("Duplicate copies on disk"));

    expect(await screen.findByText("/media/after-undo.mkv")).toBeInTheDocument();
    expect(screen.queryByText("/media/before-undo.mkv")).not.toBeInTheDocument();
  });

  it("says no deleted items, not an empty Trash, when only merges remain", async () => {
    const merge = {
      batchID: "batch-1",
      survivorID: "media-keep",
      survivorTitle: "Harbour Lights",
      mergedAt: "2026-11-20T10:00:00Z",
    };
    vi.mocked(fetchTrashList).mockResolvedValue([
      { id: "m-a", title: "Harbour Lights copy", trashedAt: merge.mergedAt, mergedInto: merge },
    ]);
    renderPage();

    expect(await screen.findByText("No deleted items.")).toBeInTheDocument();
    expect(screen.queryByText(/Trash is empty/)).not.toBeInTheDocument();
  });

  it("shows duplicate copies on disk read-only", async () => {
    vi.mocked(fetchDuplicateCopies).mockResolvedValue([
      {
        mediaID: "media-keep",
        title: "Harbour Lights",
        libraryID: "lib-1",
        copies: [
          { path: "/media/a/Harbour Lights.mkv", sizeBytes: 2_000_000_000, label: "" },
          { path: "/media/b/Harbour Lights.mkv", sizeBytes: 2_000_000_000, label: "Version 2" },
        ],
      },
    ]);
    renderPage();
    await userEvent.setup().click(await screen.findByText("Duplicate copies on disk"));

    expect(await screen.findByText("/media/b/Harbour Lights.mkv")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /delete/i })).not.toBeInTheDocument();
  });

  it("groups merged entries by batch and keeps deletions apart", () => {
    const m = (batchID: string, mergedAt: string) => ({
      batchID,
      survivorID: `s-${batchID}`,
      survivorTitle: "",
      mergedAt,
    });
    const { deleted, merges } = splitTrash([
      { id: "d", title: "d", trashedAt: "2026-11-01T00:00:00Z" },
      { id: "a1", title: "a1", trashedAt: "x", mergedInto: m("a", "2026-11-19T00:00:00Z") },
      { id: "b1", title: "b1", trashedAt: "x", mergedInto: m("b", "2026-11-20T00:00:00Z") },
      { id: "a2", title: "a2", trashedAt: "x", mergedInto: m("a", "2026-11-19T00:00:00Z") },
    ]);
    expect(deleted.map((e) => e.id)).toEqual(["d"]);
    expect(merges.map((b) => [b.merge.batchID, b.entries.map((e) => e.id)])).toEqual([
      ["b", ["b1"]],
      ["a", ["a1", "a2"]],
    ]);
  });
});
