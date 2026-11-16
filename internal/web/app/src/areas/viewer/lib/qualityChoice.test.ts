import { afterEach, describe, expect, it, vi } from "vitest";
import type { CapabilityProfile } from "./capabilityProfile";
import { readQualityChoice, withQualityCeiling, writeQualityChoice } from "./qualityChoice";

const profile = { client: "web" } as CapabilityProfile;

describe("quality choice (CORE-1971)", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    window.localStorage.clear();
  });

  it("remembers a step and caps later profiles at its bandwidth", () => {
    writeQualityChoice({ id: "720p", maxBitrate: 4_256_000 });
    expect(readQualityChoice()).toEqual({ id: "720p", maxBitrate: 4_256_000 });
    expect(withQualityCeiling(profile)).toEqual({ client: "web", maxStreamingBitrate: 4_256_000 });
  });

  it("forgets the step on Auto and leaves the profile as probed", () => {
    writeQualityChoice({ id: "720p", maxBitrate: 4_256_000 });
    writeQualityChoice(null);
    expect(readQualityChoice()).toBeNull();
    expect(withQualityCeiling(profile)).toBe(profile);
  });

  it.each([
    ["not JSON", "{"],
    ["no id", JSON.stringify({ maxBitrate: 1 })],
    ["zero bitrate", JSON.stringify({ id: "480p", maxBitrate: 0 })],
    ["fractional bitrate", JSON.stringify({ id: "480p", maxBitrate: 1.5 })],
  ])("reads a stored value with %s as Auto", (_name, raw) => {
    window.localStorage.setItem("beamfall.quality", raw);
    expect(readQualityChoice()).toBeNull();
  });

  it("is Auto when storage is unavailable", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(() => writeQualityChoice({ id: "720p", maxBitrate: 4_256_000 })).not.toThrow();
    expect(readQualityChoice()).toBeNull();
    expect(withQualityCeiling(profile)).toBe(profile);
  });
});
