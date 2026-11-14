import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setAdvancedSettings } from "@/lib/advancedSettings";
import {
  fetchConsoleConfig,
  fetchPrePlayClips,
  fetchTranscodeConfig,
  search,
  setConsoleConfig,
  setPrePlayClips,
  setTranscodeConfig,
} from "@/lib/graphql";
import type { ConsoleConfigInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { Config } from "./Config";

vi.mock("@/lib/graphql", () => ({
  MAX_PRE_PLAY_CLIPS: 2,
  fetchConsoleConfig: vi.fn(),
  fetchPrePlayClips: vi.fn(),
  fetchTranscodeConfig: vi.fn(),
  search: vi.fn(),
  setConsoleConfig: vi.fn(),
  setPrePlayClips: vi.fn(),
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
  vi.mocked(fetchPrePlayClips).mockResolvedValue({ items: [] });
});

afterEach(() => {
  cleanup();
  setAdvancedSettings(false);
  vi.mocked(fetchConsoleConfig).mockReset();
  vi.mocked(fetchTranscodeConfig).mockReset();
  vi.mocked(setConsoleConfig).mockReset();
  vi.mocked(setTranscodeConfig).mockReset();
  vi.mocked(fetchPrePlayClips).mockReset();
  vi.mocked(setPrePlayClips).mockReset();
  vi.mocked(search).mockReset();
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

const clip = (mediaID: string, title: string) => ({
  mediaID,
  title,
  durationSeconds: 30,
  role: "clip",
  label: "",
});

describe("pre-play clips", () => {
  it("adds from search, reorders, removes and saves the list in order", async () => {
    vi.mocked(fetchPrePlayClips).mockResolvedValue({ items: [clip("m-1", "Intro One")] });
    vi.mocked(search).mockResolvedValue([
      { id: "m-1", kind: "clip", title: "Intro One" },
      { id: "m-2", kind: "clip", title: "Intro Two" },
      { id: "m-3", kind: "clip", title: "Intro Three" },
    ]);
    vi.mocked(setPrePlayClips).mockImplementation(async (ids) => ({
      items: ids.map((id) => clip(id, id)),
    }));
    const user = userEvent.setup();
    renderConfig();

    expect(await screen.findByText("Intro One")).toBeInTheDocument();
    expect(screen.getByText(/Up to 2\./)).toBeInTheDocument();
    await user.type(screen.getByRole("searchbox", { name: "Find a clip" }), "intro{Enter}");
    // An already-chosen clip is not offered again.
    expect(await screen.findByRole("button", { name: "Add Intro Two" })).toBeEnabled();
    expect(screen.queryByRole("button", { name: "Add Intro One" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Add Intro Two" }));
    // The list is full at MAX_PRE_PLAY_CLIPS.
    expect(screen.getByRole("button", { name: "Add Intro Three" })).toBeDisabled();

    await user.click(screen.getByRole("button", { name: "Move Intro Two up" }));
    expect(screen.getByRole("button", { name: "Move Intro Two up" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Save clips" }));
    await waitFor(() => expect(setPrePlayClips).toHaveBeenCalledWith(["m-2", "m-1"]));
    expect(await screen.findByText("Clips saved")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Remove m-1" }));
    expect(screen.queryByText("Clips saved")).toBeNull();
    await user.click(screen.getByRole("button", { name: "Save clips" }));
    await waitFor(() => expect(setPrePlayClips).toHaveBeenLastCalledWith(["m-2"]));
  });

  it("shows the empty state, and an error line when loading or saving fails", async () => {
    vi.mocked(setPrePlayClips).mockRejectedValue(new Error("boom"));
    const user = userEvent.setup();
    renderConfig();

    expect(await screen.findByText("No intro clips yet.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save clips" }));
    expect(await screen.findByText("Could not save intro clips.")).toBeInTheDocument();

    cleanup();
    vi.mocked(fetchPrePlayClips).mockRejectedValue(new Error("down"));
    renderConfig();
    expect(await screen.findByText("Could not load intro clips.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save clips" })).toBeNull();
  });
});
