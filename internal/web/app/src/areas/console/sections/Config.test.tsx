import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchConsoleConfig, fetchTranscodeConfig, setConsoleConfig } from "@/lib/graphql";
import type { ConsoleConfigInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { Config } from "./Config";

// jsdom has no ResizeObserver; the Radix Switch (via react-use-size) needs one.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= ResizeObserverStub as unknown as typeof ResizeObserver;

vi.mock("@/lib/graphql", () => ({
  fetchConsoleConfig: vi.fn(),
  fetchTranscodeConfig: vi.fn(),
  setConsoleConfig: vi.fn(),
  setTranscodeConfig: vi.fn(),
}));

const saved: ConsoleConfigInfo = {
  backgroundWorkers: 4,
  updateCheckEnabled: false,
  updateCheckChannel: "stable",
  remoteEnrollmentEnabled: true,
  viewerPairingCodesDisabled: false,
  eventsRetentionDays: 180,
  plugins: [],
};

beforeEach(() => {
  vi.mocked(fetchConsoleConfig).mockReset().mockResolvedValue(saved);
  vi.mocked(fetchTranscodeConfig).mockReset().mockResolvedValue({
    hardwareEnabled: false,
    codec: "",
    quality: "",
    device: "",
    configuredPreference: "",
    hardwareAcceleration: [],
  });
  vi.mocked(setConsoleConfig).mockReset();
});

function renderConfig() {
  return render(
    <I18nProvider>
      <Config />
    </I18nProvider>,
  );
}

describe("Config server section", () => {
  it("keeps an unsaved worker-count edit when the Viewer-code switch is toggled", async () => {
    vi.mocked(setConsoleConfig).mockResolvedValue({ ...saved, viewerPairingCodesDisabled: true });
    const user = userEvent.setup();
    renderConfig();

    const workers = await screen.findByRole("spinbutton", { name: "Background workers" });
    await waitFor(() => expect(workers).toHaveValue(4));
    await user.clear(workers);
    await user.type(workers, "9");

    const toggle = screen.getByRole("switch", { name: /Viewer pairing codes/i });
    await user.click(toggle);

    await waitFor(() => expect(toggle).toBeChecked());
    expect(setConsoleConfig).toHaveBeenCalledWith({ viewerPairingCodesDisabled: true });
    expect(workers).toHaveValue(9);
  });

  it("shows the saved worker count after saving", async () => {
    vi.mocked(setConsoleConfig).mockResolvedValue({ ...saved, backgroundWorkers: 7 });
    const user = userEvent.setup();
    renderConfig();

    const workers = await screen.findByRole("spinbutton", { name: "Background workers" });
    await waitFor(() => expect(workers).toHaveValue(4));
    await user.clear(workers);
    await user.type(workers, "7{Enter}");

    expect(setConsoleConfig).toHaveBeenCalledWith({ backgroundWorkers: 7 });
    await waitFor(() => expect(workers).toHaveValue(7));
  });
});
