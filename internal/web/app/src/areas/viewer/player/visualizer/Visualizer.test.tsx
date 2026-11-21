import { render } from "@testing-library/react";
import { createRef } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AudioStage } from "../AudioStage";
import { easeFrame, silentFrame } from "./analysis";
import {
  backingSize,
  canVisualize,
  lagBound,
  lagBounds,
  lagHistory,
  type Rotation,
  recordLag,
  slotPixels,
  takeUpcoming,
  Visualizer,
  viewClip,
} from "./Visualizer";
import { chosenVisual, VISUALS, visualFor } from "./visuals";

// The Oscillux core never loads in jsdom: a pending load keeps the visualizer mounted and idle.
vi.mock("./oscillux", () => ({
  loadOscillux: () => new Promise(() => {}),
  shaderSource: () => new Promise(() => {}),
}));

const direct = { kind: "direct-play", url: "/api/media/m1/stream" };

function withWebGL2() {
  vi.stubGlobal("WebGL2RenderingContext", class {});
  vi.stubGlobal("OfflineAudioContext", class {});
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("Visualizer (CORE-1309)", () => {
  it("mounts a canvas in the audio stage's slot for a decodable track", () => {
    withWebGL2();
    const { container } = render(
      <AudioStage
        title="Song"
        artworkURL=""
        credits={[]}
        videoRef={createRef<HTMLVideoElement>()}
        stream={direct}
        trackKey="m1"
      />,
    );
    const slot = container.querySelector("[data-visualizer-slot]");
    expect(slot?.getAttribute("aria-hidden")).toBe("true");
    expect(slot?.querySelector("canvas.bf-audio-visualizer-canvas")).toBeTruthy();
  });

  // Codex round 25: the slot showed the theme's scrim behind the canvas, so a strip that came
  // into view rose from a level its tracker, which starts at black, did not hold.
  it("paints the slot black behind the canvas, and not once the canvas is gone", () => {
    withWebGL2();
    const stage = (stream: typeof direct) => (
      <AudioStage
        title="Song"
        artworkURL=""
        credits={[]}
        videoRef={createRef<HTMLVideoElement>()}
        stream={stream}
        trackKey="m1"
      />
    );
    const { container, rerender } = render(stage(direct));
    const slot = container.querySelector<HTMLElement>("[data-visualizer-slot]");
    expect(slot?.querySelector("canvas")).toBeTruthy();
    expect(slot?.style.backgroundColor).toBe("rgb(0, 0, 0)");
    rerender(stage({ kind: "transcode", url: "/api/media/m1/master.m3u8" }));
    expect(slot?.querySelector("canvas")).toBeNull();
    expect(slot?.style.backgroundColor).toBe("");
  });

  it("leaves the slot empty without WebGL2", () => {
    expect(canVisualize()).toBe(false);
    const { container } = render(
      <Visualizer videoRef={createRef<HTMLVideoElement>()} stream={direct} trackKey="m1" />,
    );
    expect(container.querySelector("canvas")).toBeNull();
  });

  // Codex round 19: without an observer a resize before the paint could show an over-limit frame.
  it("leaves the slot empty without ResizeObserver", () => {
    withWebGL2();
    vi.stubGlobal("ResizeObserver", undefined);
    expect(canVisualize()).toBe(false);
    const { container } = render(
      <Visualizer videoRef={createRef<HTMLVideoElement>()} stream={direct} trackKey="m1" />,
    );
    expect(container.querySelector("canvas")).toBeNull();
  });

  it("leaves the slot empty for a stream it cannot decode", () => {
    withWebGL2();
    for (const stream of [
      null,
      { kind: "transcode", url: "/api/media/m1/master.m3u8" },
      { kind: "direct-play", url: "https://elsewhere.example/a.flac" },
    ]) {
      const { container, unmount } = render(
        <Visualizer videoRef={createRef<HTMLVideoElement>()} stream={stream} trackKey="m1" />,
      );
      expect(container.querySelector("canvas")).toBeNull();
      unmount();
    }
  });
});

describe("Visualizer track change (CORE-2218)", () => {
  it("keeps the canvas while the next source loads", () => {
    withWebGL2();
    const ref = createRef<HTMLVideoElement>();
    const { container, rerender } = render(
      <Visualizer videoRef={ref} stream={direct} trackKey="m1" />,
    );
    const canvas = container.querySelector("canvas");
    expect(canvas).toBeTruthy();
    rerender(<Visualizer videoRef={ref} stream={null} trackKey="m2" />);
    expect(container.querySelector("canvas")).toBe(canvas);
    rerender(
      <Visualizer videoRef={ref} stream={{ kind: "transcode", url: "/x.m3u8" }} trackKey="m2" />,
    );
    expect(container.querySelector("canvas")).toBeNull();
  });
});

describe("easeFrame", () => {
  it("glides levels and spectrum and takes the beat clock as it is", () => {
    const shown = silentFrame();
    const target = silentFrame();
    target.values.bass = 1;
    target.values.beatCount = 7;
    target.spectrum[3] = 0.8;
    easeFrame(shown, target, 0.25);
    expect(shown.values.bass).toBeCloseTo(0.25);
    expect(shown.spectrum[3]).toBeCloseTo(0.2);
    expect(shown.values.beatCount).toBe(7);
    easeFrame(shown, target, 1);
    expect(shown.values.bass).toBe(1);
  });
});

describe("visuals", () => {
  it("lists every visual in the pinned release with its default tuning", () => {
    expect(VISUALS.length).toBeGreaterThan(0);
    for (const v of VISUALS) {
      expect(v.id).toMatch(/^[a-z0-9-]+$/);
      expect(v.params.every(Number.isFinite)).toBe(true);
      expect(Number.isFinite(v.paletteOffset)).toBe(true);
    }
  });

  it("picks a stable visual per track", () => {
    expect(visualFor("m1")).toBe(visualFor("m1"));
    const picked = new Set(Array.from({ length: 64 }, (_, i) => visualFor(`track-${i}`).id));
    expect(picked.size).toBeGreaterThan(1);
  });
});

describe("rotation (CORE-2368)", () => {
  const rotation = (upcoming: (typeof VISUALS)[number] | null): Rotation => ({
    rotating: true,
    played: 0,
    at: 40,
    upcoming,
  });
  const calm = VISUALS.find((v) => v.calm);
  const lively = VISUALS.find((v) => !v.calm);

  it("moves to the visual compiled ahead, once", () => {
    const r = rotation(VISUALS[1]);
    expect(takeUpcoming(r, VISUALS[0], false)).toBe(VISUALS[1]);
    expect(r.upcoming).toBeNull();
  });

  it("picks afresh when the one compiled ahead no longer fits", () => {
    expect(takeUpcoming(rotation(VISUALS[0]), VISUALS[0], false)).not.toBe(VISUALS[0]);
    if (!calm || !lively) throw new Error("the release has calm and lively visuals");
    expect(takeUpcoming(rotation(lively), calm, true).calm).toBe(true);
  });
});

describe("backingSize", () => {
  it("uses the device-pixel box, one physical pixel a texel, at DPR up to 2", () => {
    expect(backingSize({ inlineSize: 781, blockSize: 439 }, 390.5, 219.5, 2)).toEqual([
      781, 439, 1,
    ]);
  });

  // Codex round 12: a fractional texel interpolates between texels on the display, and the
  // texel size, not the CSS ratio, is the display scale.
  it("covers whole physical pixels with each texel above DPR 2", () => {
    expect(backingSize({ inlineSize: 1200, blockSize: 900 }, 400, 300, 3)).toEqual([600, 450, 2]);
    expect(backingSize({ inlineSize: 1600, blockSize: 1200 }, 400, 300, 4)).toEqual([800, 600, 2]);
    expect(backingSize({ inlineSize: 1200, blockSize: 900 }, 600, 450, 2)).toEqual([1200, 900, 1]);
  });

  it("keeps each side within the limiter's largest frame", () => {
    expect(backingSize({ inlineSize: 10000, blockSize: 5000 }, 10000, 5000, 1)).toEqual([
      5000, 2500, 2,
    ]);
  });

  // Codex round 13: the texels fit inside the box, so a texel never spans a fractional pixel.
  it("rounds the texels down to fit the device box", () => {
    expect(backingSize({ inlineSize: 1201, blockSize: 901 }, 400.3, 300.3, 3)).toEqual([
      600, 450, 2,
    ]);
  });

  // Codex rounds 22 and 23: a slot one pixel wide that grows past MAX_SIDE needs a texel wider
  // than the slot. The renderer shows whole texels only, so it fades that slot to black.
  it("keeps the larger texel a side over MAX_SIDE needs, though the slot cuts it", () => {
    const device = { inlineSize: 1, blockSize: 8193 };
    expect(backingSize(device, 1, 8193, 1)).toEqual([1, 4096, 2]);
    expect(slotPixels(device, 1, 8193, 1)).toEqual([1, 8193]);
  });

  it("asks for one texel of a slot with no pixels, which shows none", () => {
    const device = { inlineSize: 0, blockSize: 360 };
    expect(backingSize(device, 0, 360, 1)).toEqual([1, 360, 1]);
    expect(slotPixels(device, 0, 360, 1)).toEqual([0, 360]);
  });

  it("falls back to the CSS box, and to DPR 1 for a bad ratio", () => {
    expect(backingSize(null, 400.4, 300.6, 1.5)).toEqual([600, 450, 1]);
    expect(backingSize(null, 400, 300, 0)).toEqual([400, 300, 1]);
  });
});

// Codex round 24: the canvas shows no pixel past the view the renderer reports for its frame.
describe("viewClip", () => {
  const shown = { width: 64, height: 36, texel: 2, visibleWidth: 64, visibleHeight: 36 };

  it("clips nothing when the whole frame is in view", () => {
    expect(viewClip(shown, 4)).toBe("");
  });

  it("clips every texel the slot cut when the frame started", () => {
    expect(viewClip({ ...shown, visibleWidth: 1 }, 4)).toBe("inset(0 31.5px 0px 0)");
    expect(viewClip({ ...shown, visibleWidth: 1, visibleHeight: 35 }, 1)).toBe(
      "inset(0 126px 2px 0)",
    );
    expect(viewClip({ ...shown, visibleWidth: 0, visibleHeight: 0 }, 2)).toBe(
      "inset(0 64px 36px 0)",
    );
  });
});

describe("chosenVisual", () => {
  const calm = VISUALS.find((v) => v.calm);
  const lively = VISUALS.find((v) => !v.calm);

  it("keeps a pinned visual unless it is lively under reduced motion", () => {
    if (!calm || !lively) throw new Error("the release has calm and lively visuals");
    expect(chosenVisual(lively.id, "k", false)).toBe(lively);
    expect(chosenVisual(calm.id, "k", true)).toBe(calm);
    expect(chosenVisual(lively.id, "k", true).calm).toBe(true);
  });

  it("draws a per-key visual from the calm set under reduced motion, stably", () => {
    for (const key of ["a", "album-1", "track-42", "zz"]) {
      expect(chosenVisual("album", key, true).calm).toBe(true);
      expect(chosenVisual("album", key, true)).toBe(visualFor(key, true));
    }
  });
});

describe("lagBound", () => {
  it("holds the largest interval for the budget's second and recency's coverage interval", () => {
    const h = lagHistory();
    recordLag(h, 33, 100);
    for (let t = 105; t <= 1340; t += 5) recordLag(h, 5, t);
    // A late paint at the slow refresh is still allowed for by the budget after the display
    // speeds up, and by recency only for the coverage interval after it.
    expect(lagBounds(h, 1340)).toEqual([35, 7]);
    expect(lagBounds(h, 160)).toEqual([35, 35]);
    // Once it is older than the budget's hold, both follow the faster refresh.
    recordLag(h, 5, 1400);
    expect(lagBounds(h, 1400)).toEqual([7, 7]);
  });

  it("keeps an interval its hold however many frames come after it", () => {
    const h = lagHistory();
    recordLag(h, 16, 0);
    for (let t = 1; t <= 1200; t += 0.5) recordLag(h, 0.5, t);
    expect(lagBound(h, 1200, 1250)).toBe(18);
  });

  it("assumes the slowest cadence before any interval is measured", () => {
    expect(lagBound(lagHistory(), 0, 1250)).toBeCloseTo(1000 / 15, 9);
  });
});
