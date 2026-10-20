import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  fetchConsoleConfig,
  fetchDevices,
  mintPairingCode,
  revokeAllDevices,
  revokeDevice,
  setConsoleConfig,
  setDeviceRole,
} from "@/lib/graphql";
import type { ConsoleConfigInfo, DeviceInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { Devices } from "./Devices";

vi.mock("@/lib/graphql", () => ({
  fetchDevices: vi.fn(),
  revokeDevice: vi.fn(),
  revokeAllDevices: vi.fn(),
  fetchConsoleConfig: vi.fn(),
  setConsoleConfig: vi.fn(),
  mintPairingCode: vi.fn(),
  setDeviceRole: vi.fn(),
}));

const config: ConsoleConfigInfo = {
  backgroundWorkers: 4,
  updateCheckEnabled: false,
  updateCheckChannel: "stable",
  remoteEnrollmentEnabled: true,
  eventsRetentionDays: 180,
  plugins: [],
};

const devices: DeviceInfo[] = [
  { deviceID: "device-mac", name: "My Browser", role: "manager", revoked: false, current: true },
  { deviceID: "device-tv", name: "Living Room TV", role: "viewer", revoked: false, current: false },
  {
    deviceID: "device-tablet",
    name: "Kitchen Tablet",
    role: "manager",
    revoked: false,
    current: false,
  },
];

function renderDevices() {
  return render(
    <I18nProvider>
      <Devices />
    </I18nProvider>,
  );
}

beforeEach(() => {
  vi.mocked(fetchDevices).mockResolvedValue(devices);
  vi.mocked(fetchConsoleConfig).mockResolvedValue(config);
  vi.mocked(revokeDevice).mockResolvedValue({ deviceID: "device-tv", transcodesAborted: 0 });
  vi.mocked(revokeAllDevices).mockResolvedValue({ revokedCount: 1, transcodesAborted: 0 });
  vi.mocked(setConsoleConfig).mockResolvedValue({ ...config, remoteEnrollmentEnabled: false });
  vi.mocked(mintPairingCode).mockResolvedValue({
    code: "PAIR1234",
    expiresAt: "2026-06-20T00:00:00Z",
  });
  vi.mocked(setDeviceRole).mockResolvedValue({
    ...devices[1],
    role: "manager",
  });
});

afterEach(cleanup);

describe("Devices (ADR-0081 administration)", () => {
  it("lists devices with roles and marks this device — never offering to revoke it", async () => {
    renderDevices();

    const currentRow = (await screen.findByText("My Browser")).closest("tr")!;
    expect(within(currentRow).getByText(/this device/i)).toBeInTheDocument();
    // The current device has NO revoke button (it would lock the operator out).
    expect(within(currentRow).queryByRole("button", { name: /^revoke$/i })).toBeNull();

    const otherRow = screen.getByText("Living Room TV").closest("tr")!;
    expect(within(otherRow).getByText(/viewer/i)).toBeInTheDocument();
    expect(within(otherRow).getByRole("button", { name: /^promote$/i })).toBeInTheDocument();
    expect(within(otherRow).getByRole("button", { name: /^revoke$/i })).toBeInTheDocument();
  });

  it("promotes a non-current device behind a confirm", async () => {
    renderDevices();
    const otherRow = (await screen.findByText("Living Room TV")).closest("tr")!;
    await userEvent.click(within(otherRow).getByRole("button", { name: /^promote$/i }));
    const dialog = await screen.findByRole("alertdialog");
    expect(
      within(dialog).getByRole("heading", { name: /promote this device to manager/i }),
    ).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: /^promote$/i }));
    await waitFor(() => expect(setDeviceRole).toHaveBeenCalledWith("device-tv", "manager"));
  });

  it("demotes a non-current manager device behind a confirm", async () => {
    renderDevices();
    const otherRow = (await screen.findByText("Kitchen Tablet")).closest("tr")!;
    await userEvent.click(within(otherRow).getByRole("button", { name: /^demote$/i }));
    const dialog = await screen.findByRole("alertdialog");
    expect(
      within(dialog).getByRole("heading", { name: /demote this device to viewer/i }),
    ).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: /^demote$/i }));
    await waitFor(() => expect(setDeviceRole).toHaveBeenCalledWith("device-tablet", "viewer"));
  });

  it("revokes a non-current device behind a confirm", async () => {
    renderDevices();
    const otherRow = (await screen.findByText("Living Room TV")).closest("tr")!;
    await userEvent.click(within(otherRow).getByRole("button", { name: /^revoke$/i }));
    // The confirm dialog opens; click its action (scoped to the dialog so we
    // don't re-click the trigger).
    const dialog = await screen.findByRole("alertdialog");
    expect(
      within(dialog).getAllByText(/invalidates its sessions and stream tokens/i).length,
    ).toBeGreaterThan(0);
    await userEvent.click(within(dialog).getByRole("button", { name: /^revoke$/i }));
    await waitFor(() => expect(revokeDevice).toHaveBeenCalledWith("device-tv"));
  });

  it("mints a manager pairing code behind a confirm and shows the code", async () => {
    renderDevices();
    await screen.findByText("My Browser");
    await userEvent.click(screen.getByRole("button", { name: /manager code/i }));
    const dialog = await screen.findByRole("alertdialog");
    expect(
      within(dialog).getByRole("heading", { name: /whoever redeems it can manage this server/i }),
    ).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: /manager code/i }));
    await waitFor(() => expect(mintPairingCode).toHaveBeenCalledWith("manager"));
    expect(await screen.findByText("PAIR1234")).toBeInTheDocument();
  });

  it("persists the remote-enrollment toggle", async () => {
    renderDevices();
    const toggle = await screen.findByRole("switch", { name: /allow remote enrollment/i });
    await userEvent.click(toggle);
    await waitFor(() =>
      expect(setConsoleConfig).toHaveBeenCalledWith({ remoteEnrollmentEnabled: false }),
    );
  });
});
