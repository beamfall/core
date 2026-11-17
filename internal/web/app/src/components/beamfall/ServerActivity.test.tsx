import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchAdminSession, fetchJobs } from "@/lib/graphql";
import type { JobInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { ServerActivity } from "./ServerActivity";

vi.mock("@/lib/graphql", () => ({ fetchAdminSession: vi.fn(), fetchJobs: vi.fn() }));

const rescan = vi.hoisted(() => ({ active: false }));
vi.mock("@/components/beamfall/LibraryRescan", () => ({
  useAnyRescanActive: () => rescan.active,
}));

function job(status: JobInfo["status"]): JobInfo {
  return {
    id: `job-${status}`,
    kind: "scan",
    status,
    progress: { current: 0, total: 0, unit: "" },
  };
}

let visibility: DocumentVisibilityState = "visible";

function setVisibility(next: DocumentVisibilityState) {
  visibility = next;
  document.dispatchEvent(new Event("visibilitychange"));
}

function activity() {
  return (
    <I18nProvider>
      <MemoryRouter>
        <ServerActivity />
      </MemoryRouter>
    </I18nProvider>
  );
}

function renderActivity() {
  return render(activity());
}

// Lets the pending read settle and runs the timers due within ms.
async function advance(ms: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

beforeEach(() => {
  vi.useFakeTimers();
  rescan.active = false;
  visibility = "visible";
  Object.defineProperty(document, "visibilityState", {
    configurable: true,
    get: () => visibility,
  });
  vi.mocked(fetchJobs).mockReset().mockResolvedValue([]);
  vi.mocked(fetchAdminSession)
    .mockReset()
    .mockResolvedValue({ active: true, expiresAt: "2999-01-01T00:00:00Z" });
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  Reflect.deleteProperty(document, "visibilityState");
});

describe("ServerActivity", () => {
  it("asks an idle server for its jobs only at the slow cadence", async () => {
    vi.mocked(fetchJobs).mockResolvedValue([job("succeeded")]);
    renderActivity();
    await advance(0);
    expect(fetchJobs).toHaveBeenCalledTimes(1);
    // A background read: a refusal must not open the Manage in-place unlock.
    expect(fetchJobs).toHaveBeenCalledWith(undefined, { adminUnlock: false });
    expect(screen.getByRole("link", { name: "Activity" })).toBeInTheDocument();

    await advance(25000);
    expect(fetchJobs).toHaveBeenCalledTimes(1);

    await advance(5000);
    expect(fetchJobs).toHaveBeenCalledTimes(2);
  });

  it("follows running jobs at the fast cadence and slows down when they finish", async () => {
    vi.mocked(fetchJobs).mockResolvedValueOnce([job("running")]);
    renderActivity();
    await advance(0);
    expect(screen.getByRole("link", { name: "1 job running" })).toBeInTheDocument();

    // The next read finds nothing running, so the one after it waits the idle interval.
    await advance(5000);
    expect(fetchJobs).toHaveBeenCalledTimes(2);
    expect(screen.getByRole("link", { name: "Activity" })).toBeInTheDocument();

    await advance(5000);
    expect(fetchJobs).toHaveBeenCalledTimes(2);
  });

  it("stays at the slow cadence while the jobs read is refused", async () => {
    vi.mocked(fetchJobs).mockRejectedValue(new Error("admin session required"));
    renderActivity();
    await advance(0);
    expect(fetchJobs).toHaveBeenCalledTimes(1);

    await advance(25000);
    expect(fetchJobs).toHaveBeenCalledTimes(1);
  });

  it("reads nothing while the tab is hidden and catches up when it is shown again", async () => {
    visibility = "hidden";
    renderActivity();
    await advance(60000);
    expect(fetchJobs).not.toHaveBeenCalled();

    setVisibility("visible");
    await advance(0);
    expect(fetchJobs).toHaveBeenCalledTimes(1);

    setVisibility("hidden");
    await advance(60000);
    expect(fetchJobs).toHaveBeenCalledTimes(1);
  });

  it("counts jobs at once and at the fast cadence when a rescan starts on this page", async () => {
    const view = renderActivity();
    await advance(0);
    expect(fetchJobs).toHaveBeenCalledTimes(1);

    rescan.active = true;
    view.rerender(activity());
    await advance(0);
    expect(fetchJobs).toHaveBeenCalledTimes(2);

    await advance(5000);
    expect(fetchJobs).toHaveBeenCalledTimes(3);
  });

  // CORE-2318: without an Admin Session the header must not ask for jobs the
  // server refuses; it reads only the session's standing at the slow cadence.
  it("reads no jobs while the Admin Session is inactive", async () => {
    vi.mocked(fetchAdminSession).mockResolvedValue({ active: false });
    renderActivity();
    await advance(0);
    expect(fetchAdminSession).toHaveBeenCalledTimes(1);
    expect(fetchJobs).not.toHaveBeenCalled();
    expect(screen.getByRole("link", { name: "Activity" })).toBeInTheDocument();

    await advance(30000);
    expect(fetchAdminSession).toHaveBeenCalledTimes(2);
    expect(fetchJobs).not.toHaveBeenCalled();
  });

  it("trusts a known Admin Session until a jobs read is refused", async () => {
    renderActivity();
    await advance(0);
    await advance(30000);
    expect(fetchAdminSession).toHaveBeenCalledTimes(1);
    expect(fetchJobs).toHaveBeenCalledTimes(2);

    vi.mocked(fetchJobs).mockRejectedValueOnce(new Error("admin session required"));
    vi.mocked(fetchAdminSession).mockResolvedValue({ active: false });
    await advance(30000);
    expect(fetchJobs).toHaveBeenCalledTimes(3);
    await advance(30000);
    expect(fetchAdminSession).toHaveBeenCalledTimes(2);
    expect(fetchJobs).toHaveBeenCalledTimes(3);
  });
});
