import Hls from "hls.js";
import { describe, expect, it } from "vitest";
import {
  freshRecoveryBudget,
  HLS_MEDIA_ERROR,
  HLS_NETWORK_ERROR,
  nextRecoveryStep,
  resetRecoveryBudget,
} from "./hlsRecovery";

// feature:hls-error-recovery
describe("hlsRecovery", () => {
  it("pins the inlined error-type strings to the real hls.js enum", () => {
    expect(HLS_NETWORK_ERROR).toBe(Hls.ErrorTypes.NETWORK_ERROR);
    expect(HLS_MEDIA_ERROR).toBe(Hls.ErrorTypes.MEDIA_ERROR);
  });

  it("retries network errors with 1s/2s/4s backoff, then destroys", () => {
    const budget = freshRecoveryBudget();
    expect(nextRecoveryStep(budget, HLS_NETWORK_ERROR)).toEqual({ kind: "network", delayMs: 1000 });
    expect(nextRecoveryStep(budget, HLS_NETWORK_ERROR)).toEqual({ kind: "network", delayMs: 2000 });
    expect(nextRecoveryStep(budget, HLS_NETWORK_ERROR)).toEqual({ kind: "network", delayMs: 4000 });
    expect(nextRecoveryStep(budget, HLS_NETWORK_ERROR)).toEqual({ kind: "destroy" });
  });

  it("recovers media errors once plainly, once with an audio codec swap, then destroys", () => {
    const budget = freshRecoveryBudget();
    expect(nextRecoveryStep(budget, HLS_MEDIA_ERROR)).toEqual({
      kind: "media",
      swapAudioCodec: false,
    });
    expect(nextRecoveryStep(budget, HLS_MEDIA_ERROR)).toEqual({
      kind: "media",
      swapAudioCodec: true,
    });
    expect(nextRecoveryStep(budget, HLS_MEDIA_ERROR)).toEqual({ kind: "destroy" });
  });

  it("destroys on any other fatal type", () => {
    expect(nextRecoveryStep(freshRecoveryBudget(), "otherError")).toEqual({ kind: "destroy" });
    expect(nextRecoveryStep(freshRecoveryBudget(), undefined)).toEqual({ kind: "destroy" });
  });

  it("starts the backoff over once the budget resets", () => {
    const budget = freshRecoveryBudget();
    nextRecoveryStep(budget, HLS_NETWORK_ERROR);
    nextRecoveryStep(budget, HLS_NETWORK_ERROR);
    resetRecoveryBudget(budget);
    expect(nextRecoveryStep(budget, HLS_NETWORK_ERROR)).toEqual({ kind: "network", delayMs: 1000 });
  });
});
