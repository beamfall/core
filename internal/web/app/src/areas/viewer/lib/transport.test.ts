import { describe, expect, it } from "vitest";
import {
  autoSkipSegmentAt,
  autoSkipSegmentKey,
  buildRenditionMenuModel,
  chapterSeekTarget,
  renditionIndex,
  renditionKey,
} from "./transport";

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

describe("rendition keys", () => {
  it("uses the first non-empty of id, height and bandwidth", () => {
    expect(renditionKey({ id: "r1", height: 720 })).toBe("r1");
    expect(renditionKey({ height: 720, bandwidth: 4_000_000 })).toBe("720");
    expect(renditionKey({ bandwidth: 4_000_000 })).toBe("4000000");
    expect(renditionKey({})).toBe("");
  });

  it("never resolves an id-less rendition to level 0 on a miss", () => {
    const renditions = [{ height: 1080 }, { height: 720 }];
    expect(renditionIndex(renditions, "720")).toBe(1);
    expect(renditionIndex(renditions, "480")).toBe(-1);
    expect(renditionIndex(renditions, "")).toBe(-1);
    expect(renditionIndex(undefined, "720")).toBe(-1);
  });
});

describe("buildRenditionMenuModel", () => {
  const direct = { kind: "direct-play", url: "/d" };
  const hls = {
    kind: "hls",
    url: "/h",
    renditions: [{ id: "1080", height: 1080 }, { height: 720 }],
  };

  it("offers direct play plus the HLS sibling's renditions on a direct-play stream", () => {
    const model = buildRenditionMenuModel({ stream: direct, alternate: hls });
    expect(model.manualPinActive).toBe(false);
    expect(model.options.map((o) => [o.id, o.selected])).toEqual([
      ["direct", true],
      ["1080", false],
      ["720", false],
    ]);
  });

  it("keeps a single direct-play row when no sibling exists", () => {
    const model = buildRenditionMenuModel({ stream: direct });
    expect(model.options.map((o) => o.id)).toEqual(["direct"]);
  });

  it("marks the pinned rendition on an HLS stream, including id-less ones", () => {
    const model = buildRenditionMenuModel({ stream: hls, manualRenditionID: "720" });
    expect(model.manualPinActive).toBe(true);
    expect(model.options.map((o) => [o.id, o.selected])).toEqual([
      ["auto", false],
      ["1080", false],
      ["720", true],
    ]);
  });

  it("drops renditions that carry no usable key", () => {
    const model = buildRenditionMenuModel({
      stream: { kind: "hls", url: "/h", renditions: [{}, { height: 480 }] },
    });
    expect(model.options.map((o) => o.id)).toEqual(["auto", "480"]);
  });
});
