import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError, subscribeEvents } from "@/lib/api";
import {
  acceptWatchParty,
  calibrateWatchPartyClock,
  createWatchParty,
  fetchMySessions,
  inviteToWatchParty,
  leaveWatchParty,
  leaveWatchPartyKeepalive,
  requestWatchPartyTransport,
  watchPartyTransport,
} from "@/lib/graphql";
import {
  endParty,
  getWatchParty,
  inviteToParty,
  joinWatchParty,
  leaveParty,
  leavePartyOnPageHide,
  resetWatchPartyForTest,
  sendPartyTransport,
  startWatchParty,
} from "./watchPartyStore";

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return { ...actual, subscribeEvents: vi.fn() };
});

vi.mock("@/lib/graphql", () => ({
  acceptWatchParty: vi.fn(),
  calibrateWatchPartyClock: vi.fn(),
  createWatchParty: vi.fn(),
  fetchMySessions: vi.fn(),
  inviteToWatchParty: vi.fn(),
  leaveWatchParty: vi.fn(),
  leaveWatchPartyKeepalive: vi.fn(),
  requestWatchPartyTransport: vi.fn(),
  watchPartyTransport: vi.fn(),
}));

const unsubscribe = vi.fn();

function deliver(payload: Record<string, unknown>) {
  const onEvent = vi.mocked(subscribeEvents).mock.calls.at(-1)?.[0];
  if (!onEvent) throw new Error("no group.sync stream open");
  onEvent({ type: "group.sync", data: JSON.stringify({ payload }) } as never);
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(subscribeEvents).mockReturnValue(unsubscribe);
  vi.mocked(calibrateWatchPartyClock).mockResolvedValue({ offsetMS: 25, roundTripMS: 4 });
  vi.mocked(leaveWatchParty).mockResolvedValue();
});

afterEach(() => {
  resetWatchPartyForTest();
});

describe("startWatchParty", () => {
  it("hosts a relaxed party held at the current position and opens the stream", async () => {
    vi.mocked(createWatchParty).mockResolvedValue({
      partyID: "p1",
      content: "m1",
      relaxed: true,
      members: [{ displayName: "Host", present: true }],
    });

    expect(await startWatchParty("m1", 4200.4)).toBe(true);

    expect(createWatchParty).toHaveBeenCalledWith({
      content: "m1",
      relaxed: true,
      clockOffsetMS: 25,
      renderLatencyMS: 0,
    });
    expect(getWatchParty()).toMatchObject({
      status: "active",
      partyID: "p1",
      isHost: true,
      offsetMS: 25,
      members: ["Host"],
      timeline: { item: "m1", positionMS: 4200, paused: true },
    });
    expect(subscribeEvents).toHaveBeenCalledTimes(1);
  });

  it("maps a refusal to the home-network error", async () => {
    vi.mocked(createWatchParty).mockRejectedValue(
      new Error("Watch Party is available on your home network only"),
    );

    expect(await startWatchParty("m1")).toBe(false);
    expect(getWatchParty()).toMatchObject({ status: "idle", error: "home-network" });
  });

  it("maps a 403 to the home-network error and anything else to failed", async () => {
    vi.mocked(calibrateWatchPartyClock).mockRejectedValueOnce(new ApiError("forbidden", 403));
    await startWatchParty("m1");
    expect(getWatchParty().error).toBe("home-network");

    vi.mocked(calibrateWatchPartyClock).mockRejectedValueOnce(new Error("boom"));
    await startWatchParty("m1");
    expect(getWatchParty()).toMatchObject({ error: "failed", errorDetail: "boom" });
  });
});

describe("joinWatchParty", () => {
  it("opens the stream before accepting and takes the paused flag from the roster", async () => {
    vi.mocked(acceptWatchParty).mockImplementation(async () => {
      expect(subscribeEvents).toHaveBeenCalledTimes(1);
      // The roster carries the live anchor, as the server sends it.
      deliver({
        groupID: "p1",
        signal: "roster",
        item: "m9",
        positionMS: 3000,
        presentAtMS: 90_000,
        paused: false,
        members: ["Guest", "Host"],
      });
      return { partyID: "p1", item: "m9", positionMS: 3000, presentAtMS: 90_000 };
    });

    expect(await joinWatchParty("p1")).toBe("m9");

    expect(getWatchParty()).toMatchObject({
      status: "active",
      isHost: false,
      members: ["Guest", "Host"],
      timeline: { item: "m9", positionMS: 3000, presentAtMS: 90_000, paused: false },
    });
  });

  it("waits for a late roster before settling the timeline", async () => {
    vi.mocked(acceptWatchParty).mockResolvedValue({
      partyID: "p1",
      item: "m9",
      positionMS: 3000,
      presentAtMS: 90_000,
    });
    const joined = joinWatchParty("p1");
    await vi.waitFor(() => expect(acceptWatchParty).toHaveBeenCalled());
    await Promise.resolve();
    deliver({ groupID: "p1", signal: "roster", paused: true, members: ["Host"] });

    expect(await joined).toBe("m9");
    expect(getWatchParty().timeline?.paused).toBe(true);
  });

  it("ignores frames for another party and follows transitions", async () => {
    vi.mocked(acceptWatchParty).mockImplementation(async () => {
      deliver({ groupID: "p1", signal: "roster", paused: true, members: ["Host"] });
      return { partyID: "p1", item: "m9", positionMS: 0, presentAtMS: 0 };
    });
    await joinWatchParty("p1");

    deliver({ groupID: "other", signal: "transition", item: "x", positionMS: 1, paused: false });
    expect(getWatchParty().timeline?.item).toBe("m9");

    deliver({
      groupID: "p1",
      signal: "transition",
      item: "m10",
      positionMS: 500,
      presentAtMS: 7000,
      paused: false,
    });
    expect(getWatchParty().timeline).toEqual({
      item: "m10",
      positionMS: 500,
      presentAtMS: 7000,
      paused: false,
    });
  });

  it("returns null and closes the stream when the accept fails", async () => {
    vi.mocked(acceptWatchParty).mockRejectedValue(new Error("unknown invite"));

    expect(await joinWatchParty("p1")).toBeNull();
    expect(getWatchParty()).toMatchObject({
      status: "idle",
      error: "failed",
      errorDetail: "unknown invite",
    });
    expect(unsubscribe).toHaveBeenCalled();
  });
});

describe("party actions", () => {
  async function host() {
    vi.mocked(createWatchParty).mockResolvedValue({
      partyID: "p1",
      content: "m1",
      relaxed: true,
      members: [{ displayName: "Host", present: true }],
    });
    await startWatchParty("m1");
  }

  it("lists an invitee until the roster shows them joined", async () => {
    await host();
    vi.mocked(inviteToWatchParty).mockResolvedValue({
      partyID: "p1",
      content: "m1",
      relaxed: true,
      members: [
        { displayName: "Host", present: true },
        { displayName: "Guest", present: false },
      ],
    });

    expect(await inviteToParty(" Guest ")).toBe(true);
    expect(inviteToWatchParty).toHaveBeenCalledWith("p1", "Guest");
    expect(getWatchParty().invited).toEqual(["Guest"]);

    deliver({ groupID: "p1", signal: "roster", paused: true, members: ["Guest", "Host"] });
    expect(getWatchParty()).toMatchObject({ members: ["Guest", "Host"], invited: [] });
  });

  it("sends the host's transport as authoritative and a member's as a request", async () => {
    await host();
    vi.mocked(watchPartyTransport).mockResolvedValue({
      item: "m1",
      positionMS: 6000,
      presentAtMS: 12_000,
    });
    await sendPartyTransport("m1", 6000.2, false);
    expect(watchPartyTransport).toHaveBeenCalledWith({
      partyID: "p1",
      item: "m1",
      positionMS: 6000,
      paused: false,
    });
    expect(getWatchParty().timeline).toEqual({
      item: "m1",
      positionMS: 6000,
      presentAtMS: 12_000,
      paused: false,
    });

    resetWatchPartyForTest();
    vi.mocked(acceptWatchParty).mockImplementation(async () => {
      deliver({ groupID: "p2", signal: "roster", paused: true, members: ["Host"] });
      return { partyID: "p2", item: "m1", positionMS: 0, presentAtMS: 0 };
    });
    await joinWatchParty("p2");
    vi.mocked(requestWatchPartyTransport).mockResolvedValue({ partyID: "p2", approved: true });
    await sendPartyTransport("m1", 100, true);
    expect(requestWatchPartyTransport).toHaveBeenCalledWith({
      partyID: "p2",
      item: "m1",
      positionMS: 100,
      paused: true,
    });
  });

  it("leaves locally first, then tells the server", async () => {
    await host();
    await leaveParty();
    expect(getWatchParty().status).toBe("idle");
    expect(unsubscribe).toHaveBeenCalled();
    expect(leaveWatchParty).toHaveBeenCalledWith("p1");
  });
});

describe("races", () => {
  it("lets a roster that arrives after the join settled move the timeline", async () => {
    vi.useFakeTimers();
    try {
      vi.mocked(acceptWatchParty).mockResolvedValue({
        partyID: "p1",
        item: "m1",
        positionMS: 1000,
        presentAtMS: 5000,
      });
      const joined = joinWatchParty("p1");
      await vi.advanceTimersByTimeAsync(1600);
      expect(await joined).toBe("m1");
      expect(getWatchParty().timeline?.paused).toBe(true);
      deliver({
        groupID: "p1",
        signal: "roster",
        item: "m1",
        positionMS: 1000,
        presentAtMS: 5000,
        paused: false,
        members: ["Guest", "Host"],
      });
      expect(getWatchParty().timeline?.paused).toBe(false);
    } finally {
      vi.useRealTimers();
    }
  });

  it("keeps a playing timeline that a roster only re-states", async () => {
    vi.mocked(acceptWatchParty).mockImplementation(async () => {
      deliver({
        groupID: "p1",
        signal: "transition",
        item: "m1",
        positionMS: 1000,
        presentAtMS: 5000,
        paused: false,
      });
      return { partyID: "p1", item: "m1", positionMS: 1000, presentAtMS: 5000 };
    });
    await joinWatchParty("p1");
    const playing = getWatchParty().timeline;

    // Someone joins at server time 9000: the roster carries the same timeline,
    // anchored afresh 2 s ahead.
    deliver({
      groupID: "p1",
      signal: "roster",
      item: "m1",
      positionMS: 8000,
      presentAtMS: 12_000,
      paused: false,
      members: ["Guest", "Host", "Late"],
    });
    expect(getWatchParty().timeline).toBe(playing);
    expect(getWatchParty().members).toEqual(["Guest", "Host", "Late"]);

    // A roster that disagrees (a pause the stream has not shown) still moves it.
    deliver({
      groupID: "p1",
      signal: "roster",
      item: "m1",
      positionMS: 9000,
      presentAtMS: 13_000,
      paused: true,
      members: ["Guest", "Host", "Late"],
    });
    expect(getWatchParty().timeline).toEqual({
      item: "m1",
      positionMS: 9000,
      presentAtMS: 13_000,
      paused: true,
    });
  });

  it("keeps a transition that arrived before the accept returned", async () => {
    vi.mocked(acceptWatchParty).mockImplementation(async () => {
      deliver({ groupID: "p1", signal: "roster", item: "m1", paused: false, members: ["Host"] });
      deliver({
        groupID: "p1",
        signal: "transition",
        item: "m1",
        positionMS: 90_000,
        presentAtMS: 10_000,
        paused: true,
      });
      return { partyID: "p1", item: "m1", positionMS: 1000, presentAtMS: 9000 };
    });
    await joinWatchParty("p1");
    expect(getWatchParty().timeline).toMatchObject({ positionMS: 90_000, paused: true });
  });

  it("ends a party on the server when the host left while it was being created", async () => {
    let finish!: (info: Awaited<ReturnType<typeof createWatchParty>>) => void;
    vi.mocked(createWatchParty).mockImplementation(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    const starting = startWatchParty("m1");
    await vi.waitFor(() => expect(createWatchParty).toHaveBeenCalled());
    await leaveParty();
    finish({ partyID: "p1", content: "m1", relaxed: true, members: [] });
    expect(await starting).toBe(false);
    expect(getWatchParty().status).toBe("idle");
    expect(leaveWatchParty).toHaveBeenCalledWith("p1");
  });

  it("drops a host transport response that a stream frame overtook", async () => {
    vi.mocked(createWatchParty).mockResolvedValue({
      partyID: "p1",
      content: "m1",
      relaxed: true,
      members: [],
    });
    await startWatchParty("m1");
    vi.mocked(watchPartyTransport).mockImplementation(async () => {
      deliver({
        groupID: "p1",
        signal: "transition",
        item: "m1",
        positionMS: 5000,
        presentAtMS: 10_100,
        paused: true,
      });
      return { item: "m1", positionMS: 5000, presentAtMS: 10_000 };
    });
    await sendPartyTransport("m1", 5000, false);
    expect(getWatchParty().timeline).toMatchObject({ presentAtMS: 10_100, paused: true });
  });
});

describe("host leave, end and page exit (CORE-1763)", () => {
  async function member(partyID = "p3") {
    vi.mocked(acceptWatchParty).mockImplementation(async () => {
      deliver({ groupID: partyID, signal: "roster", paused: true, members: ["Guest", "Host"] });
      return { partyID, item: "m1", positionMS: 0, presentAtMS: 0 };
    });
    await joinWatchParty(partyID);
  }

  function mySession(id: string) {
    vi.mocked(fetchMySessions).mockResolvedValue({
      sessions: [
        {
          id: "other",
          deviceID: "d0",
          deviceName: "",
          deviceRole: "",
          current: false,
          expiresAt: "",
        },
        { id, deviceID: "d1", deviceName: "", deviceRole: "", current: true, expiresAt: "" },
      ],
      streamTokens: [],
    } as never);
  }

  it("tells a member who the host handed the party to, without making it host", async () => {
    await member();
    deliver({
      groupID: "p3",
      signal: "roster",
      paused: true,
      members: ["Guest"],
      handoff: true,
      hostName: "Guest2",
    });
    expect(getWatchParty()).toMatchObject({
      status: "active",
      isHost: false,
      notice: "handoff",
      noticeName: "Guest2",
      members: ["Guest"],
    });
    expect(fetchMySessions).not.toHaveBeenCalled();
  });

  it("makes this viewer host when the hand-off names its own session", async () => {
    mySession("session-guest");
    await member();
    deliver({
      groupID: "p3",
      signal: "roster",
      paused: true,
      members: ["Guest"],
      handoff: true,
      hostName: "Guest",
      host: "session-guest",
    });
    await vi.waitFor(() => expect(getWatchParty().isHost).toBe(true));
    expect(getWatchParty().notice).toBe("handoff");
  });

  it("stays a member when the hand-off names another session of the same Profile", async () => {
    mySession("session-guest");
    await member();
    deliver({
      groupID: "p3",
      signal: "roster",
      paused: true,
      handoff: true,
      hostName: "Guest",
      host: "session-tv",
    });
    await vi.waitFor(() => expect(fetchMySessions).toHaveBeenCalled());
    await Promise.resolve();
    expect(getWatchParty().isHost).toBe(false);
  });

  it("drops the party with a notice when the host ends it", async () => {
    await member();
    deliver({ groupID: "p3", signal: "ended", hostName: "Host" });
    expect(getWatchParty()).toMatchObject({
      status: "idle",
      partyID: "",
      notice: "ended",
      noticeName: "Host",
    });
    expect(unsubscribe).toHaveBeenCalled();
    expect(leaveWatchParty).not.toHaveBeenCalled();
  });

  it("ends the party for everyone only as host", async () => {
    await member();
    await endParty();
    expect(leaveWatchParty).not.toHaveBeenCalled();
    resetWatchPartyForTest();

    vi.mocked(createWatchParty).mockResolvedValue({
      partyID: "p1",
      content: "m1",
      relaxed: true,
      members: [{ displayName: "Host", present: true }],
    });
    await startWatchParty("m1");
    await endParty();
    expect(getWatchParty().status).toBe("idle");
    expect(leaveWatchParty).toHaveBeenCalledWith("p1", true);
  });

  it("leaves with a keepalive request when the page goes away", async () => {
    await member();
    leavePartyOnPageHide();
    expect(leaveWatchPartyKeepalive).toHaveBeenCalledWith("p3");
    expect(getWatchParty().status).toBe("idle");
    leavePartyOnPageHide();
    expect(leaveWatchPartyKeepalive).toHaveBeenCalledTimes(1);
  });
});
