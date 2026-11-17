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
import { I18nProvider } from "@/lib/i18n";
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
    const prompt = await screen.findByRole("alertdialog");
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
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
  });

  it("denies a request", async () => {
    renderPrompt();
    const prompt = await screen.findByRole("alertdialog");
    vi.mocked(fetchPairingRequests).mockResolvedValue([]);
    await userEvent.click(within(prompt).getByRole("button", { name: "Deny" }));
    expect(decidePairingRequest).toHaveBeenCalledWith("deny", "ABCD-1234");
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
  });

  it("keeps the prompt and says so when the decision fails", async () => {
    vi.mocked(decidePairingRequest).mockRejectedValue(new ApiError("boom", 500));
    renderPrompt();
    const prompt = await screen.findByRole("alertdialog");
    await userEvent.click(within(prompt).getByRole("button", { name: "Approve" }));
    expect(await within(prompt).findByText(/didn't go through/i)).toBeInTheDocument();
  });

  it("clears a request that is already gone (404)", async () => {
    vi.mocked(decidePairingRequest).mockRejectedValue(new ApiError("gone", 404));
    renderPrompt();
    const prompt = await screen.findByRole("alertdialog");
    vi.mocked(fetchPairingRequests).mockResolvedValue([]);
    await userEvent.click(within(prompt).getByRole("button", { name: "Approve" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
  });

  it("says so when the server refuses the decision and keeps the request (404)", async () => {
    vi.mocked(decidePairingRequest).mockRejectedValue(new ApiError("refused", 404));
    renderPrompt();
    const prompt = await screen.findByRole("alertdialog");
    await userEvent.click(within(prompt).getByRole("button", { name: "Approve" }));
    expect(await within(prompt).findByText(/didn't go through/i)).toBeInTheDocument();
  });

  it("shows nothing for a non-admin session or a server without the endpoint", async () => {
    vi.mocked(fetchPairingRequests).mockRejectedValue(new ApiError("forbidden", 403));
    renderPrompt();
    await screen.findByText(/no devices are waiting/i);
    expect(screen.queryByRole("alertdialog")).toBeNull();
    cleanup();

    vi.mocked(fetchPairingRequests).mockRejectedValue(new ApiError("missing", 404));
    renderPrompt();
    await screen.findByText(/no devices are waiting/i);
    expect(screen.queryByRole("alertdialog")).toBeNull();
  });

  it("appears on the next poll without a reload, then clears when it expires", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "Date"], shouldAdvanceTime: true });
    vi.mocked(fetchPairingRequests).mockResolvedValue([]);
    renderPrompt();
    await waitFor(() => expect(fetchPairingRequests).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole("alertdialog")).toBeNull();

    vi.mocked(fetchPairingRequests).mockResolvedValue([
      { ...tv, expiresAt: new Date(Date.now() + 8_000).toISOString() },
    ]);
    await act(() => vi.advanceTimersByTimeAsync(PAIRING_POLL_MS));
    expect(await screen.findByRole("alertdialog")).toBeInTheDocument();

    // The server stops listing it once expired; the client drops it either way.
    await act(() => vi.advanceTimersByTimeAsync(PAIRING_POLL_MS));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
  });

  it("points at the rest of the queue when several devices wait", async () => {
    vi.mocked(fetchPairingRequests).mockResolvedValue([tv, tablet]);
    renderPrompt();
    const prompt = await screen.findByRole("alertdialog");
    expect(within(prompt).getByRole("link", { name: /1 more waiting/i })).toBeInTheDocument();
  });
});
