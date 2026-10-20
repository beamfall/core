import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  fetchFolderTree,
  fetchLibraries,
  fetchPlugins,
  fetchSkipDetectionEnabled,
  scanLibrary,
} from "@/lib/graphql";
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
  fetchFolderTree: vi.fn(),
  fetchLibraries: vi.fn(),
  fetchPlugins: vi.fn(),
  fetchSkipDetectionEnabled: vi.fn(),
  scanLibrary: vi.fn(),
  setLibraryPreviewClips: vi.fn(),
  setLibraryRoots: vi.fn(),
  setSkipDetectionEnabled: vi.fn(),
}));

describe("Console Libraries scan", () => {
  beforeEach(() => {
    vi.clearAllMocks();
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
      <I18nProvider>
        <Libraries />
      </I18nProvider>,
    );
  }

  it("offers only the Library roots and descendants", async () => {
    const user = userEvent.setup();
    renderLibraries();

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

    await user.click(await screen.findByRole("button", { name: "Scan" }));

    await waitFor(() => expect(scanLibrary).toHaveBeenCalledWith("library-1", "/Volumes/library"));
    expect(screen.queryByRole("alert")).toBeNull();
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
