import "@testing-library/jest-dom/vitest";
import { existsSync } from "node:fs";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { EventStreamState } from "@/lib/api";
import {
  fetchConsoleConfig,
  fetchJobs,
  fetchLiveSessions,
  fetchUpdateStatus,
  setConsoleConfig,
} from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { Operations } from "./Operations";

// jsdom has no ResizeObserver; the Radix Switch (via react-use-size) needs one.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= ResizeObserverStub as unknown as typeof ResizeObserver;

vi.mock("@/lib/graphql", () => ({
  fetchConsoleConfig: vi.fn(),
  fetchUpdateStatus: vi.fn(),
  fetchJobs: vi.fn(),
  fetchLiveSessions: vi.fn(),
  setConsoleConfig: vi.fn(),
  terminateSession: vi.fn(),
}));

// Capture every onState observer the page wires so a test can drive the SSE
// lifecycle (live / reconnecting / paused) without a real EventSource.
let stateObservers: ((s: EventStreamState) => void)[] = [];
vi.mock("@/lib/api", () => ({
  subscribeEvents: (
    _onEvent: unknown,
    _onError: unknown,
    onState?: (s: EventStreamState) => void,
  ) => {
    if (onState) stateObservers.push(onState);
    return () => {};
  },
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
const jobs = [
  {
    id: "job-1",
    kind: "scan",
    status: "running",
    progress: { current: 64, total: 100, unit: "pct" },
  },
  {
    id: "job-2",
    kind: "generation",
    status: "yielded",
    progress: { current: 18, total: 100, unit: "pct" },
  },
];

beforeEach(() => {
  vi.clearAllMocks();
  stateObservers = [];
  vi.mocked(fetchConsoleConfig).mockResolvedValue(config);
  vi.mocked(fetchUpdateStatus).mockResolvedValue(updateStatus);
  vi.mocked(fetchJobs).mockResolvedValue(jobs);
  vi.mocked(fetchLiveSessions).mockResolvedValue([]);
  vi.mocked(setConsoleConfig).mockResolvedValue(config);
});

function renderOps() {
  return render(
    <I18nProvider>
      <Operations />
    </I18nProvider>,
  );
}

function drive(s: EventStreamState) {
  act(() => stateObservers.forEach((o) => o(s)));
}

describe("Operations jobs-live", () => {
  it("renders the live-status indicator (pulsing) once the stream opens; no refresh button on the live path", async () => {
    renderOps();
    await screen.findByText("Scan");
    // Before any state event, the indicator is absent (connecting).
    expect(screen.queryByText("Live")).toBeNull();
    drive("live");
    expect(await screen.findByText("Live")).toBeVisible();
    // The live path exposes no manual refresh button (honest, never-silent).
    expect(screen.queryByRole("button", { name: /refresh now/i })).toBeNull();
  });

  it("shows reconnecting on a transport error and the paused refresh fallback after retries exhaust", async () => {
    renderOps();
    await screen.findByText("Scan");
    drive("live");
    drive("reconnecting");
    expect(await screen.findByText("Reconnecting…")).toBeVisible();
    // Three consecutive errors with no re-open => paused, surfacing the manual
    // refresh fallback (mockup State 3).
    drive("reconnecting");
    drive("reconnecting");
    drive("reconnecting");
    expect(await screen.findByRole("button", { name: /refresh now/i })).toBeVisible();
  });

  it("renders jobs as flat job-rows with a progress bar, not a data table", async () => {
    const { container } = renderOps();
    await screen.findByText("Scan");
    expect(container.querySelectorAll(".console-job-row").length).toBe(2);
    // The yielded job's bar carries the warn tint.
    expect(container.querySelector(".console-jbar i.warn")).not.toBeNull();
    // No DataTable is used for the jobs panel.
    expect(container.querySelector(".console-job-list table")).toBeNull();
  });

  it("offers the update channel as a segmented Stable/Beta toggle", async () => {
    const user = userEvent.setup();
    renderOps();
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
describe("Operations update-check state", () => {
  it("renders a failed check distinctly from an up-to-date one", async () => {
    vi.mocked(fetchUpdateStatus).mockResolvedValue({
      state: "unavailable",
      enabled: true,
      channel: "stable",
      currentVersion: "v1.5",
      latestVersion: "",
      updateAvailable: false,
    });
    renderOps();

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
    renderOps();

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
    renderOps();

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
      renderOps();

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
      const { container } = renderOps();

      const warning = await screen.findByRole("status", {
        name: "Migration caution for available Core update",
      });
      expect(warning).toHaveTextContent(
        "Migration impact is unknown. The signed release manifest does not represent migrations.",
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
    const { container } = renderOps();

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
    renderOps();

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
    expect(offline).toHaveTextContent("trusted pre-migration manifest");
    expect(offline).toHaveTextContent(
      "beamfall restore --rollback-manifest /path/to/pre-migration/manifest.json --data-dir /var/lib/beamfall",
    );
    expect(offline).toHaveTextContent(
      "Restart Beamfall and verify health and an authenticated read",
    );

    const canonical = within(offline).getByRole("link", { name: "Canonical restore procedure" });
    expect(canonical).toHaveAttribute(
      "href",
      "https://github.com/beamfall/core/blob/main/docs/operations/restore.md",
    );
    expect(existsSync("../../../docs/operations/restore.md")).toBe(true);
  });
});
