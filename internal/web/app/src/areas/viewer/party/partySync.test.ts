import { describe, expect, it } from "vitest";
import type { PartyPlayback } from "@/lib/graphql";
import { anchorTargetMS } from "./partySync";

const anchor: PartyPlayback = {
  entryID: "e1",
  state: "playing",
  positionMS: 10_000,
  anchorAtMS: 1_000,
  tempo: 1,
  pitchSemitones: 0,
};

describe("anchorTargetMS", () => {
  it("advances one song ms per wall ms at the recorded tempo", () => {
    expect(anchorTargetMS(anchor, 5_000)).toBe(14_000);
  });

  it("advances tempo song ms per wall ms, matching an adjusted stream", () => {
    // A 1.25x stream covers 1.25 song seconds per wall second.
    expect(anchorTargetMS({ ...anchor, tempo: 1.25 }, 5_000)).toBe(15_000);
  });

  it("holds the position while paused and treats a zero tempo as 1", () => {
    expect(anchorTargetMS({ ...anchor, state: "paused", tempo: 1.25 }, 5_000)).toBe(10_000);
    expect(anchorTargetMS({ ...anchor, tempo: 0 }, 5_000)).toBe(14_000);
  });
});
