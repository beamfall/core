import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { resetRescansForTest } from "@/components/beamfall/LibraryRescan";
import { setAdvancedSettings } from "@/lib/advancedSettings";
import {
  deleteLibrary,
  fetchFolderTree,
  fetchJobs,
  fetchLibraries,
  fetchPathPicker,
  fetchPlugins,
  fetchSkipDetectionEnabled,
  scanLibrary,
  setLibraryRoots,
} from "@/lib/graphql";
import type { LibraryInfo, PathPickerEntryInfo, PathPickerInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { Libraries } from "./Libraries";

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= ResizeObserverStub as unknown as typeof ResizeObserver;

vi.mock("@/lib/graphql", () => ({
  addPathPickerRoot: vi.fn(),
  createLibrary: vi.fn(),
  deleteLibrary: vi.fn(),
  fetchFolderTree: vi.fn(),
  fetchJobs: vi.fn(),
  fetchLibraries: vi.fn(),
  fetchPathPicker: vi.fn(),
  fetchPlugins: vi.fn(),
  fetchSkipDetectionEnabled: vi.fn(),
  fetchWatcherStatus: vi
    .fn()
    .mockResolvedValue([{ libraryID: "library-1", title: "Library", state: "watching" }]),
  scanLibrary: vi.fn(),
  setLibraryPreviewClips: vi.fn(),
  setLibraryRoots: vi.fn(),
  setSkipDetectionEnabled: vi.fn(),
}));

function pickerEntry(path: string, children?: PathPickerEntryInfo[]): PathPickerEntryInfo {
  return {
    name: path.slice(path.lastIndexOf("/") + 1),
    path,
    kind: "folder",
    accessError: "none",
    entryClass: "ordinary",
    selectable: true,
    mediaCount: 0,
    previewTimeoutMs: 1000,
    maxPreviewEntries: 50,
    truncated: false,
    children,
  };
}

// The folder browser's server answer: /mnt is the one location and holds /mnt/tv.
function pickerAnswer(path?: string): PathPickerInfo {
  const children: Record<string, PathPickerEntryInfo[]> = {
    "/mnt": [pickerEntry("/mnt/tv")],
    "/mnt/tv": [],
  };
  return {
    runtime: "bare",
    roles: ["libraryRoot"],
    role: "libraryRoot",
    purposeKey: "pathPicker.libraryRoot",
    previewTimeoutMs: 1000,
    maxPreviewEntries: 50,
    roots: [{ name: "mnt", path: "/mnt", entryClass: "mount-point" }],
    entries: path ? [pickerEntry(path, children[path])] : [pickerEntry("/mnt")],
  };
}

describe("Console Libraries scan", () => {
  afterEach(() => {
    localStorage.clear();
    setAdvancedSettings(false);
  });

  beforeEach(() => {
    // The one-folder scan is an Advanced control (CORE-1117).
    setAdvancedSettings(true);
    vi.clearAllMocks();
    resetRescansForTest();
    vi.mocked(fetchLibraries).mockResolvedValue([
      {
        id: "library-1",
        title: "Library",
        pluginID: null,
        roots: ["/Volumes/library"],
        previewClips: true,
        restricted: false,
      },
    ]);
    vi.mocked(fetchPlugins).mockResolvedValue([]);
    vi.mocked(fetchSkipDetectionEnabled).mockResolvedValue([]);
    vi.mocked(fetchFolderTree).mockResolvedValue({
      name: "roots",
      path: "",
      kind: "folder",
      size: 0,
      modifiedAt: "",
      children: [
        {
          name: "library",
          path: "/Volumes/library",
          kind: "folder",
          size: 0,
          modifiedAt: "",
          children: [
            {
              name: "incoming",
              path: "/Volumes/library/incoming",
              kind: "folder",
              size: 0,
              modifiedAt: "",
            },
          ],
        },
      ],
    });
    vi.mocked(scanLibrary).mockResolvedValue({ id: "scan-job", status: "queued" });
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

  async function openFolderScan() {
    await screen.findByText("Scan one folder");
  }

  it("rescans every library folder in one click and follows the jobs", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchLibraries).mockResolvedValue([
      {
        id: "library-1",
        title: "Library",
        pluginID: null,
        roots: ["/Volumes/library", "/Volumes/more"],
        previewClips: true,
        restricted: false,
      },
    ]);
    vi.mocked(scanLibrary)
      .mockResolvedValueOnce({ id: "job-a", status: "queued" })
      .mockResolvedValueOnce({ id: "job-b", status: "queued" });
    vi.mocked(fetchJobs).mockResolvedValue([
      {
        id: "job-a",
        kind: "scan",
        status: "completed",
        progress: { current: 4, total: 4, unit: "files" },
      },
      {
        id: "job-b",
        kind: "scan",
        status: "completed",
        progress: { current: 2, total: 2, unit: "files" },
      },
    ]);
    renderLibraries();

    await user.click(await screen.findByRole("button", { name: "Rescan library" }));

    expect(scanLibrary).toHaveBeenCalledWith("library-1", "/Volumes/library");
    expect(scanLibrary).toHaveBeenCalledWith("library-1", "/Volumes/more");
    expect(await screen.findByRole("status")).toHaveTextContent("Scanning");
    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Scan finished"), {
      timeout: 4000,
    });
  });

  it("reports a rescan with a folder that failed to queue as failed, not finished", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchLibraries).mockResolvedValue([
      {
        id: "library-1",
        title: "Library",
        pluginID: null,
        roots: ["/Volumes/library", "/Volumes/gone"],
        previewClips: true,
        restricted: false,
      },
    ]);
    vi.mocked(scanLibrary)
      .mockResolvedValueOnce({ id: "job-a", status: "queued" })
      .mockRejectedValueOnce(new Error("folder is missing"));
    vi.mocked(fetchJobs).mockResolvedValue([
      {
        id: "job-a",
        kind: "scan",
        status: "completed",
        progress: { current: 4, total: 4, unit: "files" },
      },
    ]);
    renderLibraries();

    await user.click(await screen.findByRole("button", { name: "Rescan library" }));

    expect(await screen.findByRole("status")).toHaveTextContent("Scanning");
    await waitFor(() => expect(fetchJobs).toHaveBeenCalled(), { timeout: 4000 });
    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("folder is missing"));
    expect(screen.getByRole("status")).not.toHaveTextContent("Scan finished");
  });

  it("offers only the Library roots and descendants", async () => {
    const user = userEvent.setup();
    renderLibraries();
    await openFolderScan();

    const picker = await screen.findByLabelText("Folder to scan");
    await waitFor(() => expect(picker).toHaveTextContent("/Volumes/library"));
    await user.click(picker);

    expect(await screen.findByRole("option", { name: "/Volumes/library" })).toBeVisible();
    expect(screen.getByRole("option", { name: "/Volumes/library/incoming" })).toBeVisible();
    expect(screen.queryByRole("option", { name: "/" })).toBeNull();
    expect(screen.queryByText("/Volumes/sibling")).toBeNull();
    expect(fetchFolderTree).toHaveBeenCalledWith("library-1", undefined, 2);
  });

  it("renders a rejected scan without leaking an unhandled rejection", async () => {
    const user = userEvent.setup();
    const unhandled = vi.fn();
    window.addEventListener("unhandledrejection", unhandled);
    vi.mocked(scanLibrary).mockRejectedValueOnce(new Error("folder is outside the library roots"));
    renderLibraries();
    await openFolderScan();

    await user.click(await screen.findByRole("button", { name: "Scan" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "folder is outside the library roots",
    );
    expect(unhandled).not.toHaveBeenCalled();
    window.removeEventListener("unhandledrejection", unhandled);
  });

  it("enqueues a valid in-root scan", async () => {
    const user = userEvent.setup();
    renderLibraries();
    await openFolderScan();

    await user.click(await screen.findByRole("button", { name: "Scan" }));

    await waitFor(() => expect(scanLibrary).toHaveBeenCalledWith("library-1", "/Volumes/library"));
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("saves an added folder at once", async () => {
    const user = userEvent.setup();
    renderLibraries();

    await user.type(await screen.findByLabelText("Add a folder"), "/Volumes/more");
    await user.click(screen.getByRole("button", { name: "Add folder" }));

    await waitFor(() =>
      expect(setLibraryRoots).toHaveBeenCalledWith("library-1", [
        "/Volumes/library",
        "/Volumes/more",
      ]),
    );
  });

  it("serializes folder saves so a quick second edit keeps the first", async () => {
    const user = userEvent.setup();
    let finishSave: () => void = () => {};
    vi.mocked(setLibraryRoots).mockImplementationOnce(
      () => new Promise<void>((resolve) => (finishSave = resolve)) as never,
    );
    renderLibraries();

    const field = await screen.findByLabelText("Add a folder");
    await user.type(field, "/Volumes/a");
    await user.click(screen.getByRole("button", { name: "Add folder" }));
    // The first save is still in flight, so the folder controls wait for it.
    await user.type(field, "x");
    expect(screen.getByRole("button", { name: "Add folder" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Remove /Volumes/library" })).toBeDisabled();

    finishSave();
    await waitFor(() => expect(screen.getByLabelText("Add a folder")).toHaveValue(""));
    // The library refresh still reports the old roots; the next save builds on the saved list.
    await user.type(field, "/Volumes/b");
    await user.click(screen.getByRole("button", { name: "Add folder" }));

    await waitFor(() =>
      expect(setLibraryRoots).toHaveBeenLastCalledWith("library-1", [
        "/Volumes/library",
        "/Volumes/a",
        "/Volumes/b",
      ]),
    );
    expect(setLibraryRoots).toHaveBeenCalledTimes(2);
  });

  it("keeps a path typed in another card and its folder browser through a folder save", async () => {
    const user = userEvent.setup();
    const second: LibraryInfo = {
      id: "library-2",
      title: "Second",
      pluginID: null,
      roots: ["/Volumes/second"],
      previewClips: true,
      restricted: false,
    };
    const first = (await fetchLibraries())[0];
    vi.mocked(fetchLibraries).mockClear().mockResolvedValue([first, second]);
    renderLibraries();

    const [firstField, secondField] = await screen.findAllByLabelText("Add a folder");
    await user.type(secondField, "/Volumes/unfinished");
    // The save starts a library refresh; hold it so its loading state is observable.
    let finishRefresh: (libraries: LibraryInfo[]) => void = () => {};
    vi.mocked(fetchLibraries).mockReturnValueOnce(
      new Promise<LibraryInfo[]>((resolve) => {
        finishRefresh = resolve;
      }),
    );
    await user.type(firstField, "/Volumes/more");
    await user.click(screen.getAllByRole("button", { name: "Add folder" })[0]);

    await waitFor(() => expect(fetchLibraries).toHaveBeenCalledTimes(2));
    expect(secondField).toBeInTheDocument();
    expect(secondField).toHaveValue("/Volumes/unfinished");

    finishRefresh([{ ...first, roots: ["/Volumes/library", "/Volumes/more"] }, second]);
    expect(await screen.findByText("/Volumes/more")).toBeInTheDocument();
    expect(screen.getAllByLabelText("Add a folder")[1]).toBe(secondField);
    expect(secondField).toHaveValue("/Volumes/unfinished");
  });

  it("holds the folder browser's Add while a save runs, so a second folder is not dropped", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchPathPicker).mockImplementation(({ path }) =>
      Promise.resolve(pickerAnswer(path ?? undefined)),
    );
    let finishSave: () => void = () => {};
    vi.mocked(setLibraryRoots).mockImplementationOnce(
      () => new Promise<void>((resolve) => (finishSave = resolve)) as never,
    );
    renderLibraries();

    await user.click(await screen.findByRole("button", { name: "Browse" }));
    const browser = screen.getByRole("group", { name: "Folder browser" });
    await user.click(await within(browser).findByRole("button", { name: "/mnt" }));
    const add = within(browser).getByRole("button", { name: "Add this folder" });
    await waitFor(() => expect(add).toBeEnabled());
    await user.click(add);
    // The first save is in flight: open the next folder and try to add it too.
    await user.click(await within(browser).findByRole("button", { name: "tv" }));
    await within(browser).findByText("No folders inside this one.");
    expect(add).toBeDisabled();
    await user.click(add);
    expect(setLibraryRoots).toHaveBeenCalledTimes(1);

    finishSave();
    // The browser is still on the second folder, and it can now be added.
    await waitFor(() => expect(add).toBeEnabled());
    expect(browser).toBeInTheDocument();
    await user.click(add);

    await waitFor(() =>
      expect(setLibraryRoots).toHaveBeenLastCalledWith("library-1", [
        "/Volumes/library",
        "/mnt",
        "/mnt/tv",
      ]),
    );
    await waitFor(() => expect(browser).not.toBeInTheDocument());
  });

  it("keeps the folder browser open when its folder could not be saved", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchPathPicker).mockImplementation(({ path }) =>
      Promise.resolve(pickerAnswer(path ?? undefined)),
    );
    vi.mocked(setLibraryRoots).mockRejectedValueOnce(new Error("folder overlaps another library"));
    renderLibraries();

    await user.click(await screen.findByRole("button", { name: "Browse" }));
    const browser = screen.getByRole("group", { name: "Folder browser" });
    await user.click(await within(browser).findByRole("button", { name: "/mnt" }));
    const add = within(browser).getByRole("button", { name: "Add this folder" });
    await waitFor(() => expect(add).toBeEnabled());
    await user.click(add);

    expect(await screen.findByRole("alert")).toHaveTextContent("folder overlaps another library");
    expect(browser).toBeInTheDocument();
    expect(add).toBeEnabled();
  });

  it("removes a folder only after the confirm", async () => {
    const user = userEvent.setup();
    renderLibraries();

    await user.click(await screen.findByRole("button", { name: "Remove /Volumes/library" }));
    expect(setLibraryRoots).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Remove" }));

    await waitFor(() => expect(setLibraryRoots).toHaveBeenCalledWith("library-1", []));
  });

  it("deletes a library through the confirm dialog and reloads the list (CORE-1171)", async () => {
    const user = userEvent.setup();
    vi.mocked(deleteLibrary).mockResolvedValue(undefined);
    renderLibraries();

    const card = await screen.findByRole("article", { name: "Library" });
    vi.mocked(fetchLibraries).mockResolvedValue([]);
    await user.click(within(card).getByRole("button", { name: "Delete library" }));
    const dialog = screen.getByRole("alertdialog", { name: "Delete Library?" });
    expect(deleteLibrary).not.toHaveBeenCalled();
    await user.click(within(dialog).getByRole("button", { name: "Delete library" }));

    await waitFor(() => expect(deleteLibrary).toHaveBeenCalledWith("library-1"));
    await waitFor(() =>
      expect(screen.queryByRole("article", { name: "Library" })).not.toBeInTheDocument(),
    );
  });
});

// Evidence — WEBUI-CONSOLE-SCAN-ERROR-SILENT-1 (2026-08-21)
// Mutation check: `npm run test -- src/areas/console/sections/Libraries.test.tsx` exited 1 after
// changing the required in-root path to `/Volumes/outside`. Observed failure:
// `expected "vi.fn()" to be called with arguments: [ 'library-1', '/Volumes/outside' ]`; received
// `[ "library-1", "/Volumes/library" ]`. The assertion above was then restored.
// Verify receipt: `npm run test`, head 70ee3aa6dc5e17f00caa9c57831714317cbb746a plus this
// ticket's working-tree diff, exit 0, elapsed 13.8 seconds, UTC 2026-08-21T21:48:09Z through
// 2026-08-21T21:48:31Z, log `/tmp/WEBUI-CONSOLE-SCAN-ERROR-SILENT-1-npm-test.log`;
// 93 files and 619 tests passed.
