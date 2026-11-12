import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, renderHook, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api";
import { fetchJobs, scanLibrary } from "@/lib/graphql";
import type { JobInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import {
  RescanStatus,
  rescanState,
  resetRescansForTest,
  startRescan,
  useAnyRescanActive,
} from "./LibraryRescan";

vi.mock("@/lib/graphql", () => ({ fetchJobs: vi.fn(), scanLibrary: vi.fn() }));

function job(status: string): JobInfo {
  return { id: "job-1", kind: "scan", status, progress: { current: 0, total: 0, unit: "" } };
}

// Lets pending reads settle and runs the timers due within ms.
async function advance(ms: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

beforeEach(() => {
  vi.useFakeTimers();
  resetRescansForTest();
  vi.mocked(scanLibrary).mockReset().mockResolvedValue({ id: "job-1", status: "queued" });
  vi.mocked(fetchJobs)
    .mockReset()
    .mockResolvedValue([job("running")]);
});

afterEach(() => {
  cleanup();
  resetRescansForTest();
  vi.useRealTimers();
});

describe("library rescan tracker", () => {
  it("stops polling and claims nothing running once the jobs read is refused", async () => {
    const active = renderHook(() => useAnyRescanActive());
    await act(() => startRescan("library-1", ["/media"]));
    await advance(1500);
    expect(fetchJobs).toHaveBeenCalledTimes(1);
    expect(active.result.current).toBe(true);

    // The Console is locked, or the admin window lapsed.
    vi.mocked(fetchJobs).mockRejectedValue(new ApiError("admin session required", 403));
    await advance(1500);
    expect(fetchJobs).toHaveBeenCalledTimes(2);
    expect(active.result.current).toBe(false);

    await advance(60000);
    expect(fetchJobs).toHaveBeenCalledTimes(2);
  });

  it("catches up when a rescan control is shown again after unlock", async () => {
    await act(() => startRescan("library-1", ["/media"]));
    vi.mocked(fetchJobs).mockRejectedValue(new ApiError("admin session required", 403));
    await advance(1500);
    expect(fetchJobs).toHaveBeenCalledTimes(1);
    expect(rescanState("library-1").phase).toBe("scanning");

    // Unlocking remounts the gated page; the scan finished meanwhile.
    vi.mocked(fetchJobs).mockResolvedValue([job("succeeded")]);
    render(
      <I18nProvider>
        <RescanStatus libraryID="library-1" hasRoots />
      </I18nProvider>,
    );
    await advance(0);

    expect(fetchJobs).toHaveBeenCalledTimes(2);
    expect(screen.getByRole("status")).toHaveTextContent("Scan finished");
    await advance(60000);
    expect(fetchJobs).toHaveBeenCalledTimes(2);
  });

  it("keeps retrying through a failure that is not a refusal", async () => {
    const active = renderHook(() => useAnyRescanActive());
    await act(() => startRescan("library-1", ["/media"]));
    vi.mocked(fetchJobs).mockRejectedValue(new Error("network down"));
    await advance(4500);

    expect(fetchJobs).toHaveBeenCalledTimes(3);
    expect(active.result.current).toBe(true);
  });
});
