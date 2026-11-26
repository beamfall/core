import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, describe, expect, it } from "vitest";
import { Menu } from "./astryx-primitives";
import { ActionTooltip, TooltipProvider } from "./tooltip";

afterEach(cleanup);

describe("ActionTooltip", () => {
  it("shows on hover and focus, keeps aria-hidden on popup, and dismisses on Escape", async () => {
    const user = userEvent.setup();
    render(
      <TooltipProvider>
        <ActionTooltip label="Play episode">
          <button type="button" aria-label="Play episode">
            Play
          </button>
        </ActionTooltip>
      </TooltipProvider>,
    );

    const btn = screen.getByRole("button", { name: "Play episode" });
    await user.hover(btn);
    const popup = await screen.findByText("Play episode");
    expect(popup).toHaveAttribute("aria-hidden", "true");

    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByText("Play episode")).toBeNull());
    await user.unhover(btn);

    await user.tab();
    expect(btn).toHaveFocus();
    expect(await screen.findByText("Play episode")).toBeInTheDocument();
  });

  it("suppresses the tooltip when disabled is true", async () => {
    const user = userEvent.setup();
    render(
      <TooltipProvider>
        <ActionTooltip label="More actions" disabled>
          <button type="button" aria-label="More actions">
            More
          </button>
        </ActionTooltip>
      </TooltipProvider>,
    );

    await user.hover(screen.getByRole("button", { name: "More actions" }));
    expect(screen.queryByText("More actions")).toBeNull();
  });

  it("composes cleanly with Menu.Trigger and restores focus on Escape when disabled while open", async () => {
    const user = userEvent.setup();
    function MenuWithTooltip() {
      const [open, setOpen] = useState(false);
      return (
        <TooltipProvider>
          <Menu.Root open={open} onOpenChange={setOpen}>
            <ActionTooltip label="More actions" disabled={open}>
              <Menu.Trigger aria-label="More actions">...</Menu.Trigger>
            </ActionTooltip>
            <Menu.Portal>
              <Menu.Positioner>
                <Menu.Popup>
                  <Menu.Item>Add to list</Menu.Item>
                </Menu.Popup>
              </Menu.Positioner>
            </Menu.Portal>
          </Menu.Root>
        </TooltipProvider>
      );
    }

    render(<MenuWithTooltip />);

    const trigger = screen.getByRole("button", { name: "More actions" });
    await user.hover(trigger);
    expect(await screen.findByText("More actions")).toBeInTheDocument();

    await user.click(trigger);
    expect(await screen.findByRole("menuitem", { name: "Add to list" })).toBeInTheDocument();
    expect(trigger).toHaveAttribute("aria-expanded", "true");
    await waitFor(() => expect(screen.queryByText("More actions")).toBeNull());

    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("menuitem", { name: "Add to list" })).toBeNull());
    expect(trigger).toHaveFocus();
  });
});
