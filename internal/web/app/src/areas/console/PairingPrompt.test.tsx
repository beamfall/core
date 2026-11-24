import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api";
import {
  decidePairingRequest,
  fetchConsoleConfig,
  fetchDevices,
  fetchPairingRequests,
  fetchRemoteAccess,
  type PairingRequest,
} from "@/lib/graphql";
import { formatMessage, I18nProvider } from "@/lib/i18n";
import type { Locale } from "@/strings";
import { profileDeviceRequestStrings } from "@/strings/profiles-devices-requests";
import { PAIRING_POLL_MS, PairingRequestsProvider } from "./lib/pairingRequests";
import { PairingPrompt } from "./PairingPrompt";
import { Devices } from "./sections/Devices";

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
  fetchPlayerProfiles: vi.fn(async () => []),
  setDevicePlayerBinding: vi.fn(),
  clearDevicePlayerBinding: vi.fn(),
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

const office: PairingRequest = {
  ...tv,
  code: "MNGR-0001",
  deviceName: "Office Mac",
  requestedRole: "manager",
};

// The prompt is a labelled region, not a dialog: it never takes the page over.
const findPrompt = () => screen.findByRole("region", { name: "A device wants to join" });
const queryPrompt = () => screen.queryByRole("region", { name: "A device wants to join" });

function renderPrompt() {
  return render(
    <I18nProvider>
      <MemoryRouter>
        <PairingRequestsProvider>
          <PairingPrompt />
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

describe("PairingPrompt (CORE-2064)", () => {
  it("shows the request with its code and approves with defaults in one click", async () => {
    renderPrompt();
    const prompt = await findPrompt();
    // Untrusted text renders as text, never as markup.
    expect(within(prompt).getByText("Living Room <b>TV</b>")).toBeInTheDocument();
    expect(within(prompt).getByText(/tvOS/)).toBeInTheDocument();
    expect(within(prompt).getByText(/viewer/i)).toBeInTheDocument();
    expect(within(prompt).getByText(/on this network/i)).toBeInTheDocument();
    expect(within(prompt).getByText("ABCD-1234")).toBeInTheDocument();

    vi.mocked(fetchPairingRequests).mockResolvedValue([]);
    await userEvent.click(within(prompt).getByRole("button", { name: "Approve" }));
    expect(decidePairingRequest).toHaveBeenCalledTimes(1);
    expect(decidePairingRequest).toHaveBeenCalledWith("approve", "ABCD-1234");
    await waitFor(() => expect(queryPrompt()).toBeNull());
  });

  it("never moves focus to a control that grants access", async () => {
    renderPrompt();
    const prompt = await findPrompt();
    expect(within(prompt).getByRole("button", { name: "Approve" })).not.toHaveFocus();
    expect(prompt.contains(document.activeElement)).toBe(false);
    expect(document.activeElement).toBe(document.body);
    // A key press meant for the page approves nothing.
    await userEvent.keyboard("{Enter}");
    await userEvent.keyboard(" ");
    expect(decidePairingRequest).not.toHaveBeenCalled();
  });

  it("is a polite announcement, not a dialog", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "Date"], shouldAdvanceTime: true });
    vi.mocked(fetchPairingRequests).mockResolvedValue([]);
    renderPrompt();
    await waitFor(() => expect(fetchPairingRequests).toHaveBeenCalledTimes(1));
    // The live region is there before anything asks, so the request is announced when it lands.
    const announcer = screen.getByRole("status");
    expect(announcer).toHaveAttribute("aria-live", "polite");
    expect(announcer).toBeEmptyDOMElement();

    vi.mocked(fetchPairingRequests).mockResolvedValue([tv]);
    await act(() => vi.advanceTimersByTimeAsync(PAIRING_POLL_MS));
    const prompt = await findPrompt();
    expect(announcer).toHaveTextContent("Living Room <b>TV</b> is asking to join this server.");
    expect(announcer).toHaveTextContent("ABCD-1234");
    expect(prompt.contains(announcer)).toBe(false);
    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(prompt).toHaveAccessibleDescription(/only if this code matches/i);
  });

  it("makes manager a separate, named choice that shows what the device asked for", async () => {
    vi.mocked(fetchPairingRequests).mockResolvedValue([office]);
    renderPrompt();
    const prompt = await findPrompt();
    expect(within(prompt).getByText(/wants to join as manager/i)).toBeInTheDocument();
    expect(within(prompt).getByText(/asked for the manager role/i)).toBeInTheDocument();
    expect(within(prompt).queryByRole("button", { name: "Approve" })).toBeNull();
    expect(prompt.contains(document.activeElement)).toBe(false);

    // The ordinary approval names no role, so the server grants Viewer.
    await userEvent.click(within(prompt).getByRole("button", { name: "Approve as viewer" }));
    expect(decidePairingRequest).toHaveBeenLastCalledWith("approve", "MNGR-0001");

    await userEvent.click(within(prompt).getByRole("button", { name: "Approve as manager" }));
    expect(decidePairingRequest).toHaveBeenLastCalledWith("approve", "MNGR-0001", {
      deviceRole: "manager",
    });
  });

  it("offers no manager approval to a device that asked to be a viewer", async () => {
    renderPrompt();
    const prompt = await findPrompt();
    expect(within(prompt).queryByRole("button", { name: /manager/i })).toBeNull();
  });

  it("denies a request", async () => {
    renderPrompt();
    const prompt = await findPrompt();
    vi.mocked(fetchPairingRequests).mockResolvedValue([]);
    await userEvent.click(within(prompt).getByRole("button", { name: "Deny" }));
    expect(decidePairingRequest).toHaveBeenCalledWith("deny", "ABCD-1234");
    await waitFor(() => expect(queryPrompt()).toBeNull());
  });

  it("keeps the prompt and says so when the decision fails", async () => {
    vi.mocked(decidePairingRequest).mockRejectedValue(new ApiError("boom", 500));
    renderPrompt();
    const prompt = await findPrompt();
    await userEvent.click(within(prompt).getByRole("button", { name: "Approve" }));
    expect(await within(prompt).findByText(/didn't go through/i)).toBeInTheDocument();
  });

  it("clears a request that is already gone (404)", async () => {
    vi.mocked(decidePairingRequest).mockRejectedValue(new ApiError("gone", 404));
    renderPrompt();
    const prompt = await findPrompt();
    vi.mocked(fetchPairingRequests).mockResolvedValue([]);
    await userEvent.click(within(prompt).getByRole("button", { name: "Approve" }));
    await waitFor(() => expect(queryPrompt()).toBeNull());
  });

  it("says so when the server refuses the decision and keeps the request (404)", async () => {
    vi.mocked(decidePairingRequest).mockRejectedValue(new ApiError("refused", 404));
    renderPrompt();
    const prompt = await findPrompt();
    await userEvent.click(within(prompt).getByRole("button", { name: "Approve" }));
    expect(await within(prompt).findByText(/didn't go through/i)).toBeInTheDocument();
  });

  it("shows nothing for a non-admin session or a server without the endpoint", async () => {
    vi.mocked(fetchPairingRequests).mockRejectedValue(new ApiError("forbidden", 403));
    renderPrompt();
    await screen.findByText(/no devices are waiting/i);
    expect(queryPrompt()).toBeNull();
    cleanup();

    vi.mocked(fetchPairingRequests).mockRejectedValue(new ApiError("missing", 404));
    renderPrompt();
    await screen.findByText(/no devices are waiting/i);
    expect(queryPrompt()).toBeNull();
  });

  it("appears on the next poll without a reload, then clears when it expires", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "Date"], shouldAdvanceTime: true });
    vi.mocked(fetchPairingRequests).mockResolvedValue([]);
    renderPrompt();
    await waitFor(() => expect(fetchPairingRequests).toHaveBeenCalledTimes(1));
    expect(queryPrompt()).toBeNull();

    vi.mocked(fetchPairingRequests).mockResolvedValue([
      { ...tv, expiresAt: new Date(Date.now() + 8_000).toISOString() },
    ]);
    await act(() => vi.advanceTimersByTimeAsync(PAIRING_POLL_MS));
    expect(await findPrompt()).toBeInTheDocument();

    // The server stops listing it once expired; the client drops it either way.
    await act(() => vi.advanceTimersByTimeAsync(PAIRING_POLL_MS));
    await waitFor(() => expect(queryPrompt()).toBeNull());
  });

  it("points at the rest of the queue when several devices wait", async () => {
    vi.mocked(fetchPairingRequests).mockResolvedValue([tv, tablet]);
    renderPrompt();
    const prompt = await findPrompt();
    expect(within(prompt).getByRole("link", { name: /1 more waiting/i })).toBeInTheDocument();
  });

  it("counts the rest of the queue in each language's plural form", () => {
    const more = (locale: Locale, count: number) =>
      formatMessage(profileDeviceRequestStrings[locale].pairingMoreWaiting, { count }, locale);
    expect(more("en", 1)).toBe("1 more waiting. Open Devices");
    expect(more("en", 3)).toBe("3 more waiting. Open Devices");
    expect(more("fr", 1)).toBe("1 autre en attente. Ouvrir Appareils");
    expect(more("fr", 2)).toBe("2 autres en attente. Ouvrir Appareils");
    expect(more("de", 1)).toBe("1 weiteres Gerät wartet. Geräte öffnen");
    expect(more("de", 2)).toBe("2 weitere Geräte warten. Geräte öffnen");
    expect(more("es", 1)).toBe("1 más en espera. Abrir Dispositivos");
    expect(more("es", 2)).toBe("2 más en espera. Abrir Dispositivos");
    expect(more("pt", 1)).toBe("Mais 1 aguardando. Abrir Dispositivos");
    expect(more("pt", 2)).toBe("Mais 2 aguardando. Abrir Dispositivos");
    for (const catalog of Object.values(profileDeviceRequestStrings)) {
      expect(catalog.pairingMoreWaiting).toContain("plural");
    }
  });
});
