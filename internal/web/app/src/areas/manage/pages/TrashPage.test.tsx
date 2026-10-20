import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchActivityLog, fetchTrashList, purgeTrash, restoreMedia } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { TrashPage } from "./TrashPage";

vi.mock("@/lib/graphql", () => ({
  fetchTrashList: vi.fn(),
  restoreMedia: vi.fn(),
  purgeTrash: vi.fn(),
  fetchActivityLog: vi.fn(),
}));

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(fetchTrashList).mockResolvedValue([
    { id: "media-9c08", title: "Untitled clip 9c08", trashedAt: "2026-06-14T09:12:00Z" },
  ]);
  vi.mocked(restoreMedia).mockResolvedValue([]);
  vi.mocked(purgeTrash).mockResolvedValue({ affected: 3 });
  vi.mocked(fetchActivityLog).mockResolvedValue([
    {
      id: "1",
      occurredAt: "2026-06-14T14:02:00Z",
      action: "media.trash",
      reversible: true,
      targetType: "media",
      targetID: "9c08",
    },
    {
      id: "2",
      occurredAt: "2026-06-14T11:20:00Z",
      action: "trash.purge",
      reversible: false,
      targetType: "trash",
    },
  ]);
});

function renderPage() {
  return render(
    <I18nProvider>
      <TrashPage />
    </I18nProvider>,
  );
}

describe("TrashPage", () => {
  it("lists retained Media and restores a row", async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findByText("Untitled clip 9c08");

    await user.click(screen.getByRole("button", { name: "Restore" }));
    await waitFor(() => expect(restoreMedia).toHaveBeenCalledWith("media-9c08"));
  });

  it("does not let an older refresh replace the latest trash list", async () => {
    let resolveOld: ((value: Awaited<ReturnType<typeof fetchTrashList>>) => void) | undefined;
    vi.mocked(fetchTrashList)
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveOld = resolve;
          }),
      )
      .mockResolvedValueOnce([]);
    const user = userEvent.setup();
    renderPage();
    await waitFor(() => expect(fetchTrashList).toHaveBeenCalledTimes(1));

    await user.click(screen.getAllByRole("button", { name: "Refresh" })[0]);
    expect(await screen.findByText(/Trash is empty/)).toBeInTheDocument();
    resolveOld?.([{ id: "stale", title: "Stale trash row", trashedAt: "2026-06-14T09:12:00Z" }]);

    await waitFor(() => expect(screen.queryByText("Stale trash row")).not.toBeInTheDocument());
    expect(screen.getByText(/Trash is empty/)).toBeInTheDocument();
  });

  it("confirm-gates the irreversible purge — button disabled until acknowledged", async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findByText("Untitled clip 9c08");

    const purgeBtn = screen.getByRole("button", { name: "Purge before cutoff" });
    expect(purgeBtn).toBeDisabled();

    // Pick a cutoff and acknowledge → button enables.
    const cutoff = document.getElementById("purge-before") as HTMLInputElement;
    await user.clear(cutoff);
    await user.type(cutoff, "2026-06-01T00:00");
    await user.click(screen.getByRole("checkbox"));
    expect(purgeBtn).toBeEnabled();

    // Trigger opens the confirm dialog; confirming calls purgeTrash with the
    // explicit confirm flag.
    await user.click(purgeBtn);
    await user.click(
      await screen.findByRole("button", { name: "Purge before cutoff", hidden: false }),
    );
    await waitFor(() => expect(purgeTrash).toHaveBeenCalledTimes(1));
    expect(vi.mocked(purgeTrash).mock.calls[0][1]).toBe(true);
  });

  it("surfaces a reversible-action undo and flags the irreversible purge", async () => {
    renderPage();
    await screen.findByText("media.trash");
    // media.trash row carries an Undo control; trash.purge row is flagged.
    expect(screen.getByRole("button", { name: "Undo" })).toBeInTheDocument();
    expect(screen.getByText("No undo")).toBeInTheDocument();
  });
});
