import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { ConfirmButton } from "./ConfirmButton";

function renderButton(onConfirm: () => void | Promise<void>) {
  render(
    <I18nProvider>
      <ConfirmButton label="Revoke" confirm="cancel" onConfirm={onConfirm} />
    </I18nProvider>,
  );
}

async function openAndConfirm(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: "Revoke" }));
  const dialog = await screen.findByRole("alertdialog");
  await user.click(screen.getByRole("button", { name: "Revoke" }));
  return dialog;
}

describe("ConfirmButton (CORE-1207)", () => {
  it("closes the dialog once the confirmed action resolves", async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn().mockResolvedValue(undefined);
    renderButton(onConfirm);

    await openAndConfirm(user);

    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it("keeps the dialog open with the reason when the action rejects, and retries", async () => {
    const user = userEvent.setup();
    const onConfirm = vi
      .fn()
      .mockRejectedValueOnce(new Error("device is busy"))
      .mockResolvedValueOnce(undefined);
    renderButton(onConfirm);

    const dialog = await openAndConfirm(user);

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Request failed.");
    expect(alert).toHaveTextContent("device is busy");
    expect(dialog).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Try again" }));

    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    expect(onConfirm).toHaveBeenCalledTimes(2);
  });

  it("ignores a second confirm and a dismissal while the action is in flight", async () => {
    const user = userEvent.setup();
    let settle: () => void = () => {};
    const onConfirm = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          settle = resolve;
        }),
    );
    renderButton(onConfirm);

    await openAndConfirm(user);
    await user.click(screen.getByRole("button", { name: "Revoke" }));
    await user.keyboard("{Escape}");

    expect(onConfirm).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();

    settle();
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
  });

  it("clears a previous failure when the dialog is reopened", async () => {
    const user = userEvent.setup();
    renderButton(vi.fn().mockRejectedValue(new Error("nope")));

    await openAndConfirm(user);
    await screen.findByText("nope");
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());

    await user.click(screen.getByRole("button", { name: "Revoke" }));
    await screen.findByRole("alertdialog");
    expect(screen.queryByText("nope")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Revoke" })).toBeInTheDocument();
  });
});
