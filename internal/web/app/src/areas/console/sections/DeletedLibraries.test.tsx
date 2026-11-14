import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { resetRescansForTest } from "@/components/beamfall/LibraryRescan";
import {
  deleteLibrary,
  fetchFolderTree,
  fetchLibraries,
  fetchPlugins,
  fetchSkipDetectionEnabled,
  fetchTrashedLibraries,
  purgeLibrary,
  restoreLibrary,
} from "@/lib/graphql";
import type { LibraryPurgeResult, TrashedLibraryInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { Libraries } from "./Libraries";

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= ResizeObserverStub as unknown as typeof ResizeObserver;

vi.mock("@/lib/graphql", () => ({
  createLibrary: vi.fn(),
  deleteLibrary: vi.fn(),
  fetchFolderTree: vi.fn(),
  fetchJobs: vi.fn(),
  fetchLibraries: vi.fn(),
  fetchPlugins: vi.fn(),
  fetchSkipDetectionEnabled: vi.fn(),
  fetchTrashedLibraries: vi.fn(),
  fetchWatcherStatus: vi.fn().mockResolvedValue([]),
  purgeLibrary: vi.fn(),
  restoreLibrary: vi.fn(),
  scanLibrary: vi.fn(),
  setLibraryPreviewClips: vi.fn(),
  setLibraryRoots: vi.fn(),
  setSkipDetectionEnabled: vi.fn(),
}));

// CORE-1201, openspec/changes/archive/2026-11-14-library-trash-restore.
describe("Console Libraries recently deleted", () => {
  const films = {
    id: "library-1",
    title: "Films",
    pluginID: null,
    roots: ["/Volumes/films"],
    previewClips: true,
    restricted: false,
  };
  // Midday UTC, so the formatted day is the same in every time zone.
  const deletedFilms: TrashedLibraryInfo = {
    id: "library-1",
    title: "Films",
    trashedAt: "2026-11-01T12:00:00Z",
    mediaCount: 3,
    purgeable: true,
    purgeAfter: "2026-12-01T12:00:00Z",
  };
  const purged: LibraryPurgeResult = {
    libraryID: "library-1",
    title: "Films",
    mediaRemoved: 3,
    entitiesRemoved: 1,
  };

  beforeEach(() => {
    vi.clearAllMocks();
    resetRescansForTest();
    vi.mocked(fetchLibraries).mockResolvedValue([]);
    vi.mocked(fetchPlugins).mockResolvedValue([]);
    vi.mocked(fetchSkipDetectionEnabled).mockResolvedValue([]);
    vi.mocked(fetchFolderTree).mockRejectedValue(new Error("no tree"));
    vi.mocked(fetchTrashedLibraries).mockResolvedValue([deletedFilms]);
    vi.mocked(deleteLibrary).mockResolvedValue(undefined);
    vi.mocked(restoreLibrary).mockResolvedValue(true);
    vi.mocked(purgeLibrary).mockResolvedValue(purged);
  });

  function renderLibraries() {
    return render(
      <MemoryRouter>
        <I18nProvider>
          <Libraries />
        </I18nProvider>
      </MemoryRouter>,
    );
  }

  async function deletedGroup() {
    return screen.findByRole("region", { name: "Recently deleted" });
  }

  async function openPurgeDialog(user: ReturnType<typeof userEvent.setup>) {
    await user.click(await screen.findByRole("button", { name: "Delete Films permanently" }));
    return screen.findByRole("alertdialog");
  }

  it("lists a deleted library with what it holds and when it goes for good", async () => {
    renderLibraries();

    const group = await deletedGroup();
    expect(within(group).getByText("Films")).toBeInTheDocument();
    expect(within(group).getByText(/Deleted Nov 1, 2026/)).toHaveTextContent(
      "Deleted Nov 1, 2026 · Media items: 3 · Removed for good on Dec 1, 2026",
    );
    expect(within(group).getByRole("button", { name: "Restore Films" })).toBeInTheDocument();
    expect(
      within(group).getByRole("button", { name: "Delete Films permanently" }),
    ).toBeInTheDocument();
  });

  it("shows no group when nothing is deleted or the list cannot be read", async () => {
    vi.mocked(fetchLibraries).mockResolvedValue([films]);
    vi.mocked(fetchTrashedLibraries).mockResolvedValue([]);
    const first = renderLibraries();
    await screen.findByRole("heading", { name: "Films" });
    await waitFor(() => expect(fetchTrashedLibraries).toHaveBeenCalledTimes(1));
    expect(screen.queryByText("Recently deleted")).toBeNull();
    first.unmount();

    // A server that does not serve the query yet leaves the section as it was.
    vi.mocked(fetchTrashedLibraries).mockRejectedValue(new Error("unknown field"));
    renderLibraries();
    await screen.findByRole("heading", { name: "Films" });
    await waitFor(() => expect(fetchTrashedLibraries).toHaveBeenCalledTimes(2));
    expect(screen.queryByText("Recently deleted")).toBeNull();
  });

  it("offers no permanent delete for a library the server keeps until restored", async () => {
    vi.mocked(fetchTrashedLibraries).mockResolvedValue([
      { ...deletedFilms, purgeable: false, purgeAfter: null },
    ]);
    renderLibraries();

    const group = await deletedGroup();
    expect(within(group).getByText(/Kept until you restore it/)).toBeInTheDocument();
    expect(within(group).getByRole("button", { name: "Restore Films" })).toBeInTheDocument();
    expect(within(group).queryByRole("button", { name: "Delete Films permanently" })).toBeNull();
  });

  it("restores without asking, once, then reloads both lists and says so", async () => {
    const user = userEvent.setup();
    let release: (restored: boolean) => void = () => {};
    vi.mocked(restoreLibrary).mockReturnValue(
      new Promise<boolean>((resolve) => {
        release = resolve;
      }),
    );
    renderLibraries();

    await user.click(await screen.findByRole("button", { name: "Restore Films" }));
    // A second activation while the request runs must not send a second restore,
    // and the entry's other control waits with it.
    await user.click(screen.getByRole("button", { name: "Restore Films" }));
    expect(restoreLibrary).toHaveBeenCalledTimes(1);
    expect(restoreLibrary).toHaveBeenCalledWith("library-1");
    expect(screen.getByRole("button", { name: "Restore Films" })).toHaveTextContent("Restoring…");
    expect(screen.getByRole("button", { name: "Delete Films permanently" })).toBeDisabled();
    expect(screen.queryByRole("alertdialog")).toBeNull();

    vi.mocked(fetchLibraries).mockResolvedValue([films]);
    vi.mocked(fetchTrashedLibraries).mockResolvedValue([]);
    release(true);

    const notice = await screen.findByText(
      "Restored “Films”. Beamfall is scanning its folders for changes.",
    );
    expect(await screen.findByRole("heading", { name: "Films" })).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByText("Recently deleted")).toBeNull());
    expect(fetchLibraries).toHaveBeenCalledTimes(2);
    expect(fetchTrashedLibraries).toHaveBeenCalledTimes(2);
    await waitFor(() => expect(notice).toHaveFocus());
  });

  it("keeps the entry and shows the reason when a restore is refused", async () => {
    const user = userEvent.setup();
    vi.mocked(restoreLibrary).mockRejectedValueOnce(new Error("admin session required"));
    renderLibraries();

    await user.click(await screen.findByRole("button", { name: "Restore Films" }));

    const alert = await within(await deletedGroup()).findByRole("alert");
    expect(alert).toHaveTextContent("Couldn't restore this library. Try again.");
    expect(alert).toHaveTextContent("admin session required");
    expect(fetchLibraries).toHaveBeenCalledTimes(1);

    // The same button retries.
    await user.click(screen.getByRole("button", { name: "Restore Films" }));
    await waitFor(() => expect(restoreLibrary).toHaveBeenCalledTimes(2));
  });

  it("confirms a permanent delete with the library name and what it removes", async () => {
    const user = userEvent.setup();
    renderLibraries();

    const dialog = await openPurgeDialog(user);

    expect(dialog).toHaveAccessibleName("Delete “Films” permanently?");
    expect(dialog).toHaveAccessibleDescription(
      /catalog records, watch history and generated artwork are removed and cannot be recovered/i,
    );
    expect(dialog).toHaveAccessibleDescription(/Media items: 3\./);
    expect(dialog).toHaveAccessibleDescription(/media files stay on disk/i);
    // The safe choice holds focus, so Enter alone never purges.
    await waitFor(() =>
      expect(within(dialog).getByRole("button", { name: "Cancel" })).toHaveFocus(),
    );
    expect(purgeLibrary).not.toHaveBeenCalled();
  });

  it("sends nothing when the permanent delete is cancelled or dismissed with Escape", async () => {
    const user = userEvent.setup();
    renderLibraries();

    const dialog = await openPurgeDialog(user);
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());

    await openPurgeDialog(user);
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());

    expect(purgeLibrary).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Restore Films" })).toBeInTheDocument();
  });

  it("purges once with the confirmation, reloads the group and reports the result", async () => {
    const user = userEvent.setup();
    let release: (result: LibraryPurgeResult) => void = () => {};
    vi.mocked(purgeLibrary).mockReturnValue(
      new Promise<LibraryPurgeResult>((resolve) => {
        release = resolve;
      }),
    );
    renderLibraries();

    const dialog = await openPurgeDialog(user);
    await user.click(within(dialog).getByRole("button", { name: "Delete permanently" }));
    // A second activation while the request runs must not send a second purge.
    await user.click(within(dialog).getByRole("button", { name: "Deleting…" }));
    expect(purgeLibrary).toHaveBeenCalledTimes(1);
    expect(purgeLibrary).toHaveBeenCalledWith("library-1", true);
    expect(within(dialog).getByRole("button", { name: "Cancel" })).toBeDisabled();

    vi.mocked(fetchTrashedLibraries).mockResolvedValue([]);
    release(purged);

    const notice = await screen.findByText(
      "Permanently deleted “Films”. Media items removed: 3. Its media files are still on disk.",
    );
    expect(screen.queryByRole("alertdialog")).toBeNull();
    await waitFor(() => expect(screen.queryByText("Recently deleted")).toBeNull());
    expect(fetchTrashedLibraries).toHaveBeenCalledTimes(2);
    await waitFor(() => expect(notice).toHaveFocus());
  });

  it("keeps the dialog open with the reason when a permanent delete is refused", async () => {
    const user = userEvent.setup();
    vi.mocked(purgeLibrary).mockRejectedValueOnce(new Error("library is custodial"));
    renderLibraries();

    const dialog = await openPurgeDialog(user);
    await user.click(within(dialog).getByRole("button", { name: "Delete permanently" }));

    const alert = await within(dialog).findByRole("alert");
    expect(alert).toHaveTextContent("Couldn't delete this library permanently. Try again.");
    expect(alert).toHaveTextContent("library is custodial");
    expect(screen.getByRole("button", { name: "Restore Films", hidden: true })).toBeInTheDocument();
    expect(fetchTrashedLibraries).toHaveBeenCalledTimes(1);
  });

  describe("Undo after a delete", () => {
    beforeEach(() => {
      vi.mocked(fetchLibraries).mockResolvedValue([films]);
      vi.mocked(fetchTrashedLibraries).mockResolvedValue([]);
    });

    async function deleteFilms(user: ReturnType<typeof userEvent.setup>) {
      await user.click(await screen.findByRole("button", { name: "Delete library" }));
      const dialog = await screen.findByRole("alertdialog");
      vi.mocked(fetchLibraries).mockResolvedValue([]);
      vi.mocked(fetchTrashedLibraries).mockResolvedValue([deletedFilms]);
      await user.click(within(dialog).getByRole("button", { name: "Delete library" }));
      await screen.findByText("Deleted “Films”. Its media files are still on disk.");
      return screen.findByRole("button", { name: "Undo" });
    }

    it("restores the library just deleted and lists it again", async () => {
      const user = userEvent.setup();
      renderLibraries();

      const undo = await deleteFilms(user);
      // The deleted library is also waiting in the group below.
      expect(await screen.findByRole("button", { name: "Restore Films" })).toBeInTheDocument();
      expect(undo).toHaveAccessibleDescription(
        "Deleted “Films”. Its media files are still on disk.",
      );

      vi.mocked(fetchLibraries).mockResolvedValue([films]);
      vi.mocked(fetchTrashedLibraries).mockResolvedValue([]);
      await user.click(undo);

      expect(restoreLibrary).toHaveBeenCalledTimes(1);
      expect(restoreLibrary).toHaveBeenCalledWith("library-1");
      expect(await screen.findByRole("heading", { name: "Films" })).toBeInTheDocument();
      const notice = await screen.findByText(
        "Restored “Films”. Beamfall is scanning its folders for changes.",
      );
      // The delete announcement and its Undo are gone with the delete.
      expect(screen.queryByText("Deleted “Films”. Its media files are still on disk.")).toBeNull();
      expect(screen.queryByRole("button", { name: "Undo" })).toBeNull();
      await waitFor(() => expect(screen.queryByText("Recently deleted")).toBeNull());
      await waitFor(() => expect(notice).toHaveFocus());
    });

    it("keeps Undo and shows the reason when the restore is refused", async () => {
      const user = userEvent.setup();
      vi.mocked(restoreLibrary).mockRejectedValueOnce(new Error("admin session required"));
      renderLibraries();

      const undo = await deleteFilms(user);
      await user.click(undo);

      const failure = await screen.findByText("admin session required");
      expect(failure.closest('[role="alert"]')).toHaveTextContent(
        "Couldn't restore this library. Try again.",
      );
      expect(screen.getByRole("button", { name: "Undo" })).toBeInTheDocument();
      expect(screen.queryByRole("heading", { name: "Films" })).toBeNull();
    });
  });
});
