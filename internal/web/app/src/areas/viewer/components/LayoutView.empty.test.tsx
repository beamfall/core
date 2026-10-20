import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { LayoutView } from "./LayoutView";

// Empty-as-activation (onboarding-funnel §6): an empty browse is an ACTIVATION
// surface for the owner/manager (Start scan / Migrate), but stays a plain inert
// empty for every other surface — a managed/locked Profile or any no-access
// Reveal-filtered empty must be byte-indistinguishable from genuine-empty
// (ADR-0101 §2 / ADR-0004).

const mockSession = vi.fn();
vi.mock("@/lib/session", () => ({
  useSession: () => mockSession(),
}));

function renderEmpty(isManager: boolean) {
  mockSession.mockReturnValue({ manifest: null, isManager });
  return render(
    <I18nProvider>
      <MemoryRouter>
        {/* A layout with no sections renders the empty branch. */}
        <LayoutView layout={{ id: "browse", sections: [] }} libraryID="lib1" />
      </MemoryRouter>
    </I18nProvider>,
  );
}

afterEach(() => {
  cleanup();
  mockSession.mockReset();
});

describe("LayoutView empty-as-activation", () => {
  it("offers Start-scan / Migrate activation links to the owner/manager", () => {
    renderEmpty(true);

    expect(screen.getByRole("link", { name: /start first scan/i })).toHaveAttribute(
      "href",
      "/console/libraries",
    );
    expect(screen.getByRole("link", { name: /migrate a library/i })).toHaveAttribute(
      "href",
      "/console/remote",
    );
  });

  it("stays inert (no activation CTA) for a non-manager / no-access empty", () => {
    renderEmpty(false);

    // The original inert copy, and crucially NO activation links that could
    // betray hidden content on a Reveal-filtered or managed-profile surface.
    expect(screen.getByText(/nothing to show in this library yet/i)).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /start first scan/i })).toBeNull();
    expect(screen.queryByRole("link", { name: /migrate/i })).toBeNull();
  });
});
