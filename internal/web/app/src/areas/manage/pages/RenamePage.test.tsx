import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchFolderTree, fetchLibraries } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { batchRenameManagedPaths, undoBatchRename } from "../lib/powertools";
import { RenamePage } from "./RenamePage";

vi.mock("@/lib/graphql", () => ({
  fetchLibraries: vi.fn(),
  fetchFolderTree: vi.fn(),
  fetchPathPicker: vi.fn().mockResolvedValue({ roles: ["libraryRoot"], roots: [], entries: [] }),
  addPathPickerRoot: vi.fn(),
}));
vi.mock("../lib/powertools", () => ({
  batchRenameManagedPaths: vi.fn(),
  undoBatchRename: vi.fn(),
}));

beforeEach(() => {
  vi.clearAllMocks();
  delete (window as { beamfallHost?: unknown }).beamfallHost;
  vi.mocked(fetchLibraries).mockResolvedValue([
    { id: "library-local", title: "Local", pluginID: null, previewClips: false, restricted: false },
  ]);
  vi.mocked(fetchFolderTree).mockResolvedValue({
    name: "Movies",
    path: "/srv/Movies",
    kind: "folder",
    size: 0,
    modifiedAt: "",
    children: [
      { name: "one.mp4", path: "/srv/Movies/one.mp4", kind: "file", size: 1, modifiedAt: "" },
      { name: "two.mp4", path: "/srv/Movies/two.mp4", kind: "file", size: 1, modifiedAt: "" },
    ],
  });
});

function renderPage() {
  return render(
    <I18nProvider>
      <RenamePage />
    </I18nProvider>,
  );
}

async function loadFiles(user: ReturnType<typeof userEvent.setup>) {
  await screen.findByText("Batch rename");
  await user.type(screen.getByLabelText("Folder"), "/srv/Movies");
  await user.click(screen.getByRole("button", { name: "Load files" }));
  // Under the default (no-op) pattern each file shows in both the Current and
  // New columns, so wait on the row count rather than a single unique match.
  await screen.findAllByText("one.mp4");
}

describe("RenamePage", () => {
  it("starts with the empty preview catalog state", async () => {
    renderPage();
    expect(await screen.findByText("Load a folder to preview renames.")).toBeInTheDocument();
  });

  it("previews a find/replace pattern over the loaded files", async () => {
    const user = userEvent.setup();
    renderPage();
    await loadFiles(user);

    await user.type(screen.getByLabelText("Find"), "one");
    await user.type(screen.getByLabelText("Replace"), "uno");

    await waitFor(() => expect(screen.getByText("uno.mp4")).toBeInTheDocument());
    // Apply button reflects the single changed file.
    expect(screen.getByRole("button", { name: /^Apply \(1\)$/ })).toBeEnabled();
  });

  it("applies a journaled batch then undoes it", async () => {
    vi.mocked(batchRenameManagedPaths).mockResolvedValue({
      batchID: "batch-7",
      kind: "rename",
      affected: 1,
    });
    vi.mocked(undoBatchRename).mockResolvedValue({
      batchID: "batch-7",
      kind: "rename",
      affected: 1,
    });
    const user = userEvent.setup();
    renderPage();
    await loadFiles(user);

    await user.type(screen.getByLabelText("Find"), "one");
    await user.type(screen.getByLabelText("Replace"), "uno");
    await user.click(screen.getByRole("button", { name: /^Apply \(1\)$/ }));
    // The batch is destructive: nothing is renamed until the dialog confirms.
    expect(batchRenameManagedPaths).not.toHaveBeenCalled();
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: /^Apply \(1\)$/ }));

    await waitFor(() =>
      expect(batchRenameManagedPaths).toHaveBeenCalledWith("library-local", [
        { path: "/srv/Movies/one.mp4", name: "uno.mp4" },
      ]),
    );
    await screen.findByText(/Renamed 1 file/);

    // Undo is enabled once a batch landed; it targets the kept batch id.
    const undoBtn = screen.getByRole("button", { name: "Undo" });
    expect(undoBtn).toBeEnabled();
    await user.click(undoBtn);
    await waitFor(() => expect(undoBatchRename).toHaveBeenCalledWith("batch-7"));
  });
});
