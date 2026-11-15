import { describe, expect, it } from "vitest";
import {
  correctionFor,
  driftLevel,
  expectedPositionMS,
  parseGroupSync,
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
