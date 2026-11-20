import "@testing-library/jest-dom/vitest";
import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setAdvancedSettings } from "@/lib/advancedSettings";
import {
  browseServerFolders,
  fetchBackupKeyStatus,
  fetchCapabilityStates,
  fetchLibraries,
  fetchLibraryHealth,
  fetchSetupState,
  setCapabilityEnabled,
} from "@/lib/graphql";
import type { CapabilityStateInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { useRegister } from "@/lib/register";
import { useSession } from "@/lib/session";
import { MENU_PAGES } from "@/lib/settingsIndex";
import { CONSOLE_IA_PLACEMENTS, CONSOLE_NAV_GROUPS, ConsoleArea } from ".";
import { fetchConsoleJobs } from "./lib/jobQueue";

vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: ReactNode }) => <>{children}</>,
}));
vi.mock("@/lib/register", () => ({ useRegister: vi.fn() }));
vi.mock("@/lib/session", () => ({ useSession: vi.fn() }));
vi.mock("@/lib/graphql", () => ({
  fetchCapabilityStates: vi.fn(),
  setCapabilityEnabled: vi.fn(),
  fetchSetupState: vi.fn(),
  fetchJobs: vi.fn().mockResolvedValue([]),
  fetchLibraries: vi.fn().mockResolvedValue([]),
  fetchUnmatchedQueue: vi.fn().mockResolvedValue([]),
  fetchConsoleConfig: vi.fn().mockResolvedValue({ plugins: [] }),
  fetchWatcherStatus: vi.fn().mockResolvedValue([]),
  fetchLibraryHealth: vi.fn().mockResolvedValue([]),
  fetchBackupKeyStatus: vi.fn().mockResolvedValue({ pending: false }),
  browseServerFolders: vi.fn().mockResolvedValue({ name: "x", path: "/x", kind: "folder" }),
}));
vi.mock("./lib/jobQueue", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./lib/jobQueue")>()),
  fetchConsoleJobs: vi.fn().mockResolvedValue([]),
}));
vi.mock("./AdminGate", () => ({
  AdminGate: ({ children }: { children: ReactNode | ((lock: () => void) => ReactNode) }) => (
    <>{typeof children === "function" ? children(() => {}) : children}</>
  ),
}));

vi.mock("./sections/SetupChecklist", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./sections/SetupChecklist")>()),
  SetupChecklist: () => <section aria-label="First-run checklist">setup-panel</section>,
  fetchHasBrowsableContent: vi.fn().mockResolvedValue(false),
}));
vi.mock("./sections/Libraries", () => ({ Libraries: () => <div>libraries-panel</div> }));
vi.mock("./sections/Plugins", () => ({ Plugins: () => <div>plugins-panel</div> }));
vi.mock("./sections/Config", () => ({ Config: () => <div>config-panel</div> }));
vi.mock("./sections/AccessAdmin", () => ({
  AccessAdmin: () => <div>access-panel</div>,
  ScheduleAdmin: () => <div>schedule-panel</div>,
}));
vi.mock("./sections/Operations", () => ({
  UpdateAwareness: () => <div>update-awareness-panel</div>,
}));
vi.mock("./sections/OperationsControls", () => ({
  OperationsControls: () => <div>operations-controls-panel</div>,
}));
vi.mock("./sections/Dashboard", () => ({
  HostGraphs: () => <div>host-graphs-panel</div>,
  LiveSessionCount: () => <span>live-count</span>,
  LiveSessions: () => <div>live-sessions-panel</div>,
}));
vi.mock("./sections/PlayHistory", () => ({ PlayHistory: () => <div>play-history-panel</div> }));
vi.mock("./sections/Health", () => ({ Health: () => <div>health-panel</div> }));
vi.mock("./sections/PlaybackCache", () => ({
  PlaybackCache: () => <div>playback-cache-panel</div>,
}));
vi.mock("./sections/RemoteAccess", () => ({
  RemoteAccess: ({ suppressDispatchSurfaces = false }: { suppressDispatchSurfaces?: boolean }) => (
    <div>
      <div>remote-status</div>
      {!suppressDispatchSurfaces && <div>qbittorrent-panel</div>}
    </div>
  ),
  MigrationWizard: () => <div>migration-panel</div>,
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
vi.mock("./sections/JellyfinCompat", () => ({
  JellyfinCompat: () => <div>jellyfin-compat-panel</div>,
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
  vi.mocked(fetchCapabilityStates).mockReset().mockResolvedValue([]);
  vi.mocked(useRegister).mockReturnValue({
    register: "manage",
    theme: "dark",
    setRegister: vi.fn(),
    setTheme: vi.fn(),
    schemePreference: "dark",
    setSchemePreference: vi.fn(),
    preset: "beamfall",
    setPreset: vi.fn(),
    applyThemeDocument: vi.fn(),
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
  localStorage.clear();
  setAdvancedSettings(false);
});

describe("ConsoleArea App Store host firewall", () => {
  // The full Console, Advanced destinations included, so the firewall is what removes them.
  beforeEach(() => setAdvancedSettings(true));

  it("removes app-store-firewalled routes and dispatch surfaces for iPad App Store hosts", () => {
    setHost("ipad-manager-app-store");

    renderConsole("/console/remote");

    expect(screen.getByText("remote-status")).toBeVisible();
    expect(screen.queryByRole("link", { name: "Optional features" })).toBeNull();
    expect(fetchCapabilityStates).not.toHaveBeenCalled();
    expect(screen.queryByText("qbittorrent-panel")).toBeNull();
    expect(screen.queryByRole("link", { name: "Migration" })).toBeNull();
  });

  it("keeps the full Console for direct-distribution macOS hosts", () => {
    setHost("apple-manager-macos-direct");

    renderConsole("/console/remote");

    expect(screen.getByRole("link", { name: "Optional features" })).toBeVisible();
    expect(screen.getByText("qbittorrent-panel")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Migration" })).toBeInTheDocument();
  });

  it("keeps the full Console for browser hosts with no native bridge", () => {
    renderConsole("/console/remote");

    expect(screen.getByRole("link", { name: "Optional features" })).toBeVisible();
    expect(screen.getByText("qbittorrent-panel")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Migration" })).toBeInTheDocument();
  });

  it("hides the Jellyfin compatibility page from App Store hosts (CORE-2683)", async () => {
    setHost("ipad-manager-app-store");

    const { container } = renderConsole("/console/jellyfin-compat");

    await screen.findByRole("heading", { name: "Common tasks" });
    expect(screen.queryByText("jellyfin-compat-panel")).toBeNull();
    expect(screen.queryByRole("link", { name: "Jellyfin compatibility" })).toBeNull();
    expect(container.textContent).not.toMatch(/Jellyfin|Seerr|Radarr|Sonarr/i);
  });

  it("shows the Jellyfin compatibility page on direct and browser hosts", async () => {
    setHost("apple-manager-macos-direct");
    const direct = renderConsole("/console/jellyfin-compat");
    expect(await screen.findByText("jellyfin-compat-panel")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Jellyfin compatibility" })).toBeInTheDocument();
    direct.unmount();

    delete window.beamfallHost;
    renderConsole("/console/jellyfin-compat");
    expect(await screen.findByText("jellyfin-compat-panel")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Jellyfin compatibility" })).toBeInTheDocument();
  });

  it("does not render the capabilities route for iPad App Store hosts", async () => {
    setHost("ipad-manager-app-store");

    const { container } = renderConsole("/console/capabilities");

    await screen.findByRole("heading", { name: "Common tasks" });
    expect(screen.queryByRole("heading", { name: "Optional features" })).toBeNull();
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

const ADVANCED_RAIL = [
  "Activity",
  "API keys & tokens",
  "Webhooks",
  "Leaving soon rules",
  "Optional features",
];

describe("Console IA conformance", () => {
  it("hides the Manage Rename result after disabling Rename in Console", async () => {
    vi.mocked(useSession).mockReturnValue({
      loading: false,
      needsEnroll: false,
      isManager: true,
    } as unknown as ReturnType<typeof useSession>);
    let states: CapabilityStateInfo[] = [
      { id: "rename", kind: "workflow", enabled: true, flippable: true },
    ];
    vi.mocked(fetchCapabilityStates).mockImplementation(async () => states);
    vi.mocked(setCapabilityEnabled).mockImplementationOnce(async () => {
      states = [{ id: "rename", kind: "workflow", enabled: false, flippable: true }];
      return states;
    });
    const { container } = renderConsole("/console/identification");
    const user = userEvent.setup();
    const toggle = await screen.findByRole("switch", { name: /rename/i });
    const search = screen.getByRole("searchbox", { name: "Search settings" });
    const renameResult = () =>
      container.querySelector('.settings-search-results a[href="/manage/rename"]');

    await user.type(search, "Rename");
    expect(renameResult()).toBeVisible();

    await user.click(toggle);
    expect(setCapabilityEnabled).toHaveBeenCalledWith("rename", false);
    expect(toggle).not.toBeChecked();
    expect(renameResult()).toBeNull();
  });

  it("renders the fixed grouped rail with expert destinations left to Advanced", async () => {
    renderConsole("/console");

    await screen.findByRole("heading", { name: "Common tasks" });
    const nav = screen.getByRole("navigation", { name: "Console" });
    expect(
      Array.from(nav.querySelectorAll(".console-nav-group-label")).map((n) => n.textContent),
    ).toEqual(["Monitor", "Library", "Access", "Maintain"]);
    expect(within(nav).getByText("Operations")).toBeVisible();
    expect(within(nav).getByRole("link", { name: "Backups" })).toBeVisible();
    expect(within(nav).queryByRole("link", { name: "Server Config" })).toBeNull();
    for (const label of ADVANCED_RAIL) {
      expect(within(nav).queryByRole("link", { name: label })).toBeNull();
    }
  });

  it("shows every destination once the Advanced switch is on (CORE-1117)", async () => {
    const user = userEvent.setup();
    renderConsole("/console");

    await screen.findByRole("heading", { name: "Common tasks" });
    const nav = screen.getByRole("navigation", { name: "Console" });
    await user.click(within(nav).getByRole("switch", { name: "Show advanced settings" }));
    for (const label of ADVANCED_RAIL) {
      expect(within(nav).getByRole("link", { name: label })).toBeVisible();
    }

    act(() => setAdvancedSettings(false));
    expect(within(nav).queryByRole("link", { name: "API keys & tokens" })).toBeNull();
  });

  it("keeps an Advanced destination in the rail while it is the current page", async () => {
    renderConsole("/console/retention");

    const nav = screen.getByRole("navigation", { name: "Console" });
    expect(within(nav).getByRole("link", { name: "Leaving soon rules" })).toHaveAttribute(
      "aria-current",
      "page",
    );
    expect(within(nav).queryByRole("link", { name: "API keys & tokens" })).toBeNull();
  });

  it("keeps the unlocked status and Lock action outside the folding phone menu", async () => {
    renderConsole("/console");

    await screen.findByRole("heading", { name: "Common tasks" });
    const nav = screen.getByRole("navigation", { name: "Console" });
    const lock = within(nav).getByRole("button", { name: "Lock Console" });
    expect(lock.closest(".console-session-bar")).not.toBeNull();
    expect(lock.closest(".section-menu-body")).toBeNull();
    const advanced = within(nav).getByRole("switch", { name: "Show advanced settings" });
    expect(advanced.closest(".section-menu-body")).toBeNull();
  });

  it("gives sessions, updates and migration one destination each", async () => {
    const { unmount } = renderConsole("/console/sessions");
    expect(await screen.findByText("live-sessions-panel")).toBeVisible();
    expect(screen.queryByText("update-awareness-panel")).toBeNull();
    unmount();

    const updates = renderConsole("/console/updates");
    expect(await screen.findByText("update-awareness-panel")).toBeVisible();
    expect(screen.queryByText("live-sessions-panel")).toBeNull();
    updates.unmount();

    renderConsole("/console/remote");
    expect(await screen.findByText("remote-status")).toBeVisible();
    expect(screen.queryByText("migration-panel")).toBeNull();
  });

  it("renders Overview status, triage, and first-run regions with queue deep links", async () => {
    renderConsole("/console");

    expect(await screen.findByRole("heading", { name: "Common tasks" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Rescan all libraries" })).toBeVisible();
    expect(screen.getByRole("link", { name: /Fix unmatched items/ })).toHaveAttribute(
      "href",
      "/manage/unmatched",
    );
    expect(screen.getByRole("link", { name: /Provider keys/ })).toHaveAttribute(
      "href",
      "/console/plugins",
    );
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

  it("lists a missing Library folder under Needs attention with a fix link (CORE-1606)", async () => {
    vi.mocked(fetchSetupState).mockResolvedValueOnce({
      adminPasswordSet: true,
      hasLibrary: true,
      hasOwnerProfile: true,
      pluginsActivated: true,
      complete: true,
    });
    vi.mocked(fetchLibraries).mockResolvedValue([
      { id: "lib-1", title: "Films", roots: ["/gone"] } as never,
    ]);
    vi.mocked(browseServerFolders).mockRejectedValueOnce(new Error("folder does not exist"));

    renderConsole("/console");

    const row = await screen.findByRole("link", { name: /Films: \/gone/ });
    // The link opens that Library's folders (CORE-2309).
    expect(row).toHaveAttribute("href", "/console/libraries#folders-lib-1");
    expect(row).toHaveTextContent("Folder not found");
    expect(screen.queryByText("Nothing needs attention.")).toBeNull();
    vi.mocked(fetchLibraries).mockResolvedValue([]);
  });

  it("lists failed jobs, Library health and an unsaved backup key under Needs attention (CORE-2310, CORE-2309)", async () => {
    vi.mocked(fetchSetupState).mockResolvedValueOnce({
      adminPasswordSet: true,
      hasLibrary: true,
      hasOwnerProfile: true,
      pluginsActivated: true,
      complete: true,
    });
    vi.mocked(fetchConsoleJobs).mockResolvedValueOnce([
      {
        id: "scan-1",
        kind: "scan",
        status: "failed",
        error: "open /mnt/films: no such file (Bearer abc123)",
        finishedAt: "2026-11-17T10:00:00Z",
        progress: { current: 0, total: 0, unit: "files" },
        target: { libraryID: "lib-9", libraryTitle: "Films", root: "/mnt/films" },
      } as never,
      {
        id: "poll-1",
        kind: "watcher-poll",
        status: "completed",
        progress: { current: 0, total: 0, unit: "" },
      } as never,
    ]);
    vi.mocked(fetchLibraryHealth).mockResolvedValue([
      { libraryID: "a", counts: { unmatched: 3, broken: 0 } },
      { libraryID: "b", counts: { unmatched: 2, "missing-artwork": 1 } },
    ] as never);
    vi.mocked(fetchBackupKeyStatus).mockResolvedValueOnce({ pending: true } as never);

    renderConsole("/console");

    const failed = await screen.findByRole("link", { name: /Scan failed/ });
    expect(failed).toHaveAttribute("href", "/console/libraries#folders-lib-9");
    expect(failed).toHaveTextContent("Films · /mnt/films");
    expect(failed).toHaveTextContent("no such file");
    expect(failed).not.toHaveTextContent("abc123");
    expect(await screen.findByRole("link", { name: /Unmatched: 5 items/ })).toHaveAttribute(
      "href",
      "/manage/unmatched",
    );
    expect(screen.getByRole("link", { name: /Missing artwork: 1 item/i })).toHaveAttribute(
      "href",
      "/console/health",
    );
    expect(
      await screen.findByRole("link", { name: /backup key is not saved yet/ }),
    ).toHaveAttribute("href", "/console/backups");
    expect(screen.getByText("1 failed")).toBeInTheDocument();
    expect(screen.queryByText("Nothing needs attention.")).toBeNull();
    vi.mocked(fetchLibraryHealth).mockResolvedValue([]);
  });

  it("puts the first-value checklist above Server resources while a step is open", async () => {
    renderConsole("/console");

    const checklist = await screen.findByLabelText("First-run checklist");
    const graphs = screen.getByText("host-graphs-panel");
    expect(
      checklist.compareDocumentPosition(graphs) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
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

describe("Console settings search pages", () => {
  it("lists every Console menu page, and no other, in MENU_PAGES", () => {
    const nav = CONSOLE_NAV_GROUPS.flatMap((g) => g.items).map(({ path, label }) => ({
      path,
      label,
    }));
    const indexed = MENU_PAGES.filter((p) => p.area === "console").map(({ path, label }) => ({
      path,
      label,
    }));
    expect(indexed).toEqual(nav);
  });
});
