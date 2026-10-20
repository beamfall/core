import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { NotAvailable } from "./NotAvailable";

describe("NotAvailable", () => {
  it("renders a generic, leak-safe terminal message with no specifics", () => {
    render(
      <I18nProvider>
        <NotAvailable />
      </I18nProvider>,
    );
    expect(screen.getByText("Not available")).toBeInTheDocument();
    expect(screen.getByText(/isn’t available for the current profile/i)).toBeInTheDocument();
    // No status role leaks a count or title; it is a calm status region.
    expect(screen.getByRole("status")).toBeInTheDocument();
  });
});
