import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setAdvancedSettings } from "@/lib/advancedSettings";
import {
  browseServerFolders,
  fetchConsoleConfig,
  fetchPrePlayClips,
  fetchTranscodeConfig,
  search,
  setConsoleConfig,
  setPrePlayClips,
  setTranscodeConfig,
} from "@/lib/graphql";
import type { ConsoleConfigInfo, FolderEntryInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { Config } from "./Config";

vi.mock("@/lib/graphql", () => ({
  MAX_PRE_PLAY_CLIPS: 2,
  browseServerFolders: vi.fn(),
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
  maxConcurrentTranscodes: 0,
  effectiveMaxConcurrentTranscodes: 3,
  maxConcurrentTranscodesEnvOverride: false,
  maxConcurrentTranscodesRestartRequired: false,
  tempDir: "",
  effectiveTempDir: "/srv/beamfall/transcode",
  tempDirEnvOverride: false,
  tempDirRestartRequired: false,
  toneMapEnabled: true,
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

// The codec, quality and cap controls are listbox selects: open one and pick an option.
async function choose(
  user: ReturnType<typeof userEvent.setup>,
  control: HTMLElement,
  option: string,
) {
  await user.click(control);
  await user.click(await screen.findByRole("option", { name: option }));
}

function renderConfig() {
  return render(
    <I18nProvider>
      <Config />
    </I18nProvider>,
  );
}

describe("transcoding config", () => {
  it("keeps an edited Advanced field visible in Normal so Save never hides it", async () => {
    vi.mocked(setTranscodeConfig).mockResolvedValue(savedTranscode);
    setAdvancedSettings(true);
    const user = userEvent.setup();
    renderConfig();

    await choose(
      user,
      await screen.findByRole("combobox", { name: "Codec" }),
      "HEVC (smaller files)",
    );
    act(() => setAdvancedSettings(false));

    expect(screen.getByRole("combobox", { name: "Codec" })).toHaveTextContent(
      "HEVC (smaller files)",
    );
    expect(screen.queryByRole("combobox", { name: "Quality" })).toBeNull();
  });

  it("saves the cap and tone-map switch and shows the cap in effect", async () => {
    vi.mocked(setTranscodeConfig).mockResolvedValue({
      ...savedTranscode,
      maxConcurrentTranscodes: 2,
      effectiveMaxConcurrentTranscodes: 2,
      toneMapEnabled: false,
    });
    const user = userEvent.setup();
    renderConfig();

    const cap = await screen.findByRole("combobox", { name: "Maximum simultaneous transcodes" });
    expect(await screen.findByText("In effect: 3")).toBeInTheDocument();
    await choose(user, cap, "2");
    await user.click(screen.getByRole("switch", { name: "HDR tone mapping" }));
    const form = cap.closest("form") as HTMLFormElement;
    await user.click(within(form).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(screen.getByText("In effect: 2")).toBeInTheDocument());
    expect(setTranscodeConfig).toHaveBeenCalledWith({
      hardwareEnabled: false,
      codec: "",
      quality: "",
      device: "",
      maxConcurrentTranscodes: 2,
      tempDir: "",
      toneMapEnabled: false,
    });
  });

  it("sends Automatic as no cap", async () => {
    vi.mocked(fetchTranscodeConfig).mockResolvedValue({
      ...savedTranscode,
      maxConcurrentTranscodes: 4,
    });
    vi.mocked(setTranscodeConfig).mockResolvedValue(savedTranscode);
    const user = userEvent.setup();
    renderConfig();

    const cap = await screen.findByRole("combobox", { name: "Maximum simultaneous transcodes" });
    await waitFor(() => expect(cap).toHaveTextContent("4"));
    await choose(user, cap, "Automatic");
    await user.click(
      within(cap.closest("form") as HTMLFormElement).getByRole("button", { name: "Save" }),
    );

    await waitFor(() =>
      expect(setTranscodeConfig).toHaveBeenCalledWith(
        expect.objectContaining({ maxConcurrentTranscodes: null }),
      ),
    );
  });

  it("shows the server's rejection of a temp directory", async () => {
    vi.mocked(setTranscodeConfig).mockRejectedValue(
      new Error("tempDir must be an existing, writable directory"),
    );
    setAdvancedSettings(true);
    const user = userEvent.setup();
    renderConfig();

    const dir = await screen.findByRole("textbox", { name: "Transcoder temp directory" });
    await user.type(dir, "/srv/missing");
    await user.click(
      within(dir.closest("form") as HTMLFormElement).getByRole("button", { name: "Save" }),
    );

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "tempDir must be an existing, writable directory",
    );
    expect(setTranscodeConfig).toHaveBeenCalledWith(
      expect.objectContaining({ tempDir: "/srv/missing" }),
    );
  });

  it("picks the temp directory from a folder browse and confirms the save", async () => {
    const cache = { name: "cache", path: "/mnt/cache", kind: "folder" } as FolderEntryInfo;
    vi.mocked(browseServerFolders).mockImplementation(async (path?: string) =>
      path === cache.path
        ? { ...cache, children: [] }
        : ({ name: "/", path: "/", kind: "folder", children: [cache] } as FolderEntryInfo),
    );
    vi.mocked(setTranscodeConfig).mockImplementation(async (config) => ({
      ...savedTranscode,
      tempDir: config.tempDir,
    }));
    setAdvancedSettings(true);
    const user = userEvent.setup();
    renderConfig();

    const dir = await screen.findByRole("textbox", { name: "Transcoder temp directory" });
    const form = dir.closest("form") as HTMLFormElement;
    await user.click(within(form).getByRole("button", { name: "Browse…" }));
    await user.click(await within(form).findByRole("button", { name: "cache" }));
    await user.click(await within(form).findByRole("button", { name: "Use this folder" }));
    expect(dir).toHaveValue("/mnt/cache");

    await user.click(within(form).getByRole("button", { name: "Save" }));
    expect(await within(form).findByRole("status")).toHaveTextContent("Transcoding settings saved");
    expect(setTranscodeConfig).toHaveBeenCalledWith(
      expect.objectContaining({ tempDir: "/mnt/cache" }),
    );

    await user.type(dir, "2");
    expect(within(form).getByRole("status")).toBeEmptyDOMElement();
  });

  it("says when an environment variable overrides a setting and when a restart is needed", async () => {
    vi.mocked(fetchTranscodeConfig).mockResolvedValue({
      ...savedTranscode,
      maxConcurrentTranscodes: 2,
      effectiveMaxConcurrentTranscodes: 6,
      maxConcurrentTranscodesEnvOverride: true,
      tempDir: "/srv/scratch",
      effectiveTempDir: "/srv/beamfall/transcode",
      tempDirRestartRequired: true,
    });
    renderConfig();

    expect(await screen.findByText("In effect: 6")).toBeInTheDocument();
    expect(screen.getByText(/BEAMFALL_TRANSCODE_CAP sets the cap in effect/)).toBeInTheDocument();
    // The temp dir holds a value, so it stays visible in Normal with its status.
    const dirStatus = screen.getByTestId("transcode-temp-dir-status");
    expect(dirStatus).toHaveTextContent("In use: /srv/beamfall/transcode");
    expect(dirStatus).toHaveTextContent("Restart the server to apply this change.");
    expect(screen.getByTestId("transcode-cap-status")).not.toHaveTextContent("Restart");
  });
});

describe("Session lengths", () => {
  it("shows the defaults when the server sends 30 days and 15 minutes", async () => {
    vi.mocked(fetchConsoleConfig).mockResolvedValue({
      ...savedConsole,
      sessionLifetimeDays: 30,
      adminSessionMinutes: 15,
    });
    renderConfig();

    const signIn = screen.getByRole("combobox", { name: "Sign-in session length" });
    await waitFor(() => expect(signIn).toBeEnabled());
    expect(signIn).toHaveDisplayValue("30 days");
    expect(screen.getByRole("combobox", { name: "Console unlock length" })).toHaveDisplayValue(
      "15 minutes",
    );
    expect(screen.getByText(/keeps that access until you lock the Console/)).toBeInTheDocument();
  });

  it("saves a changed length and shows it", async () => {
    vi.mocked(setConsoleConfig).mockResolvedValue({
      ...savedConsole,
      sessionLifetimeDays: 30,
      adminSessionMinutes: 480,
    });
    const user = userEvent.setup();
    renderConfig();

    const unlock = screen.getByRole("combobox", { name: "Console unlock length" });
    await waitFor(() => expect(unlock).toBeEnabled());
    await user.selectOptions(unlock, "8 hours");

    expect(setConsoleConfig).toHaveBeenCalledWith({ adminSessionMinutes: 480 });
    await waitFor(() => expect(unlock).toHaveDisplayValue("8 hours"));
  });

  it("saves Never expire as 0 and keeps a saved off-list value as a choice", async () => {
    vi.mocked(fetchConsoleConfig).mockResolvedValue({
      ...savedConsole,
      sessionLifetimeDays: 45,
      adminSessionMinutes: 15,
    });
    vi.mocked(setConsoleConfig).mockResolvedValue({
      ...savedConsole,
      sessionLifetimeDays: 0,
      adminSessionMinutes: 15,
    });
    const user = userEvent.setup();
    renderConfig();

    const signIn = screen.getByRole("combobox", { name: "Sign-in session length" });
    await waitFor(() => expect(signIn).toHaveDisplayValue("45 days"));
    await user.selectOptions(signIn, "Never expire");

    expect(setConsoleConfig).toHaveBeenCalledWith({ sessionLifetimeDays: 0 });
    await waitFor(() => expect(signIn).toHaveDisplayValue("Never expire"));
  });

  it("says so when the change could not be saved", async () => {
    vi.mocked(setConsoleConfig).mockRejectedValue(new Error("nope"));
    const user = userEvent.setup();
    renderConfig();

    const signIn = screen.getByRole("combobox", { name: "Sign-in session length" });
    await waitFor(() => expect(signIn).toBeEnabled());
    await user.selectOptions(signIn, "90 days");

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Could not save the session length.",
    );
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

    await choose(
      user,
      await screen.findByRole("combobox", { name: "Codec" }),
      "HEVC (smaller files)",
    );
    act(() => setAdvancedSettings(false));

    const toggle = screen.getByRole("switch", { name: /Viewer pairing codes/i });
    await waitFor(() => expect(toggle).toBeEnabled());
    await user.click(toggle);

    await waitFor(() => expect(toggle).toBeChecked());
    expect(setConsoleConfig).toHaveBeenCalledWith({ viewerPairingCodesDisabled: true });
    expect(setTranscodeConfig).not.toHaveBeenCalled();
    expect(screen.getByRole("combobox", { name: "Codec" })).toHaveTextContent(
      "HEVC (smaller files)",
    );
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
