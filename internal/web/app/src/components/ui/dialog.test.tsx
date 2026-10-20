import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import type { Locale } from "@/strings";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "./dialog";

afterEach(() => {
  cleanup();
});

function renderDialog() {
  return render(
    <I18nProvider>
      <Dialog>
        <DialogTrigger>Open dialog</DialogTrigger>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Dialog title</DialogTitle>
            <DialogDescription>Dialog description</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <DialogClose>Cancel</DialogClose>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </I18nProvider>,
  );
}

function renderLocalizedDialog(locale: Locale) {
  return render(
    <I18nProvider initialLocale={locale}>
      <Dialog open>
        <DialogContent>
          <DialogTitle>Test dialog</DialogTitle>
        </DialogContent>
      </Dialog>
    </I18nProvider>,
  );
}

describe("Dialog", () => {
  it("opens on trigger click and shows title/description", async () => {
    const user = userEvent.setup();
    renderDialog();

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Open dialog" }));

    const dialog = await screen.findByRole("dialog");
    expect(dialog).toBeInTheDocument();
    expect(screen.getByText("Dialog title")).toBeInTheDocument();
    expect(screen.getByText("Dialog description")).toBeInTheDocument();
  });

  it("closes on Escape and returns focus to the trigger", async () => {
    const user = userEvent.setup();
    renderDialog();

    const trigger = screen.getByRole("button", { name: "Open dialog" });
    await user.click(trigger);
    await screen.findByRole("dialog");

    await user.keyboard("{Escape}");

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    await waitFor(() => expect(trigger).toHaveFocus());
  });

  it("closes via the built-in close button and via a custom DialogClose", async () => {
    const user = userEvent.setup();
    renderDialog();

    await user.click(screen.getByRole("button", { name: "Open dialog" }));
    await screen.findByRole("dialog");

    await user.click(screen.getByRole("button", { name: "Close" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    await user.click(screen.getByRole("button", { name: "Open dialog" }));
    await screen.findByRole("dialog");

    await user.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("supports asChild on the trigger without adding a wrapper element", async () => {
    const user = userEvent.setup();
    render(
      <I18nProvider>
        <Dialog>
          <DialogTrigger asChild>
            <a href="#open">Open via link</a>
          </DialogTrigger>
          <DialogContent>
            <DialogTitle>Linked dialog</DialogTitle>
          </DialogContent>
        </Dialog>
      </I18nProvider>,
    );

    const link = screen.getByRole("link", { name: "Open via link" });
    expect(link.tagName).toBe("A");

    await user.click(link);
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
  });

  it("uses the English catalog for the close button accessible name", () => {
    renderLocalizedDialog("en");

    expect(screen.getByRole("button", { name: "Close" })).toBeInTheDocument();
  });

  it("uses the active non-English catalog for the close button accessible name", () => {
    renderLocalizedDialog("fr");

    expect(screen.getByRole("button", { name: "Fermer" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Close" })).not.toBeInTheDocument();
  });
});
