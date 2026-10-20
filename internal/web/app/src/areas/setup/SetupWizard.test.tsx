import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { StrictMode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
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
  fetchJobs: vi.fn(),
  fetchPlugins: vi.fn(),
}));

vi.mock("@/lib/graphql", async () => {
  const actual = await vi.importActual<typeof import("@/lib/graphql")>("@/lib/graphql");
  return {
    ...actual,
    browseServerFolders: mocks.browseServerFolders,
    fetchJobs: mocks.fetchJobs,
    fetchPlugins: mocks.fetchPlugins,
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
  };
});

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
});

describe("SetupWizard first scan", () => {
  beforeEach(() => {
    mocks.bootstrapResume.mockResolvedValue({
      alreadyBootstrapped: true,
      state: { complete: false, pluginsActivated: true },
    });
    mocks.fetchPlugins.mockResolvedValue([]);
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

    await screen.findByRole("heading", { name: "First scan is running" });
    expect(mocks.createFirstLibrary).toHaveBeenCalledWith({
      title: "Films",
      type: "films",
      rootPath: "/media/films",
      progress: { rootsSet: false },
    });
    expect(await screen.findByText("214/500 files - Scanning")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Browse what's ready" })).toBeInTheDocument();
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
      await screen.findByRole("heading", { name: "First scan is running" }),
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

  it("keeps the configured root fixed when retrying only the scan", async () => {
    mocks.createFirstLibrary.mockRejectedValueOnce(
      new FirstLibraryError(new Error("scan queue unavailable"), {
        libraryID: "lib1",
        rootsSet: true,
        rootPath: "/media/films",
      }),
    );
    const user = userEvent.setup();
    renderSetupWizard();

    await screen.findByRole("heading", { name: "Create your first Library" });
    await user.type(screen.getByLabelText("Root folder"), "/media/films");
    await user.click(screen.getByRole("button", { name: "Continue" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("scan queue unavailable");
    expect(screen.getByLabelText("Root folder")).toBeDisabled();
    expect(screen.getByRole("button", { name: /^Browse/ })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Continue" }));
    expect(mocks.createFirstLibrary).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({
        rootPath: "/media/films",
        progress: { libraryID: "lib1", rootsSet: true, rootPath: "/media/films" },
      }),
    );
  });

  it("surfaces scan polling failures through the existing setup error", async () => {
    mocks.fetchJobs.mockRejectedValue(new Error("offline"));
    const user = userEvent.setup();
    renderSetupWizard();

    await screen.findByRole("heading", { name: "Create your first Library" });
    await user.type(screen.getByLabelText("Root folder"), "/media/films");
    await user.click(screen.getByRole("button", { name: "Continue" }));

    expect(await screen.findByRole("alert")).not.toBeEmptyDOMElement();
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
    await screen.findByRole("heading", { name: "First scan is running" });
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

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Something went wrong. Please try again.",
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
    expect(screen.getByLabelText("Setup token")).toHaveValue("");
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

    expect(await screen.findByRole("heading", { name: "Activate plugins" })).toBeInTheDocument();
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
