import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchFolderTree, fetchLibraries, fetchPlugins } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { LibrariesPage } from "./LibrariesPage";

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= ResizeObserverStub as unknown as typeof ResizeObserver;

vi.mock("@/lib/graphql", () => ({
  createLibrary: vi.fn(),
  createManagedFolder: vi.fn(),
  deleteManagedPath: vi.fn(),
  fetchFolderTree: vi.fn(),
  fetchLibraries: vi.fn(),
  fetchPlugins: vi.fn(),
  moveManagedPath: vi.fn(),
  renameManagedPath: vi.fn(),
  restoreManagedPath: vi.fn(),
  scanLibrary: vi.fn(),
  setLibraryPreviewClips: vi.fn(),
  setLibraryRoots: vi.fn(),
}));

describe("LibrariesPage folder tree", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(fetchLibraries).mockResolvedValue([
      {
        id: "library-1",
        title: "Library",
        pluginID: null,
        roots: ["/media"],
        previewClips: true,
        restricted: false,
      },
    ]);
    vi.mocked(fetchPlugins).mockResolvedValue([]);
    vi.mocked(fetchFolderTree).mockResolvedValue({
      name: "roots",
      path: "",
      kind: "folder",
      size: 0,
      modifiedAt: "",
      children: [
        {
          name: "Folder A",
          path: "/media/a",
          kind: "folder",
          size: 0,
          modifiedAt: "",
          children: [
            {
              name: "Child file",
              path: "/media/a/file",
              kind: "file",
              size: 10,
              modifiedAt: "",
            },
          ],
        },
        {
          name: "Folder B",
          path: "/media/b",
          kind: "folder",
          size: 0,
          modifiedAt: "",
          children: [],
        },
      ],
    });
  });

  it("exposes tree a11y semantics and keyboard selection", async () => {
    render(
      <I18nProvider>
        <LibrariesPage />
      </I18nProvider>,
    );
    const first = await screen.findByRole("treeitem", { name: /Folder A/ });
    const child = screen.getByRole("treeitem", { name: /Child file/ });
    const last = screen.getByRole("treeitem", { name: /Folder B/ });
    expect(first).toHaveAttribute("aria-expanded", "true");
    expect(first).toHaveAttribute("aria-level", "1");
    expect(child).not.toHaveAttribute("aria-expanded");
    expect(child).toHaveAttribute("aria-level", "2");
    expect(last).not.toHaveAttribute("aria-expanded");

    fireEvent.keyDown(child, { key: "Enter" });
    await waitFor(() => expect(child).toHaveAttribute("aria-selected", "true"));

    fireEvent.keyDown(first, { key: " " });
    await waitFor(() => expect(first).toHaveAttribute("aria-selected", "true"));
    expect(child).toHaveAttribute("aria-selected", "false");
  });

  it("provides roving focus and complete tree arrow-key navigation", async () => {
    const user = userEvent.setup();
    render(
      <I18nProvider>
        <LibrariesPage />
      </I18nProvider>,
    );
    const first = await screen.findByRole("treeitem", { name: /Folder A/ });
    const child = screen.getByRole("treeitem", { name: /Child file/ });
    const last = screen.getByRole("treeitem", { name: /Folder B/ });
    await waitFor(() => expect(first).toHaveAttribute("tabindex", "0"));
    expect(child).toHaveAttribute("tabindex", "-1");
    expect(last).toHaveAttribute("tabindex", "-1");

    first.focus();
    await user.keyboard("{ArrowRight}");
    expect(child).toHaveFocus();
    await user.keyboard("{ArrowRight}");
    expect(child).toHaveFocus();
    await user.keyboard("{ArrowLeft}");
    expect(first).toHaveFocus();
    await user.keyboard("{End}");
    expect(last).toHaveFocus();
    await user.keyboard("{Home}");
    expect(first).toHaveFocus();
    await user.keyboard("{ArrowDown}");
    expect(child).toHaveFocus();
    await user.keyboard("{ArrowUp}");
    expect(first).toHaveFocus();

    expect(screen.getAllByRole("treeitem").filter((item) => item.tabIndex === 0)).toHaveLength(1);
  });
});
