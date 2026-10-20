import "@testing-library/jest-dom/vitest";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { graphql } from "@/lib/api";
import {
  fetchConsoleConfig,
  setPluginActive,
  setPluginConfig,
  uninstallPluginData,
} from "@/lib/graphql";
import type { ConsoleConfigInfo, PluginActivationInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { setAdminEpochSignal } from "../adminEpoch";
import { Plugins } from "./Plugins";

vi.mock("@/lib/graphql", () => ({
  fetchConsoleConfig: vi.fn(),
  setPluginActive: vi.fn(),
  setPluginConfig: vi.fn(),
  uninstallPluginData: vi.fn(),
  setScraperCredential: vi.fn(),
  validateScraperCredential: vi.fn(),
  disableScraperCredential: vi.fn(),
}));

vi.mock("@/lib/api", () => ({
  graphql: vi.fn(),
}));

type PluginStandingFixture = PluginActivationInfo & {
  provenance: string;
  trustState: string;
};

type ConsoleConfigFixture = Omit<ConsoleConfigInfo, "plugins"> & {
  plugins: PluginStandingFixture[];
};

const activePlugin: PluginStandingFixture = {
  id: "studio-meta",
  displayName: "Studio Metadata",
  provenance: "Third-Party",
  trustState: "Trusted",
  active: true,
  controlPolicy: "managed",
  config: [
    {
      name: "baseURL",
      type: "string",
      label: "Base URL",
      required: true,
      value: "https://meta.example.org",
      configured: true,
    },
    {
      name: "fetchArtwork",
      type: "bool",
      label: "Fetch artwork",
      required: false,
      value: "true",
      configured: true,
    },
  ],
  permissions: [
    {
      kind: "net.outbound",
      label: "Outbound network",
      description: "Reach metadata endpoints.",
      highRisk: false,
    },
    {
      kind: "media.write",
      label: "Write metadata",
      highRisk: true,
    },
  ],
  status: [
    { id: "endpoint", label: "Endpoint", state: "ok", detail: "Reachable." },
    { id: "lastRun", label: "Last run", state: "degraded", detail: "3 skipped." },
  ],
  actions: [
    {
      id: "refresh",
      label: "Refresh catalog",
      risk: "info",
      requiredPermissions: ["net.outbound"],
    },
    {
      id: "purge",
      label: "Purge cached metadata",
      risk: "high",
      requiredPermissions: ["media.write"],
      confirmation: "Drop all cached metadata?",
    },
  ],
  scrapers: [],
};

const config: ConsoleConfigFixture = {
  backgroundWorkers: 2,
  updateCheckEnabled: false,
  updateCheckChannel: "stable",
  remoteEnrollmentEnabled: true,
  eventsRetentionDays: 180,
  plugins: [activePlugin],
};

function withoutStanding(nextConfig: ConsoleConfigFixture): ConsoleConfigInfo {
  return {
    ...nextConfig,
    plugins: nextConfig.plugins.map((plugin) => {
      const { provenance: _provenance, trustState: _trustState, ...basePlugin } = plugin;
      return basePlugin;
    }),
  };
}

function mockConsoleConfig(nextConfig: ConsoleConfigFixture) {
  const baseConfig = withoutStanding(nextConfig);
  vi.mocked(fetchConsoleConfig).mockResolvedValue(baseConfig);
  vi.mocked(graphql).mockResolvedValue({
    consoleConfig: {
      plugins: nextConfig.plugins.map(({ id, provenance, trustState }) => ({
        id,
        provenance,
        trustState,
      })),
    },
    runPluginLifecycleAction: { pluginID: "studio-meta" },
  });
  vi.mocked(setPluginActive).mockResolvedValue(baseConfig);
  vi.mocked(setPluginConfig).mockResolvedValue(baseConfig);
  vi.mocked(uninstallPluginData).mockResolvedValue(baseConfig);
}

function standingPlugin(
  id: string,
  displayName: string,
  provenance: string,
  trustState: string,
): PluginStandingFixture {
  return {
    ...activePlugin,
    id,
    displayName,
    provenance,
    trustState,
    config: [],
    permissions: [],
    status: [],
    actions: [],
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

beforeEach(() => {
  setAdminEpochSignal();
  vi.mocked(fetchConsoleConfig).mockReset();
  vi.mocked(graphql).mockReset();
  vi.mocked(setPluginActive).mockReset();
  vi.mocked(setPluginConfig).mockReset();
  vi.mocked(uninstallPluginData).mockReset();
  mockConsoleConfig(config);
});

afterEach(() => setAdminEpochSignal());

function renderPanel() {
  return render(
    <I18nProvider>
      <Plugins />
    </I18nProvider>,
  );
}

describe("Plugins", () => {
  it("renders typed config, permissions, status, and action descriptors", async () => {
    renderPanel();

    expect(await screen.findAllByText("Studio Metadata")).not.toHaveLength(0);
    // Config field labels.
    expect(screen.getByLabelText("Fetch artwork")).toBeVisible();
    // Permission inventory with the high-risk chip.
    expect(screen.getByText("net.outbound")).toBeVisible();
    expect(screen.getByText("High risk")).toBeVisible();
    // Status row state (the server-provided state string, rendered as-is).
    expect(screen.getByText("degraded")).toBeVisible();
    // Action descriptors with risk labels.
    expect(screen.getByText("Refresh catalog")).toBeVisible();
    expect(screen.getByText("Purge cached metadata")).toBeVisible();
    expect(screen.getByText("High")).toBeVisible();
  });

  it("renders a provenance chip and trust badge for each provenance class", async () => {
    const user = userEvent.setup();
    const cases = [
      {
        name: "Beamfall Plugin",
        plugin: standingPlugin("beamfall-plugin", "Beamfall Plugin", "First-Party", "Trusted"),
        provenance: "First-party",
        trust: "Trusted",
      },
      {
        name: "Registry Plugin",
        plugin: standingPlugin("registry-plugin", "Registry Plugin", "Third-Party", "Revoked"),
        provenance: "Third-party",
        trust: "Verification failed",
      },
      {
        name: "Local Plugin",
        plugin: standingPlugin("local-plugin", "Local Plugin", "Sideloaded", "Unsigned"),
        provenance: "Sideloaded",
        trust: "Unsigned",
      },
    ];
    mockConsoleConfig({ ...config, plugins: cases.map(({ plugin }) => plugin) });
    renderPanel();

    for (const expected of cases) {
      const toggle = await screen.findByRole("switch", { name: expected.name });
      const row = toggle.closest<HTMLElement>(".console-control-row");
      if (!row) throw new Error("Plugin row missing.");
      expect(within(row).getByText(expected.provenance)).toBeVisible();
      expect(within(row).getByText(expected.trust)).toBeVisible();
    }
    const signal = vi.mocked(fetchConsoleConfig).mock.calls[0]?.[0];
    expect(signal).toBeInstanceOf(AbortSignal);
    expect(graphql).toHaveBeenCalledWith(
      expect.stringContaining("provenance trustState"),
      {},
      signal,
    );

    const revokedToggle = screen.getByRole("switch", { name: "Registry Plugin" });
    const revokedRow = revokedToggle.closest<HTMLElement>(".console-control-row");
    if (!revokedRow) throw new Error("Revoked plugin row missing.");
    await user.click(within(revokedRow).getByText("Verification failed"));
    expect(within(revokedRow).getByText("Key revoked")).toBeVisible();
  });

  it("shows tamper detail and consolidates unrecognized trust into one banner", async () => {
    const user = userEvent.setup();
    mockConsoleConfig({
      ...config,
      plugins: [
        standingPlugin("tampered-plugin", "Tampered Plugin", "Sideloaded", "Tampered"),
        standingPlugin("unknown-plugin", "Unknown Plugin", "Sideloaded", "Unrecognized"),
        standingPlugin("unknown-plugin-2", "Unknown Plugin 2", "Sideloaded", "Unrecognized"),
      ],
    });
    renderPanel();

    const tamperedToggle = await screen.findByRole("switch", { name: "Tampered Plugin" });
    const tamperedRow = tamperedToggle.closest<HTMLElement>(".console-control-row");
    if (!tamperedRow) throw new Error("Tampered plugin row missing.");
    await user.click(within(tamperedRow).getByText("Verification failed"));
    expect(within(tamperedRow).getByText("Signature invalid")).toBeVisible();

    const unknownToggle = screen.getByRole("switch", { name: "Unknown Plugin" });
    const unknownRow = unknownToggle.closest<HTMLElement>(".console-control-row");
    if (!unknownRow) throw new Error("Unrecognized plugin row missing.");
    expect(within(unknownRow).getByText("Sideloaded")).toBeVisible();
    expect(within(unknownRow).queryByText("Verification failed")).not.toBeInTheDocument();
    expect(within(unknownRow).queryByText("Unrecognized")).not.toBeInTheDocument();
    const unknownToggle2 = screen.getByRole("switch", { name: "Unknown Plugin 2" });
    const unknownRow2 = unknownToggle2.closest<HTMLElement>(".console-control-row");
    if (!unknownRow2) throw new Error("Second unrecognized plugin row missing.");
    expect(within(unknownRow2).queryByText("Unrecognized")).not.toBeInTheDocument();
    expect(screen.getAllByText("Update your server to refresh plugin trust.")).toHaveLength(1);
    expect(screen.getAllByRole("status")).toHaveLength(1);
  });

  it("warns when plugin standing cannot be loaded", async () => {
    vi.mocked(graphql).mockRejectedValue(new Error("standing unavailable"));
    renderPanel();

    expect(await screen.findAllByText("Studio Metadata")).not.toHaveLength(0);
    expect(screen.getByRole("alert")).toHaveTextContent("Plugin trust information is unavailable.");
    expect(screen.queryByText("Third-party")).not.toBeInTheDocument();
  });

  it("warns when plugin standing is incomplete", async () => {
    const first = standingPlugin("first-plugin", "First Plugin", "First-Party", "Trusted");
    const second = standingPlugin("second-plugin", "Second Plugin", "Third-Party", "Trusted");
    mockConsoleConfig({ ...config, plugins: [first, second] });
    vi.mocked(graphql).mockResolvedValue({
      consoleConfig: {
        plugins: [{ id: first.id, provenance: first.provenance, trustState: first.trustState }],
      },
    });
    renderPanel();

    const firstToggle = await screen.findByRole("switch", { name: "First Plugin" });
    const firstRow = firstToggle.closest<HTMLElement>(".console-control-row");
    if (!firstRow) throw new Error("First plugin row missing.");
    expect(within(firstRow).getByText("First-party")).toBeVisible();
    expect(screen.getByRole("alert")).toHaveTextContent("Plugin trust information is unavailable.");
  });

  it("preserves prior standing and warns when an action cannot refresh it", async () => {
    const user = userEvent.setup();
    renderPanel();
    expect(await screen.findByText("Third-party")).toBeVisible();
    vi.mocked(graphql).mockRejectedValue(new Error("standing refresh unavailable"));

    await user.click(screen.getByRole("switch", { name: "Studio Metadata" }));

    await waitFor(() => expect(setPluginActive).toHaveBeenCalledWith("studio-meta", false));
    expect(screen.getByText("Third-party")).toBeVisible();
    expect(screen.getByRole("alert")).toHaveTextContent("Plugin trust information is unavailable.");
  });

  it("aborts and drops a post-mutation standing read from a departed Admin epoch", async () => {
    const admin = new AbortController();
    const replacement = new AbortController();
    const staleStanding = deferred<{
      consoleConfig: { plugins: Array<{ id: string; provenance: string; trustState: string }> };
    }>();
    setAdminEpochSignal(admin.signal);
    vi.mocked(graphql)
      .mockResolvedValueOnce({
        consoleConfig: {
          plugins: [
            {
              id: activePlugin.id,
              provenance: activePlugin.provenance,
              trustState: activePlugin.trustState,
            },
          ],
        },
      })
      .mockReturnValueOnce(staleStanding.promise)
      .mockResolvedValueOnce({ runPluginLifecycleAction: { pluginID: activePlugin.id } })
      .mockResolvedValueOnce({
        consoleConfig: {
          plugins: [
            {
              id: activePlugin.id,
              provenance: "Sideloaded",
              trustState: "Unsigned",
            },
          ],
        },
      });

    const user = userEvent.setup();
    renderPanel();
    expect(await screen.findByText("Third-party")).toBeVisible();

    await user.click(screen.getByRole("switch", { name: "Studio Metadata" }));
    await waitFor(() => expect(graphql).toHaveBeenCalledTimes(2));
    const standingSignal = vi.mocked(graphql).mock.calls[1]?.[2];

    admin.abort();
    setAdminEpochSignal(replacement.signal);
    await waitFor(() => expect(standingSignal?.aborted).toBe(true));

    await user.click(screen.getAllByRole("button", { name: "Run" })[0]);
    await waitFor(() => expect(graphql).toHaveBeenCalledTimes(4));
    const replacementSignal = vi.mocked(graphql).mock.calls[3]?.[2];
    expect(replacementSignal).toBeInstanceOf(AbortSignal);
    expect(replacementSignal?.aborted).toBe(false);
    expect(await screen.findByText("Sideloaded")).toBeVisible();
    expect(screen.getByText("Unsigned")).toBeVisible();

    await act(async () => {
      staleStanding.resolve({
        consoleConfig: {
          plugins: [
            {
              id: activePlugin.id,
              provenance: "First-Party",
              trustState: "Tampered",
            },
          ],
        },
      });
      await staleStanding.promise;
    });

    expect(screen.getByText("Sideloaded")).toBeVisible();
    expect(screen.getByText("Unsigned")).toBeVisible();
    expect(screen.queryByText("Third-party")).not.toBeInTheDocument();
    expect(screen.queryByText("First-party")).not.toBeInTheDocument();
    expect(screen.queryByText("Verification failed")).not.toBeInTheDocument();
  });

  it("saves edited config through setPluginConfig", async () => {
    const user = userEvent.setup();
    renderPanel();
    await screen.findByLabelText("Base URL");

    const input = screen.getByLabelText("Base URL");
    await user.clear(input);
    await user.type(input, "https://new.example.org");
    await user.click(screen.getByRole("button", { name: "Save configuration" }));

    await waitFor(() =>
      expect(setPluginConfig).toHaveBeenCalledWith(
        "studio-meta",
        { baseURL: "https://new.example.org" },
        [],
      ),
    );
  });

  it("disables config editing for an inactive plugin", async () => {
    mockConsoleConfig({
      ...config,
      plugins: [{ ...activePlugin, active: false }],
    });
    renderPanel();

    expect(await screen.findByLabelText("Base URL")).toBeDisabled();
    expect(screen.getByRole("button", { name: "Save configuration" })).toBeDisabled();
  });

  it("runs action descriptors through runPluginAction", async () => {
    const user = userEvent.setup();
    renderPanel();
    await screen.findByText("Refresh catalog");

    // The info-risk action runs without a confirm dialog (first "Run" button).
    await user.click(screen.getAllByRole("button", { name: "Run" })[0]);

    await waitFor(() =>
      expect(graphql).toHaveBeenCalledWith(expect.stringContaining("runPluginLifecycleAction"), {
        pluginID: "studio-meta",
        actionID: "refresh",
      }),
    );
  });

  it("purges plugin data through uninstallPluginData after confirmation", async () => {
    const user = userEvent.setup();
    renderPanel();

    // The destructive Uninstall trigger opens a confirm dialog; only the
    // confirmed action calls the mutation (HARD invariant #6 / ADR-0104).
    const trigger = await screen.findByRole("button", {
      name: "Uninstall (purge data)",
    });
    await user.click(trigger);

    expect(await screen.findByText("Uninstall this plugin and purge its data?")).toBeVisible();
    expect(uninstallPluginData).not.toHaveBeenCalled();

    // The dialog's confirm action (the second matching button) runs the purge.
    const confirms = screen.getAllByRole("button", {
      name: "Uninstall (purge data)",
    });
    await user.click(confirms[confirms.length - 1]);

    await waitFor(() => expect(uninstallPluginData).toHaveBeenCalledWith("studio-meta"));
  });

  it("deactivates through setPluginActive without purging data", async () => {
    const user = userEvent.setup();
    renderPanel();

    await user.click(await screen.findByRole("button", { name: "Deactivate" }));

    await waitFor(() => expect(setPluginActive).toHaveBeenCalledWith("studio-meta", false));
    expect(uninstallPluginData).not.toHaveBeenCalled();
  });

  it("disables built-in plugin controls with an explanation", async () => {
    const user = userEvent.setup();
    mockConsoleConfig({
      ...config,
      plugins: [{ ...activePlugin, id: "filmtv", displayName: "Film and TV" }],
    });
    renderPanel();

    const toggle = await screen.findByRole("switch", { name: "Film and TV" });
    expect(toggle).toBeDisabled();
    expect(screen.getByTitle("Built-in plugins cannot be disabled.")).toContainElement(toggle);

    await user.click(toggle);
    expect(setPluginActive).not.toHaveBeenCalled();
  });

  it("keeps non-built-in plugin controls interactive", async () => {
    const user = userEvent.setup();
    renderPanel();

    await user.click(await screen.findByRole("switch", { name: "Studio Metadata" }));

    await waitFor(() => expect(setPluginActive).toHaveBeenCalledWith("studio-meta", false));
    await waitFor(() => expect(graphql).toHaveBeenCalledTimes(2));
    expect(screen.getByText("Third-party")).toBeVisible();
  });
});
