import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { ViewerActions } from "./ViewerActions";

const mocks = vi.hoisted(() => ({
  sessionState: {
    session: {
      deviceRole: "viewer",
      profileID: "p2",
      profile: "managed",
      revealed: false,
    },
    profiles: [
      { id: "p1", name: "Owner", kind: "adult" },
      { id: "p2", name: "Kid", kind: "managed" },
    ],
  },
}));

vi.mock("@/lib/session", () => ({
  useSession: () => mocks.sessionState,
}));

function renderActions() {
  return render(
    <I18nProvider>
      <MemoryRouter>
        <ViewerActions />
      </MemoryRouter>
    </I18nProvider>,
  );
}

describe("ViewerActions", () => {
  it("shows the selected Profile and exposes Profile settings", () => {
    renderActions();

    expect(screen.getByRole("link", { name: "Profile: Kid" })).toHaveAttribute("href", "/profiles");
    expect(screen.getByRole("link", { name: "Profile settings" })).toHaveAttribute(
      "href",
      "/settings",
    );
  });
});
