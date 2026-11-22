import { describe, expect, it } from "vitest";
import { containerSpeedKey, PREF_KEYS, resolvePreferences } from "../lib/preferences";
import {
  chapterEndAt,
  chapterSleep,
  clampSpeed,
  hasChapters,
  minutesAndSeconds,
  nextPauseStart,
  parseSilenceMap,
  pauseSkipTarget,
  resolveSpeed,
  sleepFade,
  sleepRemaining,
  speedValue,
} from "./listening";

describe("speed", () => {
  it("clamps to 0.5–3 in 0.05 steps and stores a short form", () => {
    expect(clampSpeed(0.1)).toBe(0.5);
    expect(clampSpeed(9)).toBe(3);
    expect(speedValue(1.234)).toBe("1.25");
    expect(speedValue(2)).toBe("2");
  });

  it("prefers the container's speed, then the Profile default, then 1x", () => {
    const key = containerSpeedKey("entity/1");
    expect(key).toBe("playback.speed.container.entity%2F1");
    const prefs = [
      { key: PREF_KEYS.playbackSpeedDefault, value: "1.2" },
      { key, value: "1.75" },
    ];
    expect(resolveSpeed(prefs, "entity/1")).toEqual({ speed: 1.75, fromContainer: true });
    expect(resolveSpeed(prefs, "other")).toEqual({ speed: 1.2, fromContainer: false });
    expect(resolveSpeed([{ key, value: "7" }], "entity/1")).toEqual({
      speed: 1,
      fromContainer: false,
    });
    expect(containerSpeedKey("x".repeat(200))).toBe("");
  });

  it("honours any stored default speed in range", () => {
    const speed = (value: string) =>
      resolvePreferences([{ key: PREF_KEYS.playbackSpeedDefault, value }]).playbackSpeed;
    expect(speed("1.2")).toBe("1.2");
    expect(speed("2.50")).toBe("2.5");
    expect(speed("1.23")).toBe("1.23");
    expect(speed("0.2")).toBe("1");
    expect(speed("fast")).toBe("1");
  });
});

describe("long pauses", () => {
  const map = {
    version: 1,
    spans: [
      { startSeconds: 30, endSeconds: 31.5 },
      { startSeconds: 10, endSeconds: 10.5 },
      { startSeconds: 12, endSeconds: 14 },
      { startSeconds: 13.5, endSeconds: 15 },
      { startSeconds: "x", endSeconds: 2 },
      { startSeconds: 40, endSeconds: 40.7 },
      { startSeconds: 2, endSeconds: 2.8 },
    ],
  };

  it("keeps pauses of 0.8 s or more, sorted and merged", () => {
    expect(parseSilenceMap(map)).toEqual([
      { start: 2, end: 2.8 },
      { start: 12, end: 15 },
      { start: 30, end: 31.5 },
    ]);
    expect(parseSilenceMap({ version: 2, spans: map.spans })).toEqual([]);
    expect(parseSilenceMap(null)).toEqual([]);
  });

  it("jumps from inside a pause to its end and finds the next one", () => {
    const spans = parseSilenceMap(map);
    expect(pauseSkipTarget(spans, 12.2)).toBe(15);
    expect(pauseSkipTarget(spans, 14.97)).toBeUndefined();
    expect(pauseSkipTarget(spans, 20)).toBeUndefined();
    expect(nextPauseStart(spans, 20)).toBe(30);
    expect(nextPauseStart(spans, 31)).toBeUndefined();
    expect(minutesAndSeconds(372.9)).toEqual({ minutes: 6, seconds: 12 });
  });
});

describe("sleep timer", () => {
  const markers = [
    { startSeconds: 0, segmentKind: "", title: "One" },
    { startSeconds: 5, endSeconds: 20, segmentKind: "intro", title: "" },
    { startSeconds: 60, endSeconds: 90, segmentKind: "", title: "Two" },
    { startSeconds: 120, segmentKind: "", title: "Three" },
  ];

  it("ends a chapter at its own end or the next chapter, ignoring skip segments", () => {
    expect(hasChapters(markers)).toBe(true);
    expect(hasChapters([markers[1]!])).toBe(false);
    expect(chapterEndAt(markers, 4)).toBe(60);
    expect(chapterEndAt(markers, 59.6)).toBe(60);
    expect(chapterEndAt(markers, 70)).toBe(90);
    expect(chapterEndAt(markers, 95)).toBe(120);
    expect(chapterEndAt(markers, 130)).toBeNull();
    expect(chapterSleep(markers, 70, 150)).toEqual({ kind: "chapter", end: 90 });
    expect(chapterSleep(markers, 95, 120.5)).toEqual({ kind: "item" });
    expect(chapterSleep(markers, 130, 150)).toEqual({ kind: "item" });
  });

  const media = { currentTime: 100, duration: 160, playbackRate: 2 };

  it("counts wall time for minutes and media time for the item or chapter", () => {
    expect(sleepRemaining({ kind: "minutes", minutes: 15, until: 61_000 }, 1_000, media)).toBe(60);
    expect(sleepRemaining({ kind: "item" }, 0, media)).toBeCloseTo(29.7);
    expect(sleepRemaining({ kind: "chapter", end: 120 }, 0, media)).toBe(10);
  });

  it("fades over the last ten seconds", () => {
    expect(sleepFade(30)).toBe(1);
    expect(sleepFade(5)).toBe(0.5);
    expect(sleepFade(0)).toBe(0);
  });
});
