import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { describe, expect, it } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { ViewerActions } from "./ViewerActions";

function Where() {
  const location = useLocation();
  return <output aria-label="location">{location.pathname + location.search}</output>;
}

function renderActions() {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/"]}>
        <ViewerActions />
        <Routes>
          <Route path="*" element={<Where />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

describe("ViewerActions", () => {
  it("opens Search with the text typed into the header field", async () => {
    const user = userEvent.setup();
    renderActions();

    await user.type(screen.getByRole("searchbox", { name: "Search" }), "blue planet{Enter}");

    expect(screen.getByLabelText("location")).toHaveTextContent("/search?q=blue%20planet");
    expect(screen.getByRole("searchbox", { name: "Search" })).toHaveValue("");
  });

  it("offers a Search link for phones", () => {
    renderActions();
    expect(screen.getByRole("link", { name: "Search" })).toHaveAttribute("href", "/search");
  });
});
