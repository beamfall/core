import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { uploadMediaArtwork } from "@/lib/api";
import {
  fetchArtworkCandidates,
  fetchMediaFields,
  fetchMediaMetadata,
  pinArtworkCandidate,
  refreshMediaMetadata,
  setMediaFieldLock,
} from "@/lib/graphql";
import type { FieldLockInfo, MediaMetadataInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { MediaMetadataEditor } from "./MediaMetadataEditor";

vi.mock("@/lib/api", () => ({ uploadMediaArtwork: vi.fn() }));
vi.mock("@/lib/graphql", () => ({
  bulkEditApply: vi.fn(),
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

function locks(lockedFields: string[] = []): FieldLockInfo[] {
  return FIXED.map((field) => ({ field, locked: lockedFields.includes(field) }));
}

function info(overrides: Partial<MediaMetadataInfo> = {}): MediaMetadataInfo {
  return {
    mediaID: "media-1",
    title: "Edited Title",
    sortTitle: "",
    summary: "",
    year: 2024,
    refreshable: true,
    locks: locks(["title"]),
    ...overrides,
  };
}

function renderEditor(onChanged = vi.fn()) {
  render(
    <I18nProvider>
      <MediaMetadataEditor
        mediaID="media-1"
        currentArtwork={{ poster: "/api/media/media-1/artwork/poster" }}
        onChanged={onChanged}
      />
    </I18nProvider>,
  );
  return onChanged;
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(fetchMediaMetadata).mockResolvedValue(info());
  vi.mocked(fetchMediaFields).mockResolvedValue([]);
  vi.mocked(fetchArtworkCandidates).mockResolvedValue([]);
});

describe("MediaMetadataEditor", () => {
  it("reverts a locked field to the provider by unlocking and refreshing", async () => {
    vi.mocked(setMediaFieldLock).mockResolvedValue(locks());
    vi.mocked(refreshMediaMetadata).mockResolvedValue(
      info({ title: "Provider Title", locks: locks() }),
    );
    const onChanged = renderEditor();

    await userEvent.click(await screen.findByRole("button", { name: "Revert Title to provider" }));

    expect(setMediaFieldLock).toHaveBeenCalledWith("media-1", "title", false, true);
    expect(refreshMediaMetadata).toHaveBeenCalledWith("media-1");
    expect(await screen.findByDisplayValue("Provider Title")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("Reverted. The provider's value is back.");
    expect(screen.queryByRole("button", { name: "Revert Title to provider" })).toBeNull();
    expect(onChanged).toHaveBeenCalled();
  });

  it("only unlocks when the item has no provider match to refresh from", async () => {
    vi.mocked(fetchMediaMetadata).mockResolvedValue(info({ refreshable: false }));
    vi.mocked(setMediaFieldLock).mockResolvedValue(locks());
    renderEditor();

    await userEvent.click(await screen.findByRole("button", { name: "Revert Title to provider" }));

    expect(setMediaFieldLock).toHaveBeenCalledWith("media-1", "title", false, true);
    expect(refreshMediaMetadata).not.toHaveBeenCalled();
    expect(await screen.findByRole("status")).toHaveTextContent("returns on the next refresh");
  });

  it("uploads artwork for a slot and shows the slot locked", async () => {
    vi.mocked(uploadMediaArtwork).mockResolvedValue();
    const onChanged = renderEditor();
    const preview = await screen.findByRole("img", { name: "Current Poster" });
    expect(preview).toHaveAttribute("src", "/api/media/media-1/artwork/poster");
    vi.mocked(fetchMediaMetadata).mockResolvedValue(
      info({ locks: locks(["title", "artworkPoster"]) }),
    );

    const file = new File(["png"], "poster.png", { type: "image/png" });
    fireEvent.change(screen.getByTestId("item-metadata-upload-poster"), {
      target: { files: [file] },
    });

    await waitFor(() => expect(uploadMediaArtwork).toHaveBeenCalledWith("media-1", "poster", file));
    expect(await screen.findByRole("status")).toHaveTextContent(
      "Uploaded. This slot is now locked.",
    );
    expect(screen.getByRole("button", { name: "Unlock Poster" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "Current Poster" }).getAttribute("src")).toMatch(
      /\?edit=1$/,
    );
    expect(onChanged).toHaveBeenCalled();
  });

  it("reports a failed upload", async () => {
    vi.mocked(uploadMediaArtwork).mockRejectedValue(new Error("unsupported image"));
    renderEditor();
    await screen.findByRole("button", { name: "Upload Poster" });

    const file = new File(["x"], "poster.gif", { type: "image/gif" });
    fireEvent.change(screen.getByTestId("item-metadata-upload-poster"), {
      target: { files: [file] },
    });

    expect(await screen.findByText(/Could not upload this image/)).toBeInTheDocument();
  });

  it("drops a candidate preview that fails to load but keeps its applied state", async () => {
    vi.mocked(fetchArtworkCandidates).mockImplementation(async (_mediaID, slot) =>
      slot === "poster"
        ? [
            {
              id: "upload-1",
              slot: "poster",
              applied: true,
              previewURL: "/api/media/artwork-candidate/token/upload-1",
            },
          ]
        : [],
    );
    const { container } = render(
      <I18nProvider>
        <MediaMetadataEditor mediaID="media-1" />
      </I18nProvider>,
    );
    await screen.findByText("In use");
    const tile = container.querySelector('img[src$="/upload-1"]');
    expect(tile).not.toBeNull();

    fireEvent.error(tile as Element);

    await waitFor(() => expect(container.querySelector('img[src$="/upload-1"]')).toBeNull());
    expect(screen.getByText("In use")).toBeInTheDocument();
  });

  it("keeps an unsaved title edit when artwork is uploaded", async () => {
    vi.mocked(uploadMediaArtwork).mockResolvedValue();
    renderEditor();
    const title = await screen.findByDisplayValue("Edited Title");
    await userEvent.clear(title);
    await userEvent.type(title, "Draft Title");
    vi.mocked(fetchMediaMetadata).mockResolvedValue(
      info({ locks: locks(["title", "artworkPoster"]) }),
    );

    fireEvent.change(screen.getByTestId("item-metadata-upload-poster"), {
      target: { files: [new File(["png"], "poster.png", { type: "image/png" })] },
    });

    expect(await screen.findByRole("button", { name: "Unlock Poster" })).toBeInTheDocument();
    expect(screen.getByDisplayValue("Draft Title")).toBeInTheDocument();
  });

  it("pins a candidate and reads the lock the server set", async () => {
    vi.mocked(fetchArtworkCandidates).mockImplementation(async (_mediaID, slot) =>
      slot === "poster" ? [{ id: "cand-1", slot: "poster", applied: false, previewURL: "" }] : [],
    );
    vi.mocked(pinArtworkCandidate).mockResolvedValue({
      id: "cand-1",
      slot: "poster",
      applied: true,
      previewURL: "",
    });
    renderEditor();
    const choose = await screen.findByRole("button", { name: "Choose" });
    vi.mocked(fetchMediaMetadata).mockResolvedValue(
      info({ locks: locks(["title", "artworkPoster"]) }),
    );
    await userEvent.click(choose);

    expect(await screen.findByRole("button", { name: "Unlock Poster" })).toBeInTheDocument();
    expect(pinArtworkCandidate).toHaveBeenCalledWith("media-1", "poster", "cand-1");
    expect(setMediaFieldLock).not.toHaveBeenCalled();
  });

  it("drops stale metadata and onInfoLoaded callbacks when mediaID prop changes", async () => {
    let resolveFirst!: (value: MediaMetadataInfo) => void;
    vi.mocked(fetchMediaMetadata).mockImplementation((id) => {
      if (id === "media-1") {
        return new Promise((resolve) => {
          resolveFirst = resolve;
        });
      }
      return Promise.resolve(info({ mediaID: "media-2", title: "Second Item" }));
    });
    const onInfoLoaded = vi.fn();
    const { rerender } = render(
      <I18nProvider>
        <MediaMetadataEditor mediaID="media-1" onInfoLoaded={onInfoLoaded} />
      </I18nProvider>,
    );

    rerender(
      <I18nProvider>
        <MediaMetadataEditor mediaID="media-2" onInfoLoaded={onInfoLoaded} />
      </I18nProvider>,
    );

    expect(await screen.findByDisplayValue("Second Item")).toBeInTheDocument();
    resolveFirst(info({ mediaID: "media-1", title: "Stale First Item" }));

    await waitFor(() => expect(screen.getByDisplayValue("Second Item")).toBeInTheDocument());
    expect(onInfoLoaded).toHaveBeenCalledWith(expect.objectContaining({ mediaID: "media-2" }));
    expect(onInfoLoaded).not.toHaveBeenCalledWith(expect.objectContaining({ mediaID: "media-1" }));
  });
});
