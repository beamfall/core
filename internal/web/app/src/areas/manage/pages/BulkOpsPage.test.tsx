import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { bulkEditApply, bulkEditPreview, fetchLibraries } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { BulkOpsPage } from "./BulkOpsPage";

vi.mock("@/lib/graphql", () => ({
  bulkEditApply: vi.fn(),
  bulkEditPreview: vi.fn(),
  fetchLibraries: vi.fn(),
  organizeApply: vi.fn(),
  organizePreview: vi.fn(),
  undoBatch: vi.fn(),
}));

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(fetchLibraries).mockResolvedValue([]);
  vi.mocked(bulkEditPreview).mockResolvedValue([
    {
      mediaID: "media-1",
      libraryID: "lib-1",
      title: "One",
      old: [{ name: "year", type: "text", value: "1981" }],
      new: [{ name: "year", type: "text", value: "1982" }],
    },
    {
      mediaID: "media-2",
      libraryID: "lib-1",
      title: "Two",
      old: [{ name: "year", type: "text", value: "2010" }],
      new: [{ name: "year", type: "text", value: "1982" }],
    },
  ]);
  vi.mocked(bulkEditApply).mockResolvedValue({ batchID: "batch-1", kind: "edit", affected: 2 });
});

function renderPage() {
  return render(
    <I18nProvider>
      <BulkOpsPage />
    </I18nProvider>,
  );
}

describe("BulkOpsPage", () => {
  // HARD invariant #6: a destructive batch is confirmed against the REAL
  // previewed count. Apply is withheld until a preview exists for the current
  // inputs, and editing any input after the preview withdraws it again.
  it("offers Apply only after a preview of the current inputs, naming its count", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.type(screen.getByLabelText("Media IDs (comma-separated)"), "media-1, media-2");
    await user.type(screen.getByLabelText("Field name"), "year");
    await user.type(screen.getByLabelText("Value"), "1982");

    const apply = screen.getByRole("button", { name: "Apply edit" });
    expect(apply).toBeDisabled();

    await user.click(screen.getByRole("button", { name: "Preview" }));
    await screen.findByText("Two");
    expect(apply).toBeEnabled();

    // An input edit after the preview stales it: Apply is withdrawn until a
    // fresh preview matches the form again.
    await user.type(screen.getByLabelText("Media IDs (comma-separated)"), ", media-3");
    expect(apply).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Preview" }));
    await waitFor(() => expect(apply).toBeEnabled());

    await user.click(apply);
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText("2 affected")).toBeVisible();
    expect(bulkEditApply).not.toHaveBeenCalled();

    await user.click(within(dialog).getByRole("button", { name: "Apply edit" }));
    await waitFor(() =>
      expect(bulkEditApply).toHaveBeenCalledWith(
        ["media-1", "media-2", "media-3"],
        [{ name: "year", type: "text", value: "1982" }],
      ),
    );
  });
});
