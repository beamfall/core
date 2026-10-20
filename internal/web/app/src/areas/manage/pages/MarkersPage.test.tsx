import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  createMarker,
  deleteMarker,
  fetchMarkers,
  fetchTrashedMarkers,
  restoreMarker,
  updateMarker,
} from "@/lib/graphql";
import type { MarkerInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { MarkersPage } from "./MarkersPage";

vi.mock("@/lib/graphql", () => ({
  fetchMarkers: vi.fn(),
  fetchTrashedMarkers: vi.fn(),
  createMarker: vi.fn(),
  updateMarker: vi.fn(),
  deleteMarker: vi.fn(),
  restoreMarker: vi.fn(),
}));

const intro: MarkerInfo = {
  id: "marker-1",
  mediaID: "media-7f3a",
  title: "Opening titles",
  startSeconds: 0,
  endSeconds: 62,
  segmentKind: "intro",
  tags: ["skippable"],
};

const recap: MarkerInfo = {
  id: "marker-2",
  mediaID: "media-7f3a",
  title: "Previously on",
  startSeconds: 62,
  endSeconds: 118,
  segmentKind: "recap",
  tags: [],
};

const trashedMarker: MarkerInfo = {
  id: "marker-9",
  mediaID: "media-7f3a",
  title: "Sponsor read",
  startSeconds: 410,
  endSeconds: 430,
  segmentKind: "",
  tags: [],
  trashedAt: "2026-06-18T14:02:00Z",
};

beforeEach(() => {
  vi.mocked(fetchMarkers).mockReset().mockResolvedValue([intro, recap]);
  vi.mocked(fetchTrashedMarkers).mockReset().mockResolvedValue([trashedMarker]);
  vi.mocked(createMarker).mockReset().mockResolvedValue(intro);
  vi.mocked(updateMarker).mockReset().mockResolvedValue(intro);
  vi.mocked(deleteMarker).mockReset().mockResolvedValue(intro);
  vi.mocked(restoreMarker).mockReset().mockResolvedValue(trashedMarker);
});

function renderPage() {
  return render(
    <I18nProvider>
      <MarkersPage />
    </I18nProvider>,
  );
}

async function loadMedia(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText("Media ID"), "media-7f3a");
  await user.click(screen.getByRole("button", { name: "Load" }));
  await screen.findByDisplayValue("Opening titles");
}

describe("MarkersPage", () => {
  it("loads markers and trashed markers for a media id", async () => {
    const user = userEvent.setup();
    renderPage();
    await loadMedia(user);

    expect(fetchMarkers).toHaveBeenCalledWith("media-7f3a");
    expect(fetchTrashedMarkers).toHaveBeenCalledWith("media-7f3a");
    expect(screen.getByDisplayValue("Opening titles")).toBeVisible();
    expect(screen.getByDisplayValue("Previously on")).toBeVisible();
    // Trashed marker shows in the restore table.
    expect(screen.getByText("Sponsor read")).toBeVisible();
  });

  it("saves a marker's edited title through updateMarker", async () => {
    const user = userEvent.setup();
    renderPage();
    await loadMedia(user);

    const titleField = screen.getByDisplayValue("Opening titles");
    await user.clear(titleField);
    await user.type(titleField, "Main titles");
    // First Save button belongs to the first marker row.
    await user.click(screen.getAllByRole("button", { name: "Save" })[0]);

    await waitFor(() => {
      expect(updateMarker).toHaveBeenCalledWith(
        "marker-1",
        expect.objectContaining({ title: "Main titles", segmentKind: "intro" }),
      );
    });
  });

  // After a ↑/↓ swap the reload hands each row new seconds; the draft must
  // follow, or the next Save would write the pre-swap values back.
  it("resyncs the marker draft to the post-swap seconds", async () => {
    const user = userEvent.setup();
    renderPage();
    await loadMedia(user);

    vi.mocked(fetchMarkers).mockResolvedValue([
      { ...recap, startSeconds: 0, endSeconds: 56 },
      { ...intro, startSeconds: 62, endSeconds: 124 },
    ]);
    await user.click(screen.getAllByRole("button", { name: "Move later" })[0]);

    await waitFor(() => expect(updateMarker).toHaveBeenCalledTimes(2));
    const introRow = screen.getByDisplayValue("Opening titles").closest(".manage-section")!;
    await waitFor(() =>
      expect(within(introRow as HTMLElement).getByLabelText("Start (s)")).toHaveValue(62),
    );
    expect(within(introRow as HTMLElement).getByLabelText("End (s)")).toHaveValue(124);

    vi.mocked(updateMarker).mockClear();
    await user.click(within(introRow as HTMLElement).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(updateMarker).toHaveBeenCalledWith(
        "marker-1",
        expect.objectContaining({ startSeconds: 62, endSeconds: 124 }),
      ),
    );
  });

  it("creates a blank marker on the loaded media item", async () => {
    const user = userEvent.setup();
    renderPage();
    await loadMedia(user);

    await user.click(screen.getByRole("button", { name: "New marker" }));

    await waitFor(() => {
      expect(createMarker).toHaveBeenCalledWith(
        "media-7f3a",
        expect.objectContaining({ startSeconds: 0 }),
      );
    });
  });

  it("bulk-retags the selected markers in add mode", async () => {
    const user = userEvent.setup();
    renderPage();
    await loadMedia(user);

    // Select the first marker.
    await user.click(screen.getAllByRole("checkbox")[0]);
    expect(screen.getByText("1 selected")).toBeVisible();

    // Add a bulk tag, then apply. The bulk panel's tag input is the last
    // "Add tag…" field (after each marker row's own tag input).
    const tagInputs = screen.getAllByPlaceholderText("Add tag…");
    await user.type(tagInputs[tagInputs.length - 1], "review{enter}");
    await user.click(screen.getByRole("button", { name: "Retag 1" }));

    await waitFor(() => {
      expect(updateMarker).toHaveBeenCalledWith(
        "marker-1",
        expect.objectContaining({ tags: expect.arrayContaining(["review"]) }),
      );
    });
  });

  it("restores a soft-deleted marker", async () => {
    const user = userEvent.setup();
    renderPage();
    await loadMedia(user);

    const restoreRow = screen.getByText("Sponsor read").closest("tr")!;
    await user.click(within(restoreRow as HTMLElement).getByRole("button", { name: "Restore" }));

    await waitFor(() => {
      expect(restoreMarker).toHaveBeenCalledWith("marker-9");
    });
  });
});
