import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  decidePairingRequest,
  fetchConsoleConfig,
  fetchDevices,
  fetchPairingRequests,
  fetchRemoteAccess,
  type PairingRequest,
} from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { PairingRequestsProvider } from "../lib/pairingRequests";
import { Devices } from "./Devices";

vi.mock("@/lib/graphql", () => ({
  fetchPairingRequests: vi.fn(),
  decidePairingRequest: vi.fn(),
  fetchDevices: vi.fn(),
  fetchConsoleConfig: vi.fn(),
  fetchRemoteAccess: vi.fn(),
  setConsoleConfig: vi.fn(),
  mintPairingCode: vi.fn(),
  revokeDevice: vi.fn(),
  revokeAllDevices: vi.fn(),
  setDeviceRole: vi.fn(),
}));

const tv: PairingRequest = {
  code: "ABCD-1234",
  deviceName: "Living Room <b>TV</b>",
  platform: "tvOS",
  requestedRole: "viewer",
  lan: true,
  requestedAt: new Date(Date.now() - 20_000).toISOString(),
  expiresAt: new Date(Date.now() + 300_000).toISOString(),
};
const tablet: PairingRequest = { ...tv, code: "WXYZ-9876", deviceName: "Tablet", lan: false };

function renderPrompt() {
  return render(
    <I18nProvider>
      <MemoryRouter>
        <PairingRequestsProvider>
          <Devices />
        </PairingRequestsProvider>
      </MemoryRouter>
    </I18nProvider>,
  );
}

beforeEach(() => {
  vi.mocked(fetchPairingRequests).mockResolvedValue([tv]);
  vi.mocked(decidePairingRequest).mockResolvedValue(undefined);
  vi.mocked(fetchDevices).mockResolvedValue([]);
  vi.mocked(fetchConsoleConfig).mockResolvedValue({
    backgroundWorkers: 4,
    updateCheckEnabled: false,
    updateCheckChannel: "stable",
    remoteEnrollmentEnabled: false,
    eventsRetentionDays: 180,
    plugins: [],
  });
  vi.mocked(fetchRemoteAccess).mockResolvedValue({ plainHTTPPublicWarning: false, endpoints: [] });
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.clearAllMocks();
});

describe("Devices pending list (CORE-0317)", () => {
  it("lists pending requests and decides them from the table", async () => {
    vi.mocked(fetchPairingRequests).mockResolvedValue([tv, tablet]);
    renderPrompt();
    const row = (await screen.findByText("Tablet")).closest("tr")!;
    expect(within(row).getByText("WXYZ-9876")).toBeInTheDocument();
    expect(within(row).getByText(/not on this network/i)).toBeInTheDocument();
    await userEvent.click(within(row).getByRole("button", { name: "Deny" }));
    expect(decidePairingRequest).toHaveBeenCalledWith("deny", "WXYZ-9876");
  });

  it("puts the request named by the ?code link first and marks it", async () => {
    window.history.pushState({}, "", "/console/access?code=WXYZ-9876");
    try {
      vi.mocked(fetchPairingRequests).mockResolvedValue([tv, tablet]);
      renderPrompt();
      await screen.findByText(/from your link/i);
      const rows = screen.getAllByRole("row").slice(1);
      expect(within(rows[0]).getByText("Tablet")).toBeInTheDocument();
    } finally {
      window.history.pushState({}, "", "/");
    }
  });
});
