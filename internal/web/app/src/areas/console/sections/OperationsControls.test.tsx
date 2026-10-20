import "@testing-library/jest-dom/vitest";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  cancelJob,
  fetchConsoleConfig,
  fetchJobEvents,
  fetchJobs,
  fetchOperationsGovernor,
  retryJob,
  runEventsCompaction,
  setConsoleConfig,
} from "@/lib/graphql";
import type { JobEventInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
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
  eventObservers = [];
  vi.mocked(fetchOperationsGovernor).mockReset().mockResolvedValue(governorAvailable);
  vi.mocked(fetchConsoleConfig).mockReset().mockResolvedValue(config);
  vi.mocked(setConsoleConfig).mockReset().mockResolvedValue(config);
  vi.mocked(fetchJobs).mockReset().mockResolvedValue([job()]);
  vi.mocked(fetchJobEvents).mockReset().mockResolvedValue([]);
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

describe("OperationsControls", () => {
  it("renders the live governor posture from operationsGovernor", async () => {
    renderPanel();

    expect(await screen.findByText("Generation allowed")).toBeVisible();
    expect(screen.getByText("Transcode preempting background")).toBeVisible();
    expect(screen.getByText("Generation workers")).toBeVisible();
    expect(fetchOperationsGovernor).toHaveBeenCalled();
  });

  it("renders the unwired state when the governor is not available", async () => {
    vi.mocked(fetchOperationsGovernor).mockResolvedValue({
      ...governorAvailable,
      available: false,
    });
    renderPanel();

    expect(
      await screen.findByText("The resource governor is not active on this server."),
    ).toBeVisible();
  });

  it("renders the governor-unavailable state when the query fails", async () => {
    vi.mocked(fetchOperationsGovernor).mockRejectedValue(new Error("boom"));
    renderPanel();

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
    vi.mocked(fetchJobs).mockResolvedValue([job(), job({ id: "job-2", status: "queued" })]);
    const { container } = renderPanel();
    await waitFor(() => expect(container.querySelectorAll(".console-job-row").length).toBe(2));

    expect(container.querySelectorAll(".console-job-row").length).toBe(2);
    expect(container.querySelector(".console-table")).toBeNull();
    expect(container.querySelector(".console-job-row .console-jbar")).not.toBeNull();
    expect(container.querySelector(".console-job-actions")).not.toBeNull();
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
    vi.mocked(fetchJobs).mockResolvedValue([job({ status: "failed" })]);
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
    ]);
    const user = userEvent.setup();
    renderPanel();
    await screen.findByText("Scan");

    await user.click(screen.getByRole("button", { name: "Logs" }));

    expect(await screen.findByText("started")).toBeVisible();
    expect(screen.getByText("worker=w2")).toBeVisible();
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
});
