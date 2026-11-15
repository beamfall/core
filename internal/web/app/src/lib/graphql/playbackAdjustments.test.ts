import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchPlaybackAdjustmentRanges } from "./viewer";

// CORE-1659: the key and tempo ranges a server advertises in serverInfo.
function serve(data: unknown) {
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      Promise.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ data }),
      } as Response),
    ),
  );
}

afterEach(() => vi.unstubAllGlobals());

describe("fetchPlaybackAdjustmentRanges", () => {
  const ranges = {
    pitchShiftSemitonesMin: -6,
    pitchShiftSemitonesMax: 6,
    tempoMin: 0.75,
    tempoMax: 1.25,
  };

  it("returns the advertised ranges", async () => {
    serve({ serverInfo: { playbackAdjustments: ranges } });
    await expect(fetchPlaybackAdjustmentRanges()).resolves.toEqual(ranges);
  });

  it("reads an absent capability as unsupported", async () => {
    serve({ serverInfo: { playbackAdjustments: null } });
    await expect(fetchPlaybackAdjustmentRanges()).resolves.toBeNull();
  });

  it("rejects ranges that leave out the recorded key or tempo", async () => {
    serve({ serverInfo: { playbackAdjustments: { ...ranges, tempoMin: 1.05 } } });
    await expect(fetchPlaybackAdjustmentRanges()).resolves.toBeNull();
  });

  it("reads a failed request as unsupported", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => Promise.reject(new TypeError("Failed to fetch"))),
    );
    await expect(fetchPlaybackAdjustmentRanges()).resolves.toBeNull();
  });
});
