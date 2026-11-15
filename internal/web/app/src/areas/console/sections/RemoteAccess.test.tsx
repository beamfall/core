import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  checkRemoteAccessReachability,
  fetchConsoleConfig,
  fetchRemoteAccess,
  setConsoleConfig,
  setRemoteAccessPortMapping,
} from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import {
  currentEndpointKind,
  isPublicCleartextConsoleLocation,
  RemoteAccessStatus,
  RemoteBitrateLimit,
} from "./RemoteAccess";

vi.mock("@/lib/graphql", () => ({
  checkRemoteAccessReachability: vi.fn(),
  fetchConsoleConfig: vi.fn(),
  fetchRemoteAccess: vi.fn(),
  setConsoleConfig: vi.fn(),
  setRemoteAccessPortMapping: vi.fn(),
}));

const baseConfig = {
  backgroundWorkers: 2,
  updateCheckEnabled: false,
  updateCheckChannel: "stable",
  remoteEnrollmentEnabled: false,
  remoteMaxBitrate: 0,
  eventsRetentionDays: 30,
  plugins: [],
};

beforeEach(() => {
  vi.mocked(fetchConsoleConfig).mockResolvedValue(baseConfig);
  setRemoteAccess({
    directHTTPS: null,
    relay: null,
    plainHTTPPublicWarning: false,
    endpoints: [
      { kind: "lan", url: "http://beamfall.local" },
      { kind: "direct", url: "https://example.test" },
      { kind: "overlay", url: "https://overlay.test" },
      { kind: "self-hosted-relay", url: "https://relay.test" },
      { kind: "hosted", url: "https://hosted.test" },
    ],
  });
});

function setRemoteAccess(data: Awaited<ReturnType<typeof fetchRemoteAccess>>) {
  vi.mocked(fetchRemoteAccess).mockResolvedValue(data);
}

function renderStatus() {
  return render(
    <I18nProvider>
      <RemoteAccessStatus />
    </I18nProvider>,
  );
}

describe("RemoteAccessStatus", () => {
  it("surfaces connection classes from remote-access endpoints", async () => {
    renderStatus();

    // One plain line names every class, in place of a chip per class (CORE-1117).
    const line = (await screen.findByText(/^Reachable through/)).textContent;
    for (const label of [
      "Home network",
      "Direct HTTPS",
      "Private network",
      "Self-hosted relay",
      "Hosted relay",
    ]) {
      expect(line).toContain(label);
    }
  });

  it("guides install and polling when no overlay is detected", async () => {
    setRemoteAccess({
      directHTTPS: null,
      relay: null,
      plainHTTPPublicWarning: false,
      endpoints: [{ kind: "lan", url: "http://beamfall.local" }],
    });

    renderStatus();

    expect(await screen.findByText("Not detected")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Install Tailscale" })).toHaveAttribute(
      "href",
      "https://tailscale.com/download",
    );
    expect(screen.getByRole("button", { name: "Check again" })).toBeInTheDocument();
  });

  it("shows the phone test URL when an overlay is present", async () => {
    setRemoteAccess({
      directHTTPS: null,
      relay: null,
      plainHTTPPublicWarning: false,
      endpoints: [{ kind: "overlay", url: "https://100.64.0.5:8096" }],
    });

    renderStatus();

    expect(await screen.findByText("Detected")).toBeInTheDocument();
    expect(screen.getByText("Phone test URL")).toBeInTheDocument();
    expect(screen.getByText("https://100.64.0.5:8096")).toBeInTheDocument();
  });

  it("marks overlay verified when the current host matches it", async () => {
    setRemoteAccess({
      directHTTPS: null,
      relay: null,
      plainHTTPPublicWarning: false,
      endpoints: [{ kind: "overlay", url: "https://localhost:3000" }],
    });

    renderStatus();

    expect(await screen.findByText("Verified")).toBeInTheDocument();
    expect(
      screen.getByText(/already reaches Beamfall through Tailscale or WireGuard/),
    ).toBeInTheDocument();
  });

  it("classifies the current endpoint kind by advertised host", () => {
    expect(
      currentEndpointKind([{ kind: "overlay", url: "https://100.64.0.5:8096" }], {
        host: "100.64.0.5:8096",
      }),
    ).toBe("overlay");
  });

  it("classifies public cleartext Console origins", () => {
    expect(isPublicCleartextConsoleLocation({ protocol: "http:", hostname: "203.0.113.10" })).toBe(
      true,
    );
    expect(isPublicCleartextConsoleLocation({ protocol: "https:", hostname: "203.0.113.10" })).toBe(
      false,
    );
    expect(isPublicCleartextConsoleLocation({ protocol: "http:", hostname: "192.168.1.20" })).toBe(
      false,
    );
  });

  it("shows the server cleartext warning", async () => {
    setRemoteAccess({
      directHTTPS: null,
      relay: null,
      plainHTTPPublicWarning: true,
      endpoints: [],
    });

    renderStatus();

    expect(
      await screen.findByText(
        /This Console is open over an unencrypted connection from the internet/,
      ),
    ).toBeInTheDocument();
  });
});

describe("RemoteBitrateLimit", () => {
  function renderLimit() {
    return render(
      <I18nProvider>
        <RemoteBitrateLimit />
      </I18nProvider>,
    );
  }

  it("shows the stored server-wide cap in Mbit/s", async () => {
    vi.mocked(fetchConsoleConfig).mockResolvedValue({
      ...baseConfig,
      remoteMaxBitrate: 12_500_000,
    });
    renderLimit();
    expect(await screen.findByLabelText("Remote bitrate cap (Mbit/s)")).toHaveValue(12.5);
  });

  it("writes the cap as bits per second and clears it with 0 (CORE-1717)", async () => {
    vi.mocked(setConsoleConfig).mockResolvedValue({ ...baseConfig, remoteMaxBitrate: 4_000_000 });
    const user = userEvent.setup();
    renderLimit();
    const input = await screen.findByLabelText("Remote bitrate cap (Mbit/s)");
    await user.type(input, "4");
    await user.click(screen.getByRole("button", { name: "Apply" }));
    expect(setConsoleConfig).toHaveBeenLastCalledWith({ remoteMaxBitrate: 4_000_000 });
    expect(await screen.findByText("Applied.")).toBeInTheDocument();
    expect(input).toHaveValue(4);

    vi.mocked(setConsoleConfig).mockResolvedValue({ ...baseConfig, remoteMaxBitrate: 0 });
    await user.clear(input);
    await user.click(screen.getByRole("button", { name: "Apply" }));
    expect(setConsoleConfig).toHaveBeenLastCalledWith({ remoteMaxBitrate: 0 });
  });
});

describe("PortMapping (CORE-1969)", () => {
  const lanOnly = {
    directHTTPS: null,
    relay: null,
    plainHTTPPublicWarning: false,
    endpoints: [{ kind: "lan", url: "http://beamfall.local" }],
  };
  const mapped = {
    ...lanOnly,
    portMapping: {
      enabled: true,
      state: "mapped",
      protocol: "natpmp",
      internalPort: 8443,
      externalPort: 18443,
      externalAddress: "203.0.113.7",
      leaseSeconds: 7200,
      expiresAt: "2030-01-02T03:04:05Z",
    },
    reachability: {
      checkedAt: "2030-01-02T01:04:05Z",
      verdicts: [
        { path: "direct", reachable: true, method: "gateway_loopback" },
        { path: "relay", reachable: false, method: "relay", reason: "relay_not_configured" },
      ],
    },
  };

  it("stays hidden when the server has no port-mapping controller", async () => {
    setRemoteAccess(lanOnly);
    renderStatus();
    await screen.findByText(/^Reachable through/);
    expect(screen.queryByTestId("port-mapping")).not.toBeInTheDocument();
  });

  it("shows the mapped port, external address and per-path verdicts with reason codes", async () => {
    setRemoteAccess(mapped);
    renderStatus();

    expect(await screen.findByText("Automatic port mapping")).toBeInTheDocument();
    expect(screen.getByText("Mapped")).toBeInTheDocument();
    expect(screen.getByText("NATPMP")).toBeInTheDocument();
    expect(screen.getByText("18443")).toBeInTheDocument();
    expect(screen.getByText("203.0.113.7")).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "Map a router port automatically" })).toBeChecked();

    const direct = screen.getByTestId("reach-direct");
    expect(direct).toHaveTextContent("Direct (mapped port)");
    expect(direct).toHaveTextContent("Reachable");
    expect(direct).toHaveTextContent(
      "Checked from inside your network through the router’s loopback, not from outside it.",
    );
    const relay = screen.getByTestId("reach-relay");
    expect(relay).toHaveTextContent("Unreachable");
    expect(relay).toHaveTextContent("No relay is configured.");
    expect(relay).toHaveTextContent("relay_not_configured");
  });

  it("calls a failed loopback check unverified, not unreachable", async () => {
    setRemoteAccess({
      ...mapped,
      reachability: {
        checkedAt: "2030-01-02T01:04:05Z",
        verdicts: [
          {
            path: "direct",
            reachable: false,
            method: "gateway_loopback",
            reason: "hairpin_unverified",
          },
          { path: "relay", reachable: false, method: "relay", reason: "timeout" },
        ],
      },
    });
    renderStatus();

    const direct = await screen.findByTestId("reach-direct");
    expect(direct).toHaveTextContent("Not verified");
    expect(direct).not.toHaveTextContent("Unreachable");
    expect(direct).not.toHaveTextContent("The probe timed out.");
    expect(direct).toHaveTextContent("this check runs from inside your network");
    expect(direct).toHaveTextContent("Check from a device outside your network");
    expect(direct).toHaveTextContent("hairpin_unverified");
    const relay = screen.getByTestId("reach-relay");
    expect(relay).toHaveTextContent("Unreachable");
    expect(relay).toHaveTextContent("The probe timed out.");
    expect(relay).not.toHaveTextContent("router’s loopback");
  });

  it("explains a refused mapping with the reason and router code", async () => {
    setRemoteAccess({
      ...lanOnly,
      portMapping: {
        enabled: true,
        state: "failed",
        protocol: "upnp",
        internalPort: 8443,
        leaseSeconds: 0,
        reason: "gateway_refused",
        gatewayCode: 718,
      },
    });
    renderStatus();

    expect(await screen.findByText("Not mapped")).toBeInTheDocument();
    const status = screen.getByRole("status");
    expect(status).toHaveTextContent("The router refused the mapping.");
    expect(status).toHaveTextContent("gateway_refused");
    expect(status).toHaveTextContent("Router code 718");
    expect(screen.getByText("Not checked yet.")).toBeInTheDocument();
  });

  it("keeps a failed removal visible after the opt-out", async () => {
    setRemoteAccess({
      ...lanOnly,
      portMapping: {
        enabled: false,
        state: "disabled",
        internalPort: 8443,
        leaseSeconds: 0,
        reason: "removal_failed",
      },
    });
    renderStatus();

    expect(await screen.findByRole("status")).toHaveTextContent(
      "The router did not confirm removing the mapping.",
    );
    expect(
      screen.getByRole("checkbox", { name: "Map a router port automatically" }),
    ).not.toBeChecked();
  });

  it("toggles the opt-in and reruns the self-test on demand", async () => {
    setRemoteAccess({
      ...lanOnly,
      portMapping: { enabled: false, state: "disabled", internalPort: 8443, leaseSeconds: 0 },
    });
    vi.mocked(setRemoteAccessPortMapping).mockResolvedValue(mapped);
    vi.mocked(checkRemoteAccessReachability).mockResolvedValue({
      ...mapped,
      reachability: {
        checkedAt: "2030-01-02T02:04:05Z",
        verdicts: [{ path: "direct", reachable: false, reason: "double_nat" }],
      },
    });
    const user = userEvent.setup();
    renderStatus();

    await user.click(
      await screen.findByRole("checkbox", { name: "Map a router port automatically" }),
    );
    expect(setRemoteAccessPortMapping).toHaveBeenCalledWith(true);
    expect(await screen.findByText("203.0.113.7")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Check reachability" }));
    expect(checkRemoteAccessReachability).toHaveBeenCalledTimes(1);
    expect(await screen.findByTestId("reach-direct")).toHaveTextContent("double_nat");
    expect(screen.queryByTestId("reach-relay")).not.toBeInTheDocument();
  });

  it("reports a failed request without dropping the last status", async () => {
    setRemoteAccess(mapped);
    vi.mocked(checkRemoteAccessReachability).mockRejectedValue(new Error("network"));
    const user = userEvent.setup();
    renderStatus();

    await user.click(await screen.findByRole("button", { name: "Check reachability" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "The request did not complete. Try again.",
    );
    expect(screen.getByText("203.0.113.7")).toBeInTheDocument();
  });
});
