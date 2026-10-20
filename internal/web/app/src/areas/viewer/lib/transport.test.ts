import { describe, expect, it } from "vitest";
import { autoSkipSegmentAt, autoSkipSegmentKey, chapterSeekTarget } from "./transport";

describe("chapterSeekTarget", () => {
  it("moves to adjacent marker starts without looping on the current chapter", () => {
    const markers = [
      { title: "One", startSeconds: 0, endSeconds: 10, segmentKind: "" },
      { title: "Two", startSeconds: 30, endSeconds: 40, segmentKind: "" },
      { title: "Three", startSeconds: 60, endSeconds: 70, segmentKind: "" },
    ];

    expect(chapterSeekTarget(markers, 31, -1)).toBe(0);
    expect(chapterSeekTarget(markers, 31, 1)).toBe(60);
    expect(chapterSeekTarget(markers, 60.5, 1)).toBeNull();
  });
});

describe("autoSkipSegmentAt", () => {
  const allOn = { intro: true, recap: true, credits: true };
  const markers = [
    { title: "Intro", startSeconds: 0, endSeconds: 30, segmentKind: "intro" },
    { title: "Recap", startSeconds: 40, endSeconds: 55, segmentKind: "recap" },
    { title: "Ad Break", startSeconds: 60, endSeconds: 90, segmentKind: "commercial" },
    { title: "Credits", startSeconds: 1200, endSeconds: 1260, segmentKind: "credits" },
  ];

  it("returns the matching segment while the playhead is inside its range", () => {
    expect(autoSkipSegmentAt(markers, 10, allOn)).toEqual({
      segmentKind: "intro",
      startSeconds: 0,
      endSeconds: 30,
      title: "Intro",
    });
    expect(autoSkipSegmentAt(markers, 30, allOn)).toBeNull(); // end is exclusive
    expect(autoSkipSegmentAt(markers, 35, allOn)).toBeNull(); // gap between segments
  });

  it("gates each kind on its own pref, independent of the others", () => {
    expect(autoSkipSegmentAt(markers, 10, { intro: false, recap: true, credits: true })).toBeNull();
    expect(autoSkipSegmentAt(markers, 45, { intro: true, recap: false, credits: true })).toBeNull();
    expect(autoSkipSegmentAt(markers, 45, { intro: true, recap: true, credits: false })).toEqual(
      expect.objectContaining({ segmentKind: "recap" }),
    );
  });

  it("never auto-skips a kind with no autoskip pref (commercial/intermission)", () => {
    expect(autoSkipSegmentAt(markers, 70, allOn)).toBeNull();
  });

  it("reports the marker's own segment end, never EOF, for credits", () => {
    const segment = autoSkipSegmentAt(markers, 1230, allOn);
    expect(segment?.endSeconds).toBe(1260);
  });
});

describe("autoSkipSegmentKey", () => {
  it("is stable per kind+start so re-entering the same segment matches", () => {
    const a = { segmentKind: "intro", startSeconds: 0, endSeconds: 30, title: "" };
    const b = { segmentKind: "intro", startSeconds: 0, endSeconds: 30, title: "" };
    const c = { segmentKind: "recap", startSeconds: 0, endSeconds: 30, title: "" };
    expect(autoSkipSegmentKey(a)).toBe(autoSkipSegmentKey(b));
    expect(autoSkipSegmentKey(a)).not.toBe(autoSkipSegmentKey(c));
  });
});
