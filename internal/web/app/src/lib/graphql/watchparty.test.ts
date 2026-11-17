import { beforeEach, describe, expect, it, vi } from "vitest";
import { graphql, postJSON } from "@/lib/api";
import {
  CALIBRATION_EXCHANGES,
  calibrateWatchPartyClock,
  createWatchParty,
  reportWatchPartyLeadPosition,
} from "./watchparty";

vi.mock("@/lib/api", () => ({
  graphql: vi.fn(),
  postJSON: vi.fn(),
  postKeepalive: vi.fn(),
}));

beforeEach(() => {
  vi.mocked(graphql).mockReset();
  vi.mocked(postJSON).mockReset();
});

// A local clock that reads the listed instants in turn: t1 and t4 of each exchange.
function clock(...readings: number[]) {
  let i = 0;
  return () => readings[i++] ?? Number.NaN;
}

describe("calibrateWatchPartyClock (CORE-2244)", () => {
  it("takes several exchanges and keeps the one with the shortest round trip", async () => {
    // The server clock runs 500 ms ahead. The second exchange is the quick one.
    vi.mocked(postJSON)
      .mockResolvedValueOnce({ t1MS: 1000, t2MS: 1540, t3MS: 1541 })
      .mockResolvedValueOnce({ t1MS: 2000, t2MS: 2502, t3MS: 2503 })
      .mockResolvedValueOnce({ t1MS: 3000, t2MS: 3510, t3MS: 3511 });
    const result = await calibrateWatchPartyClock({
      exchanges: 3,
      now: clock(1000, 1081, 2000, 2005, 3000, 3061),
    });

    expect(postJSON).toHaveBeenCalledTimes(3);
    expect(postJSON).toHaveBeenNthCalledWith(2, "/api/group/calibrate", { t1MS: 2000 });
    expect(result).toEqual({ offsetMS: 500, roundTripMS: 4 });
  });

  it("takes eight exchanges unless told otherwise, one at a time", async () => {
    let open = 0;
    let most = 0;
    vi.mocked(postJSON).mockImplementation(async () => {
      most = Math.max(most, ++open);
      await Promise.resolve();
      open--;
      return { t1MS: 0, t2MS: 10, t3MS: 10 };
    });
    await calibrateWatchPartyClock({ now: () => 0 });
    expect(CALIBRATION_EXCHANGES).toBe(8);
    expect(postJSON).toHaveBeenCalledTimes(8);
    expect(most).toBe(1);
  });

  it("reads the server's microsecond stamps and sends a whole t1", async () => {
    vi.mocked(postJSON).mockResolvedValue({
      t1MS: 1000,
      t2MS: 1500,
      t3MS: 1500,
      t2US: 1_500_250,
      t3US: 1_500_750,
    });
    const result = await calibrateWatchPartyClock({ exchanges: 1, now: clock(1000.4, 1001.4) });

    expect(postJSON).toHaveBeenCalledWith("/api/group/calibrate", { t1MS: 1000 });
    // ((1500.25 − 1000.4) + (1500.75 − 1001.4)) / 2, over a 0.5 ms round trip.
    expect(result.offsetMS).toBeCloseTo(499.6, 6);
    expect(result.roundTripMS).toBeCloseTo(0.5, 6);
  });

  it("falls back to the millisecond stamps of an older server", async () => {
    vi.mocked(postJSON).mockResolvedValue({ t1MS: 1000, t2MS: 1300, t3MS: 1302 });
    await expect(
      calibrateWatchPartyClock({ exchanges: 1, now: clock(1000, 1010) }),
    ).resolves.toEqual({ offsetMS: 296, roundTripMS: 8 });
  });

  it("fails when the first exchange fails", async () => {
    vi.mocked(postJSON).mockRejectedValue(new Error("not on the home network"));
    await expect(calibrateWatchPartyClock()).rejects.toThrow("not on the home network");
    expect(postJSON).toHaveBeenCalledTimes(1);
  });

  it("keeps what it has when a later exchange fails", async () => {
    vi.mocked(postJSON)
      .mockResolvedValueOnce({ t1MS: 1000, t2MS: 1105, t3MS: 1105 })
      .mockRejectedValueOnce(new Error("offline"));
    await expect(calibrateWatchPartyClock({ now: clock(1000, 1010, 2000) })).resolves.toEqual({
      offsetMS: 100,
      roundTripMS: 10,
    });
    expect(postJSON).toHaveBeenCalledTimes(2);
  });

  it("fails on a reply with no usable stamps", async () => {
    vi.mocked(postJSON).mockResolvedValue({});
    await expect(calibrateWatchPartyClock({ exchanges: 2, now: () => 0 })).rejects.toThrow(
      "no usable exchange",
    );
  });
});

describe("lead party operations (CORE-2245)", () => {
  it("creates a lead party", async () => {
    vi.mocked(graphql).mockResolvedValue({ createWatchParty: { partyID: "p1" } });
    await createWatchParty({
      content: "m1",
      relaxed: false,
      clockOffsetMS: 0,
      renderLatencyMS: 0,
      lead: true,
    });
    const [query, variables] = vi.mocked(graphql).mock.calls[0] ?? [];
    expect(query).toContain("$lead: Boolean");
    expect(query).toContain("lead: $lead");
    expect(variables).toMatchObject({ content: "m1", lead: true });
  });

  it("reports the lead's position, instant and speed in whole numbers", async () => {
    vi.mocked(graphql).mockResolvedValue({
      reportDrift: { groupID: "p1", action: "anchor", rate: 0 },
    });
    await expect(
      reportWatchPartyLeadPosition("p1", {
        positionMS: 12_480.6,
        atMS: 1_800_000_000_123.4,
        ratePermille: 1250,
      }),
    ).resolves.toMatchObject({ action: "anchor" });

    const [query, variables] = vi.mocked(graphql).mock.calls[0] ?? [];
    expect(query).toContain("positionMS: $positionMS, atMS: $atMS, ratePermille: $ratePermille");
    expect(variables).toEqual({
      groupID: "p1",
      driftMS: 0,
      positionMS: 12_481,
      atMS: 1_800_000_000_123,
      ratePermille: 1250,
    });
  });

  it("leaves normal speed off the wire", async () => {
    vi.mocked(graphql).mockResolvedValue({
      reportDrift: { groupID: "p1", action: "none", rate: 0 },
    });
    for (const ratePermille of [1000, 0]) {
      await reportWatchPartyLeadPosition("p1", { positionMS: -5, atMS: 10, ratePermille });
    }
    for (const [, variables] of vi.mocked(graphql).mock.calls) {
      expect(variables).toEqual({ groupID: "p1", driftMS: 0, positionMS: 0, atMS: 10 });
    }
  });
});
