import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  fetchConsoleConfig,
  fetchDevices,
  fetchRemoteAccess,
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
  fetchPlayerProfiles: vi.fn(async () => []),
  setDevicePlayerBinding: vi.fn(),
  clearDevicePlayerBinding: vi.fn(),
  fetchRemoteAccess: vi.fn(),
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
      <MemoryRouter>
        <Devices />
      </MemoryRouter>
    </I18nProvider>,
  );
}

beforeEach(() => {
  vi.mocked(fetchDevices).mockResolvedValue(devices);
  vi.mocked(fetchConsoleConfig).mockResolvedValue(config);
  vi.mocked(fetchRemoteAccess).mockResolvedValue({ plainHTTPPublicWarning: false, endpoints: [] });
  vi.mocked(revokeDevice).mockResolvedValue({ deviceID: "device-tv", transcodesAborted: 0 });
  vi.mocked(revokeAllDevices).mockResolvedValue({ revokedCount: 1, transcodesAborted: 0 });
  vi.mocked(setConsoleConfig).mockResolvedValue({ ...config, remoteEnrollmentEnabled: false });
  vi.mocked(mintPairingCode).mockResolvedValue({
    code: "PAIR1234",
    expiresAt: new Date(Date.now() + 10 * 60 * 1000).toISOString(),
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
    await userEvent.click(screen.getByRole("button", { name: /^[^:]*manager code/i }));
    const dialog = await screen.findByRole("alertdialog");
    expect(
      within(dialog).getByRole("heading", { name: /whoever redeems it can manage this server/i }),
    ).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: /manager code/i }));
    await waitFor(() => expect(mintPairingCode).toHaveBeenCalledWith("manager"));
    expect(await screen.findByText("PAIR1234")).toBeInTheDocument();
    expect(screen.getByText(/^Manager pairing code\./)).toBeInTheDocument();
  });

  it("shows the code's role, time left and the enroll address, and copies a link carrying the code in the fragment", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    renderDevices();
    await screen.findByText("My Browser");
    await userEvent.click(screen.getByRole("button", { name: /viewer code/i }));

    expect(await screen.findByText(/^Viewer pairing code\./)).toBeInTheDocument();
    expect(screen.getByRole("timer")).toHaveTextContent(/^Expires in (10:00|9:[0-5]\d)$/);
    const enrollURL = `${window.location.origin}/enroll`;
    expect(screen.getByText(enrollURL)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Copy link" }));
    expect(writeText).toHaveBeenCalledWith(`${enrollURL}#code=PAIR1234`);
    expect(writeText.mock.calls[0][0]).not.toContain("?");
    expect(screen.queryByRole("textbox", { name: "Link with the pairing code" })).toBeNull();
  });

  it.each([
    ["is missing", undefined],
    ["refuses", { writeText: vi.fn().mockRejectedValue(new Error("NotAllowedError")) }],
  ])(
    "reveals the link selected in a read-only field when the clipboard %s (CORE-1607)",
    async (_label, clipboard) => {
      Object.defineProperty(navigator, "clipboard", { configurable: true, value: clipboard });
      const log = vi.spyOn(console, "log");
      renderDevices();
      await screen.findByText("My Browser");
      await userEvent.click(screen.getByRole("button", { name: /viewer code/i }));
      await userEvent.click(await screen.findByRole("button", { name: "Copy link" }));

      const link = `${window.location.origin}/enroll#code=PAIR1234`;
      const field = await screen.findByRole("textbox", { name: "Link with the pairing code" });
      expect(field).toHaveValue(link);
      expect(field).toHaveAttribute("readonly");
      expect(field).toHaveAccessibleDescription(/press Ctrl\+C or ⌘C/);
      await waitFor(() => expect(field).toHaveFocus());
      const input = field as HTMLInputElement;
      expect([input.selectionStart, input.selectionEnd]).toEqual([0, link.length]);
      expect(link).not.toContain("?");
      expect(log).not.toHaveBeenCalledWith(expect.stringContaining("PAIR1234"));
      log.mockRestore();
    },
  );

  it("shows a QR of the LAN enroll URL with the URL as text when the Console is on loopback (CORE-1614)", async () => {
    vi.mocked(fetchRemoteAccess).mockResolvedValue({
      plainHTTPPublicWarning: false,
      endpoints: [{ kind: "lan", url: "http://192.168.1.10:8800" }],
    });
    renderDevices();
    await screen.findByText("My Browser");
    await userEvent.click(screen.getByRole("button", { name: /viewer code/i }));

    const target = "http://192.168.1.10:8800/enroll";
    const qr = await screen.findByRole("img", { name: `QR code that opens ${target}` });
    expect(qr).toHaveAttribute("data-qr-value", target);
    expect(qr.getAttribute("data-qr-value")).not.toContain("PAIR1234");
    expect(screen.getByText(target)).toBeInTheDocument();
  });

  it("explains how to get a QR instead of encoding localhost when the server has no LAN endpoint", async () => {
    renderDevices();
    await screen.findByText("My Browser");
    await userEvent.click(screen.getByRole("button", { name: /viewer code/i }));

    expect(
      await screen.findByText(/open this Console at the server's network address/),
    ).toBeInTheDocument();
    expect(screen.queryByRole("img", { name: /QR code/ })).toBeNull();
  });

  it("says when a minted code has expired instead of showing it", async () => {
    vi.mocked(mintPairingCode).mockResolvedValue({
      code: "PAIR1234",
      expiresAt: new Date(Date.now() - 1000).toISOString(),
    });
    renderDevices();
    await screen.findByText("My Browser");
    await userEvent.click(screen.getByRole("button", { name: /viewer code/i }));

    expect(await screen.findByText(/This code has expired/)).toBeInTheDocument();
    expect(screen.queryByText("PAIR1234")).not.toBeInTheDocument();
  });

  it("shows a browser that signed out as signed out, not active", async () => {
    vi.mocked(fetchDevices).mockResolvedValue([devices[0], { ...devices[1], signedIn: false }]);
    renderDevices();

    const row = (await screen.findByText("Living Room TV")).closest("tr")!;
    expect(within(row).getByText("Signed out")).toBeInTheDocument();
    expect(within(row).queryByText("Active")).toBeNull();
  });

  // CORE-2324: same-named devices are told apart by their ID tail, and revoked
  // devices are folded away from the live list.
  it("tells same-named devices apart and folds revoked devices away", async () => {
    vi.mocked(fetchDevices).mockResolvedValue([
      {
        deviceID: "dev-aaa111",
        name: "Chrome on macOS",
        role: "manager",
        revoked: false,
        current: true,
      },
      {
        deviceID: "dev-bbb222",
        name: "Chrome on macOS",
        role: "viewer",
        revoked: false,
        current: false,
      },
      { deviceID: "dev-ccc333", name: "Old Phone", role: "viewer", revoked: true, current: false },
    ]);
    renderDevices();

    expect(await screen.findByText("ID …aaa111")).toBeInTheDocument();
    expect(screen.getByText("ID …bbb222")).toBeInTheDocument();
    const fold = screen.getByText("1 revoked device").closest("details")!;
    expect(fold).not.toHaveAttribute("open");
    expect(within(fold).getByText("Old Phone")).toBeInTheDocument();
    expect(screen.getByText("ID …bbb222").closest("details")).toBeNull();
  });

  it("persists the remote-enrollment toggle", async () => {
    renderDevices();
    const toggle = await screen.findByRole("switch", { name: /allow remote enrollment/i });
    await userEvent.click(toggle);
    await waitFor(() =>
      expect(setConsoleConfig).toHaveBeenCalledWith({ remoteEnrollmentEnabled: false }),
    );
  });

  it("explains pairing codes and remote enrollment and links to remote access (CORE-2445)", async () => {
    renderDevices();
    await screen.findByRole("switch", { name: /allow remote enrollment/i });
    expect(
      screen.getByRole("button", { name: "Help: Viewer and manager codes" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /^Help: Allow remote enrollment/i }),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Remote access settings" })).toHaveAttribute(
      "href",
      "/console/remote",
    );
  });
});
