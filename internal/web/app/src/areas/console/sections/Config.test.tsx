import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setAdvancedSettings } from "@/lib/advancedSettings";
import {
  fetchConsoleConfig,
  fetchTranscodeConfig,
  setConsoleConfig,
  setTranscodeConfig,
} from "@/lib/graphql";
import type { ConsoleConfigInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { Config } from "./Config";

vi.mock("@/lib/graphql", () => ({
  fetchConsoleConfig: vi.fn(),
  fetchTranscodeConfig: vi.fn(),
  setConsoleConfig: vi.fn(),
  setTranscodeConfig: vi.fn(),
}));

// jsdom has no ResizeObserver; the Radix Switch (via react-use-size) needs one.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= ResizeObserverStub as unknown as typeof ResizeObserver;

const savedTranscode = {
  hardwareEnabled: false,
  codec: "",
  quality: "",
  device: "",
  configuredPreference: "",
  hardwareAcceleration: [],
};

const savedConsole: ConsoleConfigInfo = {
  backgroundWorkers: 4,
  updateCheckEnabled: false,
  updateCheckChannel: "stable",
  remoteEnrollmentEnabled: true,
  viewerPairingCodesDisabled: false,
  eventsRetentionDays: 180,
  plugins: [],
};

beforeEach(() => {
  vi.mocked(fetchConsoleConfig).mockResolvedValue(savedConsole);
  vi.mocked(fetchTranscodeConfig).mockResolvedValue(savedTranscode);
});

afterEach(() => {
  cleanup();
  setAdvancedSettings(false);
  vi.mocked(fetchConsoleConfig).mockReset();
  vi.mocked(fetchTranscodeConfig).mockReset();
  vi.mocked(setConsoleConfig).mockReset();
  vi.mocked(setTranscodeConfig).mockReset();
});

function renderConfig() {
  return render(
    <I18nProvider>
      <Config />
    </I18nProvider>,
  );
}

describe("transcoding config", () => {
  it("keeps an edited Advanced field visible in Normal so Save never hides it", async () => {
    vi.mocked(setTranscodeConfig).mockImplementation(async (next) => next);
    setAdvancedSettings(true);
    const user = userEvent.setup();
    renderConfig();

    await user.type(await screen.findByRole("textbox", { name: "Codec" }), "hevc");
    act(() => setAdvancedSettings(false));

    expect(screen.getByRole("textbox", { name: "Codec" })).toHaveValue("hevc");
    expect(screen.queryByRole("textbox", { name: "Quality" })).toBeNull();
  });
});

describe("Viewer pairing-code switch", () => {
  it("is a Normal control that saves on toggle without touching an unsaved transcode edit", async () => {
    vi.mocked(setConsoleConfig).mockResolvedValue({
      ...savedConsole,
      viewerPairingCodesDisabled: true,
    });
    setAdvancedSettings(true);
    const user = userEvent.setup();
    renderConfig();

    const codec = await screen.findByRole("textbox", { name: "Codec" });
    await user.type(codec, "hevc");
    act(() => setAdvancedSettings(false));

    const toggle = screen.getByRole("switch", { name: /Viewer pairing codes/i });
    await waitFor(() => expect(toggle).toBeEnabled());
    await user.click(toggle);

    await waitFor(() => expect(toggle).toBeChecked());
    expect(setConsoleConfig).toHaveBeenCalledWith({ viewerPairingCodesDisabled: true });
    expect(setTranscodeConfig).not.toHaveBeenCalled();
    expect(screen.getByRole("textbox", { name: "Codec" })).toHaveValue("hevc");
  });
});
