import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Section } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { fetchSectionItems } from "../lib/sections";
import { LayoutView } from "./LayoutView";

// Empty-as-activation (onboarding-funnel §6): an empty browse is an ACTIVATION
// surface for the owner/manager (Start scan / Migrate), but stays a plain inert
// empty for every other surface — a managed/locked Profile or any no-access
// Reveal-filtered empty must be byte-indistinguishable from genuine-empty
// (ADR-0101 §2 / ADR-0004).

const mockSession = vi.fn();
vi.mock("@/lib/session", () => ({
  useSession: () => mockSession(),
  useViewerIdentity: () => null,
}));
vi.mock("../lib/sections", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/sections")>();
  return { ...actual, fetchSectionItems: vi.fn() };
});

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
  vi.mocked(fetchSectionItems).mockReset();
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
      "/console/migrate",
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

  it("never offers the owner first-scan CTA when every Section failed to load", async () => {
    vi.mocked(fetchSectionItems).mockRejectedValue(new Error("offline"));
    mockSession.mockReturnValue({ manifest: null, isManager: true });
    const shelf: Section = {
      id: "recent",
      kind: "shelf",
      title: { en: "Recent" },
      artworkSlot: "poster",
      source: { kind: "recent" } as Section["source"],
    };
    render(
      <I18nProvider>
        <MemoryRouter>
          <LayoutView layout={{ id: "browse", sections: [shelf] }} libraryID="lib1" />
        </MemoryRouter>
      </I18nProvider>,
    );

    expect(await screen.findByRole("button", { name: /try again/i })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /start first scan/i })).toBeNull();
    expect(screen.queryByRole("link", { name: /migrate/i })).toBeNull();
  });

  it("treats a missing manifest as a load failure whose retry reloads the session", async () => {
    const refresh = vi.fn().mockResolvedValue(undefined);
    mockSession.mockReturnValue({ manifest: null, isManager: true, refresh });
    render(
      <I18nProvider>
        <MemoryRouter>
          <LayoutView layout={null} libraryID="" />
        </MemoryRouter>
      </I18nProvider>,
    );

    expect(screen.getByText(/could not reach the server/i)).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /start first scan/i })).toBeNull();
    await userEvent.setup().click(screen.getByRole("button", { name: /try again/i }));
    expect(refresh).toHaveBeenCalledTimes(1);
  });
});
