import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { MAIN_CONTENT_ID, SkipLink } from "./SkipLink";

function Page() {
  return (
    <I18nProvider>
      <SkipLink />
      <header>
        <button type="button">Menu</button>
      </header>
      <main id={MAIN_CONTENT_ID} tabIndex={-1}>
        <button type="button">Content</button>
      </main>
    </I18nProvider>
  );
}

describe("SkipLink (CORE-1718)", () => {
  it("is the first Tab stop and moves focus to the main content", async () => {
    const user = userEvent.setup();
    render(<Page />);
    await user.tab();
    const link = screen.getByRole("link", { name: "Skip to main content" });
    expect(document.activeElement).toBe(link);
    await user.click(link);
    expect(document.activeElement).toBe(document.getElementById(MAIN_CONTENT_ID));
    await user.tab();
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "Content" }));
  });
});
