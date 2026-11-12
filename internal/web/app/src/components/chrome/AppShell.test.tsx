import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { AppShell } from "./AppShell";

vi.mock("@/lib/graphql", () => ({
  fetchNotifications: vi.fn().mockResolvedValue([]),
}));

vi.mock("@/lib/register", () => ({
  useRegister: () => ({ register: "viewer" }),
}));

const sessionState = vi.hoisted(() => ({ isManager: false }));

vi.mock("@/lib/session", () => ({
  useSession: () => ({
    session: null,
    manifest: null,
    isManager: sessionState.isManager,
    revealed: false,
    needsEnroll: false,
  }),
}));

function renderShell() {
  return render(
    <I18nProvider>
      <MemoryRouter>
        <AppShell>
          <div>Surface content</div>
        </AppShell>
      </MemoryRouter>
    </I18nProvider>,
  );
}

beforeEach(() => {
  sessionState.isManager = false;
  delete window.beamfallHost;
});

afterEach(() => {
  cleanup();
  delete window.beamfallHost;
});

describe("AppShell host chrome suppression", () => {
  it("renders normal chrome in an ordinary browser", () => {
    renderShell();
    expect(screen.getByLabelText("Beamfall")).toBeInTheDocument();
    expect(screen.getByText("Surface content")).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "Beamfall Core corresponding source" }),
    ).toHaveAttribute("href", "/source");
  });

  it("links the Console and Manage from the header for managers", () => {
    sessionState.isManager = true;
    renderShell();
    expect(screen.getByRole("link", { name: "Console" })).toHaveAttribute("href", "/console");
    expect(screen.getByRole("link", { name: "Manage" })).toHaveAttribute("href", "/manage");
  });

  it("suppresses app chrome inside the Apple Manager host", () => {
    Object.defineProperty(window, "beamfallHost", {
      configurable: true,
      value: {
        kind: "apple-manager",
        platform: "macos",
        clientClass: "apple-manager-macos-direct",
      },
    });

    renderShell();
    expect(screen.queryByLabelText("Beamfall")).toBeNull();
    expect(screen.getByText("Surface content")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /corresponding source/i })).toBeNull();
  });

  it("suppresses app chrome inside the iPad Manager host", () => {
    Object.defineProperty(window, "beamfallHost", {
      configurable: true,
      value: {
        kind: "apple-manager",
        platform: "ipad",
        clientClass: "ipad-manager-app-store",
      },
    });

    renderShell();
    expect(screen.queryByLabelText("Beamfall")).toBeNull();
    expect(screen.getByText("Surface content")).toBeInTheDocument();
  });
});
