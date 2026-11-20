import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError, setIdentityEpochSignal, subscribeEvents } from "@/lib/api";
import {
  acceptWatchParty,
  calibrateWatchPartyClock,
  createWatchParty,
  decideWatchPartyRequest,
  fetchMySessions,
  inviteToWatchParty,
  leaveWatchParty,
  leaveWatchPartyKeepalive,
  reportWatchPartyDrift,
  reportWatchPartyLeadPosition,
  requestWatchPartyTransport,
  watchPartyTransport,
} from "@/lib/graphql";
import {
  CLOCK_REFRESH_INTERVAL_MS,
  DRIFT_REPORT_INTERVAL_MS,
  decidePartyRequest,
  endParty,
  followLeadParty,
  getWatchParty,
  inviteToParty,
  joinWatchParty,
  leaveParty,
  leavePartyOnPageHide,
  MAX_PARTY_REQUESTS,
  partyNowMS,
  reportLeadPosition,
  reportPartyDrift,
  resetWatchPartyForTest,
  resumeFollowedParty,
  sendPartyTransport,
  startWatchParty,
  watchForLeadParty,
  watchPartyDefersUpNext,
} from "./watchPartyStore";
import { localClockMS } from "./watchPartySync";

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return { ...actual, subscribeEvents: vi.fn() };
});

vi.mock("@/lib/graphql", () => ({
  acceptWatchParty: vi.fn(),
  calibrateWatchPartyClock: vi.fn(),
  createWatchParty: vi.fn(),
  decideWatchPartyRequest: vi.fn(),
  fetchMySessions: vi.fn(),
  inviteToWatchParty: vi.fn(),
  leaveWatchParty: vi.fn(),
  leaveWatchPartyKeepalive: vi.fn(),
  reportWatchPartyDrift: vi.fn(),
  reportWatchPartyLeadPosition: vi.fn(),
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
  vi.mocked(reportWatchPartyDrift).mockResolvedValue({ groupID: "", action: "none", rate: 0 });
  vi.mocked(reportWatchPartyLeadPosition).mockResolvedValue({
    groupID: "",
    action: "anchor",
    rate: 0,
  });
});

afterEach(() => {
  resetWatchPartyForTest();
  setIdentityEpochSignal();
  window.sessionStorage.clear();
});

describe("startWatchParty", () => {
  it("sets the server anchor before exposing a party at the viewer's saved position (CORE-1858)", async () => {
    vi.mocked(createWatchParty).mockResolvedValue({
      partyID: "p1",
      content: "m1",
      relaxed: true,
      members: [{ displayName: "Host", present: true }],
    });
    vi.mocked(watchPartyTransport).mockImplementationOnce(async ({ item, positionMS }) => {
      expect(getWatchParty().status).toBe("starting");
      expect(subscribeEvents).not.toHaveBeenCalled();
      return { item, positionMS, presentAtMS: 1000 };
    });

    expect(await startWatchParty("m1", 4200.4)).toBe(true);

    expect(createWatchParty).toHaveBeenCalledWith({
      content: "m1",
      relaxed: true,
      clockOffsetMS: 25,
      renderLatencyMS: 0,
    });
    expect(watchPartyTransport).toHaveBeenCalledExactlyOnceWith({
      partyID: "p1",
      item: "m1",
      positionMS: 4200,
      paused: true,
    });
    expect(getWatchParty()).toMatchObject({
      status: "active",
      partyID: "p1",
      isHost: true,
      offsetMS: 25,
      members: ["Host"],
      timeline: { item: "m1", positionMS: 4200, presentAtMS: 1000, paused: true },
    });
    expect(subscribeEvents).toHaveBeenCalledTimes(1);
  });

  it("sends the current position and playing state in the startup sequence (CORE-1858)", async () => {
    vi.mocked(createWatchParty).mockResolvedValue({
      partyID: "p1",
      content: "m1",
      relaxed: true,
      members: [],
    });
    vi.mocked(watchPartyTransport).mockImplementationOnce(async ({ item, positionMS }) => ({
      item,
      positionMS,
      presentAtMS: 1000,
    }));
    expect(await startWatchParty("m1", 42_500, true, false, false)).toBe(true);
    expect(watchPartyTransport).toHaveBeenCalledExactlyOnceWith({
      partyID: "p1",
      item: "m1",
      positionMS: 42_500,
      paused: false,
    });
    expect(getWatchParty().timeline).toEqual({
      item: "m1",
      positionMS: 42_500,
      presentAtMS: 1000,
      paused: false,
    });

    // A second tab gets the same initial server anchor on accept.
    const anchor = await vi.mocked(watchPartyTransport).mock.results[0]?.value;
    if (!anchor) throw new Error("missing startup transport");
    resetWatchPartyForTest();
    vi.mocked(acceptWatchParty).mockImplementationOnce(async () => {
      deliver({ groupID: "p1", signal: "roster", ...anchor, paused: false });
      return { partyID: "p1", ...anchor };
    });
    expect(await joinWatchParty("p1")).toBe("m1");
    expect(getWatchParty().timeline).toEqual({ ...anchor, paused: false });
  });

  it("leaves the server party when its initial transport fails (CORE-1858)", async () => {
    vi.mocked(createWatchParty).mockResolvedValue({
      partyID: "p1",
      content: "m1",
      relaxed: true,
      members: [],
    });
    vi.mocked(watchPartyTransport).mockRejectedValueOnce(new Error("offline"));
    expect(await startWatchParty("m1", 42_500)).toBe(false);
    expect(leaveWatchParty).toHaveBeenCalledExactlyOnceWith("p1");
    expect(getWatchParty()).toMatchObject({ status: "idle", error: "failed" });
    expect(subscribeEvents).not.toHaveBeenCalled();
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
    window.dispatchEvent(new Event("pagehide"));
    expect(leaveWatchPartyKeepalive).toHaveBeenCalledWith("p3");
    expect(getWatchParty().status).toBe("idle");
    window.dispatchEvent(new Event("pagehide"));
    expect(leaveWatchPartyKeepalive).toHaveBeenCalledTimes(1);
  });

  it("clears membership and the event stream when the signed-in identity ends", async () => {
    const epoch = new AbortController();
    setIdentityEpochSignal(epoch.signal);
    await member();
    epoch.abort();
    expect(leaveWatchPartyKeepalive).toHaveBeenCalledExactlyOnceWith("p3");
    expect(unsubscribe).toHaveBeenCalledTimes(1);
    expect(getWatchParty()).toMatchObject({ status: "idle", partyID: "", timeline: null });
  });

  it("leaves a starting party on pagehide even if its initial transport is still pending", async () => {
    vi.mocked(createWatchParty).mockResolvedValueOnce({
      partyID: "p1",
      content: "m1",
      relaxed: true,
      members: [],
    });
    let finish!: (anchor: Awaited<ReturnType<typeof watchPartyTransport>>) => void;
    vi.mocked(watchPartyTransport).mockReturnValueOnce(
      new Promise((resolve) => (finish = resolve)),
    );
    const starting = startWatchParty("m1", 42_500);
    await vi.waitFor(() => expect(watchPartyTransport).toHaveBeenCalled());
    window.dispatchEvent(new Event("pagehide"));
    expect(leaveWatchPartyKeepalive).toHaveBeenCalledExactlyOnceWith("p1");

    finish({ item: "m1", positionMS: 42_500, presentAtMS: 1000 });
    expect(await starting).toBe(false);
    expect(leaveWatchParty).not.toHaveBeenCalled();
    expect(subscribeEvents).not.toHaveBeenCalled();
    expect(getWatchParty()).toMatchObject({ status: "idle", partyID: "", timeline: null });
  });
});

describe("host-only requests (CORE-1765)", () => {
  async function strictHost() {
    vi.mocked(createWatchParty).mockResolvedValue({
      partyID: "p5",
      content: "m1",
      relaxed: false,
      members: [{ displayName: "Host", present: true }],
    });
    await startWatchParty("m1", 0, false);
  }

  function request(id: string, extra: Record<string, unknown> = {}) {
    deliver({
      groupID: "p5",
      signal: "request",
      request: id,
      from: "Guest",
      item: "m1",
      positionMS: 42000,
      paused: true,
      ...extra,
    });
  }

  it("starts host-only when asked", async () => {
    await strictHost();
    expect(createWatchParty).toHaveBeenCalledWith(expect.objectContaining({ relaxed: false }));
  });

  it("lists a request on the host without moving the timeline", async () => {
    await strictHost();
    const before = getWatchParty().timeline;
    request("r1");
    expect(getWatchParty().requests).toEqual([
      { id: "r1", from: "Guest", item: "m1", positionMS: 42000, paused: true },
    ]);
    expect(getWatchParty().timeline).toBe(before);
  });

  it("replaces a repeated request id and keeps only the newest few", async () => {
    await strictHost();
    request("r1");
    request("r1", { positionMS: 1000 });
    expect(getWatchParty().requests).toHaveLength(1);
    expect(getWatchParty().requests[0].positionMS).toBe(1000);
    for (let i = 0; i < MAX_PARTY_REQUESTS + 3; i++) request(`n${i}`);
    const ids = getWatchParty().requests.map((r) => r.id);
    expect(ids).toHaveLength(MAX_PARTY_REQUESTS);
    expect(ids.at(-1)).toBe(`n${MAX_PARTY_REQUESTS + 2}`);
  });

  it("ignores requests on a member's player", async () => {
    vi.mocked(acceptWatchParty).mockImplementation(async () => {
      deliver({ groupID: "p5", signal: "roster", paused: true, members: ["Guest", "Host"] });
      return { partyID: "p5", item: "m1", positionMS: 0, presentAtMS: 0 };
    });
    await joinWatchParty("p5");
    request("r1");
    expect(getWatchParty().requests).toEqual([]);
  });

  it("approves or declines through the server and drops the request", async () => {
    await strictHost();
    vi.mocked(decideWatchPartyRequest).mockResolvedValue({
      item: "m1",
      positionMS: 42000,
      presentAtMS: 0,
    });
    request("r1");
    request("r2");
    expect(await decidePartyRequest("r1", true)).toBe(true);
    expect(decideWatchPartyRequest).toHaveBeenCalledWith("p5", "r1", true);
    expect(await decidePartyRequest("r2", false)).toBe(true);
    expect(decideWatchPartyRequest).toHaveBeenCalledWith("p5", "r2", false);
    expect(getWatchParty().requests).toEqual([]);
  });

  it("reports a refused decision", async () => {
    await strictHost();
    vi.mocked(decideWatchPartyRequest).mockRejectedValue(new Error("unknown request"));
    request("r1");
    expect(await decidePartyRequest("r1", true)).toBe(false);
    expect(getWatchParty()).toMatchObject({ error: "failed", errorDetail: "unknown request" });
  });

  it("drops pending requests when the host role moves", async () => {
    await strictHost();
    request("r1");
    deliver({
      groupID: "p5",
      signal: "roster",
      paused: true,
      members: ["Guest"],
      handoff: true,
      hostName: "Guest",
    });
    expect(getWatchParty()).toMatchObject({ isHost: false, requests: [] });
    expect(await decidePartyRequest("r1", true)).toBe(false);
  });
});

describe("drift reporting and Up Next deferral (CORE-1766)", () => {
  async function member(partyID = "p6") {
    vi.mocked(acceptWatchParty).mockImplementation(async () => {
      deliver({ groupID: partyID, signal: "roster", paused: true, members: ["Guest", "Host"] });
      return { partyID, item: "m1", positionMS: 0, presentAtMS: 0 };
    });
    await joinWatchParty(partyID);
  }

  it("reports at most once per interval, whatever the sync rate", async () => {
    await member();
    const start = 1_000_000;
    // The player measures every 250 ms; only the first of each interval is sent.
    let sent = 0;
    for (let t = 0; t < 3 * DRIFT_REPORT_INTERVAL_MS; t += 250) {
      if (reportPartyDrift(40 + t / 1000, start + t)) sent++;
    }
    expect(sent).toBe(3);
    expect(reportWatchPartyDrift).toHaveBeenCalledTimes(3);
    expect(reportWatchPartyDrift).toHaveBeenNthCalledWith(1, "p6", 40);
    expect(reportPartyDrift(12, start + 3 * DRIFT_REPORT_INTERVAL_MS)).toBe(true);
  });

  it("never reports outside a party, and a failed report is not an error", async () => {
    expect(reportPartyDrift(80, 0)).toBe(false);
    expect(reportWatchPartyDrift).not.toHaveBeenCalled();

    await member();
    vi.mocked(reportWatchPartyDrift).mockRejectedValueOnce(new Error("offline"));
    expect(reportPartyDrift(80, 0)).toBe(true);
    await Promise.resolve();
    await Promise.resolve();
    expect(getWatchParty().error).toBe("");
    expect(reportPartyDrift(Number.NaN, DRIFT_REPORT_INTERVAL_MS)).toBe(false);
  });

  it("starts a new interval after leaving and rejoining", async () => {
    await member();
    expect(reportPartyDrift(30, 0)).toBe(true);
    await leaveParty();
    await member("p7");
    expect(reportPartyDrift(30, 1)).toBe(true);
    expect(reportWatchPartyDrift).toHaveBeenLastCalledWith("p7", 30);
  });

  it("defers Up Next on a member's player only", async () => {
    expect(watchPartyDefersUpNext(getWatchParty())).toBe(false);
    await member();
    expect(watchPartyDefersUpNext(getWatchParty())).toBe(true);
    expect(watchPartyDefersUpNext({ ...getWatchParty(), isHost: true })).toBe(false);
  });
});

describe("lead party (CORE-2245)", () => {
  const FOLLOWING_KEY = "beamfall.watchParty.following";
  const leadFrame = (extra: Record<string, unknown> = {}) => ({
    groupID: "p1",
    signal: "transition",
    item: "m1",
    positionMS: 5000,
    presentAtMS: 10_000,
    paused: false,
    lead: true,
    ...extra,
  });

  // The roster an accept triggers: it re-states the party the lead is running.
  const rosterFrame = () => ({
    groupID: "p1",
    signal: "roster",
    item: "m1",
    positionMS: 5000,
    presentAtMS: 10_000,
    paused: false,
    members: ["Ada", "Ada"],
  });

  async function lead() {
    vi.mocked(createWatchParty).mockResolvedValue({
      partyID: "p1",
      content: "m1",
      relaxed: false,
      members: [{ displayName: "Ada", present: true }],
    });
    expect(await startWatchParty("m1", 0, true, true)).toBe(true);
  }

  async function follow(options?: { lead?: boolean; quiet?: boolean }) {
    vi.mocked(acceptWatchParty).mockResolvedValue({
      partyID: "p1",
      item: "m1",
      positionMS: 0,
      presentAtMS: 0,
    });
    const joined = joinWatchParty("p1", options);
    await vi.waitFor(() => expect(acceptWatchParty).toHaveBeenCalled());
    deliver({ groupID: "p1", signal: "roster", item: "m1", paused: true, members: ["Ada", "Ada"] });
    expect(await joined).toBe("m1");
  }

  it("starts host-only with this session as the lead", async () => {
    await lead();

    expect(createWatchParty).toHaveBeenCalledWith({
      content: "m1",
      relaxed: false,
      clockOffsetMS: 25,
      renderLatencyMS: 0,
      lead: true,
    });
    expect(getWatchParty()).toMatchObject({
      status: "active",
      isHost: true,
      lead: true,
      timeline: { item: "m1", paused: true, lead: true },
    });
    expect(window.sessionStorage.getItem(FOLLOWING_KEY)).toBeNull();
  });

  it("learns it follows a lead from the first lead timeline, and keeps its rate", async () => {
    await follow();
    expect(getWatchParty().lead).toBe(false);

    deliver(leadFrame({ ratePermille: 1250 }));

    expect(getWatchParty()).toMatchObject({
      lead: true,
      isHost: false,
      timeline: {
        item: "m1",
        positionMS: 5000,
        presentAtMS: 10_000,
        lead: true,
        ratePermille: 1250,
      },
    });
    expect(window.sessionStorage.getItem(FOLLOWING_KEY)).toBe("p1");
  });

  it("keeps the lead's running timeline when a roster re-states the party", async () => {
    await follow({ lead: true });
    deliver(leadFrame({ ratePermille: 1250 }));
    const running = getWatchParty().timeline;

    deliver({
      groupID: "p1",
      signal: "roster",
      item: "m1",
      positionMS: 99_000,
      presentAtMS: 99_000,
      paused: false,
      members: ["Ada", "Ada", "Ada"],
    });

    expect(getWatchParty().timeline).toBe(running);
    expect(getWatchParty().members).toEqual(["Ada", "Ada", "Ada"]);

    // A roster that pauses the party is a new timeline, at the speed it states.
    deliver({
      groupID: "p1",
      signal: "roster",
      item: "m1",
      positionMS: 7000,
      presentAtMS: 12_000,
      paused: true,
      lead: true,
      ratePermille: 1250,
      members: ["Ada", "Ada", "Ada"],
    });
    expect(getWatchParty().timeline).toEqual({
      item: "m1",
      positionMS: 7000,
      presentAtMS: 12_000,
      paused: true,
      lead: true,
      ratePermille: 1250,
    });
  });

  it("sends no transport and no drift report from a follower", async () => {
    await follow({ lead: true });

    await sendPartyTransport("m1", 1000, false);
    expect(reportPartyDrift(80, 1_000_000)).toBe(false);

    expect(requestWatchPartyTransport).not.toHaveBeenCalled();
    expect(watchPartyTransport).not.toHaveBeenCalled();
    expect(reportWatchPartyDrift).not.toHaveBeenCalled();
    expect(getWatchParty().error).toBe("");
  });

  it("keeps the lead's transport authoritative and marked as the lead timeline", async () => {
    await lead();
    vi.mocked(watchPartyTransport).mockResolvedValue({
      item: "m1",
      positionMS: 3000,
      presentAtMS: 20_000,
    });

    await sendPartyTransport("m1", 3000, false);

    expect(getWatchParty().timeline).toEqual({
      item: "m1",
      positionMS: 3000,
      presentAtMS: 20_000,
      paused: false,
      lead: true,
    });
  });

  it("reports the lead's position on the server clock, and only from the lead", async () => {
    expect(reportLeadPosition(1000, 50_000, 1)).toBe(false);
    await lead();

    expect(reportLeadPosition(1234.5, 50_000, 1.25)).toBe(true);
    expect(reportLeadPosition(Number.NaN, 50_000, 1)).toBe(false);

    expect(reportWatchPartyLeadPosition).toHaveBeenCalledTimes(1);
    expect(reportWatchPartyLeadPosition).toHaveBeenCalledWith("p1", {
      positionMS: 1234.5,
      atMS: 50_025,
      ratePermille: 1250,
    });
    expect(reportPartyDrift(0, 1_000_000)).toBe(false);
  });

  it("does not report a position from a follower", async () => {
    await follow({ lead: true });

    expect(reportLeadPosition(1000, 50_000, 1)).toBe(false);
    expect(reportWatchPartyLeadPosition).not.toHaveBeenCalled();
  });

  it("reads the server clock through the party's offset", async () => {
    await lead();

    const before = localClockMS();
    const now = partyNowMS();
    expect(now - before).toBeGreaterThanOrEqual(25);
    expect(now - localClockMS()).toBeLessThanOrEqual(25);
  });

  it("remembers the party it follows across a page exit and rejoins it", async () => {
    await follow({ lead: true });
    expect(window.sessionStorage.getItem(FOLLOWING_KEY)).toBe("p1");

    leavePartyOnPageHide();
    expect(getWatchParty().status).toBe("idle");
    expect(window.sessionStorage.getItem(FOLLOWING_KEY)).toBe("p1");

    vi.mocked(acceptWatchParty).mockClear();
    const resumed = resumeFollowedParty();
    await vi.waitFor(() => expect(acceptWatchParty).toHaveBeenCalled());
    expect(getWatchParty()).toMatchObject({ status: "joining", partyID: "p1", lead: true });
    deliver(leadFrame());
    deliver(rosterFrame());
    expect(await resumed).toBe("m1");
    expect(getWatchParty()).toMatchObject({ status: "active", lead: true, isHost: false });
  });

  it("forgets the party when the rejoin is refused, without showing an error", async () => {
    window.sessionStorage.setItem(FOLLOWING_KEY, "p1");
    vi.mocked(acceptWatchParty).mockRejectedValue(new Error("unknown invite"));

    expect(await resumeFollowedParty()).toBeNull();

    expect(getWatchParty()).toMatchObject({ status: "idle", error: "", errorDetail: "" });
    expect(window.sessionStorage.getItem(FOLLOWING_KEY)).toBeNull();
    expect(await resumeFollowedParty()).toBeNull();
    expect(acceptWatchParty).toHaveBeenCalledTimes(1);
  });

  it("forgets the party when the lead ends it or this screen leaves it", async () => {
    await follow({ lead: true });
    deliver({ groupID: "p1", signal: "ended", hostName: "Ada" });
    expect(getWatchParty()).toMatchObject({ status: "idle", notice: "ended", noticeName: "Ada" });
    expect(window.sessionStorage.getItem(FOLLOWING_KEY)).toBeNull();

    vi.mocked(acceptWatchParty).mockClear();
    await follow({ lead: true });
    expect(window.sessionStorage.getItem(FOLLOWING_KEY)).toBe("p1");
    await leaveParty();
    expect(window.sessionStorage.getItem(FOLLOWING_KEY)).toBeNull();
  });

  it("does nothing on resume without a remembered party", async () => {
    expect(await resumeFollowedParty()).toBeNull();
    expect(acceptWatchParty).not.toHaveBeenCalled();
    expect(subscribeEvents).not.toHaveBeenCalled();
  });

  describe("offers", () => {
    it("offers a lead party seen while a player listens, and drops it when it ends", () => {
      const stop = watchForLeadParty();
      expect(subscribeEvents).toHaveBeenCalledTimes(1);

      deliver({ groupID: "p1", signal: "roster", item: "m1", members: ["Ada"] });
      deliver({ ...leadFrame(), lead: undefined });
      expect(getWatchParty().leadOffer).toBeNull();

      deliver(leadFrame());
      expect(getWatchParty().leadOffer).toEqual({ partyID: "p1", item: "m1", declined: false });
      const offer = getWatchParty().leadOffer;
      deliver(leadFrame({ positionMS: 6000 }));
      expect(getWatchParty().leadOffer).toBe(offer);

      deliver({ groupID: "p1", signal: "ended" });
      expect(getWatchParty().leadOffer).toBeNull();

      stop();
      stop();
      expect(unsubscribe).toHaveBeenCalledTimes(1);
    });

    it("offers nothing when no player listens", async () => {
      await lead();
      deliver(leadFrame({ groupID: "p2" }));
      expect(getWatchParty().leadOffer).toBeNull();
    });

    it("never offers another party, or its own, to a session that leads", async () => {
      const stop = watchForLeadParty();
      await lead();
      deliver(leadFrame({ groupID: "p2" }));
      expect(getWatchParty().leadOffer).toBeNull();

      await endParty();
      deliver(leadFrame());
      expect(getWatchParty().leadOffer).toBeNull();
      // The player still listens, so the stream outlives the party.
      expect(unsubscribe).not.toHaveBeenCalled();

      stop();
      expect(unsubscribe).toHaveBeenCalledTimes(1);
    });

    it("follows the offered party as a follower from the first frame", async () => {
      watchForLeadParty();
      deliver(leadFrame());
      vi.mocked(acceptWatchParty).mockResolvedValue({
        partyID: "p1",
        item: "m1",
        positionMS: 0,
        presentAtMS: 0,
      });

      const joined = followLeadParty();
      expect(getWatchParty()).toMatchObject({ status: "joining", lead: true, leadOffer: null });
      await vi.waitFor(() => expect(acceptWatchParty).toHaveBeenCalled());
      deliver(leadFrame());
      deliver(rosterFrame());

      expect(await joined).toBe("m1");
      expect(getWatchParty()).toMatchObject({
        status: "active",
        lead: true,
        isHost: false,
        timeline: { lead: true, paused: false },
      });
      expect(subscribeEvents).toHaveBeenCalledTimes(1);
    });

    it("marks a party this screen left or could not join, so it is only offered", async () => {
      watchForLeadParty();
      await follow({ lead: true });
      await leaveParty();
      deliver(leadFrame());
      expect(getWatchParty().leadOffer).toEqual({ partyID: "p1", item: "m1", declined: true });

      vi.mocked(acceptWatchParty).mockRejectedValue(new Error("unknown invite"));
      deliver(leadFrame({ groupID: "p2" }));
      expect(getWatchParty().leadOffer).toEqual({ partyID: "p2", item: "m1", declined: false });
      expect(await followLeadParty("p2", true)).toBeNull();
      expect(getWatchParty()).toMatchObject({ status: "idle", error: "" });
      deliver(leadFrame({ groupID: "p2" }));
      expect(getWatchParty().leadOffer).toEqual({ partyID: "p2", item: "m1", declined: true });
    });

    it("resolves to null when there is nothing to follow", async () => {
      expect(await followLeadParty()).toBeNull();
      expect(acceptWatchParty).not.toHaveBeenCalled();
    });
  });

  describe("clock (CORE-2244)", () => {
    beforeEach(() => {
      vi.useFakeTimers();
    });
    afterEach(() => {
      vi.useRealTimers();
    });

    it("re-estimates on an interval and keeps the better estimate", async () => {
      await lead();
      expect(calibrateWatchPartyClock).toHaveBeenCalledTimes(1);

      // A slower exchange that agrees within its bound does not move the offset.
      vi.mocked(calibrateWatchPartyClock).mockResolvedValueOnce({ offsetMS: 31, roundTripMS: 40 });
      await vi.advanceTimersByTimeAsync(CLOCK_REFRESH_INTERVAL_MS);
      expect(calibrateWatchPartyClock).toHaveBeenCalledTimes(2);
      expect(getWatchParty().offsetMS).toBe(25);

      // A quicker exchange replaces it.
      vi.mocked(calibrateWatchPartyClock).mockResolvedValueOnce({ offsetMS: 26.5, roundTripMS: 1 });
      await vi.advanceTimersByTimeAsync(CLOCK_REFRESH_INTERVAL_MS);
      expect(getWatchParty().offsetMS).toBe(26.5);

      // A failed re-estimate keeps what it has.
      vi.mocked(calibrateWatchPartyClock).mockRejectedValueOnce(new Error("offline"));
      await vi.advanceTimersByTimeAsync(CLOCK_REFRESH_INTERVAL_MS);
      expect(getWatchParty()).toMatchObject({ offsetMS: 26.5, error: "" });

      // A clock that stepped is believed even from a slow exchange.
      vi.mocked(calibrateWatchPartyClock).mockResolvedValueOnce({ offsetMS: 900, roundTripMS: 60 });
      await vi.advanceTimersByTimeAsync(CLOCK_REFRESH_INTERVAL_MS);
      expect(getWatchParty().offsetMS).toBe(900);
    });

    it("stops re-estimating when the party is left", async () => {
      await lead();
      await leaveParty();
      await vi.advanceTimersByTimeAsync(CLOCK_REFRESH_INTERVAL_MS * 3);
      expect(calibrateWatchPartyClock).toHaveBeenCalledTimes(1);
    });
  });
});
