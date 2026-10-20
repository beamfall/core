import "@testing-library/jest-dom/vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchSetupState } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { useRegister } from "@/lib/register";
import { useSession } from "@/lib/session";
import { CONSOLE_IA_PLACEMENTS, ConsoleArea } from ".";
import { AdvancedDisclosure } from "./Section";

vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: ReactNode }) => <>{children}</>,
}));
vi.mock("@/lib/register", () => ({ useRegister: vi.fn() }));
vi.mock("@/lib/session", () => ({ useSession: vi.fn() }));
vi.mock("@/lib/graphql", () => ({ fetchSetupState: vi.fn() }));
vi.mock("./AdminGate", () => ({
  AdminGate: ({ children }: { children: ReactNode }) => <>{children}</>,
}));

vi.mock("./sections/SetupChecklist", () => ({
  SetupChecklist: () => <section aria-label="First-run checklist">setup-panel</section>,
}));
vi.mock("./sections/Libraries", () => ({ Libraries: () => <div>libraries-panel</div> }));
vi.mock("./sections/Plugins", () => ({ Plugins: () => <div>plugins-panel</div> }));
vi.mock("./sections/Config", () => ({ Config: () => <div>config-panel</div> }));
vi.mock("./sections/Capabilities", () => ({
  Capabilities: () => <div>capabilities-panel</div>,
}));
vi.mock("./sections/AccessAdmin", () => ({
  AccessAdmin: () => <div>access-panel</div>,
}));
vi.mock("./sections/Operations", () => ({
  Operations: () => <div>operations-panel</div>,
}));
vi.mock("./sections/OperationsControls", () => ({
  OperationsControls: () => <div>operations-controls-panel</div>,
}));
vi.mock("./sections/Health", () => ({ Health: () => <div>health-panel</div> }));
vi.mock("./sections/PlaybackCache", () => ({
  PlaybackCache: () => <div>playback-cache-panel</div>,
}));
vi.mock("./sections/RemoteAccess", () => ({
  RemoteAccess: ({ suppressDispatchSurfaces = false }: { suppressDispatchSurfaces?: boolean }) => (
    <div>
      <div>remote-status</div>
      {!suppressDispatchSurfaces && (
        <>
          <div>qbittorrent-panel</div>
          <div>migration-panel</div>
        </>
      )}
    </div>
  ),
}));
vi.mock("./sections/ApiCredentials", () => ({
  ApiCredentials: () => <div>api-panel</div>,
}));
vi.mock("./sections/ActivityLog", () => ({
  ActivityLog: () => <div>activity-panel</div>,
}));
vi.mock("./sections/Retention", () => ({
  Retention: () => <div>retention-panel</div>,
}));
vi.mock("../manage/pages/BackupExportPage", () => ({
  BackupExportPage: () => <div>backup-export-panel</div>,
}));

function setHost(clientClass: "apple-manager-macos-direct" | "ipad-manager-app-store") {
  Object.defineProperty(window, "beamfallHost", {
    configurable: true,
    value: {
      kind: "apple-manager",
      platform: clientClass === "ipad-manager-app-store" ? "ipad" : "macos",
      clientClass,
    },
  });
}

function renderConsole(path: string) {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/console/*" element={<ConsoleArea />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

beforeEach(() => {
  vi.mocked(useRegister).mockReturnValue({
    register: "manage",
    theme: "dark",
    setRegister: vi.fn(),
    setTheme: vi.fn(),
  });
  vi.mocked(useSession).mockReturnValue({
    loading: false,
    needsEnroll: false,
  } as unknown as ReturnType<typeof useSession>);
  vi.mocked(fetchSetupState).mockResolvedValue({
    adminPasswordSet: true,
    hasLibrary: false,
    hasOwnerProfile: false,
    pluginsActivated: false,
    complete: false,
  });
  delete window.beamfallHost;
});

afterEach(() => {
  delete window.beamfallHost;
});

describe("ConsoleArea App Store host firewall", () => {
  it("removes app-store-firewalled routes and dispatch surfaces for iPad App Store hosts", () => {
    setHost("ipad-manager-app-store");

    renderConsole("/console/remote");

    expect(screen.getByText("remote-status")).toBeVisible();
    expect(screen.queryByRole("link", { name: "Capabilities" })).toBeNull();
    expect(screen.queryByText("qbittorrent-panel")).toBeNull();
    expect(screen.queryByText("migration-panel")).toBeNull();
  });

  it("keeps the full Console for direct-distribution macOS hosts", () => {
    setHost("apple-manager-macos-direct");

    renderConsole("/console/remote");

    expect(screen.getByRole("link", { name: "Capabilities" })).toBeVisible();
    expect(screen.getByText("qbittorrent-panel")).toBeVisible();
    expect(screen.getByText("migration-panel")).toBeVisible();
  });

  it("keeps the full Console for browser hosts with no native bridge", () => {
    renderConsole("/console/remote");

    expect(screen.getByRole("link", { name: "Capabilities" })).toBeVisible();
    expect(screen.getByText("qbittorrent-panel")).toBeVisible();
    expect(screen.getByText("migration-panel")).toBeVisible();
  });

  it("does not render the capabilities route for iPad App Store hosts", async () => {
    setHost("ipad-manager-app-store");

    const { container } = renderConsole("/console/capabilities");

    await screen.findByText("Server");
    expect(screen.queryByText("capabilities-panel")).toBeNull();
    expect(container.textContent).not.toMatch(/qBittorrent|Capabilities/i);
  });
});

describe("Console backups route", () => {
  it("reuses BackupExportPage at /console/backups without a manager device, and keeps /console/retention on Retention", async () => {
    const { unmount } = renderConsole("/console/backups");

    expect(await screen.findByText("backup-export-panel")).toBeVisible();
    expect(screen.queryByText("retention-panel")).toBeNull();
    unmount();

    renderConsole("/console/retention");

    expect(await screen.findByText("retention-panel")).toBeVisible();
    expect(screen.queryByText("backup-export-panel")).toBeNull();
  });
});

describe("Console IA conformance", () => {
  it("renders the fixed grouped rail with Maintain collapsed by default", async () => {
    renderConsole("/console");

    await screen.findByText("Server");
    const nav = screen.getByRole("navigation", { name: "Console" });
    expect(
      Array.from(nav.querySelectorAll(".console-nav-group-label")).map((n) => n.textContent),
    ).toEqual(["Overview", "Monitor", "Library", "Access", "Maintain"]);
    expect(within(nav).getByText("Operations")).toBeVisible();
    expect(within(nav).queryByRole("link", { name: "Server Config" })).toBeNull();
    expect(within(nav).getByText("Maintain").closest("details")).not.toHaveAttribute("open");
  });

  it("renders Overview status, triage, and first-run regions with queue deep links", async () => {
    renderConsole("/console");

    expect(await screen.findByText("Server")).toBeVisible();
    expect(screen.getByRole("heading", { name: "Needs attention" })).toBeVisible();
    expect(screen.getByRole("link", { name: /Create or connect a Library/i })).toHaveAttribute(
      "href",
      "/console/libraries",
    );
    expect(screen.getByRole("link", { name: /Create the owner Profile/i })).toHaveAttribute(
      "href",
      "/console/access",
    );
    expect(screen.getByLabelText("First-run checklist")).toBeVisible();
  });

  it("uses the Empty state family for an empty attention queue", async () => {
    vi.mocked(fetchSetupState).mockResolvedValueOnce({
      adminPasswordSet: true,
      hasLibrary: true,
      hasOwnerProfile: true,
      pluginsActivated: true,
      complete: true,
    });

    renderConsole("/console");

    expect(await screen.findByText("Nothing needs attention.")).toBeVisible();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("keeps group and in-panel Advanced disclosures keyboard-operable", async () => {
    const user = userEvent.setup();
    const { unmount } = renderConsole("/console/config");

    const maintain = screen.getByText("Maintain");
    expect(maintain.closest("details")).not.toHaveAttribute("open");
    await user.click(maintain);
    expect(maintain.closest("details")).toHaveAttribute("open");

    unmount();
    render(
      <I18nProvider>
        <AdvancedDisclosure>
          <button>probe</button>
        </AdvancedDisclosure>
      </I18nProvider>,
    );

    const advanced = screen.getByText("Advanced");
    expect(advanced.closest("details")).not.toHaveAttribute("open");
    await user.click(advanced);
    expect(advanced.closest("details")).toHaveAttribute("open");
  });

  it("maps every Console IA child surface exactly once", () => {
    const childSurfaces = [
      "0080",
      "0064",
      "0069",
      "0070",
      "0071",
      "0074",
      "0075",
      "0076",
      "0073",
      "0042",
      "0061",
      "0086",
      "0081",
      "0087",
      "0012",
      "0018",
      "0025",
      "0013",
      "0040",
      "0077",
      "0078",
      "0051",
      "0037",
      "0079",
    ];
    const counts = new Map<string, number>();
    for (const row of CONSOLE_IA_PLACEMENTS) {
      const adr = row.split(":")[0];
      counts.set(adr, (counts.get(adr) ?? 0) + 1);
    }

    for (const adr of childSurfaces) {
      expect(counts.get(adr), adr).toBe(adr === "0013" ? 2 : 1);
    }
  });
});
