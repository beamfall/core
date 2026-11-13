import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { deleteLibrary } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { DeleteLibraryButton } from "./LibraryDelete";

vi.mock("@/lib/graphql", () => ({ deleteLibrary: vi.fn() }));

const library = { id: "lib-1", title: "Films" };

function renderButton(onDeleted = vi.fn()) {
  render(
    <I18nProvider>
      <DeleteLibraryButton library={library} onDeleted={onDeleted} />
    </I18nProvider>,
  );
  return onDeleted;
}

beforeEach(() => {
  vi.mocked(deleteLibrary).mockReset().mockResolvedValue(undefined);
});

afterEach(cleanup);

describe("DeleteLibraryButton", () => {
  it("names the library and says what deleting does before anything is sent", async () => {
    const user = userEvent.setup();
    renderButton();

    await user.click(screen.getByRole("button", { name: "Delete library" }));

    const dialog = screen.getByRole("alertdialog", { name: "Delete Films?" });
    expect(dialog).toHaveTextContent("every device");
    expect(dialog).toHaveTextContent("Files on disk are not touched");
    expect(dialog).toHaveTextContent("watch history is kept but hidden");
    expect(dialog).toHaveTextContent("cannot be restored yet");
    expect(dialog).toContainElement(document.activeElement as HTMLElement);
    expect(deleteLibrary).not.toHaveBeenCalled();
  });

  it("closes on Cancel and Escape without deleting and returns focus to the control", async () => {
    const user = userEvent.setup();
    const onDeleted = renderButton();
    const trigger = screen.getByRole("button", { name: "Delete library" });

    await user.click(trigger);
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();

    await user.click(trigger);
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();

    expect(deleteLibrary).not.toHaveBeenCalled();
    expect(onDeleted).not.toHaveBeenCalled();
  });

  it("disables both buttons while deleting, then closes and reloads", async () => {
    const user = userEvent.setup();
    let finish: () => void = () => {};
    vi.mocked(deleteLibrary).mockReturnValue(
      new Promise<void>((resolve) => {
        finish = resolve;
      }),
    );
    const onDeleted = renderButton();

    await user.click(screen.getByRole("button", { name: "Delete library" }));
    const dialog = screen.getByRole("alertdialog");
    await user.click(
      screen.getAllByRole("button", { name: "Delete library" }).at(-1) as HTMLElement,
    );

    expect(deleteLibrary).toHaveBeenCalledWith("lib-1");
    expect(screen.getByRole("button", { name: "Deleting…" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
    expect(dialog).toBeInTheDocument();

    finish();
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    expect(onDeleted).toHaveBeenCalledTimes(1);
  });

  it("keeps the dialog open with the error and lets the owner retry", async () => {
    const user = userEvent.setup();
    vi.mocked(deleteLibrary).mockRejectedValueOnce(new Error("admin session required"));
    const onDeleted = renderButton();

    await user.click(screen.getByRole("button", { name: "Delete library" }));
    const confirm = () =>
      screen.getAllByRole("button", { name: "Delete library" }).at(-1) as HTMLElement;
    await user.click(confirm());

    expect(await screen.findByRole("alert")).toHaveTextContent("admin session required");
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(confirm()).toBeEnabled();
    expect(onDeleted).not.toHaveBeenCalled();

    await user.click(confirm());
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    expect(deleteLibrary).toHaveBeenCalledTimes(2);
    expect(onDeleted).toHaveBeenCalledTimes(1);
  });
});
