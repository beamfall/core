import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setAdvancedSettings } from "@/lib/advancedSettings";
import { fetchTranscodeConfig, setTranscodeConfig } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { Config } from "./Config";

vi.mock("@/lib/graphql", () => ({
  fetchTranscodeConfig: vi.fn(),
  setTranscodeConfig: vi.fn(),
}));

// jsdom has no ResizeObserver; the Radix Switch (via react-use-size) needs one.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= ResizeObserverStub as unknown as typeof ResizeObserver;

const saved = {
  hardwareEnabled: false,
  codec: "",
  quality: "",
  device: "",
  configuredPreference: "",
  hardwareAcceleration: [],
};

afterEach(() => {
  cleanup();
  setAdvancedSettings(false);
  vi.mocked(fetchTranscodeConfig).mockReset();
  vi.mocked(setTranscodeConfig).mockReset();
});

describe("transcoding config", () => {
  it("keeps an edited Advanced field visible in Normal so Save never hides it", async () => {
    vi.mocked(fetchTranscodeConfig).mockResolvedValue(saved);
    vi.mocked(setTranscodeConfig).mockImplementation(async (next) => next);
    setAdvancedSettings(true);
    const user = userEvent.setup();
    render(
      <I18nProvider>
        <Config />
      </I18nProvider>,
    );

    await user.type(await screen.findByRole("textbox", { name: "Codec" }), "hevc");
    act(() => setAdvancedSettings(false));

    expect(screen.getByRole("textbox", { name: "Codec" })).toHaveValue("hevc");
    expect(screen.queryByRole("textbox", { name: "Quality" })).toBeNull();
  });
});
