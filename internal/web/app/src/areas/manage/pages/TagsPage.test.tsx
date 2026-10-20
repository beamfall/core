import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  assignTag,
  bulkAssignTags,
  createTag,
  deleteTag,
  fetchTagAssignments,
  fetchTags,
  restoreTag,
  unassignTag,
} from "@/lib/graphql";
import type { TagInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { TagsPage } from "./TagsPage";

vi.mock("@/lib/graphql", () => ({
  fetchTags: vi.fn(),
  createTag: vi.fn(),
  updateTag: vi.fn(),
  deleteTag: vi.fn(),
  restoreTag: vi.fn(),
  fetchTagAssignments: vi.fn(),
  assignTag: vi.fn(),
  unassignTag: vi.fn(),
  bulkAssignTags: vi.fn(),
}));

const favorites: TagInfo = {
  id: "tag-fav",
  name: "Favorites",
  createdAt: "1718000000000",
  updatedAt: "1718000000000",
  assignmentCount: 42,
};
const child: TagInfo = {
  id: "tag-week",
  name: "This week",
  parentTagID: "tag-fav",
  createdAt: "1718000000000",
  updatedAt: "1718000000000",
  assignmentCount: 5,
};
const archive: TagInfo = {
  id: "tag-arc",
  name: "Archive",
  createdAt: "1718000000000",
  updatedAt: "1718000000000",
  assignmentCount: 96,
};
const trashed: TagInfo = {
  id: "tag-old",
  name: "Holiday 2025",
  trashedAt: "1718100000000",
  createdAt: "1718000000000",
  updatedAt: "1718100000000",
};

// Radix Select drives PointerEvents that jsdom does not implement; stub the
// capture probes so the dropdown opens under the test renderer.
beforeEach(() => {
  if (!Element.prototype.hasPointerCapture) {
    Element.prototype.hasPointerCapture = () => false;
    Element.prototype.setPointerCapture = () => {};
    Element.prototype.releasePointerCapture = () => {};
  }
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {};
  }
});

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(fetchTags).mockResolvedValue([favorites, child, archive, trashed]);
  vi.mocked(createTag).mockResolvedValue({
    ...archive,
    id: "tag-new",
    name: "New",
  });
  vi.mocked(deleteTag).mockResolvedValue({ ...favorites, trashedAt: "1" });
  vi.mocked(restoreTag).mockResolvedValue({ ...trashed, trashedAt: undefined });
  vi.mocked(fetchTagAssignments).mockResolvedValue({
    targetType: "media",
    targetID: "media-7f3a",
    libraryID: "lib-1",
    tags: [favorites],
  });
  vi.mocked(assignTag).mockResolvedValue({
    targetType: "media",
    targetID: "media-7f3a",
    libraryID: "lib-1",
    tags: [favorites, archive],
  });
  vi.mocked(unassignTag).mockResolvedValue({
    targetType: "media",
    targetID: "media-7f3a",
    libraryID: "lib-1",
    tags: [],
  });
  vi.mocked(bulkAssignTags).mockResolvedValue({ affected: 3, tags: [archive] });
});

function renderPage() {
  return render(
    <I18nProvider>
      <TagsPage />
    </I18nProvider>,
  );
}

describe("TagsPage", () => {
  // "Favorites"/"Archive" appear in both the tree and the bulk chip row, so
  // anchor render-readiness on the unique section lead instead.
  const ready = () =>
    screen.findByText(
      "Hierarchical, domain-agnostic Core Tags. Create, rename, reparent, and recover them.",
    );

  it("renders the live tag hierarchy from the tags query", async () => {
    renderPage();
    await ready();
    // "This week" is a child tag; its presence proves the hierarchy renders.
    expect((await screen.findAllByText("This week")).length).toBeGreaterThan(0);
    expect(screen.getAllByText("Favorites").length).toBeGreaterThan(0);
    // Always fetched including trashed so live + deleted share one query.
    expect(fetchTags).toHaveBeenCalledWith(true);
  });

  it("shows per-tag assignment count badges and the total chip", async () => {
    renderPage();
    await ready();
    // Mockup: each tree node trails an inline count badge; the panel head
    // carries a total chip ("3 tags" — Favorites/This week/Archive are live).
    expect(screen.getByText("42")).toBeVisible();
    expect(screen.getByText("96")).toBeVisible();
    expect(screen.getByText("3 tags")).toBeVisible();
  });

  it("offers a Move action that opens the reparent editor", async () => {
    const user = userEvent.setup();
    renderPage();
    await ready();
    // Mockup row-actions are Rename / Move / Delete; Move opens the same
    // editor focused on the parent select.
    const moveButtons = screen.getAllByRole("button", { name: "Move" });
    await user.click(moveButtons[0]);
    expect(await screen.findByRole("combobox", { name: "Move" })).toBeVisible();
  });

  it("creates a tag and reloads", async () => {
    const user = userEvent.setup();
    renderPage();
    await ready();

    await user.type(screen.getByLabelText("New tag"), "Rewatch");
    await user.click(screen.getByRole("button", { name: "Create tag" }));

    expect(createTag).toHaveBeenCalledWith("Rewatch", undefined);
    await waitFor(() => expect(fetchTags).toHaveBeenCalledTimes(2));
  });

  it("loads a target's assignments and unassigns a tag", async () => {
    const user = userEvent.setup();
    renderPage();
    await ready();

    await user.type(screen.getByLabelText("Target ID"), "media-7f3a");
    await user.click(screen.getByRole("button", { name: "Load" }));

    expect(fetchTagAssignments).toHaveBeenCalledWith("media", "media-7f3a");
    const unassign = await screen.findByRole("button", {
      name: "Unassign Favorites",
    });
    await user.click(unassign);
    expect(unassignTag).toHaveBeenCalledWith("tag-fav", "media", "media-7f3a");
  });

  it("bulk-assigns selected tags across targets in the chosen mode", async () => {
    const user = userEvent.setup();
    renderPage();
    await ready();

    await user.type(
      screen.getByLabelText("Target IDs (comma-separated)"),
      "media-7f3a, media-22b1, media-9c08",
    );
    // Pick "replace" mode, then add the Archive tag via the bulk add-picker
    // (the selection renders as a removable ×-pill, matching the mockup).
    await user.click(screen.getByRole("button", { name: "Replace" }));
    await user.click(screen.getByRole("combobox", { name: "Tags" }));
    await user.click(await screen.findByRole("option", { name: "Archive" }));
    await user.click(screen.getByRole("button", { name: "Apply" }));

    expect(bulkAssignTags).toHaveBeenCalledWith(
      ["tag-arc"],
      "media",
      ["media-7f3a", "media-22b1", "media-9c08"],
      "replace",
    );
    expect(await screen.findByText(/3 affected/)).toBeVisible();
  });

  it("reveals trashed tags and restores one", async () => {
    const user = userEvent.setup();
    renderPage();
    await ready();

    await user.click(screen.getByRole("switch"));
    expect(await screen.findByText("Holiday 2025")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Restore" }));

    expect(restoreTag).toHaveBeenCalledWith("tag-old");
    await waitFor(() => expect(fetchTags).toHaveBeenCalledTimes(2));
  });

  it("shows the unavailable error when the query fails", async () => {
    vi.mocked(fetchTags).mockRejectedValue(new Error("boom"));
    renderPage();
    expect(await screen.findByText("Tags are unavailable right now.")).toBeVisible();
  });
});
