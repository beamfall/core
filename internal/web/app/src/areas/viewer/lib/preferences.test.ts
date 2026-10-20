import { describe, expect, it } from "vitest";
import {
  DOWNSTREAM_AUDIO_DEFAULT,
  encodeBool,
  PLAYBACK_SPEED_DEFAULT,
  PREF_KEYS,
  resolvePreferences,
  SKIP_INTERVAL_DEFAULT,
  skipIntervalOverrideKey,
} from "./preferences";

describe("resolvePreferences", () => {
  it("defaults every preference to its opt-out posture on an empty list", () => {
    expect(resolvePreferences([])).toEqual({
      autoSkipIntro: false,
      autoSkipRecap: false,
      autoSkipCredits: false,
      autoSkipAd: false,
      recommendOptIn: false,
      reduceMotion: false,
      playbackSpeed: PLAYBACK_SPEED_DEFAULT,
      trimSilence: false,
      skipInterval: SKIP_INTERVAL_DEFAULT,
      downstreamAudio: DOWNSTREAM_AUDIO_DEFAULT,
    });
  });

  it("reads the 'on' boolean convention shared with the server", () => {
    const prefs = resolvePreferences([
      { key: PREF_KEYS.autoSkipIntro, value: "on" },
      { key: PREF_KEYS.autoSkipAd, value: "on" },
      { key: PREF_KEYS.recommendOptIn, value: "on" },
      { key: PREF_KEYS.reduceMotion, value: "on" },
    ]);
    expect(prefs.autoSkipIntro).toBe(true);
    expect(prefs.autoSkipAd).toBe(true);
    expect(prefs.recommendOptIn).toBe(true);
    expect(prefs.reduceMotion).toBe(true);
    expect(prefs.autoSkipRecap).toBe(false);
  });

  it("resolves autoskip.ad on the kind alone, like the other three booleans", () => {
    expect(PREF_KEYS.autoSkipAd).toBe("autoskip.ad");
    expect(resolvePreferences([{ key: PREF_KEYS.autoSkipAd, value: "on" }]).autoSkipAd).toBe(true);
    // `sponsor` is a reserved, non-binding placeholder (ADR-0193 Decision 2): the
    // autoskip.sponsor <- autoskip.ad read-time fallback is a recorded contract
    // only, so no sponsor key exists and none is read.
    expect(Object.values(PREF_KEYS)).not.toContain("autoskip.sponsor");
  });

  it("treats any non-'on' value as false (fail-closed)", () => {
    const prefs = resolvePreferences([
      { key: PREF_KEYS.autoSkipIntro, value: "true" },
      { key: PREF_KEYS.autoSkipRecap, value: "" },
      { key: PREF_KEYS.autoSkipCredits, value: "1" },
      { key: PREF_KEYS.autoSkipAd, value: "yes" },
    ]);
    expect(prefs.autoSkipIntro).toBe(false);
    expect(prefs.autoSkipRecap).toBe(false);
    expect(prefs.autoSkipCredits).toBe(false);
    expect(prefs.autoSkipAd).toBe(false);
  });

  it("accepts a valid playback speed and rejects an invalid one", () => {
    expect(
      resolvePreferences([{ key: PREF_KEYS.playbackSpeedDefault, value: "1.5" }]).playbackSpeed,
    ).toBe("1.5");
    expect(
      resolvePreferences([{ key: PREF_KEYS.playbackSpeedDefault, value: "9" }]).playbackSpeed,
    ).toBe(PLAYBACK_SPEED_DEFAULT);
  });

  it("accepts downstream audio modes and rejects an invalid one", () => {
    expect(
      resolvePreferences([{ key: PREF_KEYS.downstreamAudio, value: "surround" }]).downstreamAudio,
    ).toBe("surround");
    expect(
      resolvePreferences([{ key: PREF_KEYS.downstreamAudio, value: "bogus" }]).downstreamAudio,
    ).toBe(DOWNSTREAM_AUDIO_DEFAULT);
  });

  it("resolves global and per-show skip intervals", () => {
    const showKey = skipIntervalOverrideKey("show/1");
    expect(
      resolvePreferences([
        { key: PREF_KEYS.skipInterval, value: "30" },
        { key: showKey, value: "45" },
      ]).skipInterval,
    ).toBe("30");
    expect(
      resolvePreferences(
        [
          { key: PREF_KEYS.skipInterval, value: "30" },
          { key: showKey, value: "45" },
        ],
        "show/1",
      ).skipInterval,
    ).toBe("45");
    expect(resolvePreferences([{ key: PREF_KEYS.skipInterval, value: "99" }]).skipInterval).toBe(
      SKIP_INTERVAL_DEFAULT,
    );
  });
});

describe("encodeBool", () => {
  it("encodes true as 'on' and false as '' (clear)", () => {
    expect(encodeBool(true)).toBe("on");
    expect(encodeBool(false)).toBe("");
  });
});
