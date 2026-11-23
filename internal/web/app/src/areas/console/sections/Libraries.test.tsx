import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { resetRescansForTest } from "@/components/beamfall/LibraryRescan";
import { setAdvancedSettings } from "@/lib/advancedSettings";
import { ApiError } from "@/lib/api";
import {
  browseServerFolders,
  createLibrary,
  deleteLibrary,
  disableScraperCredential,
  fetchConsoleConfig,
  fetchFolderTree,
  fetchJobs,
  fetchLibraries,
  fetchPathPicker,
  fetchPlugins,
  fetchSkipDetectionEnabled,
  refreshLibraryMetadata,
  scanLibrary,
  setLibraryRestricted,
  setLibraryRoots,
  setLibraryShowOnHome,
  setScraperCredential,
} from "@/lib/graphql";
import type {
  ConsoleConfigInfo,
  LibraryInfo,
  PathPickerEntryInfo,
  PathPickerInfo,
} from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { setManifestRefresher } from "@/lib/manifestRefresh";
import { type SessionState, useSession } from "@/lib/session";
import { Libraries } from "./Libraries";

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= ResizeObserverStub as unknown as typeof ResizeObserver;

vi.mock("@/lib/session", () => ({ useSession: vi.fn(() => ({ revealed: false })) }));
vi.mock("@/lib/graphql", () => ({
  addPathPickerRoot: vi.fn(),
  browseServerFolders: vi.fn(),
  createLibrary: vi.fn(),
  deleteLibrary: vi.fn(),
  disableScraperCredential: vi.fn(),
  fetchConsoleConfig: vi.fn().mockResolvedValue({ plugins: [] }),
  fetchFolderTree: vi.fn(),
  fetchJobs: vi.fn(),
  fetchLibraries: vi.fn(),
  fetchPathPicker: vi.fn(),
  fetchPlugins: vi.fn(),
  fetchSkipDetectionEnabled: vi.fn(),
  fetchTrashedLibraries: vi.fn().mockResolvedValue([]),
  fetchWatcherStatus: vi
    .fn()
    .mockResolvedValue([{ libraryID: "library-1", title: "Library", state: "watching" }]),
  refreshLibraryMetadata: vi.fn(),
  scanLibrary: vi.fn(),
  setLibraryPreviewClips: vi.fn(),
  setLibraryShowOnHome: vi.fn(),
  setLibraryRestricted: vi.fn(),
  setLibraryRoots: vi.fn(),
  setScraperCredential: vi.fn(),
  setSkipDetectionEnabled: vi.fn(),
  validateScraperCredential: vi.fn(),
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
  let refreshed: ReturnType<typeof vi.fn<() => void>>;

  afterEach(() => {
    localStorage.clear();
    setAdvancedSettings(false);
    setManifestRefresher();
  });

  beforeEach(() => {
    // The one-folder scan is an Advanced control (CORE-1117).
    setAdvancedSettings(true);
    vi.clearAllMocks();
    resetRescansForTest();
    refreshed = vi.fn();
    setManifestRefresher(refreshed);
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
    vi.mocked(browseServerFolders).mockResolvedValue({
      name: "library",
      path: "/Volumes/library",
      kind: "folder",
    } as never);
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

  it("marks a folder the server cannot find on the library card (CORE-1606)", async () => {
    vi.mocked(browseServerFolders).mockRejectedValue(new Error("folder does not exist"));
    renderLibraries();

    expect(await screen.findByText("Folder not found", {}, { timeout: 5000 })).toBeVisible();
    expect(screen.getByText(/Remove this folder and add the right one below/)).toBeVisible();
    expect(browseServerFolders).toHaveBeenCalledWith("/Volumes/library", 0);
  });

  it("opens the Library's folders when a Needs attention link targets them (CORE-2309)", async () => {
    const scrollIntoView = vi.fn();
    Element.prototype.scrollIntoView = scrollIntoView;
    render(
      <MemoryRouter initialEntries={["/console/libraries#folders-library-1"]}>
        <I18nProvider>
          <Libraries />
        </I18nProvider>
      </MemoryRouter>,
    );

    await screen.findByRole("button", { name: "Rescan library" });
    const folders = document.getElementById("folders-library-1");
    expect(folders?.closest("details")).toHaveAttribute("open");
    expect(scrollIntoView).toHaveBeenCalled();
    delete (Element.prototype as Partial<Element>).scrollIntoView;
  });

  it("keeps a healthy Library's card folded without a folders link", async () => {
    renderLibraries();

    await screen.findByRole("button", { name: "Rescan library" });
    await waitFor(() => expect(browseServerFolders).toHaveBeenCalled());
    expect(document.getElementById("folders-library-1")?.closest("details")).not.toHaveAttribute(
      "open",
    );
  });

  it("shows no folder warning when the server can read the folder", async () => {
    renderLibraries();

    await screen.findByRole("button", { name: "Rescan library" });
    await waitFor(() => expect(browseServerFolders).toHaveBeenCalled());
    expect(screen.queryByText("Folder not found")).toBeNull();
  });

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

    // The card's scan status reports the failure (CORE-2012).
    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Scan failed"));
    expect(screen.getByRole("status")).toHaveTextContent("folder is outside the library roots");
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

  it("acknowledges a one-folder scan and follows it to the end (CORE-2012)", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchJobs).mockResolvedValue([
      {
        id: "scan-job",
        kind: "scan",
        status: "completed",
        progress: { current: 3, total: 3, unit: "files" },
      },
    ]);
    renderLibraries();
    await openFolderScan();

    await user.click(await screen.findByRole("button", { name: "Scan" }));

    expect(await screen.findByRole("status")).toHaveTextContent("Scanning");
    // The folder's Scan button turns into a disabled "Scanning…" like the rescan buttons.
    expect(screen.queryByRole("button", { name: "Scan" })).toBeNull();
    for (const button of screen.getAllByRole("button", { name: "Scanning…" })) {
      expect(button).toBeDisabled();
    }
    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Scan finished"), {
      timeout: 4000,
    });
    expect(screen.getByRole("status")).toHaveTextContent("Checked 3 files.");
  });

  it("asks to unlock the Console when a one-folder scan is refused (CORE-2012)", async () => {
    const user = userEvent.setup();
    vi.mocked(scanLibrary).mockRejectedValueOnce(new ApiError("admin session required", 403));
    renderLibraries();
    await openFolderScan();

    await user.click(await screen.findByRole("button", { name: "Scan" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Unlock the Console, then scan again.",
    );
    expect(screen.queryByRole("status")).toBeNull();
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

  it("refreshes all of a Library's metadata only after a confirm that warns it takes a while (CORE-1136)", async () => {
    const user = userEvent.setup();
    vi.mocked(refreshLibraryMetadata).mockResolvedValue({ id: "job-1", status: "queued" });
    renderLibraries();

    const card = await screen.findByRole("article", { name: "Library" });
    await user.click(within(card).getByRole("button", { name: "Refresh all metadata" }));
    const dialog = screen.getByRole("alertdialog", {
      name: "Refresh metadata for every item in this library?",
    });
    expect(within(dialog).getByText(/Locked fields keep their values/)).toBeInTheDocument();
    expect(within(dialog).getByText(/may take a while/)).toBeInTheDocument();
    expect(refreshLibraryMetadata).not.toHaveBeenCalled();
    await user.click(within(dialog).getByRole("button", { name: "Refresh all metadata" }));

    await waitFor(() => expect(refreshLibraryMetadata).toHaveBeenCalledWith("library-1"));
    const status = await within(card).findByRole("status");
    expect(status).toHaveTextContent("Metadata refresh queued.");
    expect(within(status).getByRole("link", { name: "Follow it in Operations" })).toHaveAttribute(
      "href",
      "/console/operations",
    );
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });

  it("keeps the refresh dialog open and says so when the server refuses (CORE-1136)", async () => {
    const user = userEvent.setup();
    vi.mocked(refreshLibraryMetadata).mockRejectedValue(new Error("admin session required"));
    renderLibraries();

    const card = await screen.findByRole("article", { name: "Library" });
    await user.click(within(card).getByRole("button", { name: "Refresh all metadata" }));
    const dialog = screen.getByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Refresh all metadata" }));

    expect(await within(dialog).findByText("admin session required")).toBeInTheDocument();
    expect(within(card).queryByText("Metadata refresh queued.")).not.toBeInTheDocument();
  });

  it("keeps a Library off Home with its Show on Home switch and reloads the list (CORE-2360)", async () => {
    const user = userEvent.setup();
    vi.mocked(setLibraryShowOnHome).mockResolvedValue({ id: "library-1", showOnHome: false });
    renderLibraries();

    const card = await screen.findByRole("article", { name: "Library" });
    // A Library the server sends without the field is shown on Home (the default).
    const toggle = within(card).getByRole("switch", { name: /Show on Home/ });
    expect(toggle).toBeChecked();
    expect(within(card).getByText(/stays in the menu and in search/)).toBeInTheDocument();
    const loads = vi.mocked(fetchLibraries).mock.calls.length;

    toggle.focus();
    await user.keyboard(" ");

    await waitFor(() => expect(setLibraryShowOnHome).toHaveBeenCalledWith("library-1", false));
    await waitFor(() => expect(vi.mocked(fetchLibraries).mock.calls.length).toBeGreaterThan(loads));
  });

  it("shows a Library already hidden from Home with its switch off (CORE-2360)", async () => {
    vi.mocked(fetchLibraries).mockResolvedValue([
      {
        id: "library-1",
        title: "Library",
        pluginID: null,
        roots: ["/Volumes/library"],
        previewClips: true,
        showOnHome: false,
        restricted: false,
      },
    ]);
    renderLibraries();

    const card = await screen.findByRole("article", { name: "Library" });
    expect(within(card).getByRole("switch", { name: /Show on Home/ })).not.toBeChecked();
  });

  it("restricts a Library from an unlocked session and reloads the list (CORE-2463)", async () => {
    const user = userEvent.setup();
    vi.mocked(useSession).mockReturnValue({ revealed: true } as SessionState);
    vi.mocked(setLibraryRestricted).mockResolvedValue({ id: "library-1", restricted: true });
    renderLibraries();

    const card = await screen.findByRole("article", { name: "Library" });
    const toggle = within(card).getByRole("switch", { name: /Restricted/ });
    expect(toggle).not.toBeChecked();
    expect(toggle).toBeEnabled();
    const loads = vi.mocked(fetchLibraries).mock.calls.length;

    toggle.focus();
    await user.keyboard(" ");

    await waitFor(() => expect(setLibraryRestricted).toHaveBeenCalledWith("library-1", true));
    await waitFor(() => expect(vi.mocked(fetchLibraries).mock.calls.length).toBeGreaterThan(loads));
    expect(refreshed).toHaveBeenCalledTimes(1);
  });

  it("holds the Restricted switch until the session is unlocked (CORE-2463)", async () => {
    vi.mocked(useSession).mockReturnValue({ revealed: false } as SessionState);
    renderLibraries();

    const card = await screen.findByRole("article", { name: "Library" });
    expect(within(card).getByRole("switch", { name: /Restricted/ })).toBeDisabled();
    expect(within(card).getByText(/Unlock with the Restrictions PIN first/)).toBeInTheDocument();
    expect(setLibraryRestricted).not.toHaveBeenCalled();
  });

  it("says so when the server refuses a restriction change (CORE-2463)", async () => {
    const user = userEvent.setup();
    vi.mocked(useSession).mockReturnValue({ revealed: true } as SessionState);
    vi.mocked(setLibraryRestricted).mockRejectedValue(
      new Error("reveal required to change library restriction"),
    );
    renderLibraries();

    const card = await screen.findByRole("article", { name: "Library" });
    const toggle = within(card).getByRole("switch", { name: /Restricted/ });
    toggle.focus();
    await user.keyboard(" ");

    expect(
      await within(card).findByText("The change was not saved. Try again."),
    ).toBeInTheDocument();
    expect(refreshed).not.toHaveBeenCalled();
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

describe("Console Libraries add (CORE-1156)", () => {
  let refreshed: ReturnType<typeof vi.fn<() => void>>;
  const PLUGINS = [
    { id: "filmtv", displayName: "Film & TV" },
    { id: "music", displayName: "Music" },
    { id: "photos", displayName: "Photos" },
    { id: "audiobook", displayName: "Audiobooks" },
    { id: "familyarchive", displayName: "Family Archive" },
    { id: "karaoke", displayName: "Karaoke" },
  ];

  afterEach(() => setManifestRefresher());

  beforeEach(() => {
    vi.clearAllMocks();
    resetRescansForTest();
    refreshed = vi.fn();
    setManifestRefresher(refreshed);
    vi.mocked(fetchLibraries).mockResolvedValue([]);
    vi.mocked(fetchPlugins).mockResolvedValue(PLUGINS);
    vi.mocked(fetchSkipDetectionEnabled).mockResolvedValue([]);
    vi.mocked(scanLibrary).mockResolvedValue({ id: "scan-job", status: "queued" });
    vi.mocked(fetchJobs).mockResolvedValue([]);
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

  it("offers every media type from the server's plugins, with the Film & TV presets", async () => {
    renderLibraries();
    const group = await screen.findByRole("group", { name: "Media type" });
    for (const name of [
      "Films",
      "TV Shows",
      "Music",
      "Photos",
      "Audiobooks",
      "Family Archive",
      "Karaoke",
    ]) {
      expect(
        within(group).getByRole("radio", { name: new RegExp(`^${name}`) }),
      ).toBeInTheDocument();
    }
    expect(within(group).queryByRole("radio", { name: /No media type/ })).toBeNull();
    expect(within(group).queryByRole("radio", { checked: true })).toBeNull();
  });

  it("asks for the type and then a folder, and sends nothing until both are given", async () => {
    const user = userEvent.setup();
    renderLibraries();
    const create = await screen.findByRole("button", { name: "Create Library" });
    await screen.findByRole("radio", { name: /^Photos/ });

    await user.click(create);
    expect(screen.getByText("Choose the kind of media this library holds.")).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: /^Films/ })).toHaveFocus();

    await user.click(screen.getByRole("radio", { name: /^Photos/ }));
    await user.click(create);
    expect(screen.getByText("Add the folder this library's media is in.")).toBeInTheDocument();
    expect(screen.getByLabelText("Folder")).toHaveFocus();
    expect(screen.getByLabelText("Folder")).toHaveAttribute("aria-invalid", "true");
    expect(createLibrary).not.toHaveBeenCalled();
  });

  it("creates the library with its folders in one call and starts the scan", async () => {
    const user = userEvent.setup();
    vi.mocked(createLibrary).mockResolvedValue({
      id: "lib-new",
      title: "Films",
      pluginID: "filmtv",
    });
    renderLibraries();

    await user.click(await screen.findByRole("radio", { name: /^Films/ }));
    await user.type(screen.getByLabelText("Folder"), "/media/films");
    await user.click(screen.getByRole("button", { name: "Add another folder" }));
    await user.type(screen.getByLabelText("Folder 2"), "/media/more-films");
    await user.click(screen.getByRole("button", { name: "Create Library" }));

    await waitFor(() => {
      expect(createLibrary).toHaveBeenCalledWith("Films", "filmtv", "films", [
        "/media/films",
        "/media/more-films",
      ]);
    });
    expect(createLibrary).toHaveBeenCalledTimes(1);
    expect(refreshed).toHaveBeenCalledTimes(1);
    expect(setLibraryRoots).not.toHaveBeenCalled();
    await waitFor(() => {
      expect(scanLibrary).toHaveBeenCalledWith("lib-new", "/media/films");
    });
    expect(scanLibrary).toHaveBeenCalledWith("lib-new", "/media/more-films");
    expect(await screen.findByText("Added Films. Scanning its folders now.")).toHaveFocus();
  });

  it("shows the new library's card without a manual Refresh (CORE-1102)", async () => {
    const user = userEvent.setup();
    const created: LibraryInfo = {
      id: "lib-new",
      title: "Films",
      pluginID: "filmtv",
      roots: ["/media/films"],
      previewClips: true,
      restricted: false,
    };
    // The server lists the library only once the create has committed.
    vi.mocked(createLibrary).mockImplementation(async () => {
      vi.mocked(fetchLibraries).mockResolvedValue([created]);
      return { id: created.id, title: created.title, pluginID: created.pluginID };
    });
    renderLibraries();

    await user.click(await screen.findByRole("radio", { name: /^Films/ }));
    await user.type(screen.getByLabelText("Folder"), "/media/films");
    expect(screen.queryByRole("article", { name: "Films" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Create Library" }));

    expect(await screen.findByRole("article", { name: "Films" })).toBeInTheDocument();
  });

  it("keeps what was typed and shows the server's reason when the create is refused", async () => {
    const user = userEvent.setup();
    vi.mocked(createLibrary).mockRejectedValue(new Error("folder is not readable"));
    renderLibraries();

    await user.click(await screen.findByRole("radio", { name: /^Karaoke/ }));
    await user.type(screen.getByLabelText("Name"), "Party songs");
    await user.type(screen.getByLabelText("Folder"), "/media/karaoke");
    await user.click(screen.getByRole("button", { name: "Create Library" }));

    const alert = await screen.findByRole("alert", { name: "" });
    await waitFor(() => expect(alert).toHaveTextContent("folder is not readable"));
    expect(alert).toHaveTextContent("Could not");
    expect(createLibrary).toHaveBeenCalledWith("Party songs", "karaoke", undefined, [
      "/media/karaoke",
    ]);
    expect(screen.getByLabelText("Name")).toHaveValue("Party songs");
    expect(screen.getByLabelText("Folder")).toHaveValue("/media/karaoke");
    expect(screen.getByRole("radio", { name: /^Karaoke/ })).toBeChecked();
    expect(scanLibrary).not.toHaveBeenCalled();
    expect(refreshed).not.toHaveBeenCalled();
  });

  it("points to Plugins when no media type is available", async () => {
    vi.mocked(fetchPlugins).mockResolvedValue([]);
    renderLibraries();
    expect(await screen.findByText(/No media types are available yet/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open Plugins" })).toHaveAttribute(
      "href",
      "/console/plugins",
    );
  });
});

// CORE-1534: the Libraries page names a missing metadata-provider key and sets
// it in place; the stored value is never echoed back.
describe("Console Libraries provider keys (CORE-1534)", () => {
  function keyConfig(status: string, active = true): ConsoleConfigInfo {
    return {
      plugins: [
        {
          id: "catalog",
          active,
          scrapers: ["catalog-a", "catalog-b"].map((scraperID) => ({
            pluginID: "catalog",
            scraperID,
            displayName: "Example Catalog",
            kind: "metadata",
            endpoint: "https://api.example.org/3",
            authRef: "example-api-key",
            authStyle: "query",
            credentialRequired: true,
            credentialConfigured: status === "configured",
            credentialStatus: status,
          })),
        },
      ],
    } as unknown as ConsoleConfigInfo;
  }

  beforeEach(() => {
    vi.clearAllMocks();
    resetRescansForTest();
    vi.mocked(fetchLibraries).mockResolvedValue([
      {
        id: "library-1",
        title: "Library",
        pluginID: "catalog",
        roots: ["/Volumes/library"],
        previewClips: true,
        restricted: false,
      },
    ]);
    vi.mocked(fetchPlugins).mockResolvedValue([]);
    vi.mocked(fetchSkipDetectionEnabled).mockResolvedValue([]);
  });

  function renderPage() {
    return render(
      <MemoryRouter>
        <I18nProvider>
          <Libraries />
        </I18nProvider>
      </MemoryRouter>,
    );
  }

  it("asks for no key when no Library uses the plugin (CORE-2311)", async () => {
    const [library] = await fetchLibraries();
    vi.mocked(fetchLibraries).mockResolvedValue([{ ...library, pluginID: null }]);
    vi.mocked(fetchConsoleConfig).mockResolvedValue(keyConfig("missing"));
    renderPage();

    await screen.findByText("Library");
    await waitFor(() => expect(fetchConsoleConfig).toHaveBeenCalled());
    expect(screen.queryByRole("region", { name: "Metadata provider keys needed" })).toBeNull();
  });

  it("shows a missing key, sets it without echoing it back, then clears it", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchConsoleConfig).mockResolvedValue(keyConfig("missing"));
    renderPage();

    const panel = await screen.findByRole("region", { name: "Metadata provider keys needed" });
    expect(within(panel).getByText("Example Catalog")).toBeInTheDocument();
    expect(within(panel).getByText("Missing")).toBeInTheDocument();
    expect(within(panel).queryByRole("button", { name: "Disable" })).toBeNull();

    // Set: the value goes to the server once and the field empties.
    vi.mocked(setScraperCredential).mockResolvedValue({
      authRef: "example-api-key",
      credentialConfigured: true,
      credentialStatus: "configured",
      scraperIDs: ["catalog-a", "catalog-b"],
    });
    vi.mocked(fetchConsoleConfig).mockResolvedValue(keyConfig("configured"));
    const field = within(panel).getByLabelText("Example Catalog API key");
    await user.type(field, "example-secret-value");
    await user.click(within(panel).getByRole("button", { name: "Save" }));

    expect(setScraperCredential).toHaveBeenCalledWith("example-api-key", "example-secret-value");
    const configuredPanel = await screen.findByRole("region", { name: "Metadata provider keys" });
    expect(await within(configuredPanel).findByText("Configured")).toBeInTheDocument();
    expect(within(configuredPanel).getByLabelText("Example Catalog API key")).toHaveValue("");
    expect(screen.queryByDisplayValue("example-secret-value")).toBeNull();
    expect(document.body.textContent).not.toContain("example-secret-value");

    // Clear: Disable stores an empty key and the row reports it missing again.
    vi.mocked(disableScraperCredential).mockResolvedValue({
      authRef: "example-api-key",
      credentialConfigured: false,
      credentialStatus: "missing",
      scraperIDs: ["catalog-a", "catalog-b"],
    });
    vi.mocked(fetchConsoleConfig).mockResolvedValue(keyConfig("missing"));
    await user.click(within(configuredPanel).getByRole("button", { name: "Disable" }));

    expect(disableScraperCredential).toHaveBeenCalledWith("example-api-key");
    const clearedPanel = await screen.findByRole("region", {
      name: "Metadata provider keys needed",
    });
    expect(await within(clearedPanel).findByText("Missing")).toBeInTheDocument();
    expect(within(clearedPanel).getByLabelText("Example Catalog API key")).toHaveValue("");
  });

  it("shows no provider-key panel when every required key is configured", async () => {
    vi.mocked(fetchConsoleConfig).mockResolvedValue(keyConfig("configured"));
    renderPage();
    await screen.findByText("Library");
    await waitFor(() => expect(fetchConsoleConfig).toHaveBeenCalled());
    expect(screen.queryByRole("region", { name: /Metadata provider keys/ })).toBeNull();
  });

  it("ignores keys of inactive plugins", async () => {
    vi.mocked(fetchConsoleConfig).mockResolvedValue(keyConfig("missing", false));
    renderPage();
    await screen.findByText("Library");
    await waitFor(() => expect(fetchConsoleConfig).toHaveBeenCalled());
    expect(screen.queryByRole("region", { name: /Metadata provider keys/ })).toBeNull();
  });
});
