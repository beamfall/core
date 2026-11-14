import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Link, MemoryRouter } from "react-router";
import { describe, expect, it } from "vitest";
import { SectionMenu } from "./SectionMenu";

function renderMenu() {
  render(
    <MemoryRouter initialEntries={["/area/one"]}>
      <SectionMenu label="Area" current="One" footer={<p>footer status</p>}>
        <Link to="/area/one">One</Link>
      </SectionMenu>
      <button type="button">outside</button>
    </MemoryRouter>,
  );
  const toggle = screen.getByRole("button", { name: "One" });
  return { toggle, nav: screen.getByRole("navigation", { name: "Area" }) };
}

describe("SectionMenu", () => {
  it("renders the footer outside the folding body", () => {
    const { nav } = renderMenu();
    const footer = screen.getByText("footer status");
    expect(nav).toContainElement(footer);
    expect(footer.closest(".section-menu-body")).toBeNull();
  });

  it("closes on Escape and returns focus to the toggle", async () => {
    const user = userEvent.setup();
    const { toggle } = renderMenu();
    await user.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    await user.tab();
    await user.keyboard("{Escape}");
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(toggle).toHaveFocus();
  });

  it("closes on a press outside the menu", async () => {
    const user = userEvent.setup();
    const { toggle, nav } = renderMenu();
    await user.click(toggle);
    await user.click(screen.getByText("footer status"));
    expect(nav).toHaveAttribute("data-open");
    await user.click(screen.getByRole("button", { name: "outside" }));
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(nav).not.toHaveAttribute("data-open");
  });

  it("closes when following a link to the current page", async () => {
    const user = userEvent.setup();
    const { toggle } = renderMenu();
    await user.click(toggle);
    await user.click(screen.getByRole("link", { name: "One" }));
    expect(toggle).toHaveAttribute("aria-expanded", "false");
  });
});
