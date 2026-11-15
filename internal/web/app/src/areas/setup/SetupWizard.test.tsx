import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { StrictMode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api";
import { I18nProvider } from "@/lib/i18n";
import { groupProviderKeys } from "@/lib/providerKeys";
import { FirstLibraryError } from "./orchestrate";
import { SetupWizard } from "./SetupWizard";

// jsdom has no ResizeObserver; the Radix Switch (via react-use-size) needs one.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= ResizeObserverStub as unknown as typeof ResizeObserver;

const mocks = vi.hoisted(() => ({
  activatePlugins: vi.fn(),
  bootstrapResume: vi.fn(),
  bootstrapSetup: vi.fn(),
  browseServerFolders: vi.fn(),
  configureUpdateCheck: vi.fn(),
  createFirstLibrary: vi.fn(),
  fetchImporters: vi.fn(),
  fetchJobs: vi.fn(),
  fetchPlugins: vi.fn(),
  loadProviderKeys: vi.fn(),
  loadSavedPluginChoices: vi.fn(),
  reopenAdminSession: vi.fn(),
  setScraperCredential: vi.fn(),
}));

vi.mock("@/lib/graphql", async () => {
  const actual = await vi.importActual<typeof import("@/lib/graphql")>("@/lib/graphql");
  return {
    ...actual,
    browseServerFolders: mocks.browseServerFolders,
    fetchImporters: mocks.fetchImporters,
    fetchJobs: mocks.fetchJobs,
    fetchPlugins: mocks.fetchPlugins,
    setScraperCredential: mocks.setScraperCredential,
  };
});

vi.mock("./orchestrate", async () => {
  const actual = await vi.importActual<typeof import("./orchestrate")>("./orchestrate");
  return {
    ...actual,
    activatePlugins: mocks.activatePlugins,
    bootstrapResume: mocks.bootstrapResume,
    bootstrapSetup: mocks.bootstrapSetup,
    configureUpdateCheck: mocks.configureUpdateCheck,
    createFirstLibrary: mocks.createFirstLibrary,
    loadProviderKeys: mocks.loadProviderKeys,
    loadSavedPluginChoices: mocks.loadSavedPluginChoices,
    reopenAdminSession: mocks.reopenAdminSession,
  };
});

beforeEach(() => {
  mocks.loadProviderKeys.mockResolvedValue([]);
  mocks.fetchImporters.mockResolvedValue([]);
  // By default the saved choices can't be read (the Admin Session lapsed), so
  // a resumed wizard's activation state is unknown and the Library step offers
  // every installed type.
  mocks.loadSavedPluginChoices.mockRejectedValue(new ApiError("admin session required", 403));
});

afterEach(() => {
  window.sessionStorage.clear();
});

// A reload after the plugin step returns to the Library step.
function resumeAtLibraryStep() {
  window.sessionStorage.setItem("beamfall.setup.resumeStep", "library");
}

function renderSetupWizard() {
  return render(
    <StrictMode>
      <I18nProvider>
        <SetupWizard />
      </I18nProvider>
    </StrictMode>,
  );
}

describe("SetupWizard folder picker", () => {
  beforeEach(() => {
    resumeAtLibraryStep();
    mocks.bootstrapResume.mockResolvedValue({
      alreadyBootstrapped: true,
      state: { complete: false, pluginsActivated: true },
    });
    mocks.fetchPlugins.mockResolvedValue([]);
    mocks.browseServerFolders.mockResolvedValue({
      name: "Server roots",
      path: "/",
      kind: "folder",
      children: [{ name: "Media", path: "/Volumes/Media", kind: "folder" }],
    });
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    vi.clearAllMocks();
  });

  it("fills the root path from the server folder picker", async () => {
    const user = userEvent.setup();
    renderSetupWizard();

    await screen.findByRole("heading", { name: "Create your first Library" });
    await user.click(screen.getByRole("button", { name: "Browse…" }));
    await waitFor(() => {
      expect(mocks.browseServerFolders).toHaveBeenCalledWith(undefined);
    });
    expect(screen.getByText("Media")).toBeInTheDocument();
    await user.click(await screen.findByRole("button", { name: "Use this folder" }));

    await waitFor(() => {
      expect(screen.getByLabelText("Root folder")).toHaveValue("/");
    });
  });

  it("moves focus into the picker and returns it to Browse on Escape", async () => {
    const user = userEvent.setup();
    renderSetupWizard();

    await screen.findByRole("heading", { name: "Create your first Library" });
    await user.click(screen.getByRole("button", { name: "Browse…" }));
    await waitFor(() => expect(screen.getByRole("button", { name: /Media/ })).toHaveFocus());

    await user.keyboard("{Escape}");
    expect(screen.queryByRole("button", { name: "Use this folder" })).not.toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole("button", { name: "Browse…" })).toHaveFocus());
  });

  it("opens at the top when the typed path can't be read, and says so", async () => {
    mocks.browseServerFolders.mockRejectedValueOnce(new Error("permission denied"));
    const user = userEvent.setup();
    renderSetupWizard();

    await screen.findByRole("heading", { name: "Create your first Library" });
    await user.type(screen.getByLabelText("Root folder"), "/srv/locked");
    await user.click(screen.getByRole("button", { name: "Browse…" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't open /srv/locked");
    expect(mocks.browseServerFolders).toHaveBeenNthCalledWith(1, "/srv/locked");
    expect(mocks.browseServerFolders).toHaveBeenNthCalledWith(2, undefined);
    expect(screen.getByText("Media")).toBeInTheDocument();
  });

  it("explains a picker that can't open at all instead of doing nothing", async () => {
    mocks.browseServerFolders.mockRejectedValue(new Error("permission denied"));
    const user = userEvent.setup();
    renderSetupWizard();

    await screen.findByRole("heading", { name: "Create your first Library" });
    await user.click(screen.getByRole("button", { name: "Browse…" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't read that folder.");
  });

  it("puts the caret in an empty root field when Continue is pressed", async () => {
    const user = userEvent.setup();
    renderSetupWizard();

    await screen.findByRole("heading", { name: "Create your first Library" });
    await user.click(screen.getByRole("button", { name: "Continue" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Type the folder's path");
    const root = screen.getByLabelText("Root folder");
    await waitFor(() => expect(root).toHaveFocus());
    expect(root).toHaveAttribute("aria-invalid", "true");
  });
});

const FIRST_PARTY_PLUGINS = [
  { id: "filmtv", displayName: "Film & TV" },
  { id: "music", displayName: "Music" },
  { id: "photos", displayName: "Photos" },
  { id: "karaoke", displayName: "Karaoke" },
];

describe("SetupWizard first scan", () => {
  beforeEach(() => {
    resumeAtLibraryStep();
    mocks.bootstrapResume.mockResolvedValue({
      alreadyBootstrapped: true,
      state: { complete: false, pluginsActivated: true },
    });
    mocks.fetchPlugins.mockResolvedValue(FIRST_PARTY_PLUGINS);
    mocks.createFirstLibrary.mockResolvedValue({
      libraryID: "lib1",
      scanJobID: "job-scan-1",
      scanStatus: "queued",
    });
    mocks.fetchJobs.mockResolvedValue([
      {
        id: "job-scan-1",
        kind: "scan",
        status: "running",
        progress: { current: 214, total: 500, unit: "files", message: "Scanning" },
      },
    ]);
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("starts the first scan and ends on live scan progress", async () => {
    const user = userEvent.setup();
    renderSetupWizard();

    await screen.findByRole("heading", { name: "Create your first Library" });
    await user.type(screen.getByLabelText("Root folder"), "/media/films");
    await user.click(screen.getByRole("button", { name: "Continue" }));

    await screen.findByRole("heading", { name: "Your library is filling up" });
    expect(mocks.createFirstLibrary).toHaveBeenCalledWith({
      title: "Films",
      pluginID: "filmtv",
      familyScope: "films",
      rootPath: "/media/films",
      progress: { rootsSet: false },
    });
    expect(await screen.findByText("214/500 files - Scanning")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Browse what's ready" })).toBeInTheDocument();
  });

  it("offers every media type the server's plugins provide (CORE-1156)", async () => {
    const user = userEvent.setup();
    renderSetupWizard();

    await screen.findByRole("heading", { name: "Create your first Library" });
    for (const name of ["Films", "TV Shows", "Mixed", "Music", "Photos", "Karaoke"]) {
      expect(await screen.findByRole("radio", { name })).toBeInTheDocument();
    }
    await user.click(screen.getByRole("radio", { name: "Photos" }));
    await user.type(screen.getByLabelText("Root folder"), "/media/photos");
    await user.click(screen.getByRole("button", { name: "Continue" }));

    await waitFor(() => {
      expect(mocks.createFirstLibrary).toHaveBeenCalledWith(
        expect.objectContaining({ title: "Photos", pluginID: "photos", familyScope: null }),
      );
    });
  });

  it("offers migration under the scan, with Browse what's ready as the way past it", async () => {
    mocks.fetchImporters.mockResolvedValue([
      { id: "plex", pluginID: "filmtv", displayName: { en: "Plex" } },
    ]);
    const user = userEvent.setup();
    renderSetupWizard();

    await screen.findByRole("heading", { name: "Create your first Library" });
    await user.type(screen.getByLabelText("Root folder"), "/media/films");
    await user.click(screen.getByRole("button", { name: "Continue" }));

    expect(
      await screen.findByRole("heading", { level: 2, name: "Bring your library with you" }),
    ).toBeInTheDocument();
    expect(await screen.findByText("Plex")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Choose a source" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /skip for now/i })).not.toBeInTheDocument();
  });

  it("says a stopped scan can be run again from the Console", async () => {
    mocks.fetchJobs.mockResolvedValue([{ id: "job-scan-1", kind: "scan", status: "failed" }]);
    const user = userEvent.setup();
    renderSetupWizard();

    await screen.findByRole("heading", { name: "Create your first Library" });
    await user.type(screen.getByLabelText("Root folder"), "/media/films");
    await user.click(screen.getByRole("button", { name: "Continue" }));

    expect(await screen.findByText(/The scan stopped before it finished/)).toBeInTheDocument();
  });

  it("shows the server error and retains partial Library progress for retry", async () => {
    mocks.createFirstLibrary
      .mockRejectedValueOnce(
        new FirstLibraryError(new Error("root is not readable"), {
          libraryID: "lib1",
          rootsSet: false,
        }),
      )
      .mockResolvedValueOnce({
        libraryID: "lib1",
        scanJobID: "job-scan-1",
        scanStatus: "queued",
      });
    const user = userEvent.setup();
    renderSetupWizard();

    await screen.findByRole("heading", { name: "Create your first Library" });
    await user.type(screen.getByLabelText("Root folder"), "/media/films");
    await user.click(screen.getByRole("button", { name: "Continue" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("root is not readable");
    expect(screen.getByRole("heading", { name: "Create your first Library" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Continue" }));

    expect(mocks.createFirstLibrary).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({ progress: { libraryID: "lib1", rootsSet: false } }),
    );
    expect(
      await screen.findByRole("heading", { name: "Your library is filling up" }),
    ).toBeInTheDocument();
  });

  it("resumes a rootless Library without creating another one", async () => {
    mocks.bootstrapResume.mockResolvedValue({
      alreadyBootstrapped: true,
      state: { complete: false, pluginsActivated: true },
      firstLibraryProgress: { libraryID: "lib-existing", rootsSet: false },
    });
    const user = userEvent.setup();
    renderSetupWizard();

    await screen.findByRole("heading", { name: "Create your first Library" });
    await user.type(screen.getByLabelText("Root folder"), "/media/films");
    await user.click(screen.getByRole("button", { name: "Continue" }));

    expect(mocks.createFirstLibrary).toHaveBeenCalledWith(
      expect.objectContaining({
        progress: { libraryID: "lib-existing", rootsSet: false },
      }),
    );
  });

  it("keeps the folder editable and hides the field name when only the scan fails (CORE-1598)", async () => {
    mocks.createFirstLibrary.mockRejectedValueOnce(
      new FirstLibraryError(
        new ApiError("scanLibrary unavailable", 200),
        { libraryID: "lib1", rootsSet: true, rootPath: "/media/films" },
        true,
      ),
    );
    const user = userEvent.setup();
    renderSetupWizard();

    await screen.findByRole("heading", { name: "Create your first Library" });
    await user.type(screen.getByLabelText("Root folder"), "/media/films");
    await user.click(screen.getByRole("button", { name: "Continue" }));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("The Library is created, but its first scan didn't start.");
    expect(document.body).not.toHaveTextContent("scanLibrary");
    expect(screen.getByLabelText("Root folder")).toBeEnabled();
    expect(screen.getByRole("button", { name: /^Browse/ })).toBeEnabled();
    await user.click(screen.getByRole("button", { name: "Continue" }));
    expect(mocks.createFirstLibrary).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({
        rootPath: "/media/films",
        progress: { libraryID: "lib1", rootsSet: true, rootPath: "/media/films" },
      }),
    );
  });

  it("names a missing folder inline and updates nothing until the path is fixed (CORE-1598)", async () => {
    mocks.createFirstLibrary
      .mockRejectedValueOnce(
        new FirstLibraryError(
          new ApiError("The folder /media/flims doesn't exist on the server.", 200),
          { rootsSet: false },
        ),
      )
      .mockResolvedValueOnce({ libraryID: "lib1", scanJobID: "job-scan-1", scanStatus: "queued" });
    const user = userEvent.setup();
    renderSetupWizard();

    await screen.findByRole("heading", { name: "Create your first Library" });
    const root = screen.getByLabelText("Root folder");
    await user.type(root, "/media/flims");
    await user.click(screen.getByRole("button", { name: "Continue" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "The folder /media/flims doesn't exist on the server. Check the path, or choose the folder with Browse.",
    );
    expect(root).toBeEnabled();
    expect(root).toHaveAttribute("aria-invalid", "true");
    await waitFor(() => expect(root).toHaveFocus());

    await user.clear(root);
    await user.type(root, "/media/films");
    await user.click(screen.getByRole("button", { name: "Continue" }));

    expect(mocks.createFirstLibrary).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({ rootPath: "/media/films", progress: { rootsSet: false } }),
    );
    expect(
      await screen.findByRole("heading", { name: "Your library is filling up" }),
    ).toBeInTheDocument();
  });

  it("shows plain copy, not a GraphQL field name, for an internal failure (CORE-1598)", async () => {
    mocks.createFirstLibrary.mockRejectedValueOnce(
      new FirstLibraryError(new ApiError("createLibrary unavailable", 200), { rootsSet: false }),
    );
    const user = userEvent.setup();
    renderSetupWizard();

    await screen.findByRole("heading", { name: "Create your first Library" });
    await user.type(screen.getByLabelText("Root folder"), "/media/films");
    await user.click(screen.getByRole("button", { name: "Continue" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "The server couldn't set up that folder.",
    );
    expect(document.body).not.toHaveTextContent("createLibrary");
  });

  it("names library type radios by their visible labels (CORE-1610)", async () => {
    renderSetupWizard();

    await screen.findByRole("heading", { name: "Create your first Library" });
    const group = screen.getByRole("group", { name: "Library type" });
    expect(group).toBeInTheDocument();
    expect(await screen.findByRole("radio", { name: "Films" })).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "TV Shows" })).toBeInTheDocument();
    expect(screen.queryByRole("radio", { name: /filmtv/ })).not.toBeInTheDocument();
  });

  it("surfaces scan polling failures through the existing setup error", async () => {
    mocks.fetchJobs.mockRejectedValue(new Error("offline"));
    const user = userEvent.setup();
    renderSetupWizard();

    await screen.findByRole("heading", { name: "Create your first Library" });
    await user.type(screen.getByLabelText("Root folder"), "/media/films");
    await user.click(screen.getByRole("button", { name: "Continue" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Lost contact with the server");
    expect(mocks.fetchJobs).toHaveBeenCalledTimes(1);
  });

  it("clears a polling connectivity error after a successful empty response", async () => {
    mocks.fetchJobs.mockRejectedValueOnce(new Error("offline")).mockResolvedValue([]);
    const user = userEvent.setup();
    renderSetupWizard();

    await screen.findByRole("heading", { name: "Create your first Library" });
    await user.type(screen.getByLabelText("Root folder"), "/media/films");
    await user.click(screen.getByRole("button", { name: "Continue" }));
    expect(await screen.findByRole("alert")).not.toBeEmptyDOMElement();

    await waitFor(() => expect(mocks.fetchJobs).toHaveBeenCalledTimes(2), { timeout: 2_500 });
    await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
  });

  it("does not overlap scan polling requests", async () => {
    let resolveJobs: ((jobs: unknown[]) => void) | undefined;
    mocks.fetchJobs.mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveJobs = resolve;
        }),
    );
    const user = userEvent.setup();
    renderSetupWizard();

    await screen.findByRole("heading", { name: "Create your first Library" });
    await user.type(screen.getByLabelText("Root folder"), "/media/films");
    await user.click(screen.getByRole("button", { name: "Continue" }));
    await screen.findByRole("heading", { name: "Your library is filling up" });
    await waitFor(() => expect(mocks.fetchJobs).toHaveBeenCalledTimes(1));

    await new Promise((resolve) => window.setTimeout(resolve, 2_100));
    expect(mocks.fetchJobs).toHaveBeenCalledTimes(1);
    resolveJobs?.([]);
    await waitFor(() => expect(mocks.fetchJobs).toHaveBeenCalledTimes(2), { timeout: 2_500 });
  }, 7_000);
});

describe("SetupWizard setup URL", () => {
  beforeEach(() => {
    mocks.bootstrapResume.mockResolvedValue({
      alreadyBootstrapped: false,
      state: null,
    });
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
    vi.restoreAllMocks();
    window.history.replaceState(null, "", "/web/setup");
  });

  it("consumes the credential before network work and cannot resurrect it through history", async () => {
    const canary = "URL-TOKEN-CANARY";
    window.history.replaceState({ safe: true }, "", "/web/before-setup");
    window.history.pushState({ token: canary }, "", `/web/setup?keep=1&token=${canary}#start`);
    mocks.bootstrapResume.mockImplementation(async () => {
      expect(window.location.href).not.toContain(canary);
      expect(window.location.search).toBe("?keep=1");
      expect(window.location.hash).toBe("#start");
      expect(window.history.state).toBeNull();
      return { alreadyBootstrapped: false, state: null };
    });

    renderSetupWizard();

    expect(await screen.findByLabelText("Setup token")).toHaveValue(canary);
    expect(window.location.href).not.toContain(canary);

    window.history.back();
    await waitFor(() => expect(window.location.pathname).toBe("/web/before-setup"));
    expect(window.location.href).not.toContain(canary);

    window.history.forward();
    await waitFor(() => expect(window.location.pathname).toBe("/web/setup"));
    expect(window.location.href).not.toContain(canary);
    expect(window.history.state).toBeNull();
  });

  it("clears the credential after a failed bootstrap without rendering the canary", async () => {
    const canary = "FAILED-SETUP-TOKEN-CANARY";
    const user = userEvent.setup();
    const consoleDebug = vi.spyOn(console, "debug").mockImplementation(() => undefined);
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    const consoleInfo = vi.spyOn(console, "info").mockImplementation(() => undefined);
    const consoleLog = vi.spyOn(console, "log").mockImplementation(() => undefined);
    const consoleWarn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
    window.history.replaceState({ token: canary }, "", `/web/setup?token=${canary}`);
    mocks.bootstrapSetup.mockRejectedValue(new Error(canary));

    const view = renderSetupWizard();

    await user.click(await screen.findByRole("button", { name: "Continue" }));
    await user.type(screen.getByLabelText("Admin password"), "correct horse battery staple");
    await user.type(screen.getByLabelText("Confirm password"), "correct horse battery staple");
    await user.click(screen.getByRole("button", { name: "Continue" }));
    await user.click(screen.getByRole("button", { name: "Continue" }));
    await user.click(screen.getByRole("button", { name: "Continue" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "The server couldn't finish this step.",
    );
    expect(view.container.textContent).not.toContain(canary);
    expect(window.location.href).not.toContain(canary);
    expect(JSON.stringify(window.history.state)).not.toContain(canary);
    expect(JSON.stringify(window.localStorage)).not.toContain(canary);
    expect(JSON.stringify(window.sessionStorage)).not.toContain(canary);
    expect(
      [consoleDebug, consoleError, consoleInfo, consoleLog, consoleWarn]
        .flatMap((spy) => spy.mock.calls)
        .flat()
        .join(" "),
    ).not.toContain(canary);

    await user.click(screen.getByRole("button", { name: "Back" }));
    await user.click(screen.getByRole("button", { name: "Back" }));
    await user.click(screen.getByRole("button", { name: "Back" }));
    expect(screen.getByLabelText("Setup token")).toHaveValue("");
  });

  it("asks for the token again instead of posting an empty one after a failed attempt", async () => {
    const user = userEvent.setup();
    window.history.replaceState(null, "", "/web/setup?token=ONE-USE-TOKEN");
    mocks.bootstrapSetup.mockRejectedValue(new ApiError("POST /api/setup failed", 429));

    renderSetupWizard();

    await user.click(await screen.findByRole("button", { name: "Continue" }));
    await user.type(screen.getByLabelText("Admin password"), "correct horse battery staple");
    await user.type(screen.getByLabelText("Confirm password"), "correct horse battery staple");
    await user.click(screen.getByRole("button", { name: "Continue" }));
    await user.click(screen.getByRole("button", { name: "Continue" }));
    await user.click(screen.getByRole("button", { name: "Continue" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Too many attempts.");

    await user.click(screen.getByRole("button", { name: "Continue" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Paste it again");
    await waitFor(() => expect(screen.getByLabelText("Setup token")).toHaveFocus());
    expect(mocks.bootstrapSetup).toHaveBeenCalledTimes(1);
  });

  it("rejects a blank password, focuses the field, then focuses the chosen sign-in option", async () => {
    const user = userEvent.setup();
    window.history.replaceState(null, "", "/web/setup?token=TOKEN");

    renderSetupWizard();

    await user.click(await screen.findByRole("button", { name: "Continue" }));
    const password = screen.getByLabelText("Admin password");
    await user.type(password, "   ");
    await user.type(screen.getByLabelText("Confirm password"), "   ");
    await user.click(screen.getByRole("button", { name: "Continue" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Enter an admin password.");
    await waitFor(() => expect(password).toHaveFocus());
    expect(password).toHaveAttribute("aria-invalid", "true");

    await user.clear(password);
    await user.type(password, "hunter22");
    await user.clear(screen.getByLabelText("Confirm password"));
    await user.type(screen.getByLabelText("Confirm password"), "hunter22");
    await user.click(screen.getByRole("button", { name: "Continue" }));
    await waitFor(() => expect(screen.getByRole("radio", { name: /Home/ })).toHaveFocus());
  });

  it("clears the credential after a successful bootstrap", async () => {
    const canary = "SUCCESSFUL-SETUP-TOKEN-CANARY";
    const user = userEvent.setup();
    window.history.replaceState(null, "", `/web/setup?token=${canary}`);
    mocks.bootstrapSetup.mockResolvedValue(undefined);
    mocks.fetchPlugins.mockResolvedValue([]);

    const view = renderSetupWizard();

    await user.click(await screen.findByRole("button", { name: "Continue" }));
    await user.type(screen.getByLabelText("Admin password"), "correct horse battery staple");
    await user.type(screen.getByLabelText("Confirm password"), "correct horse battery staple");
    await user.click(screen.getByRole("button", { name: "Continue" }));
    await user.click(screen.getByRole("button", { name: "Continue" }));
    await user.click(screen.getByRole("button", { name: "Continue" }));

    expect(
      await screen.findByRole("heading", { name: "Choose what Beamfall understands" }),
    ).toBeInTheDocument();
    expect(mocks.bootstrapSetup).toHaveBeenCalledWith(
      expect.objectContaining({
        token: canary,
      }),
    );
    expect(window.location.href).not.toContain(canary);
    expect(JSON.stringify(window.localStorage)).not.toContain(canary);
    expect(JSON.stringify(window.sessionStorage)).not.toContain(canary);
    expect(view.container.textContent).not.toContain(canary);

    view.unmount();
    renderSetupWizard();
    expect(await screen.findByLabelText("Setup token")).toHaveValue("");
  });

  it("does not restore a consumed credential after unmount and remount", async () => {
    const canary = "UNMOUNTED-TOKEN-CANARY";
    window.history.replaceState(null, "", `/web/setup?token=${canary}`);

    const first = renderSetupWizard();
    expect(await screen.findByLabelText("Setup token")).toHaveValue(canary);
    first.unmount();

    renderSetupWizard();
    expect(await screen.findByLabelText("Setup token")).toHaveValue("");
    expect(window.location.href).not.toContain(canary);
  });
});

describe("SetupWizard LAN claim (CORE-1105)", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/web/setup");
    mocks.fetchPlugins.mockResolvedValue([]);
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("starts at the admin step and bootstraps without a token on the local network", async () => {
    const user = userEvent.setup();
    mocks.bootstrapResume.mockResolvedValue({
      alreadyBootstrapped: false,
      state: null,
      tokenRequired: false,
    });
    mocks.bootstrapSetup.mockResolvedValue(undefined);
    renderSetupWizard();

    expect(
      await screen.findByRole("heading", { name: "Set the admin password" }),
    ).toBeInTheDocument();
    expect(screen.queryByLabelText("Setup token")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Back" })).not.toBeInTheDocument();

    await user.type(screen.getByLabelText("Admin password"), "correct horse battery staple");
    await user.type(screen.getByLabelText("Confirm password"), "correct horse battery staple");
    await user.click(screen.getByRole("button", { name: "Continue" }));
    await user.click(screen.getByRole("button", { name: "Continue" }));
    await user.click(screen.getByRole("button", { name: "Continue" }));

    expect(
      await screen.findByRole("heading", { name: "Choose what Beamfall understands" }),
    ).toBeInTheDocument();
    expect(mocks.bootstrapSetup).toHaveBeenCalledWith(
      expect.objectContaining({ token: "", securityMode: "home" }),
    );
  });

  it("offers Home and Strict sign-in security and sends the choice with setup (CORE-1110)", async () => {
    const user = userEvent.setup();
    mocks.bootstrapResume.mockResolvedValue({
      alreadyBootstrapped: false,
      state: null,
      tokenRequired: false,
    });
    mocks.bootstrapSetup.mockResolvedValue(undefined);
    renderSetupWizard();

    await user.type(await screen.findByLabelText("Admin password"), "correct horse battery staple");
    await user.type(screen.getByLabelText("Confirm password"), "correct horse battery staple");
    await user.click(screen.getByRole("button", { name: "Continue" }));

    expect(
      await screen.findByRole("group", { name: "Choose how the owner signs in" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Step 2 of 6")).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: /Home/ })).toBeChecked();
    expect(screen.getByText("Recommended")).toBeInTheDocument();
    await user.click(screen.getByRole("radio", { name: /Strict/ }));
    await user.click(screen.getByRole("button", { name: "Continue" }));
    await user.click(screen.getByRole("button", { name: "Continue" }));

    expect(
      await screen.findByRole("heading", { name: "Choose what Beamfall understands" }),
    ).toBeInTheDocument();
    expect(mocks.bootstrapSetup).toHaveBeenCalledWith(
      expect.objectContaining({ securityMode: "strict" }),
    );
  });

  it("keeps the token step when the server requires the token", async () => {
    mocks.bootstrapResume.mockResolvedValue({
      alreadyBootstrapped: false,
      state: null,
      tokenRequired: true,
    });
    renderSetupWizard();

    expect(
      await screen.findByRole("heading", { name: "Unlock with the setup token" }),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("Setup token")).toHaveValue("");
  });
});

describe("SetupWizard provider keys (CORE-1115)", () => {
  const scraper = (scraperID: string, displayName: string) => ({
    pluginID: "catalog",
    scraperID,
    displayName,
    kind: "metadata",
    endpoint: "https://api.example.org/3",
    authRef: "example-api-key",
    authStyle: "query",
    credentialRequired: true,
    credentialConfigured: false,
    credentialStatus: "missing",
  });

  beforeEach(() => {
    vi.clearAllMocks();
    mocks.bootstrapResume.mockResolvedValue({
      alreadyBootstrapped: false,
      state: null,
      tokenRequired: false,
    });
    mocks.bootstrapSetup.mockResolvedValue(undefined);
    mocks.configureUpdateCheck.mockResolvedValue(undefined);
    mocks.activatePlugins.mockResolvedValue(undefined);
    mocks.fetchPlugins.mockResolvedValue([{ id: "catalog", displayName: "Catalog" }]);
    mocks.loadProviderKeys.mockResolvedValue(
      groupProviderKeys([scraper("movies", "Example (Movies)"), scraper("tv", "Example (TV)")]),
    );
    mocks.setScraperCredential.mockResolvedValue({
      authRef: "example-api-key",
      credentialConfigured: true,
      credentialStatus: "configured",
      scraperIDs: ["movies", "tv"],
    });
  });

  async function reachPluginsStep(user: ReturnType<typeof userEvent.setup>) {
    renderSetupWizard();
    await user.type(await screen.findByLabelText("Admin password"), "correct horse battery staple");
    await user.type(screen.getByLabelText("Confirm password"), "correct horse battery staple");
    await user.click(screen.getByRole("button", { name: "Continue" }));
    await user.click(screen.getByRole("button", { name: "Continue" }));
    await user.click(screen.getByRole("button", { name: "Continue" }));
    await screen.findByRole("heading", { name: "Choose what Beamfall understands" });
    await user.click(screen.getByRole("switch", { name: "Catalog" }));
  }

  it("saves a key typed on the Plugins step after activating plugins", async () => {
    const user = userEvent.setup();
    await reachPluginsStep(user);

    const field = await screen.findByLabelText("Example API key");
    expect(screen.getAllByLabelText(/API key/)).toHaveLength(1);
    await user.type(field, "test-key-value");
    await user.click(screen.getByRole("button", { name: "Continue" }));

    await waitFor(() =>
      expect(mocks.setScraperCredential).toHaveBeenCalledWith("example-api-key", "test-key-value"),
    );
    expect(mocks.loadProviderKeys).toHaveBeenCalledWith("correct horse battery staple");
    expect(mocks.activatePlugins.mock.invocationCallOrder[0]).toBeLessThan(
      mocks.setScraperCredential.mock.invocationCallOrder[0],
    );
  });

  it("skips saving when the key is left blank and hides it when the plugin is off", async () => {
    const user = userEvent.setup();
    await reachPluginsStep(user);

    expect(await screen.findByLabelText("Example API key")).toBeInTheDocument();
    await user.click(screen.getByRole("switch", { name: "Catalog" }));
    expect(screen.queryByLabelText("Example API key")).not.toBeInTheDocument();
    await user.click(screen.getByRole("switch", { name: "Catalog" }));
    await user.click(screen.getByRole("button", { name: "Continue" }));

    await waitFor(() => expect(mocks.activatePlugins).toHaveBeenCalled());
    expect(mocks.setScraperCredential).not.toHaveBeenCalled();
  });

  it("labels a plugin-declared credential and links where to get it (CORE-1127)", async () => {
    mocks.loadProviderKeys.mockResolvedValue(
      groupProviderKeys([
        {
          ...scraper("tv", "Example (TV)"),
          authRef: "example-subscriber-pin",
          credentialHelp: {
            label: { en: "Example subscriber PIN" },
            helpURL: "https://subscribe.example.org",
            helpLabel: { en: "Subscribe at example.org" },
          },
        },
      ]),
    );
    const user = userEvent.setup();
    await reachPluginsStep(user);

    const field = await screen.findByLabelText("Example subscriber PIN");
    expect(field).toHaveAttribute("type", "password");
    expect(screen.getByRole("link", { name: "Subscribe at example.org" })).toHaveAttribute(
      "href",
      "https://subscribe.example.org",
    );
    await user.type(field, "1234");
    await user.click(screen.getByRole("button", { name: "Continue" }));
    await waitFor(() =>
      expect(mocks.setScraperCredential).toHaveBeenCalledWith("example-subscriber-pin", "1234"),
    );
  });
});

describe("SetupWizard resume (CORE-1604)", () => {
  beforeEach(() => {
    mocks.bootstrapResume.mockResolvedValue({
      alreadyBootstrapped: true,
      state: { complete: false, pluginsActivated: true },
    });
    mocks.fetchPlugins.mockResolvedValue(FIRST_PARTY_PLUGINS);
    mocks.loadSavedPluginChoices.mockResolvedValue({
      plugins: FIRST_PARTY_PLUGINS,
      active: new Set(["music"]),
      updateCheckEnabled: false,
      updateCheckChannel: "stable",
      providerKeys: [],
    });
    mocks.configureUpdateCheck.mockResolvedValue(undefined);
    mocks.activatePlugins.mockResolvedValue(undefined);
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("resumes at Plugins with the saved choices and the same step total", async () => {
    const user = userEvent.setup();
    renderSetupWizard();

    expect(
      await screen.findByRole("heading", { name: "Choose what Beamfall understands" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Step 4 of 6")).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole("switch", { name: "Music" })).toBeChecked());
    expect(screen.getByRole("switch", { name: "Film & TV" })).not.toBeChecked();

    await user.click(screen.getByRole("button", { name: "Continue" }));

    await waitFor(() =>
      expect(mocks.activatePlugins).toHaveBeenCalledWith(
        ["music"],
        ["filmtv", "photos", "karaoke"],
      ),
    );
    expect(
      await screen.findByRole("heading", { name: "Create your first Library" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Step 5 of 6")).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "Music" })).toBeInTheDocument();
    expect(screen.queryByRole("radio", { name: "Films" })).not.toBeInTheDocument();
  });

  it("keeps the Plugins step from saving until the saved choices load, with Retry", async () => {
    const saved = {
      plugins: FIRST_PARTY_PLUGINS,
      active: new Set(["music"]),
      updateCheckEnabled: false,
      updateCheckChannel: "stable",
      providerKeys: [],
    };
    mocks.loadSavedPluginChoices.mockRejectedValue(new ApiError("unavailable", 503));
    const user = userEvent.setup();
    renderSetupWizard();

    expect(
      await screen.findByText(
        "Couldn't load the plugin choices saved on the server, so they can't be changed yet.",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByRole("switch", { name: "Film & TV" })).not.toBeInTheDocument();
    expect(screen.queryByRole("switch", { name: "Check for updates" })).not.toBeInTheDocument();
    const continueButton = screen.getByRole("button", { name: "Continue" });
    expect(continueButton).toBeDisabled();
    await user.click(continueButton);
    expect(mocks.activatePlugins).not.toHaveBeenCalled();
    expect(mocks.configureUpdateCheck).not.toHaveBeenCalled();

    mocks.loadSavedPluginChoices.mockResolvedValue(saved);
    await user.click(screen.getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(screen.getByRole("switch", { name: "Music" })).toBeChecked());
    expect(screen.getByRole("switch", { name: "Film & TV" })).not.toBeChecked();
    await user.click(screen.getByRole("button", { name: "Continue" }));
    await waitFor(() =>
      expect(mocks.activatePlugins).toHaveBeenCalledWith(
        ["music"],
        ["filmtv", "photos", "karaoke"],
      ),
    );
  });

  it("reopens a lapsed Admin Session with the admin password and continues (CORE-1731)", async () => {
    const saved = {
      plugins: FIRST_PARTY_PLUGINS,
      active: new Set(["music"]),
      updateCheckEnabled: false,
      updateCheckChannel: "stable",
      providerKeys: [],
    };
    mocks.loadSavedPluginChoices.mockRejectedValue(new ApiError("admin session required", 403));
    mocks.reopenAdminSession.mockResolvedValueOnce(false);
    const user = userEvent.setup();
    renderSetupWizard();

    expect(
      await screen.findByText(
        "The admin window has closed, so the saved plugin choices can't be read. Enter the admin password to reopen it and continue.",
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Continue" })).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Try again" })).not.toBeInTheDocument();

    // A rejected password says so, clears the field and leaves the step locked.
    const password = screen.getByLabelText("Admin password");
    await user.type(password, "wrong-password{Enter}");
    expect(await screen.findByText("The admin password was not accepted.")).toBeInTheDocument();
    expect(mocks.reopenAdminSession).toHaveBeenCalledWith("wrong-password");
    expect(password).toHaveValue("");
    expect(mocks.activatePlugins).not.toHaveBeenCalled();

    mocks.reopenAdminSession.mockResolvedValueOnce(true);
    mocks.loadSavedPluginChoices.mockResolvedValue(saved);
    await user.type(password, "admin-password");
    await user.click(screen.getByRole("button", { name: "Unlock" }));
    await waitFor(() => expect(screen.getByRole("switch", { name: "Music" })).toBeChecked());
    expect(mocks.reopenAdminSession).toHaveBeenLastCalledWith("admin-password");
    await user.click(screen.getByRole("button", { name: "Continue" }));
    await waitFor(() =>
      expect(mocks.activatePlugins).toHaveBeenCalledWith(
        ["music"],
        ["filmtv", "photos", "karaoke"],
      ),
    );
  });

  it("resumes at First library with types that match the enabled plugins", async () => {
    resumeAtLibraryStep();
    renderSetupWizard();

    expect(
      await screen.findByRole("heading", { name: "Create your first Library" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Step 5 of 6")).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole("radio", { name: "Music" })).toBeInTheDocument());
    expect(screen.queryByRole("radio", { name: "Films" })).not.toBeInTheDocument();
    expect(screen.queryByRole("radio", { name: "Photos" })).not.toBeInTheDocument();
  });
});

describe("SetupWizard first-run steps (CORE-1601, CORE-1610, CORE-1600)", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/web/setup");
    mocks.fetchPlugins.mockResolvedValue([]);
    mocks.bootstrapResume.mockResolvedValue({
      alreadyBootstrapped: false,
      state: null,
      tokenRequired: false,
    });
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("counts steps from the admin password, not the token", async () => {
    mocks.bootstrapResume.mockResolvedValue({
      alreadyBootstrapped: false,
      state: null,
      tokenRequired: true,
    });
    renderSetupWizard();

    await screen.findByRole("heading", { name: "Unlock with the setup token" });
    expect(screen.queryByText(/^Step \d+ of/)).not.toBeInTheDocument();
  });

  it("offers the LAN address on the token step when the server reports one", async () => {
    mocks.bootstrapResume.mockResolvedValue({
      alreadyBootstrapped: false,
      state: null,
      tokenRequired: true,
      lanUrls: ["http://192.168.1.10:8800/web/setup"],
    });
    renderSetupWizard();

    const link = await screen.findByRole("link", {
      name: "On the same network as the server? Open http://192.168.1.10:8800/web/setup to skip the token.",
    });
    expect(link).toHaveAttribute("href", "http://192.168.1.10:8800/web/setup");
  });

  it("shows the 8-character rule, refuses a shorter password and toggles visibility", async () => {
    const user = userEvent.setup();
    renderSetupWizard();

    const password = await screen.findByLabelText("Admin password");
    expect(screen.getByText("Step 1 of 6")).toBeInTheDocument();
    expect(password).toHaveAccessibleDescription("At least 8 characters.");
    await user.type(password, "seven77");
    await user.type(screen.getByLabelText("Confirm password"), "seven77");
    await user.click(screen.getByRole("button", { name: "Continue" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Admin password must be at least 8 characters.",
    );
    expect(password).toHaveAttribute("aria-invalid", "true");
    expect(mocks.bootstrapSetup).not.toHaveBeenCalled();

    const toggle = screen.getByRole("button", { name: "Show password" });
    expect(password).toHaveAttribute("type", "password");
    await user.click(toggle);
    expect(password).toHaveAttribute("type", "text");
    expect(screen.getByLabelText("Confirm password")).toHaveAttribute("type", "text");
    expect(screen.getByRole("button", { name: "Hide password" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
  });

  it("returns to the password step when the server refuses a short password", async () => {
    mocks.bootstrapSetup.mockRejectedValueOnce(
      new ApiError("Admin password must be at least 8 characters.", 400),
    );
    const user = userEvent.setup();
    renderSetupWizard();

    await user.type(await screen.findByLabelText("Admin password"), "correct horse battery staple");
    await user.type(screen.getByLabelText("Confirm password"), "correct horse battery staple");
    await user.click(screen.getByRole("button", { name: "Continue" }));
    await user.click(screen.getByRole("button", { name: "Continue" }));
    await user.click(screen.getByRole("button", { name: "Continue" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Admin password must be at least 8 characters.",
    );
    expect(screen.getByRole("heading", { name: "Set the admin password" })).toBeInTheDocument();
  });

  it("names security radios by their labels and describes them by their details", async () => {
    const user = userEvent.setup();
    renderSetupWizard();

    await user.type(await screen.findByLabelText("Admin password"), "correct horse battery staple");
    await user.type(screen.getByLabelText("Confirm password"), "correct horse battery staple");
    await user.click(screen.getByRole("button", { name: "Continue" }));

    const group = await screen.findByRole("group", { name: "Choose how the owner signs in" });
    expect(group.querySelector("legend")).toBeNull();
    expect(screen.getAllByText("Choose how the owner signs in")).toHaveLength(1);
    expect(screen.getByRole("radio", { name: /^Home\s*Recommended$/ })).toHaveAccessibleDescription(
      "Sign in as the owner from any browser with the admin password.",
    );
    const strict = screen.getByRole("radio", { name: "Strict" });
    expect(strict).toHaveAttribute("value", "strict");
    expect(strict).toHaveAccessibleDescription(/.+/);
    expect(screen.queryByRole("radio", { name: "home" })).not.toBeInTheDocument();
  });
});

describe("SetupWizard French catalog (CORE-1310)", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("opens a fresh server's token step in French", async () => {
    mocks.bootstrapResume.mockResolvedValue({ alreadyBootstrapped: false, state: null });
    render(
      <I18nProvider initialLocale="fr">
        <SetupWizard />
      </I18nProvider>,
    );

    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "Déverrouiller avec le jeton de configuration",
      }),
    ).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Jeton de configuration" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Continuer" })).toBeInTheDocument();
  });
});
