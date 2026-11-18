import { describe, expect, it } from "vitest";
import { hdrName, type PlaybackInfo } from "./playback";
import {
  autoSkipSegmentAt,
  autoSkipSegmentKey,
  buildChapterList,
  buildRenditionMenuModel,
  buildRouteDetailModel,
  chapterSeekTarget,
  playbackDurationSeconds,
  renditionIndex,
  renditionKey,
  resolveTrickplaySheetURL,
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

describe("resolveTrickplaySheetURL", () => {
  it("resolves a sheet against a root-relative manifest URL", () => {
    expect(resolveTrickplaySheetURL("sheet-0.jpg", "/api/media/assets/t/trickplay.json")).toBe(
      `${window.location.origin}/api/media/assets/t/sheet-0.jpg`,
    );
  });

  it("keeps an absolute manifest URL and an absolute sheet", () => {
    expect(resolveTrickplaySheetURL("s.jpg", "https://cdn.example/a/trickplay.json")).toBe(
      "https://cdn.example/a/s.jpg",
    );
    expect(resolveTrickplaySheetURL("https://x.example/s.jpg", "")).toBe("https://x.example/s.jpg");
  });
});

describe("buildChapterList", () => {
  const markers = [
    { title: "Credits", startSeconds: 300, endSeconds: 330, segmentKind: "credits" },
    { title: "Second", startSeconds: 120, endSeconds: 240, segmentKind: "" },
    { title: "", startSeconds: 0, endSeconds: 120, segmentKind: "" },
    { title: "Opening", startSeconds: 0, endSeconds: null, segmentKind: "" },
    { title: "Intro", startSeconds: 5, endSeconds: 30, segmentKind: "intro" },
    { title: "Third", startSeconds: 240.5, endSeconds: null, segmentKind: "" },
  ];

  it("keeps range and point Markers without a segment kind, sorted and de-duplicated", () => {
    const chapters = buildChapterList(markers, { currentTimeSeconds: 130 });
    expect(chapters.map((chapter) => [chapter.startSeconds, chapter.title])).toEqual([
      [0, "Opening"],
      [120, "Second"],
      [240.5, "Third"],
    ]);
    expect(chapters.map((chapter) => chapter.current)).toEqual([false, true, false]);
    expect(chapters.every((chapter) => chapter.thumbnailURL === null)).toBe(true);
  });

  it("names thumbnails with the server's millisecond marker file name", () => {
    const chapters = buildChapterList(markers, { thumbnailBase: "/api/media/assets/tok/" });
    expect(chapters.map((chapter) => chapter.thumbnailURL)).toEqual([
      "/api/media/assets/tok/marker-0.jpg",
      "/api/media/assets/tok/marker-120000.jpg",
      "/api/media/assets/tok/marker-240500.jpg",
    ]);
    expect(chapters[0].current).toBe(true);
  });

  it("returns no chapters when every Marker is a segment", () => {
    expect(buildChapterList([markers[0], markers[4]])).toEqual([]);
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

  it("marks a remembered step the stream lists unless a pin overrides it (CORE-1971)", () => {
    const remembered = buildRenditionMenuModel({ stream: hls, rememberedID: "720" });
    expect(remembered.manualPinActive).toBe(false);
    expect(remembered.options.map((o) => [o.id, o.selected])).toEqual([
      ["auto", false],
      ["1080", false],
      ["720", true],
    ]);

    const pinned = buildRenditionMenuModel({
      stream: hls,
      rememberedID: "720",
      manualRenditionID: "1080",
    });
    expect(pinned.options.filter((o) => o.selected).map((o) => o.id)).toEqual(["1080"]);

    const unlisted = buildRenditionMenuModel({ stream: hls, rememberedID: "2160" });
    expect(unlisted.options.filter((o) => o.selected).map((o) => o.id)).toEqual(["auto"]);
  });

  it("offers Auto beside Original on direct play while a step is remembered (CORE-1971)", () => {
    const model = buildRenditionMenuModel({ stream: direct, alternate: hls, rememberedID: "720" });
    expect(model.options.map((o) => [o.id, o.selected])).toEqual([
      ["auto", false],
      ["direct", true],
      ["1080", false],
      ["720", false],
    ]);
  });

  it("drops renditions that carry no usable key", () => {
    const model = buildRenditionMenuModel({
      stream: { kind: "hls", url: "/h", renditions: [{}, { height: 480 }] },
    });
    expect(model.options.map((o) => o.id)).toEqual(["auto", "480"]);
  });
});

describe("playbackDurationSeconds (CORE-2314)", () => {
  it("prefers the probed duration over a growing transcode duration", () => {
    expect(playbackDurationSeconds(7200, 268)).toBe(7200);
  });
  it("falls back to the stream duration when the server does not know it", () => {
    expect(playbackDurationSeconds(undefined, 3273)).toBe(3273);
    expect(playbackDurationSeconds(0, 3273)).toBe(3273);
  });
  it("never returns NaN or a negative length", () => {
    expect(playbackDurationSeconds(undefined, Number.NaN)).toBe(0);
    expect(playbackDurationSeconds(-5, Number.POSITIVE_INFINITY)).toBe(0);
  });
});

describe("resolution labels (CORE-2485)", () => {
  it("names HDR formats the way players do", () => {
    expect(hdrName("dolby-vision-p8")).toBe("Dolby Vision");
    expect(hdrName("hdr10plus")).toBe("HDR10+");
    expect(hdrName("hlg")).toBe("HLG");
    expect(hdrName(undefined)).toBe("");
  });

  it("prefers the server's resolution class for a rendition", () => {
    const hls = {
      kind: "hls",
      url: "/h",
      renditions: [
        { id: "a", height: 1600, resolution: "4K" },
        { id: "b", height: 720 },
      ],
    };
    const model = buildRenditionMenuModel({ stream: hls });
    expect(model.options.map((o) => o.label)).toContain("4K");
    expect(model.options.map((o) => o.label)).toContain("720p");
  });

  it("shows the source video's resolution, HDR and frame size", () => {
    const info = {
      streams: [],
      sourceVideo: { resolution: "4K", width: 3840, height: 1600, hdr: "hdr10" },
    } as unknown as PlaybackInfo;
    const rows = buildRouteDetailModel({ stream: { kind: "direct-play", url: "/d" }, info });
    expect(rows.find((r) => r.labelKey === "infoSource")?.value).toBe("4K · HDR10 · 3840×1600");
  });

  it("omits the source row when the server does not know the video", () => {
    const rows = buildRouteDetailModel({
      stream: { kind: "direct-play", url: "/d" },
      info: { streams: [] } as unknown as PlaybackInfo,
    });
    expect(rows.some((r) => r.labelKey === "infoSource")).toBe(false);
  });
});
