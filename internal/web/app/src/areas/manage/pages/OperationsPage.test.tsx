import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchLibraries, fetchSchedules, setScheduleTrigger } from "@/lib/graphql";
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

describe("Schedule times are picked from a list (CORE-2445)", () => {
  it("saves the chosen time of a daily schedule as hour and minute", async () => {
    vi.mocked(fetchSchedules).mockResolvedValue([
      {
        ...schedule("backup", "backup"),
        trigger: { kind: "daily", n: 0, unit: "", weekday: 0, hour: 2, minute: 0 },
      },
    ]);
    vi.mocked(setScheduleTrigger).mockImplementation(async () => schedule("backup", "backup"));
    const user = userEvent.setup();

    render(
      <I18nProvider>
        <MemoryRouter>
          <OperationsPage />
        </MemoryRouter>
      </I18nProvider>,
    );

    const time = await screen.findByRole("combobox", { name: "Time" });
    expect(time).toHaveTextContent("02:00");
    expect(screen.getByRole("button", { name: "Help: Repeat" })).toBeInTheDocument();

    await user.click(time);
    await user.click(await screen.findByRole("option", { name: "04:30" }));
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(setScheduleTrigger).toHaveBeenCalledWith("backup", {
        kind: "daily",
        hour: 4,
        minute: 30,
      }),
    );
  });
});
