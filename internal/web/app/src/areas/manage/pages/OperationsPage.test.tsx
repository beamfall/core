import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchLibraries, fetchSchedules } from "@/lib/graphql";
import type { LibraryInfo, ScheduleInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { OperationsPage, scheduleLibraryID, scheduleLibraryLabel } from "./OperationsPage";

vi.mock("@/lib/graphql", () => ({
  fetchJobs: vi.fn().mockResolvedValue([]),
  fetchLibraries: vi.fn(),
  fetchSchedules: vi.fn(),
  fetchWatcherStatus: vi.fn().mockResolvedValue([]),
  setScheduleEnabled: vi.fn(),
  setScheduleTrigger: vi.fn(),
}));

function schedule(id: string, jobKind: string): ScheduleInfo {
  return {
    id,
    jobKind,
    enabled: false,
    orphaned: false,
    nextDue: "",
    lastEnqueued: "",
    trigger: { kind: "every", n: 1, unit: "hours" },
  } as unknown as ScheduleInfo;
}

beforeEach(() => {
  vi.mocked(fetchLibraries)
    .mockReset()
    .mockResolvedValue([{ id: "lib-a", title: "Films" }] as LibraryInfo[]);
  vi.mocked(fetchSchedules).mockReset();
});

afterEach(cleanup);

describe("Schedules name the Library of a per-Library schedule (CORE-2326)", () => {
  it("reads the Library ID from scan and playback precompute schedule IDs", () => {
    expect(scheduleLibraryID("scan:lib-a")).toBe("lib-a");
    expect(scheduleLibraryID("playback-precompute:lib-b")).toBe("lib-b");
    expect(scheduleLibraryID("watcher-poll")).toBeNull();
    expect(scheduleLibraryID("scan:")).toBeNull();
  });

  it("uses the Library title, else the raw schedule ID", () => {
    const titles = new Map([["lib-a", "Films"]]);
    expect(scheduleLibraryLabel("scan:lib-a", titles)).toBe("Films");
    expect(scheduleLibraryLabel("playback-precompute:gone", titles)).toBe(
      "playback-precompute:gone",
    );
    expect(scheduleLibraryLabel("backup", titles)).toBeNull();
  });

  it("shows each per-Library schedule's Library beside its kind", async () => {
    vi.mocked(fetchSchedules).mockResolvedValue([
      schedule("scan:lib-a", "scan"),
      schedule("playback-precompute:lib-gone", "playback-precompute"),
      schedule("backup", "backup"),
    ]);

    render(
      <I18nProvider>
        <MemoryRouter>
          <OperationsPage />
        </MemoryRouter>
      </I18nProvider>,
    );

    expect(await screen.findByText("Films")).toBeInTheDocument();
    expect(screen.getByText("playback-precompute:lib-gone")).toBeInTheDocument();
  });

  it("still lists schedules when the Libraries cannot load", async () => {
    vi.mocked(fetchLibraries).mockRejectedValue(new Error("offline"));
    vi.mocked(fetchSchedules).mockResolvedValue([schedule("scan:lib-a", "scan")]);

    render(
      <I18nProvider>
        <MemoryRouter>
          <OperationsPage />
        </MemoryRouter>
      </I18nProvider>,
    );

    expect(await screen.findByText("scan:lib-a")).toBeInTheDocument();
  });
});
