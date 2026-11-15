import "@testing-library/jest-dom/vitest";
import { act, render, renderHook, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api";
import {
  fetchHostMetrics,
  fetchLibraries,
  fetchLiveSessions,
  fetchMySessions,
  fetchPlayHistory,
  fetchPlayStats,
  fetchProfilesAdmin,
  terminateSession,
} from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { formatBitrate, HostGraphs, LiveSessions, useEventRefresh } from "./Dashboard";
import { dayStart, PlayHistory, watched } from "./PlayHistory";

// feature:live-sessions feature:host-metrics feature:play-history

vi.mock("@/lib/graphql", () => ({
  fetchHostMetrics: vi.fn(),
  fetchLibraries: vi.fn(),
  fetchLiveSessions: vi.fn(),
  fetchMySessions: vi.fn(),
  fetchPlayHistory: vi.fn(),
  fetchPlayStats: vi.fn(),
  fetchProfilesAdmin: vi.fn(),
  terminateSession: vi.fn(),
}));
const events = vi.hoisted(() => ({ emit: (_event: unknown) => {} }));
vi.mock("@/lib/api", async (importOriginal) => ({
  ApiError: (await importOriginal<typeof import("@/lib/api")>()).ApiError,
  isAuthRejection: (await importOriginal<typeof import("@/lib/api")>()).isAuthRejection,
  subscribeEvents: (onEvent: (event: unknown) => void) => {
    events.emit = onEvent;
    return () => {
      events.emit = () => {};
    };
  },
}));

function session(over: Record<string, unknown>) {
  return {
    sessionID: "s-1",
    deviceID: "d-1",
    deviceName: "Living room TV",
    profileID: "p-1",
    profileName: "Ada",
    mediaID: "m-1",
    title: "Night Train",
    positionSeconds: 600,
    durationSeconds: 1200,
    transcoding: false,
    playMethod: "direct",
    playReason: "",
    bitrate: 8_200_000,
    ...over,
  };
}

function show(node: React.ReactNode) {
  return render(<I18nProvider>{node}</I18nProvider>);
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(fetchMySessions).mockResolvedValue({ sessions: [], streamTokens: [] });
});

describe("LiveSessions", () => {
  it("shows how each session is delivered, why, its bitrate and its progress", async () => {
    vi.mocked(fetchLiveSessions).mockResolvedValue([
      session({}),
      session({
        sessionID: "s-2",
        profileName: "Grace",
        deviceName: "Phone",
        title: "Harbour",
        transcoding: true,
        playMethod: "transcode",
        playReason: "video-codec",
        bitrate: 640_000,
        positionSeconds: 30,
        durationSeconds: 120,
      }),
    ] as never);
    show(<LiveSessions />);

    const direct = (await screen.findByText("Night Train")).closest("tr") as HTMLElement;
    expect(within(direct).getByText("Ada")).toBeInTheDocument();
    expect(within(direct).getByText("Living room TV")).toBeInTheDocument();
    expect(within(direct).getByText("Direct play")).toBeInTheDocument();
    expect(within(direct).getByText("8.2 Mbps")).toBeInTheDocument();
    expect(within(direct).getByRole("progressbar")).toHaveAttribute("aria-valuenow", "50");
    expect(within(direct).getByText("10:00 / 20:00")).toBeInTheDocument();

    const transcode = screen.getByText("Harbour").closest("tr") as HTMLElement;
    expect(within(transcode).getByText("Transcoding")).toBeInTheDocument();
    expect(within(transcode).getByText(/video format/i)).toBeInTheDocument();
    expect(within(transcode).getByText("640 kbps")).toBeInTheDocument();
  });
});

describe("LiveSessions end session", () => {
  const assign = vi.fn();
  const realLocation = window.location;

  beforeEach(() => {
    Object.defineProperty(window, "location", {
      configurable: true,
      value: { ...realLocation, assign },
    });
    vi.mocked(terminateSession).mockResolvedValue({ sessionID: "x" } as never);
    vi.mocked(fetchMySessions).mockResolvedValue({
      sessions: [
        {
          id: "mine",
          deviceID: "d-1",
          deviceName: "Web Browser",
          deviceRole: "manager",
          current: true,
        },
      ],
      streamTokens: [],
    });
  });

  afterEach(() => {
    Object.defineProperty(window, "location", { configurable: true, value: realLocation });
  });

  async function endSessionOn(title: string) {
    const row = (await screen.findByText(title)).closest("tr") as HTMLElement;
    await userEvent.click(within(row).getByRole("button", { name: "End session" }));
    const dialog = await screen.findByRole("alertdialog");
    return {
      dialog,
      confirm: () => userEvent.click(within(dialog).getByRole("button", { name: "End session" })),
    };
  }

  it("ends another device's session and refreshes the list without signing out", async () => {
    vi.mocked(fetchLiveSessions)
      .mockResolvedValueOnce([
        session({ sessionID: "mine", deviceName: "Web Browser", title: "Mine" }),
        session({ sessionID: "other", deviceName: "Living room TV", title: "Theirs" }),
      ] as never)
      .mockResolvedValue([
        session({ sessionID: "mine", deviceName: "Web Browser", title: "Mine" }),
      ] as never);
    show(<LiveSessions />);

    const { dialog, confirm } = await endSessionOn("Theirs");
    expect(within(dialog).queryByText(/signs this browser out/)).not.toBeInTheDocument();
    await confirm();

    expect(terminateSession).toHaveBeenCalledWith("other");
    await vi.waitFor(() => expect(screen.queryByText("Theirs")).not.toBeInTheDocument());
    expect(screen.getByText("Mine")).toBeInTheDocument();
    expect(assign).not.toHaveBeenCalled();
  });

  it("marks this browser's session, warns, and goes to sign-in after ending it", async () => {
    vi.mocked(fetchLiveSessions).mockResolvedValue([
      session({ sessionID: "mine", deviceName: "Web Browser", title: "Mine" }),
      session({ sessionID: "other", title: "Theirs" }),
    ] as never);
    show(<LiveSessions />);

    const mine = (await screen.findByText("Mine")).closest("tr") as HTMLElement;
    expect(await within(mine).findByText("This browser")).toBeInTheDocument();
    const theirs = screen.getByText("Theirs").closest("tr") as HTMLElement;
    expect(within(theirs).queryByText("This browser")).not.toBeInTheDocument();

    const { dialog, confirm } = await endSessionOn("Mine");
    expect(within(dialog).getByText(/signs this browser out/)).toBeInTheDocument();
    await confirm();

    expect(terminateSession).toHaveBeenCalledWith("mine");
    await vi.waitFor(() => expect(assign).toHaveBeenCalledWith(import.meta.env.BASE_URL));
  });

  it("holds End session until it knows which row is this browser", async () => {
    let identify: (value: Awaited<ReturnType<typeof fetchMySessions>>) => void = () => {};
    vi.mocked(fetchMySessions).mockReturnValue(
      new Promise((resolve) => {
        identify = resolve;
      }),
    );
    vi.mocked(fetchLiveSessions).mockResolvedValue([
      session({ sessionID: "mine", deviceName: "Web Browser", title: "Mine" }),
    ] as never);
    show(<LiveSessions />);

    const row = (await screen.findByText("Mine")).closest("tr") as HTMLElement;
    expect(within(row).getByRole("button", { name: "End session" })).toBeDisabled();

    await act(async () => {
      identify({
        sessions: [
          {
            id: "mine",
            deviceID: "d-1",
            deviceName: "Web Browser",
            deviceRole: "manager",
            current: true,
          },
        ],
        streamTokens: [],
      });
    });
    expect(within(row).getByRole("button", { name: "End session" })).toBeEnabled();
    expect(within(row).getByText("This browser")).toBeInTheDocument();
  });

  it("warns on every row when it cannot tell which one is this browser", async () => {
    vi.mocked(fetchMySessions).mockRejectedValue(new Error("unavailable"));
    vi.mocked(fetchLiveSessions).mockResolvedValue([
      session({ sessionID: "s-3", title: "Either" }),
    ] as never);
    show(<LiveSessions />);

    const row = (await screen.findByText("Either")).closest("tr") as HTMLElement;
    await vi.waitFor(() =>
      expect(within(row).getByRole("button", { name: "End session" })).toBeEnabled(),
    );
    const { dialog } = await endSessionOn("Either");
    expect(within(dialog).getByText(/If this is this browser's session/)).toBeInTheDocument();
  });

  it("goes to sign-in when the list comes back unauthorized after ending a session", async () => {
    vi.mocked(fetchMySessions).mockResolvedValue({ sessions: [], streamTokens: [] });
    vi.mocked(fetchLiveSessions)
      .mockResolvedValueOnce([session({ sessionID: "s-9", title: "Unknown" })] as never)
      .mockRejectedValue(new ApiError("invalid session", 401));
    show(<LiveSessions />);

    const { confirm } = await endSessionOn("Unknown");
    await confirm();

    await vi.waitFor(() => expect(assign).toHaveBeenCalledWith(import.meta.env.BASE_URL));
  });
});

describe("LiveSessions bitrate reasons (CORE-1854)", () => {
  it("tells a client bitrate limit apart from the admin's remote cap", async () => {
    vi.mocked(fetchLiveSessions).mockResolvedValue([
      session({ transcoding: true, playMethod: "transcode", playReason: "bitrate" }),
      session({
        sessionID: "s-2",
        title: "Harbour",
        transcoding: true,
        playMethod: "transcode",
        playReason: "policy-bitrate",
      }),
    ] as never);
    show(<LiveSessions />);

    const client = (await screen.findByText("Night Train")).closest("tr") as HTMLElement;
    expect(within(client).getByText("Bitrate above the device limit")).toBeInTheDocument();
    expect(within(client).queryByText(/remote cap/i)).not.toBeInTheDocument();

    const policy = screen.getByText("Harbour").closest("tr") as HTMLElement;
    expect(within(policy).getByText("Bitrate above the server's remote cap")).toBeInTheDocument();
    expect(within(policy).queryByText("Bitrate above the device limit")).not.toBeInTheDocument();
  });
});

describe("useEventRefresh", () => {
  it("gathers a burst of events into one read and never overlaps reads", async () => {
    vi.useFakeTimers();
    try {
      let finish = () => {};
      const refresh = vi.fn(
        () =>
          new Promise<void>((resolve) => {
            finish = resolve;
          }),
      );
      const { unmount } = renderHook(() => useEventRefresh(refresh));

      for (let i = 0; i < 50; i++) events.emit({});
      await act(() => vi.advanceTimersByTimeAsync(500));
      expect(refresh).toHaveBeenCalledTimes(1);

      // Events while that read is in flight wait for it, then read once more.
      for (let i = 0; i < 50; i++) {
        events.emit({});
        await act(() => vi.advanceTimersByTimeAsync(500));
      }
      expect(refresh).toHaveBeenCalledTimes(1);
      await act(async () => finish());
      await act(() => vi.advanceTimersByTimeAsync(500));
      expect(refresh).toHaveBeenCalledTimes(2);

      unmount();
      events.emit({});
      await act(() => vi.advanceTimersByTimeAsync(500));
      expect(refresh).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });
});

describe("HostGraphs", () => {
  it("graphs CPU, memory and network with a table view of the same data", async () => {
    const now = Date.now();
    vi.mocked(fetchHostMetrics).mockResolvedValue({
      intervalSeconds: 10,
      samples: [0, 1, 2].map((i) => ({
        at: new Date(now - (2 - i) * 10 * 60 * 1000).toISOString(),
        cpuPercent: 10 + i * 10,
        cpuKnown: true,
        memoryUsedBytes: 4 * 1024 ** 3,
        memoryTotalBytes: 16 * 1024 ** 3,
        sentBytesPerSecond: 1_000_000,
        receivedBytesPerSecond: 125_000,
      })),
    } as never);
    show(<HostGraphs />);

    expect(await screen.findByRole("img", { name: "CPU" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "Memory" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "Network" })).toBeInTheDocument();
    expect(screen.getAllByText("30%").length).toBeGreaterThan(0);
    expect(screen.getByText("4.0 GB of 16.0 GB")).toBeInTheDocument();
    expect(screen.getByText("↑ 8.0 Mbps · ↓ 1.0 Mbps")).toBeInTheDocument();
    // Two series: a legend names both, and each chart carries a table.
    expect(screen.getAllByText("Sent").length).toBeGreaterThan(1);
    expect(screen.getAllByRole("table")).toHaveLength(3);
  });
});

describe("PlayHistory", () => {
  it("lists plays and re-ranks when the window changes", async () => {
    vi.mocked(fetchProfilesAdmin).mockResolvedValue([] as never);
    vi.mocked(fetchLibraries).mockResolvedValue([] as never);
    vi.mocked(fetchPlayHistory).mockResolvedValue([
      {
        profileID: "p-1",
        profileName: "Ada",
        mediaID: "m-1",
        title: "Night Train",
        libraryID: "l-1",
        libraryTitle: "Films",
        startedAt: "2026-11-10T20:00:00Z",
        watchedSeconds: 7500,
        finished: true,
      },
    ] as never);
    vi.mocked(fetchPlayStats).mockResolvedValue({
      windowDays: 30,
      profiles: [{ profileID: "p-1", profileName: "Ada", plays: 3, watchedSeconds: 7500 }],
      items: [{ mediaID: "m-1", title: "Night Train", plays: 3, watchedSeconds: 7500 }],
    } as never);
    show(<PlayHistory />);

    expect(await screen.findByText("Films")).toBeInTheDocument();
    expect(screen.getByText("Finished")).toBeInTheDocument();
    expect(await screen.findAllByText("3 plays · 2 h 05 min")).toHaveLength(2);
    expect(vi.mocked(fetchPlayStats).mock.calls[0][0]).toBe(30);

    await userEvent.click(screen.getByRole("button", { name: "7 days" }));
    expect(vi.mocked(fetchPlayStats).mock.calls.at(-1)?.[0]).toBe(7);
    expect(screen.getByRole("button", { name: "7 days" })).toHaveAttribute("aria-pressed", "true");
  });
});

describe("formatting", () => {
  it("renders rates, durations and day bounds", () => {
    expect(formatBitrate(120_000_000)).toBe("120 Mbps");
    expect(formatBitrate(900)).toBe("900 bps");
    expect(watched(40)).toBe("40 s");
    expect(watched(840)).toBe("14 min");
    expect(dayStart("")).toBeUndefined();
    expect(dayStart("2026-11-10", 1)).toBe(new Date(2026, 10, 11).toISOString());
  });
});
