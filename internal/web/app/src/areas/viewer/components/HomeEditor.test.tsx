import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { HomeConfigInfo, HomeFilterSchemaInfo } from "@/lib/graphql/types";
import {
  fetchHomeConfig,
  fetchHomeFilterSchema,
  previewHomeShelf,
  resetHomeConfig,
  saveHomeConfig,
} from "@/lib/graphql/viewer";
import { I18nProvider } from "@/lib/i18n";
import { HomeEditor } from "./HomeEditor";

vi.mock("@/lib/graphql/viewer", () => ({
  fetchHomeConfig: vi.fn(),
  fetchHomeFilterSchema: vi.fn(),
  previewHomeShelf: vi.fn(),
  saveHomeConfig: vi.fn(),
  resetHomeConfig: vi.fn(),
  saveDefaultHomeTemplate: vi.fn(),
  resetDefaultHomeTemplate: vi.fn(),
}));

const baseConfig: HomeConfigInfo = {
  customized: false,
  hasOwnerTemplate: false,
  canEditTemplate: true,
  hero: { enabled: true, mode: "auto" },
  shelves: [
    {
      id: "continue",
      title: "Continue",
      hidden: false,
      hideWhenEmpty: true,
      display: { style: "shelf", artworkSlot: "auto", limit: 20 },
      source: { type: "builtin", builtinKind: "continue-watching" },
    },
    {
      id: "recent",
      title: "Recent",
      hidden: false,
      hideWhenEmpty: true,
      display: { style: "shelf", artworkSlot: "auto", limit: 20 },
      source: { type: "builtin", builtinKind: "recently-added" },
    },
    {
      id: "watchlist",
      title: "Watchlist",
      hidden: false,
      hideWhenEmpty: true,
      display: { style: "shelf", artworkSlot: "auto", limit: 20 },
      source: { type: "builtin", builtinKind: "watchlist" },
    },
  ],
};

const baseSchema: HomeFilterSchemaInfo = {
  libraries: [
    { id: "lib-films", title: "Films" },
    { id: "lib-tv", title: "TV" },
  ],
  fields: [
    {
      key: "watched",
      label: "Watched",
      valueType: "boolean",
      group: "activity",
      operators: ["isTrue", "isFalse"],
    },
    {
      key: "rating",
      label: "Rating",
      valueType: "number",
      group: "activity",
      operators: ["gte", "lte", "between"],
    },
  ],
  pinnables: { collections: [], smartCollections: [], savedFilters: [] },
};

function renderEditor() {
  vi.mocked(fetchHomeConfig).mockResolvedValue(structuredClone(baseConfig));
  vi.mocked(fetchHomeFilterSchema).mockResolvedValue(baseSchema);
  vi.mocked(previewHomeShelf).mockResolvedValue({ totalCount: 0, items: [] });
  const onSaved = vi.fn();
  const onClose = vi.fn();
  render(
    <I18nProvider>
      <HomeEditor onSaved={onSaved} onClose={onClose} />
    </I18nProvider>,
  );
  return { onSaved, onClose };
}

function rowNames() {
  return screen.getAllByRole("listitem").map((row) => row.getAttribute("data-section-id"));
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("HomeEditor (Home Studio)", () => {
  it("reorders with buttons and the keyboard, hides a shelf, toggles hero, and saves", async () => {
    const { onSaved, onClose } = renderEditor();
    vi.mocked(saveHomeConfig).mockResolvedValue({ ...baseConfig, customized: true });

    fireEvent.click(await screen.findByRole("button", { name: "Move Recent up" }));
    expect(rowNames()).toEqual(["recent", "continue", "watchlist"]);
    expect(screen.getByRole("button", { name: "Move Recent up" })).toBeDisabled();

    const watchlistRow = screen.getAllByRole("listitem")[2];
    fireEvent.keyDown(watchlistRow, { key: "ArrowUp", altKey: true });
    expect(rowNames()).toEqual(["recent", "watchlist", "continue"]);

    fireEvent.click(within(watchlistRow).getByRole("switch", { name: "Show Watchlist on Home" }));
    fireEvent.click(screen.getByRole("switch", { name: "Show Hero banner on Home" }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(saveHomeConfig).toHaveBeenCalledWith(
      expect.objectContaining({
        hero: { enabled: false, mode: "auto" },
        shelves: expect.arrayContaining([
          expect.objectContaining({ id: "watchlist", hidden: true }),
        ]),
      }),
    );
    expect(onSaved).toHaveBeenCalled();
  });

  it("reorders by drag and drop", async () => {
    renderEditor();
    await screen.findByRole("button", { name: "Move Recent up" });
    const [first, , last] = screen.getAllByRole("listitem");
    fireEvent.dragStart(last);
    fireEvent.dragOver(first);
    fireEvent.drop(first);
    expect(rowNames()).toEqual(["watchlist", "continue", "recent"]);
  });

  it("adds a preset shelf, duplicates, and deletes", async () => {
    renderEditor();
    await screen.findByRole("button", { name: "Add shelf" });

    fireEvent.click(screen.getByRole("button", { name: "Add shelf" }));
    fireEvent.click(screen.getByRole("button", { name: "Split Recently Added by Library" }));
    expect(rowNames()).toHaveLength(5);

    fireEvent.click(screen.getByRole("button", { name: "Duplicate Continue" }));
    expect(rowNames()).toHaveLength(6);

    fireEvent.click(screen.getByRole("button", { name: "Remove Continue (Copy)" }));
    expect(rowNames()).toHaveLength(5);
  });

  it("resets to the default", async () => {
    const { onSaved, onClose } = renderEditor();
    vi.mocked(resetHomeConfig).mockResolvedValue(baseConfig);
    fireEvent.click(await screen.findByRole("button", { name: "Reset to default" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(resetHomeConfig).toHaveBeenCalled();
    expect(onSaved).toHaveBeenCalled();
    expect(saveHomeConfig).not.toHaveBeenCalled();
  });

  it("keeps editing and says so when saving fails", async () => {
    const { onClose } = renderEditor();
    vi.mocked(saveHomeConfig).mockRejectedValue(new Error("offline"));
    fireEvent.click(await screen.findByRole("button", { name: "Save" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Your Home could not be saved");
    expect(onClose).not.toHaveBeenCalled();
  });

  it("supports switching hero to a custom source and editing its type", async () => {
    vi.mocked(previewHomeShelf).mockResolvedValue({
      totalCount: 1,
      items: [
        {
          id: "m1",
          kind: "media",
          title: "Preview Film",
          artwork: { poster: "/art/m1.jpg" },
        },
      ],
    });
    renderEditor();
    const modeSelect = await screen.findByRole("combobox", { name: "Hero mode" });
    fireEvent.change(modeSelect, { target: { value: "shelf" } });
    fireEvent.click(screen.getByRole("button", { name: "Edit source" }));
    const dialog = await screen.findByRole("dialog", { name: "Hero Banner" });
    fireEvent.change(within(dialog).getByRole("combobox", { name: "Source type" }), {
      target: { value: "rules" },
    });
    expect(await within(dialog).findByText("Match ALL")).toBeInTheDocument();
  });
});
