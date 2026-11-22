import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  bulkEditApply,
  clearMediaOrdinal,
  editMediaMetadata,
  fetchArtworkCandidates,
  fetchMediaFields,
  fetchMediaMetadata,
  pinArtworkCandidate,
  refreshMediaMetadata,
  setMediaFieldLock,
} from "@/lib/graphql";
import type { FieldLockInfo, MediaMetadataInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { ItemMetadataPage } from "./ItemMetadataPage";

const access = vi.hoisted(() => ({ manager: false }));
vi.mock("@/lib/session", () => ({
  useSession: () => ({ isManager: access.manager, session: { profile: "adult" } }),
}));

vi.mock("@/lib/graphql", () => ({
  searchResult: vi.fn(async () => ({
    mode: "keyword",
    items: [],
    hasMore: false,
    restrictedResultsPresent: false,
  })),
  bulkEditApply: vi.fn(),
  clearMediaOrdinal: vi.fn(),
  editMediaMetadata: vi.fn(),
  fetchArtworkCandidates: vi.fn(),
  fetchMediaFields: vi.fn(),
  fetchMediaMetadata: vi.fn(),
  pinArtworkCandidate: vi.fn(),
  refreshMediaMetadata: vi.fn(),
  setMediaFieldLock: vi.fn(),
}));

const FIXED = [
  "title",
  "sortTitle",
  "summary",
  "year",
  "artworkPoster",
  "artworkBackdrop",
  "artworkThumb",
];

function locks(lockedFields: string[] = [], extra: string[] = []): FieldLockInfo[] {
  return [...FIXED, ...extra].map((field) => ({ field, locked: lockedFields.includes(field) }));
}

function info(overrides: Partial<MediaMetadataInfo> = {}): MediaMetadataInfo {
  return {
    mediaID: "media-1",
    title: "Sample Item",
    sortTitle: "",
    summary: "An overview.",
    year: 1999,
    refreshable: true,
    locks: locks(),
    ...overrides,
  };
}

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <I18nProvider>
        <Routes>
          <Route path="/manage/items" element={<ItemMetadataPage />} />
          <Route path="/manage/items/:mediaID" element={<ItemMetadataPage />} />
        </Routes>
      </I18nProvider>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  access.manager = false;
  vi.clearAllMocks();
  vi.mocked(fetchMediaMetadata).mockResolvedValue(info());
  vi.mocked(fetchMediaFields).mockResolvedValue([
    { name: "genres", type: "text", value: "Drama", label: { en: "Genres" } },
  ]);
  vi.mocked(fetchArtworkCandidates).mockImplementation(async (_id, slot) =>
    slot === "poster"
      ? [
          {
            id: "cand-1",
            slot: "poster",
            applied: false,
            pluginID: "plugin-a",
            scraperID: "scraper-a",
            previewURL: "/api/media/artwork-candidate/cand-1",
          },
        ]
      : [],
  );
});

describe("ItemMetadataPage", () => {
  it.each([true, false])("shows Assign only for managers (%s)", async (manager) => {
    access.manager = manager;
    renderAt("/manage/items/media-1");
    await screen.findByLabelText("Title");
    const action = screen.queryByRole("button", { name: "Assign…" });
    if (manager) expect(action).toBeEnabled();
    else expect(action).not.toBeInTheDocument();
  });

  it.each([true, false])("exposes the operator ordinal only to managers (%s)", async (manager) => {
    access.manager = manager;
    vi.mocked(fetchMediaMetadata).mockResolvedValue(
      info({ operatorOrdinal: { sequence: "0", index: 0 } }),
    );
    vi.mocked(clearMediaOrdinal).mockResolvedValue(true);
    renderAt("/manage/items/media-1");
    await screen.findByLabelText("Title");
    const section = screen.queryByRole("region", { name: "Ordinal" });
    if (!manager) {
      expect(section).not.toBeInTheDocument();
      expect(clearMediaOrdinal).not.toHaveBeenCalled();
      return;
    }
    expect(section).toBeInTheDocument();
    const ordinal = within(section!);
    expect(ordinal.getByText("Sequence 0 · Index 0")).toBeVisible();
    await userEvent.click(ordinal.getByRole("button", { name: "Clear operator ordinal" }));
    expect(await ordinal.findByText("No operator ordinal is set.")).toBeVisible();
    expect(screen.getByText("Operator ordinal cleared.")).toBeVisible();
    expect(clearMediaOrdinal).toHaveBeenCalledExactlyOnceWith("media-1");
    expect(editMediaMetadata).not.toHaveBeenCalled();
  });

  it("preserves the displayed ordinal when clearing fails", async () => {
    access.manager = true;
    vi.mocked(fetchMediaMetadata).mockResolvedValue(
      info({ operatorOrdinal: { sequence: "A", index: 3 } }),
    );
    vi.mocked(clearMediaOrdinal).mockRejectedValueOnce(new Error("network"));
    renderAt("/manage/items/media-1");
    await userEvent.click(await screen.findByRole("button", { name: "Clear operator ordinal" }));
    expect(await screen.findByText("Could not clear the operator ordinal.")).toBeVisible();
    expect(screen.getByText("Sequence A · Index 3")).toBeVisible();
  });

  it("asks for a media ID when none is in the route", () => {
    renderAt("/manage/items");
    expect(screen.getByText("Enter a media ID to edit its details.")).toBeVisible();
    expect(fetchMediaMetadata).not.toHaveBeenCalled();
  });

  it("opens the item named in the route", async () => {
    renderAt("/manage/items");
    await userEvent.type(screen.getByLabelText("Media ID"), "media-1{Enter}");
    await userEvent.click(screen.getByRole("button", { name: "Load" }));
    expect(await screen.findByLabelText("Title")).toHaveValue("Sample Item");
    expect(fetchMediaMetadata).toHaveBeenCalledWith("media-1");
  });

  it("saves only the changed fields and shows them locked", async () => {
    vi.mocked(editMediaMetadata).mockResolvedValue(
      info({
        title: "Edited Item",
        sortTitle: "Item, Edited",
        locks: locks(["title", "sortTitle"]),
      }),
    );
    renderAt("/manage/items/media-1");
    const title = await screen.findByLabelText("Title");
    expect(screen.getByLabelText("Year")).toHaveValue("1999");
    expect(screen.getByLabelText("Overview")).toHaveValue("An overview.");

    await userEvent.clear(title);
    await userEvent.type(title, "Edited Item");
    await userEvent.type(screen.getByLabelText("Sort title"), "Item, Edited");
    await userEvent.click(screen.getAllByRole("button", { name: "Save" })[0]);

    await waitFor(() =>
      expect(editMediaMetadata).toHaveBeenCalledWith("media-1", {
        title: "Edited Item",
        sortTitle: "Item, Edited",
      }),
    );
    expect(await screen.findByText("Saved. Changed fields are now locked.")).toBeVisible();
    expect(screen.getByRole("button", { name: "Unlock Title" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Unlock Sort title" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Lock Overview" })).toBeVisible();
  });

  it("rejects an out-of-range year without calling the server", async () => {
    renderAt("/manage/items/media-1");
    const year = await screen.findByLabelText("Year");
    await userEvent.clear(year);
    await userEvent.type(year, "12345");
    await userEvent.click(screen.getAllByRole("button", { name: "Save" })[0]);
    expect(await screen.findByText("Year must be a whole number from 0 to 9999.")).toBeVisible();
    expect(editMediaMetadata).not.toHaveBeenCalled();
  });

  it("reports when nothing changed", async () => {
    renderAt("/manage/items/media-1");
    await screen.findByLabelText("Title");
    await userEvent.click(screen.getAllByRole("button", { name: "Save" })[0]);
    expect(await screen.findByText("Nothing has changed.")).toBeVisible();
    expect(editMediaMetadata).not.toHaveBeenCalled();
  });

  it("toggles a field lock", async () => {
    vi.mocked(fetchMediaMetadata).mockResolvedValue(info({ locks: locks(["title"]) }));
    vi.mocked(setMediaFieldLock).mockResolvedValue(locks([]));
    renderAt("/manage/items/media-1");
    await userEvent.click(await screen.findByRole("button", { name: "Unlock Title" }));
    expect(setMediaFieldLock).toHaveBeenCalledWith("media-1", "title", false);
    expect(await screen.findByRole("button", { name: "Lock Title" })).toBeVisible();
  });

  it("edits a plugin field such as genres and shows it locked", async () => {
    vi.mocked(bulkEditApply).mockResolvedValue({ batchID: "b1", kind: "bulk_edit", affected: 1 });
    renderAt("/manage/items/media-1");
    const genres = await screen.findByLabelText("Genres");
    expect(screen.getByRole("button", { name: "Lock Genres" })).toBeVisible();
    vi.mocked(fetchMediaMetadata).mockResolvedValue(info({ locks: locks(["genres"], ["genres"]) }));
    vi.mocked(fetchMediaFields).mockResolvedValue([
      { name: "genres", type: "text", value: "Drama, Mystery", label: { en: "Genres" } },
    ]);
    await userEvent.clear(genres);
    await userEvent.type(genres, "Drama, Mystery");
    await userEvent.click(screen.getAllByRole("button", { name: "Save" })[1]);
    await waitFor(() =>
      expect(bulkEditApply).toHaveBeenCalledWith(
        ["media-1"],
        [{ name: "genres", type: "text", value: "Drama, Mystery" }],
      ),
    );
    expect(await screen.findByRole("button", { name: "Unlock Genres" })).toBeVisible();
  });

  it("choosing artwork pins the candidate and locks the slot", async () => {
    vi.mocked(pinArtworkCandidate).mockResolvedValue({
      id: "cand-1",
      slot: "poster",
      applied: true,
    });
    vi.mocked(setMediaFieldLock).mockResolvedValue(locks(["artworkPoster"]));
    renderAt("/manage/items/media-1");
    await userEvent.click(await screen.findByRole("button", { name: "Choose" }));
    await waitFor(() =>
      expect(pinArtworkCandidate).toHaveBeenCalledWith("media-1", "poster", "cand-1"),
    );
    expect(setMediaFieldLock).toHaveBeenCalledWith("media-1", "artworkPoster", true);
    expect(await screen.findByRole("button", { name: "Unlock Poster" })).toBeVisible();
  });

  it("refresh re-runs the provider and keeps locked fields", async () => {
    vi.mocked(fetchMediaMetadata).mockResolvedValue(
      info({ title: "Admin Title", locks: locks(["title"]) }),
    );
    vi.mocked(refreshMediaMetadata).mockResolvedValue(
      info({ title: "Admin Title", summary: "Provider overview.", locks: locks(["title"]) }),
    );
    renderAt("/manage/items/media-1");
    await screen.findByLabelText("Title");
    await userEvent.click(screen.getByRole("button", { name: "Refresh details" }));
    await waitFor(() => expect(refreshMediaMetadata).toHaveBeenCalledWith("media-1"));
    expect(await screen.findByText("Refreshed. Locked fields kept their values.")).toBeVisible();
    expect(screen.getByLabelText("Title")).toHaveValue("Admin Title");
    expect(screen.getByLabelText("Overview")).toHaveValue("Provider overview.");
    expect(screen.getByRole("button", { name: "Unlock Title" })).toBeVisible();
  });

  it("disables refresh until the item has been identified", async () => {
    vi.mocked(fetchMediaMetadata).mockResolvedValue(info({ refreshable: false }));
    renderAt("/manage/items/media-1");
    expect(await screen.findByRole("button", { name: "Refresh details" })).toBeDisabled();
    expect(
      screen.getByText("Identify this item with Fix Match before refreshing it."),
    ).toBeVisible();
  });

  it("shows an error for an unknown item", async () => {
    vi.mocked(fetchMediaMetadata).mockRejectedValue(new Error("unknown media"));
    renderAt("/manage/items/missing");
    expect(await screen.findByText("Could not load this item.")).toBeVisible();
  });
});
