import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { AppShell } from "./AppShell";

vi.mock("@/lib/graphql", () => ({
  fetchNotifications: vi.fn().mockResolvedValue([]),
  fetchJobs: vi
    .fn()
    .mockResolvedValue([
      { id: "j1", kind: "scan", status: "running", progress: { current: 0, total: 0, unit: "" } },
    ]),
}));

vi.mock("@/lib/register", () => ({
  useRegister: () => ({ register: "viewer" }),
}));

const sessionState = vi.hoisted(() => ({ isManager: false, signedIn: false }));

vi.mock("@/lib/session", () => ({
  useSession: () => ({
    session: sessionState.signedIn
      ? { deviceRole: "viewer", profileID: "p1", profile: "adult", revealed: false }
      : null,
    profiles: [{ id: "p1", name: "Owner", kind: "adult" }],
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
  sessionState.signedIn = false;
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

  it("links the Console from the header and Manage from the account menu for managers", async () => {
    const user = userEvent.setup();
    sessionState.isManager = true;
    sessionState.signedIn = true;
    renderShell();
    expect(screen.getByRole("link", { name: "Console" })).toHaveAttribute("href", "/console");
    expect(screen.queryByRole("link", { name: "Manage" })).toBeNull();

    await user.click(screen.getByRole("button", { name: "Account menu for Owner" }));

    expect(screen.getByRole("link", { name: "Manage" })).toHaveAttribute("href", "/manage");
    expect(screen.getByRole("link", { name: "My Data" })).toHaveAttribute("href", "/mydata");
    expect(screen.getByRole("link", { name: "Profile settings" })).toHaveAttribute(
      "href",
      "/settings",
    );
    expect(screen.getByRole("button", { name: "Sign out" })).toBeInTheDocument();

    await user.keyboard("{Escape}");
    expect(screen.queryByRole("link", { name: "Manage" })).toBeNull();
    expect(screen.getByRole("button", { name: "Account menu for Owner" })).toHaveFocus();
  });

  it("shows running jobs in the header activity link for managers", async () => {
    sessionState.isManager = true;
    renderShell();
    expect(await screen.findByRole("link", { name: "1 job running" })).toHaveAttribute(
      "href",
      "/console/operations",
    );
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
