import "@testing-library/jest-dom/vitest";
import { existsSync } from "node:fs";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  fetchBackupStatus,
  fetchConsoleConfig,
  fetchUpdateStatus,
  setConsoleConfig,
} from "@/lib/graphql";
import type { BackupStatusInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { backupGateState, UpdateAwareness } from "./Operations";

// jsdom has no ResizeObserver; the Radix Switch (via react-use-size) needs one.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= ResizeObserverStub as unknown as typeof ResizeObserver;

vi.mock("@/lib/graphql", () => ({
  fetchBackupStatus: vi.fn(),
  fetchConsoleConfig: vi.fn(),
  fetchUpdateStatus: vi.fn(),
  setConsoleConfig: vi.fn(),
}));

const config = {
  backgroundWorkers: 4,
  updateCheckEnabled: true,
  updateCheckChannel: "stable",
  remoteEnrollmentEnabled: true,
  eventsRetentionDays: 180,
  plugins: [],
};
const updateStatus = {
  state: "updateAvailable" as const,
  enabled: true,
  channel: "stable",
  currentVersion: "v1.5",
  latestVersion: "v1.6",
  updateAvailable: true,
};
// Backup status fixtures for the update gate (CORE-0778 BKUP-14).
const NO_BACKUP: BackupStatusInfo = {
  backupFailedBanner: false,
  integrityFailedBanner: false,
  lastSuccess: null,
  lastFailure: null,
  lastIntegrityFailure: null,
  upgradeGate: "soft",
  lastDrill: null,
};
const VERIFIED_BACKUP: BackupStatusInfo = {
  ...NO_BACKUP,
  lastSuccess: { at: "2026-07-03T02:30:00Z", failureClass: "", set: "beamfall-1f9a" },
  lastIntegrityPass: { at: "2026-07-03T02:30:00Z", failureClass: "", set: "beamfall-1f9a" },
  updateGateClear: true,
};

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(fetchConsoleConfig).mockResolvedValue(config);
  vi.mocked(fetchUpdateStatus).mockResolvedValue(updateStatus);
  vi.mocked(setConsoleConfig).mockResolvedValue(config);
  vi.mocked(fetchBackupStatus).mockResolvedValue(NO_BACKUP);
});

function renderUpdates() {
  return render(
    <I18nProvider>
      <UpdateAwareness />
    </I18nProvider>,
  );
}

describe("UpdateAwareness channel", () => {
  it("offers the update channel as a segmented Stable/Beta toggle", async () => {
    const user = userEvent.setup();
    renderUpdates();
    // The segmented group with both options is present (not a Select listbox).
    const group = await screen.findByRole("group", { name: "Release channel" });
    const beta = screen.getByRole("button", { name: "Beta", pressed: false });
    expect(group).toContainElement(beta);
    await user.click(beta);
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Beta", pressed: true })).toBeVisible(),
    );
  });
});

// The state field exists because a check that could not answer used to be
// indistinguishable on the wire from a current server (same empty latest
// version, same updateAvailable=false). These cases pin the three outcomes to
// three different renderings.
describe("UpdateAwareness update-check state", () => {
  it("renders a failed check distinctly from an up-to-date one", async () => {
    vi.mocked(fetchUpdateStatus).mockResolvedValue({
      state: "unavailable",
      enabled: true,
      channel: "stable",
      currentVersion: "v1.5",
      latestVersion: "",
      updateAvailable: false,
    });
    renderUpdates();

    expect(await screen.findByText("Check failed")).toBeVisible();
    expect(screen.getByRole("status")).toHaveTextContent(/could not reach a verified/i);
    expect(screen.queryByText("Up to date")).toBeNull();
  });

  it("renders an up-to-date check without the failure notice", async () => {
    vi.mocked(fetchUpdateStatus).mockResolvedValue({
      state: "upToDate",
      enabled: true,
      channel: "stable",
      currentVersion: "v1.6",
      latestVersion: "v1.6",
      updateAvailable: false,
    });
    renderUpdates();

    expect(await screen.findByText("Up to date")).toBeVisible();
    expect(screen.queryByText("Check failed")).toBeNull();
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("treats an unrecognized state as a failed check, never as reassuring", async () => {
    vi.mocked(fetchUpdateStatus).mockResolvedValue({
      state: "someFutureToken" as never,
      enabled: true,
      channel: "stable",
      currentVersion: "v1.5",
      latestVersion: "",
      updateAvailable: false,
    });
    renderUpdates();

    expect(await screen.findByText("Check failed")).toBeVisible();
    expect(screen.queryByText("Up to date")).toBeNull();
  });

  it.each([
    ["disabled", false, "Off"],
    ["unavailable", true, "Check failed"],
    ["upToDate", true, "Up to date"],
  ] as const)(
    "makes no migration or backup-freshness claim when the check is %s",
    async (state, enabled, label) => {
      vi.mocked(fetchUpdateStatus).mockResolvedValue({
        state,
        enabled,
        channel: "stable",
        currentVersion: "v1.6",
        latestVersion: state === "upToDate" ? "v1.6" : "",
        updateAvailable: false,
      });
      renderUpdates();

      expect(await screen.findByText(label)).toBeVisible();
      expect(screen.queryByText(/migration impact/i)).toBeNull();
      expect(screen.queryByText(/forward-only migrations/i)).toBeNull();
      expect(screen.queryByText(/backup freshness/i)).toBeNull();
    },
  );

  it.each(["stable", "beta"])(
    "renders exact conservative safety copy for an available %s update",
    async (channel) => {
      vi.mocked(fetchUpdateStatus).mockResolvedValue({
        ...updateStatus,
        channel,
      });
      const { container } = renderUpdates();

      const warning = await screen.findByRole("status", {
        name: "Migration caution for available Core update",
      });
      expect(warning).toHaveTextContent(
        "Beamfall can't tell whether this release changes the database.",
      );
      expect(warning).toHaveTextContent(
        "This release may contain forward-only migrations. Backup freshness is unknown.",
      );
      expect(warning).toHaveTextContent("Take and verify a backup before installing.");
      expect(warning).toHaveTextContent(
        "Metadata export is optional portability evidence, not a backup substitute.",
      );
      expect(warning).toHaveTextContent(
        "Downgrade requires an offline restore and never uses in-place rollback.",
      );
      expect(warning).toHaveAttribute("data-backup-freshness", "unknown");
      expect(
        screen.getByText(`Version v1.6 is available on the ${channel} channel.`),
      ).toBeVisible();

      const scopes = [...container.querySelectorAll("[data-update-scope]")].map((node) =>
        node.getAttribute("data-update-scope"),
      );
      expect(scopes).toEqual(["core", "plugin", "compatibility"]);
      expect(within(warning).getByText("Plugins")).toBeVisible();
      expect(within(warning).getByText("Compatibility not checked")).toBeVisible();
      expect(within(warning).queryByText(/plugin update/i)).toBeNull();
      expect(within(warning).queryByText(/bundle mismatch/i)).toBeNull();
    },
  );

  it("keeps the three recovery artifact classes explicit and non-interchangeable", async () => {
    const { container } = renderUpdates();

    await screen.findByRole("status", {
      name: "Migration caution for available Core update",
    });
    const artifacts = [...container.querySelectorAll("[data-artifact-class]")].map((node) =>
      node.getAttribute("data-artifact-class"),
    );
    expect(artifacts).toEqual(["v2-backup-set", "v1-backup", "rollback-snapshot"]);
    expect(screen.getByText(/Backup Set \(v2, encrypted\)/)).toBeVisible();
    expect(screen.getByText(/Backup \(v1, database-only\)/)).toBeVisible();
    expect(screen.getByText(/Rollback Snapshot \(pre-migration, plaintext\)/)).toBeVisible();
  });

  it("provides focusable backup and offline rollback guidance with a valid canonical document link", async () => {
    const user = userEvent.setup();
    renderUpdates();

    const warning = await screen.findByRole("status", {
      name: "Migration caution for available Core update",
    });
    const createBackup = within(warning).getByRole("link", { name: "Create and verify backup" });
    const restoreGuidance = within(warning).getByRole("link", {
      name: "Offline restore guidance",
    });
    expect(createBackup).toHaveAttribute("href", "/console/backups");
    expect(restoreGuidance).toHaveAttribute("href", "#offline-restore-guidance");

    createBackup.focus();
    expect(createBackup).toHaveFocus();
    await user.tab();
    expect(within(warning).getByRole("link", { name: "Export metadata" })).toHaveFocus();
    await user.tab();
    expect(restoreGuidance).toHaveFocus();

    const offline = within(warning).getByRole("region", { name: "Offline restore guidance" });
    expect(offline).toHaveAttribute("tabindex", "-1");
    expect(offline).toHaveTextContent("Stop Beamfall before restore.");
    expect(offline).toHaveTextContent("inside the pre-migration manifest");
    expect(offline).toHaveTextContent(
      "beamfall restore --rollback-manifest /path/to/pre-migration/manifest.json --data-dir /var/lib/beamfall",
    );
    expect(offline).toHaveTextContent(
      "Restart Beamfall, check that it is healthy and that you can sign in",
    );

    const canonical = within(offline).getByRole("link", { name: "Canonical restore procedure" });
    expect(canonical).toHaveAttribute(
      "href",
      "https://github.com/beamfall/core/blob/main/docs/operations/restore.md",
    );
    expect(existsSync("../../../docs/operations/restore.md")).toBe(true);
  });
});

describe("update backup gate (CORE-0778)", () => {
  it("defaults to the soft policy: warns and permits when no verified backup is on record", async () => {
    renderUpdates();
    const gate = await screen.findByRole("region", { name: "Backup before update" });
    expect(gate).toHaveAttribute("data-backup-gate", "soft");
    expect(gate).toHaveAttribute("data-backup-gate-clear", "false");
    expect(gate).toHaveTextContent("No verified Backup Set is on record.");
    expect(gate).toHaveTextContent("Gate policy: warn, then permit (soft).");
    const install = gate.querySelector('[data-gate-step="install"]');
    expect(install).toHaveAttribute("data-gate-step-state", "pending");
  });

  it("blocks the install step under the opt-in hard policy while a failure banner is open", async () => {
    vi.mocked(fetchBackupStatus).mockResolvedValue({
      ...VERIFIED_BACKUP,
      upgradeGate: "hard",
      integrityFailedBanner: true,
      lastIntegrityFailure: { at: "2026-07-04T02:10:00Z", failureClass: "integrity", set: "" },
      updateGateClear: false,
    });
    renderUpdates();
    const gate = await screen.findByRole("region", { name: "Backup before update" });
    expect(gate).toHaveAttribute("data-backup-gate", "hard");
    expect(gate).toHaveTextContent("Updates are blocked until a valid Backup Set exists");
    expect(gate.querySelector('[data-gate-step="backup"]')).toHaveAttribute(
      "data-gate-step-state",
      "done",
    );
    expect(gate.querySelector('[data-gate-step="verify"]')).toHaveAttribute(
      "data-gate-step-state",
      "pending",
    );
    expect(gate.querySelector('[data-gate-step="install"]')).toHaveAttribute(
      "data-gate-step-state",
      "blocked",
    );
  });

  it("clears with a verified backup and treats an unreadable status as not clear", () => {
    expect(backupGateState(VERIFIED_BACKUP).clear).toBe(true);
    expect(backupGateState({ ...VERIFIED_BACKUP, upgradeGate: "hard" }).steps.install).toBe(
      "pending",
    );
    expect(backupGateState({ ...VERIFIED_BACKUP, updateGateClear: false }).clear).toBe(false);
    expect(backupGateState(null)).toMatchObject({ policy: "soft", clear: false });
  });

  it("never infers a clear gate from a success with the banners acknowledged", () => {
    // A failure the admin acknowledged leaves no banner, but the server has
    // recorded no newer integrity pass, so the gate stays shut.
    const acknowledged: BackupStatusInfo = {
      ...NO_BACKUP,
      lastSuccess: { at: "2026-07-03T02:30:00Z", failureClass: "", set: "beamfall-1f9a" },
      lastIntegrityFailure: { at: "2026-07-04T02:30:00Z", failureClass: "integrity", set: "" },
      updateGateClear: false,
    };
    const gate = backupGateState(acknowledged);
    expect(gate.clear).toBe(false);
    expect(gate.steps.verify).toBe("pending");
    expect(backupGateState({ ...acknowledged, upgradeGate: "hard" }).steps.install).toBe("blocked");
  });

  // CORE-3516 #16: when fetchBackupStatus fails, never fall back to "soft"
  // warn-and-permit copy; render unavailable with Retry instead.
  it("renders unavailable state and Retry when backup status fetch rejects", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchBackupStatus).mockRejectedValueOnce(new Error("network"));
    renderUpdates();
    const gate = await screen.findByRole("region", { name: "Backup before update" });
    expect(gate).toHaveAttribute("data-backup-gate", "unavailable");
    expect(gate).toHaveAttribute("data-backup-gate-clear", "false");
    expect(gate).toHaveTextContent("Backup health could not be loaded");
    expect(gate.querySelector('[data-backup-gate="soft"]')).toBeNull();
    expect(screen.queryByText(/warn, then permit/)).toBeNull();

    vi.mocked(fetchBackupStatus).mockResolvedValueOnce({ ...VERIFIED_BACKUP, upgradeGate: "hard" });
    await user.click(within(gate).getByRole("button", { name: "Retry" }));
    await waitFor(() =>
      expect(screen.getByRole("region", { name: "Backup before update" })).toHaveAttribute(
        "data-backup-gate",
        "hard",
      ),
    );
    expect(screen.getByRole("region", { name: "Backup before update" })).toHaveAttribute(
      "data-backup-gate-clear",
      "true",
    );
  });

  // CORE-3516 #17: clicking Check now refreshes both update status and backup
  // clearance state together.
  it("re-fetches backup clearance when Check now is clicked", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchBackupStatus).mockResolvedValueOnce({ ...NO_BACKUP, upgradeGate: "hard" });
    renderUpdates();
    const gate = await screen.findByRole("region", { name: "Backup before update" });
    expect(gate).toHaveAttribute("data-backup-gate", "hard");
    expect(gate).toHaveAttribute("data-backup-gate-clear", "false");

    vi.mocked(fetchBackupStatus).mockResolvedValueOnce({ ...VERIFIED_BACKUP, upgradeGate: "hard" });
    await user.click(screen.getByRole("button", { name: "Check now" }));
    await waitFor(() => expect(gate).toHaveAttribute("data-backup-gate-clear", "true"));
  });

  it("keeps unavailable card mounted across Retry even when stale green data is retained", async () => {
    const user = userEvent.setup();
    let resolveRetry!: (v: BackupStatusInfo) => void;
    vi.mocked(fetchBackupStatus)
      .mockResolvedValueOnce({ ...VERIFIED_BACKUP, upgradeGate: "hard" })
      .mockRejectedValueOnce(new Error("transient outage"))
      .mockImplementationOnce(
        () =>
          new Promise<BackupStatusInfo>((resolve) => {
            resolveRetry = resolve;
          }),
      );
    renderUpdates();
    const gate = await screen.findByRole("region", { name: "Backup before update" });
    expect(gate).toHaveAttribute("data-backup-gate-clear", "true");

    await user.click(screen.getByRole("button", { name: "Check now" }));
    const unavailable = await screen.findByRole("region", { name: "Backup before update" });
    expect(unavailable).toHaveAttribute("data-backup-gate", "unavailable");

    await user.click(within(unavailable).getByRole("button", { name: "Retry" }));
    // Stale cached green data must NOT flash while the retry request is in flight.
    expect(screen.getByRole("region", { name: "Backup before update" })).toHaveAttribute(
      "data-backup-gate",
      "unavailable",
    );
    expect(screen.getByRole("region", { name: "Backup before update" })).toHaveAttribute(
      "data-backup-gate-clear",
      "false",
    );

    resolveRetry({
      ...VERIFIED_BACKUP,
      upgradeGate: "hard",
      integrityFailedBanner: true,
      lastIntegrityFailure: { at: "2026-07-04T02:10:00Z", failureClass: "integrity", set: "" },
      updateGateClear: false,
    });
    await waitFor(() =>
      expect(screen.getByRole("region", { name: "Backup before update" })).toHaveAttribute(
        "data-backup-gate",
        "hard",
      ),
    );
    expect(screen.getByRole("region", { name: "Backup before update" })).toHaveAttribute(
      "data-backup-gate-clear",
      "false",
    );
  });
});
