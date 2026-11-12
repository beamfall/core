import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { ViewerActions } from "./ViewerActions";

vi.mock("./HeaderSearch", () => ({ HeaderSearch: () => <input aria-label="Search" /> }));

function renderActions(entry = "/") {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[entry]}>
        <ViewerActions />
      </MemoryRouter>
    </I18nProvider>,
  );
}

describe("ViewerActions", () => {
  // Profile, My Data and Profile settings live in the AppShell account menu
  // (CORE-1131), so the header actions are only the search field.
  it("leaves the Profile links to the account menu", () => {
    renderActions();
    expect(screen.queryByRole("link")).toBeNull();
  });

  it("offers the header search everywhere except the search page", () => {
    const { unmount } = renderActions("/mydata");
    expect(screen.getByLabelText("Search")).toBeInTheDocument();
    unmount();
    renderActions("/search");
    expect(screen.queryByLabelText("Search")).toBeNull();
  });
});
