import "@testing-library/jest-dom/vitest";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { resetRescansForTest } from "@/components/beamfall/LibraryRescan";
import { setAdvancedSettings } from "@/lib/advancedSettings";
import {
  cancelJob,
  fetchConsoleConfig,
  fetchJobEvents,
  fetchLibraries,
  fetchOperationsGovernor,
  retryJob,
  runEventsCompaction,
  scanLibrary,
  setConsoleConfig,
} from "@/lib/graphql";
import type { JobEventInfo, JobInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { fetchConsoleJobs } from "../lib/jobQueue";
import { OperationsControls } from "./OperationsControls";

// jsdom has no ResizeObserver; the Radix Switch (via react-use-size) needs one.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= ResizeObserverStub as unknown as typeof ResizeObserver;

vi.mock("@/lib/graphql", () => ({
  fetchOperationsGovernor: vi.fn(),
  fetchConsoleConfig: vi.fn(),
  setConsoleConfig: vi.fn(),
  fetchJobs: vi.fn(),
  fetchJobEvents: vi.fn(),
  cancelJob: vi.fn(),
  retryJob: vi.fn(),
  runEventsCompaction: vi.fn(),
  fetchLibraries: vi.fn(),
  scanLibrary: vi.fn(),
}));

vi.mock("../lib/jobQueue", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/jobQueue")>()),
  fetchConsoleJobs: vi.fn(),
}));

let eventObservers: ((event: MessageEvent) => void)[] = [];
vi.mock("@/lib/api", async (importOriginal) => ({
  // Keep the real exports (useAsync's failure path does `instanceof ApiError`;
  // a factory-only mock makes that property access throw at runtime).
  ...(await importOriginal<typeof import("@/lib/api")>()),
  subscribeEvents: (onEvent: (event: MessageEvent) => void) => {
    eventObservers.push(onEvent);
    return () => {};
  },
}));

const governorAvailable = {
  available: true,
  generationWorkers: 4,
  backgroundCPUBudget: 4,
  activeTranscodes: 2,
  activeGeneration: 1,
  generationAllowed: true,
  lowPowerMode: false,
  transcodePreemptingBackground: true,
};

const config = {
  backgroundWorkers: 4,
  updateCheckEnabled: false,
  updateCheckChannel: "stable",
  remoteEnrollmentEnabled: true,
  eventsRetentionDays: 180,
  plugins: [],
};

function job(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    id: "job-1",
    kind: "scan",
    status: "running",
    progress: { current: 620, total: 1000, unit: "files", message: "Scanning" },
    ...overrides,
  };
}

beforeEach(() => {
  // Governor status, worker pool and compaction are Advanced (CORE-1117).
  setAdvancedSettings(true);
  eventObservers = [];
  vi.mocked(fetchOperationsGovernor).mockReset().mockResolvedValue(governorAvailable);
  vi.mocked(fetchConsoleConfig).mockReset().mockResolvedValue(config);
  vi.mocked(setConsoleConfig).mockReset().mockResolvedValue(config);
  vi.mocked(fetchConsoleJobs).mockReset().mockResolvedValue([job()]);
  vi.mocked(fetchJobEvents).mockReset().mockResolvedValue([]);
  vi.mocked(fetchLibraries)
    .mockReset()
    .mockResolvedValue([
      { id: "lib-1", title: "Films", roots: ["/media/films", "/media/more"] },
      { id: "lib-2", title: "Empty", roots: [] },
    ] as never);
  vi.mocked(scanLibrary)
    .mockReset()
    .mockResolvedValue(job({ id: "job-scan", status: "queued" }) as never);
  vi.mocked(cancelJob)
    .mockReset()
    .mockResolvedValue(job({ status: "canceled" }));
  vi.mocked(retryJob)
    .mockReset()
    .mockResolvedValue(job({ id: "job-2", status: "queued" }));
  vi.mocked(runEventsCompaction)
    .mockReset()
    .mockResolvedValue(job({ kind: "events-compaction", status: "queued" }));
});

// The governor readout sits in a folded card (CORE-1123); open it as a user would.
async function openGovernor() {
  await userEvent.setup().click(await screen.findByRole("heading", { name: "Resource governor" }));
}

function renderPanel() {
  return render(
    <I18nProvider>
      <OperationsControls />
    </I18nProvider>,
  );
}

function driveEventStream(data: unknown = {}) {
  const event = new MessageEvent("message", { data: JSON.stringify(data) });
  act(() => eventObservers.forEach((onEvent) => onEvent(event)));
}

afterEach(() => {
  localStorage.clear();
  setAdvancedSettings(false);
});

describe("OperationsControls", () => {
  it("renders the live governor posture from operationsGovernor", async () => {
    renderPanel();
    await openGovernor();

    expect(await screen.findByText("Generation allowed")).toBeVisible();
    expect(screen.getByText("Background work paused for playback")).toBeVisible();
    expect(screen.getByText("Generation workers")).toBeVisible();
    expect(fetchOperationsGovernor).toHaveBeenCalled();
  });

  it("renders the unwired state when the governor is not available", async () => {
    vi.mocked(fetchOperationsGovernor).mockResolvedValue({
      ...governorAvailable,
      available: false,
    });
    renderPanel();
    await openGovernor();

    expect(
      await screen.findByText("The resource governor is not active on this server."),
    ).toBeVisible();
  });

  it("renders the governor-unavailable state when the query fails", async () => {
    vi.mocked(fetchOperationsGovernor).mockRejectedValue(new Error("boom"));
    renderPanel();
    await openGovernor();

    expect(
      await screen.findByText("The resource governor is unavailable right now."),
    ).toBeVisible();
  });

  it("saves the background worker count through setConsoleConfig", async () => {
    const user = userEvent.setup();
    renderPanel();
    const input = await screen.findByLabelText("Background workers");

    await user.tripleClick(input);
    await user.keyboard("6");
    await user.click(screen.getByRole("button", { name: "Save background workers" }));

    await waitFor(() => expect(setConsoleConfig).toHaveBeenCalledWith({ backgroundWorkers: 6 }));
  });

  it("pins workers to one when low-power mode is enabled", async () => {
    const user = userEvent.setup();
    renderPanel();
    await screen.findByLabelText("Background workers");

    await user.click(screen.getByRole("switch", { name: "Low-power mode" }));

    await waitFor(() => expect(setConsoleConfig).toHaveBeenCalledWith({ backgroundWorkers: 1 }));
  });

  it("saves events retention days through setConsoleConfig", async () => {
    const user = userEvent.setup();
    renderPanel();
    const input = await screen.findByLabelText("Events retention days");

    await user.tripleClick(input);
    await user.keyboard("90");
    await user.click(screen.getByRole("button", { name: "Save events retention days" }));

    await waitFor(() => expect(setConsoleConfig).toHaveBeenCalledWith({ eventsRetentionDays: 90 }));
  });

  it("queues events compaction on demand", async () => {
    const user = userEvent.setup();
    renderPanel();
    await screen.findByLabelText("Events retention days");

    await user.click(screen.getByRole("button", { name: "Compact events now" }));

    await waitFor(() => expect(runEventsCompaction).toHaveBeenCalled());
    expect(await screen.findByText("Queued")).toBeVisible();
  });

  it("renders jobs as flat job-rows with a progress bar, not a data table", async () => {
    vi.mocked(fetchConsoleJobs).mockResolvedValue([job(), job({ id: "job-2", status: "queued" })]);
    const { container } = renderPanel();
    await waitFor(() => expect(container.querySelectorAll(".console-job-row").length).toBe(2));

    expect(container.querySelectorAll(".console-job-row").length).toBe(2);
    expect(container.querySelector(".console-table")).toBeNull();
    expect(container.querySelector(".console-job-row .console-jbar")).not.toBeNull();
    expect(container.querySelector(".console-job-actions")).not.toBeNull();
  });

  it("says what each job does and which library folder it works on", async () => {
    vi.mocked(fetchConsoleJobs).mockResolvedValue([
      job({ target: { libraryID: "lib-1", libraryTitle: "Films", root: "/media/films" } }),
      job({
        id: "job-2",
        status: "queued",
        target: { libraryID: "lib-1", libraryTitle: "Films", root: "/media/films", backfill: true },
      }),
      job({
        id: "job-3",
        kind: "artwork-sweep",
        status: "queued",
        progress: { current: 0, total: 0, unit: "" },
      }),
      job({
        id: "job-4",
        kind: "playback.precompute",
        status: "queued",
        progress: { current: 0, total: 0, unit: "" },
      }),
    ]);
    const { container } = renderPanel();
    await waitFor(() => expect(container.querySelectorAll(".console-job-row").length).toBe(4));
    const rows = Array.from(container.querySelectorAll(".console-job-row"));

    expect(rows[0]).toHaveTextContent(
      "Walks a library folder and imports new or changed media files.",
    );
    expect(rows[0]).toHaveTextContent("Films · /media/films");
    expect(rows[1]).toHaveTextContent("refreshes metadata");
    expect(rows[1]).toHaveTextContent("Metadata refresh");
    expect(rows[2]).toHaveTextContent("Fills in missing posters");
    expect(rows[2].querySelector(".console-job-target")).toBeNull();
    expect(rows[3]).toHaveTextContent("Playback precompute");
    expect(rows[3]).toHaveTextContent("Prepares playback renditions");
  });

  it("force-queues a scan of every library folder from the job queue", async () => {
    renderPanel();
    const scanNow = await screen.findByRole("button", { name: "Rescan all libraries" });
    await waitFor(() => expect(scanNow).toBeEnabled());
    await userEvent.click(scanNow);

    await waitFor(() => expect(scanLibrary).toHaveBeenCalledTimes(2));
    expect(scanLibrary).toHaveBeenCalledWith("lib-1", "/media/films");
    expect(scanLibrary).toHaveBeenCalledWith("lib-1", "/media/more");
  });

  it("says when Scan now could not queue a library folder", async () => {
    resetRescansForTest();
    vi.mocked(scanLibrary)
      .mockResolvedValueOnce(job({ id: "job-scan", status: "queued" }) as never)
      .mockRejectedValueOnce(new Error("folder is missing"));
    renderPanel();
    const scanNow = await screen.findByRole("button", { name: "Rescan all libraries" });
    await waitFor(() => expect(scanNow).toBeEnabled());
    await userEvent.click(scanNow);

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Films: Scan failed — folder is missing",
    );
    resetRescansForTest();
  });

  it("cancels a live job through the confirm dialog", async () => {
    const user = userEvent.setup();
    renderPanel();
    await screen.findByText("Scan");

    await user.click(screen.getByRole("button", { name: "Cancel job" }));
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Cancel job" }));

    await waitFor(() => expect(cancelJob).toHaveBeenCalledWith("job-1"));
  });

  it("retries a terminal job through jobRetry", async () => {
    vi.mocked(fetchConsoleJobs).mockResolvedValue([job({ status: "failed" })]);
    const user = userEvent.setup();
    renderPanel();
    await screen.findByText("Scan");

    await user.click(screen.getByRole("button", { name: "Retry" }));

    await waitFor(() => expect(retryJob).toHaveBeenCalledWith("job-1"));
  });

  it("opens the durable job log on demand", async () => {
    vi.mocked(fetchJobEvents).mockResolvedValue([
      {
        id: "1",
        jobID: "job-1",
        occurredAt: "2026-06-20T00:00:00Z",
        type: "started",
        payload: "worker=w2",
      },
      {
        id: "2",
        jobID: "job-1",
        occurredAt: "2026-06-20T00:00:01Z",
        type: "progress",
        payload: '{"type":"progress","current":7,"total":10,"unit":"files","message":"Scanning"}',
      },
    ]);
    const user = userEvent.setup();
    renderPanel();
    await screen.findByText("Scan");

    await user.click(screen.getByRole("button", { name: "Logs" }));

    expect(await screen.findByText("started")).toBeVisible();
    expect(screen.getByText("worker=w2")).toBeVisible();
    expect(screen.getByText("7/10 files - Scanning")).toBeVisible();
    expect(fetchJobEvents).toHaveBeenCalledWith("job-1", expect.any(AbortSignal));
  });

  it("refreshes the open job log from the live event stream without leaking restricted names or secret payloads", async () => {
    const restrictedName = "restricted-title-alpha";
    const token = "live-secret-token";
    const host = "private.internal";
    const streamID = "stream-secret";
    const initialEvents: JobEventInfo[] = [
      {
        id: "1",
        jobID: "job-1",
        occurredAt: "2026-06-20T00:00:00Z",
        type: "started",
        payload: "worker=w2",
      },
    ];
    const redactedEvents: JobEventInfo[] = [
      {
        id: "2",
        jobID: "job-1",
        occurredAt: "2026-06-20T00:00:01Z",
        type: "failed",
        payload: `{"error":"Bearer ${token} https://${host}/api/media/direct/${streamID}/file.mp4"}`,
      },
      { id: "3", jobID: "job-1", occurredAt: "2026-06-20T00:00:02Z", type: "restricted" },
    ];
    vi.mocked(fetchJobEvents)
      .mockResolvedValueOnce(initialEvents)
      .mockResolvedValue(redactedEvents);
    const user = userEvent.setup();
    renderPanel();
    await screen.findByText("Scan");

    await user.click(screen.getByRole("button", { name: "Logs" }));
    expect(await screen.findByText("worker=w2")).toBeVisible();

    // The SSE wake-up can carry a restricted target name, but the Console
    // live-log path must refresh jobEvents and render only the redacted stream.
    driveEventStream({ type: "job.failed", target: restrictedName });

    expect(await screen.findByText("failed")).toBeVisible();
    expect(screen.getByText("restricted")).toBeVisible();
    expect(screen.getByText("(redacted)")).toBeVisible();
    expect(screen.getByText(/\[REDACTED\]/)).toBeVisible();
    for (const leak of [restrictedName, token, host, streamID]) {
      expect(screen.queryByText(new RegExp(leak))).toBeNull();
    }
    expect(fetchJobEvents).toHaveBeenCalledTimes(2);
  });

  it("puts live work and unresolved failures first and folds finished runs per kind (CORE-2308)", async () => {
    const at = (minutesAgo: number) => new Date(Date.now() - minutesAgo * 60_000).toISOString();
    vi.mocked(fetchConsoleJobs).mockResolvedValue([
      job({ id: "poll-1", kind: "update-check", status: "completed", finishedAt: at(30) }),
      job({ id: "poll-2", kind: "update-check", status: "completed", finishedAt: at(20) }),
      job({ id: "poll-3", kind: "update-check", status: "completed", finishedAt: at(10) }),
      job({ id: "queued-1", status: "queued", createdAt: at(1) }),
      job({
        id: "failed-1",
        kind: "artwork-sweep",
        status: "failed",
        error: "provider timed out",
        finishedAt: at(5),
      }),
      job({ id: "run-1", status: "running", startedAt: at(3) }),
    ]);
    const { container } = renderPanel();
    await waitFor(() => expect(container.querySelectorAll(".console-job-row").length).toBe(4));

    const front = Array.from(
      container.querySelectorAll(
        ".console-job-list:not(.console-job-history *) > .console-job-row",
      ),
    );
    expect(front).toHaveLength(3);
    expect(front[0]).toHaveTextContent("Running");
    expect(front[0]).toHaveTextContent("Started 3 minutes ago · running for 3 min 0 s");
    expect(front[1]).toHaveTextContent("Queued 1 minute ago");
    expect(front[2]).toHaveTextContent("Failed");
    expect(front[2].querySelector(".console-job-error")).toHaveTextContent("provider timed out");

    const history = container.querySelector("details.console-job-history") as HTMLDetailsElement;
    expect(history.open).toBe(false);
    expect(history.querySelector("summary")).toHaveTextContent("Finished work (1 kind of job)");
    const folded = history.querySelectorAll(".console-job-row");
    expect(folded).toHaveLength(1);
    expect(folded[0]).toHaveTextContent("3 runs");
    expect(folded[0]).toHaveTextContent("Finished 10 minutes ago");
  });

  it("says nothing needs attention when only finished work is left", async () => {
    vi.mocked(fetchConsoleJobs).mockResolvedValue([
      job({ status: "completed", finishedAt: new Date().toISOString() }),
    ]);
    renderPanel();

    expect(
      await screen.findByText("Nothing is running or waiting, and nothing has failed."),
    ).toBeVisible();
  });

  it("gathers a burst of events into one refresh and keeps the rows on screen", async () => {
    renderPanel();
    await screen.findByText("Scan");
    await openGovernor();
    await screen.findByText("Generation allowed");
    const jobReads = vi.mocked(fetchConsoleJobs).mock.calls.length;
    const governorReads = vi.mocked(fetchOperationsGovernor).mock.calls.length;
    let finishJobs: (jobs: JobInfo[]) => void = () => {};
    vi.mocked(fetchConsoleJobs).mockImplementation(
      () => new Promise<JobInfo[]>((resolve) => (finishJobs = resolve)),
    );

    // A library scan emits events many times a second.
    for (let i = 0; i < 25; i++) driveEventStream({ type: "job.progress" });

    await waitFor(() => expect(fetchConsoleJobs).toHaveBeenCalledTimes(jobReads + 1));
    // The read is still in flight: the last rows stay, with no skeleton flash.
    expect(screen.getByText("Scan")).toBeVisible();
    expect(screen.getByText("Generation allowed")).toBeVisible();
    await act(async () => finishJobs([job()]));
    await waitFor(() => expect(fetchOperationsGovernor).toHaveBeenCalledTimes(governorReads + 1));
    expect(fetchConsoleJobs).toHaveBeenCalledTimes(jobReads + 1);
  });
});

describe("OperationsControls in the Normal view (CORE-1117)", () => {
  beforeEach(() => setAdvancedSettings(false));

  it("shows the job queue and low-power switch and keeps a changed setting in view", async () => {
    renderPanel();

    expect(await screen.findByRole("switch", { name: "Low-power mode" })).toBeVisible();
    // Four workers is not the default, so the field stays visible.
    expect(await screen.findByLabelText("Background workers")).toBeVisible();
    expect(screen.queryByLabelText("Events retention days")).toBeNull();
    expect(screen.queryByRole("button", { name: "Compact events now" })).toBeNull();
    expect(screen.queryByText("Generation allowed")).toBeNull();
  });

  it("leaves the worker pool to Advanced at its default", async () => {
    vi.mocked(fetchConsoleConfig).mockResolvedValue({ ...config, backgroundWorkers: 2 });
    renderPanel();

    await screen.findByRole("switch", { name: "Low-power mode" });
    expect(screen.queryByLabelText("Background workers")).toBeNull();
  });
});
