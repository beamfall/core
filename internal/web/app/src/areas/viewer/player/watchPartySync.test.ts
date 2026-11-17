import { describe, expect, it } from "vitest";
import {
  type ClockExchange,
  correctionFor,
  driftLevel,
  estimateClock,
  expectedPositionMS,
  FOLLOW_MAX_RATE_NUDGE,
  followerCorrection,
  followerDriftLevel,
  followerTargetMS,
  MAX_SEEK_LEAD_MS,
  mergeClockEstimate,
  nextSeekLeadMS,
  type PartyTimeline,
  parseGroupSync,
  timelineRate,
  timelineStarted,
} from "./watchPartySync";

function frame(payload: unknown, type = "group.sync") {
  return { type, data: JSON.stringify({ payload }) };
}

describe("parseGroupSync", () => {
  it("reads a transition frame", () => {
    expect(
      parseGroupSync(
        frame({
          groupID: "p1",
          signal: "transition",
          item: "m1",
          positionMS: 1200,
          presentAtMS: 5000,
          paused: false,
        }),
      ),
    ).toEqual({
      groupID: "p1",
      signal: "transition",
      item: "m1",
      positionMS: 1200,
      presentAtMS: 5000,
      paused: false,
    });
  });

  it("keeps a roster's string members only", () => {
    const parsed = parseGroupSync(
      frame({ groupID: "p1", signal: "roster", paused: true, members: ["A", 3, "B"] }),
    );
    expect(parsed?.members).toEqual(["A", "B"]);
    expect(parsed?.paused).toBe(true);
  });

  it("ignores other events, malformed data, a missing party and unknown signals", () => {
    expect(parseGroupSync(frame({ groupID: "p1", signal: "transition" }, "playback"))).toBeNull();
    expect(parseGroupSync({ type: "group.sync", data: "{nope" })).toBeNull();
    expect(parseGroupSync(frame({ signal: "transition" }))).toBeNull();
    expect(parseGroupSync(frame({ groupID: "p1", signal: "explode" }))).toBeNull();
  });

  it("zeroes non-finite numbers", () => {
    const parsed = parseGroupSync(
      frame({ groupID: "p1", signal: "nudge", positionMS: "x", presentAtMS: null }),
    );
    expect(parsed?.positionMS).toBe(0);
    expect(parsed?.presentAtMS).toBe(0);
  });
});

describe("timeline projection", () => {
  const running = { item: "m1", positionMS: 10_000, presentAtMS: 50_000, paused: false };

  it("holds a running timeline until its present time, then advances in real time", () => {
    expect(expectedPositionMS(running, 49_000)).toBe(10_000);
    expect(timelineStarted(running, 49_000)).toBe(false);
    expect(expectedPositionMS(running, 52_500)).toBe(12_500);
    expect(timelineStarted(running, 52_500)).toBe(true);
  });

  it("holds a paused timeline", () => {
    const paused = { ...running, paused: true };
    expect(expectedPositionMS(paused, 90_000)).toBe(10_000);
    expect(timelineStarted(paused, 90_000)).toBe(false);
  });
});

describe("correctionFor", () => {
  it("re-seeks past the threshold", () => {
    expect(correctionFor(800, 4000, false)).toEqual({ kind: "seek", toMS: 4000 });
    expect(correctionFor(-800, 4000, true)).toEqual({ kind: "seek", toMS: 4000 });
  });

  it("does nothing inside the in-sync band or on a paused timeline", () => {
    expect(correctionFor(30, 4000, false)).toEqual({ kind: "none" });
    expect(correctionFor(300, 4000, true)).toEqual({ kind: "none" });
  });

  it("slows a player that is ahead and speeds one that is behind, capped", () => {
    expect(correctionFor(100, 4000, false)).toEqual({ kind: "rate", rate: 0.95 });
    const behind = correctionFor(-60, 4000, false);
    expect(behind.kind).toBe("rate");
    expect(behind.kind === "rate" && behind.rate).toBeCloseTo(1.03);
  });
});

describe("driftLevel", () => {
  it("bands the drift for the indicator", () => {
    expect(driftLevel(-80)).toBe("in-sync");
    expect(driftLevel(81)).toBe("adjusting");
    expect(driftLevel(-500)).toBe("adjusting");
    expect(driftLevel(501)).toBe("resyncing");
  });
});

function leadFrame(extra: Record<string, unknown>) {
  return parseGroupSync(
    frame({
      groupID: "p1",
      signal: "transition",
      item: "m1",
      positionMS: 1000,
      presentAtMS: 5000,
      ...extra,
    }),
  );
}

describe("a lead party's timeline", () => {
  it("reads lead and the speed from a transition", () => {
    expect(leadFrame({ lead: true, ratePermille: 1250 })).toEqual({
      groupID: "p1",
      signal: "transition",
      item: "m1",
      positionMS: 1000,
      presentAtMS: 5000,
      paused: false,
      lead: true,
      ratePermille: 1250,
    });
  });

  it("leaves both off an ordinary party and treats 1000 as normal speed", () => {
    expect(leadFrame({})).not.toHaveProperty("lead");
    expect(leadFrame({})).not.toHaveProperty("ratePermille");
    expect(leadFrame({ lead: true, ratePermille: 1000 })).not.toHaveProperty("ratePermille");
  });

  it("ignores a speed no player can follow", () => {
    for (const ratePermille of [0, -500, 20, 90_000, Number.NaN, "1250"]) {
      expect(leadFrame({ lead: true, ratePermille })).not.toHaveProperty("ratePermille");
    }
  });

  it("advances 1.25 s per second at rate 1250", () => {
    const timeline: PartyTimeline = {
      item: "m1",
      positionMS: 10_000,
      presentAtMS: 50_000,
      paused: false,
      lead: true,
      ratePermille: 1250,
    };
    expect(timelineRate(timeline)).toBe(1.25);
    expect(expectedPositionMS(timeline, 51_000)).toBe(11_250);
    expect(expectedPositionMS(timeline, 54_000)).toBe(15_000);
    // Before its present time, and while paused, it holds.
    expect(expectedPositionMS(timeline, 49_000)).toBe(10_000);
    expect(expectedPositionMS({ ...timeline, paused: true }, 54_000)).toBe(10_000);
  });

  it("advances in real time without a speed", () => {
    const timeline: PartyTimeline = { item: "m1", positionMS: 0, presentAtMS: 0, paused: false };
    expect(timelineRate(timeline)).toBe(1);
    expect(expectedPositionMS(timeline, 2500)).toBe(2500);
  });
});

describe("the follower's target and correction", () => {
  const running: PartyTimeline = {
    item: "m1",
    positionMS: 10_000,
    presentAtMS: 50_000,
    paused: false,
    lead: true,
  };
  const fast: PartyTimeline = { ...running, ratePermille: 1250 };
  const paused: PartyTimeline = { ...running, paused: true };

  it("aims the display delay trim ahead of the timeline", () => {
    expect(followerTargetMS(running, 51_000, 0)).toBe(11_000);
    // An 80 ms trim shows each frame 80 ms earlier.
    expect(followerTargetMS(running, 51_000, 80)).toBe(11_080);
    expect(followerTargetMS(running, 51_000, -30)).toBe(10_970);
    // At 1.25x, 80 ms of wall time is 100 ms of the title.
    expect(followerTargetMS(fast, 51_000, 80)).toBe(11_350);
  });

  it("shows a paused timeline's own frame whatever the trim", () => {
    expect(followerTargetMS(paused, 51_000, 80)).toBe(10_000);
  });

  it("never aims before the start", () => {
    expect(followerTargetMS({ ...running, positionMS: 0 }, 50_000, -200)).toBe(0);
  });

  it("plays at the timeline's speed inside the in-sync band", () => {
    expect(followerCorrection(0, 11_000, running)).toEqual({ kind: "none" });
    expect(followerCorrection(15, 11_000, running)).toEqual({ kind: "none" });
    expect(followerCorrection(-15, 11_000, fast)).toEqual({ kind: "none" });
  });

  it("re-seeks a drift above a quarter second", () => {
    expect(followerCorrection(251, 11_000, running)).toEqual({ kind: "seek", toMS: 11_000 });
    expect(followerCorrection(-4000, 11_000, running)).toEqual({ kind: "seek", toMS: 11_000 });
    expect(followerCorrection(250, 11_000, running).kind).toBe("rate");
  });

  it("nudges in proportion to a small drift, slower when ahead and faster when behind", () => {
    const ahead = followerCorrection(30, 11_000, running);
    const behind = followerCorrection(-30, 11_000, running);
    const further = followerCorrection(-45, 11_000, running);
    expect(ahead).toEqual({ kind: "rate", rate: 0.94 });
    expect(behind).toEqual({ kind: "rate", rate: 1.06 });
    expect(further).toEqual({ kind: "rate", rate: 1.09 });
  });

  it("caps the nudge", () => {
    expect(followerCorrection(-200, 11_000, running)).toEqual({
      kind: "rate",
      rate: 1 + FOLLOW_MAX_RATE_NUDGE,
    });
    expect(followerCorrection(200, 11_000, running)).toEqual({
      kind: "rate",
      rate: 1 - FOLLOW_MAX_RATE_NUDGE,
    });
  });

  it("nudges around the timeline's own speed", () => {
    const behind = followerCorrection(-30, 11_000, fast);
    const ahead = followerCorrection(30, 11_000, fast);
    expect(behind.kind === "rate" && behind.rate).toBeCloseTo(1.25 * 1.06, 10);
    expect(ahead.kind === "rate" && ahead.rate).toBeCloseTo(1.25 * 0.94, 10);
  });

  it("re-seeks a paused timeline to its frame and never nudges it", () => {
    expect(followerCorrection(10, 10_000, paused)).toEqual({ kind: "none" });
    expect(followerCorrection(60, 10_000, paused)).toEqual({ kind: "seek", toMS: 10_000 });
    expect(followerCorrection(-900, 10_000, paused)).toEqual({ kind: "seek", toMS: 10_000 });
  });

  it("closes a 40 ms drift inside a second", () => {
    // Simulate the 100 ms loop: the nudge the correction picks, applied for a tick.
    let drift = -40;
    for (let tick = 0; tick < 10; tick++) {
      const correction = followerCorrection(drift, 0, running);
      const rate = correction.kind === "rate" ? correction.rate : 1;
      drift += (rate - 1) * 100;
    }
    expect(Math.abs(drift)).toBeLessThanOrEqual(20);
  });

  it("aims the next seek ahead by what the last one missed", () => {
    // A seek that took 300 ms left the player 300 ms behind.
    expect(nextSeekLeadMS(0, -300)).toBe(300);
    // Landing 40 ms ahead with that allowance trims it.
    expect(nextSeekLeadMS(300, 40)).toBe(260);
    expect(nextSeekLeadMS(100, 400)).toBe(0);
    expect(nextSeekLeadMS(1900, -5000)).toBe(MAX_SEEK_LEAD_MS);
    expect(nextSeekLeadMS(120, Number.NaN)).toBe(120);
  });

  it("names the follower's drift level against the 40 ms target", () => {
    expect(followerDriftLevel(0)).toBe("in-sync");
    expect(followerDriftLevel(-40)).toBe("in-sync");
    expect(followerDriftLevel(41)).toBe("adjusting");
    expect(followerDriftLevel(250)).toBe("adjusting");
    expect(followerDriftLevel(-251)).toBe("resyncing");
  });
});

describe("estimateClock", () => {
  const TRUE_OFFSET = 1234.567;

  // An exchange that left at local `t1`, spent `out` ms on the way there,
  // `held` ms in the server and `back` ms on the way home.
  function exchange(t1: number, out: number, back: number, held = 0.2): ClockExchange {
    const t2 = t1 + out + TRUE_OFFSET;
    const t3 = t2 + held;
    return { t1, t2, t3, t4: t3 - TRUE_OFFSET + back };
  }

  it("is exact for a symmetric exchange", () => {
    const estimate = estimateClock([exchange(1000, 3, 3)]);
    expect(estimate?.offsetMS).toBeCloseTo(TRUE_OFFSET, 6);
    expect(estimate?.roundTripMS).toBeCloseTo(6, 6);
  });

  it("has no estimate without an exchange", () => {
    expect(estimateClock([])).toBeNull();
    expect(estimateClock([{ t1: Number.NaN, t2: 1, t3: 2, t4: 3 }])).toBeNull();
  });

  it("stays within 5 ms of truth when one exchange of several is 80 ms slower", () => {
    const quick = [0, 1, 2, 3, 4, 5, 6].map((i) => exchange(1000 + i * 20, 1.5, 2.5));
    // The slow one's delay is all on the way back, the worst case for its offset.
    const slow = exchange(2000, 1.5, 82.5);
    expect(Math.abs((estimateClock([slow])?.offsetMS ?? 0) - TRUE_OFFSET)).toBeGreaterThan(30);
    for (let at = 0; at <= quick.length; at++) {
      const exchanges = [...quick.slice(0, at), slow, ...quick.slice(at)];
      const estimate = estimateClock(exchanges);
      expect(Math.abs((estimate?.offsetMS ?? 0) - TRUE_OFFSET)).toBeLessThan(5);
      expect(estimate?.roundTripMS).toBeCloseTo(4, 6);
    }
  });

  it("resolves better than a millisecond on a quiet link", () => {
    const exchanges = [0.3, 0.9, 0.4, 0.6].map((jitter, i) =>
      exchange(500 + i * 10, 0.4 + jitter, 0.4),
    );
    expect(Math.abs((estimateClock(exchanges)?.offsetMS ?? 0) - TRUE_OFFSET)).toBeLessThan(1);
  });

  it("never reports a negative round trip", () => {
    // Whole-millisecond server stamps can exceed a sub-millisecond round trip.
    expect(estimateClock([{ t1: 10, t2: 100, t3: 101, t4: 10.4 }])?.roundTripMS).toBe(0);
  });
});

describe("mergeClockEstimate", () => {
  const held = { offsetMS: 100, roundTripMS: 2, atMS: 0 };

  it("takes the first estimate", () => {
    expect(mergeClockEstimate(null, held)).toBe(held);
  });

  it("takes a re-estimate that is at least as tight", () => {
    const next = { offsetMS: 100.6, roundTripMS: 1.5, atMS: 30_000 };
    expect(mergeClockEstimate(held, next)).toBe(next);
  });

  it("keeps the held estimate over one slow sample", () => {
    const slow = { offsetMS: 131, roundTripMS: 80, atMS: 30_000 };
    expect(mergeClockEstimate(held, slow)).toBe(held);
  });

  it("lets a held estimate age out", () => {
    // 100 ppm of drift over ten minutes outweighs a 12 ms round trip.
    const later = { offsetMS: 104, roundTripMS: 12, atMS: 600_000 };
    expect(mergeClockEstimate(held, { ...later, atMS: 30_000 })).toBe(held);
    expect(mergeClockEstimate(held, later)).toBe(later);
  });

  it("follows a clock that stepped, even on a slow sample", () => {
    const stepped = { offsetMS: 3700, roundTripMS: 80, atMS: 30_000 };
    expect(mergeClockEstimate(held, stepped)).toBe(stepped);
  });
});
