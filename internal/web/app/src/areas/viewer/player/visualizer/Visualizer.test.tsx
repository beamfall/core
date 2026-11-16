import { render } from "@testing-library/react";
import { createRef } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AudioStage } from "../AudioStage";
import { easeFrame, silentFrame } from "./analysis";
import { canVisualize, Visualizer } from "./Visualizer";
import { VISUALS, visualFor } from "./visuals";

// The Oscillux core never loads in jsdom: a pending load keeps the visualizer mounted and idle.
vi.mock("./oscillux", () => ({
  loadOscillux: () => new Promise(() => {}),
  shaderSource: () => new Promise(() => {}),
}));

const direct = { kind: "direct-play", url: "/api/media/m1/stream" };

function withWebGL2() {
  vi.stubGlobal("WebGL2RenderingContext", class {});
  vi.stubGlobal("OfflineAudioContext", class {});
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

  it("leaves the slot empty without WebGL2", () => {
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
      expect(v.palette).toHaveLength(4);
    }
  });

  it("picks a stable visual per track", () => {
    expect(visualFor("m1")).toBe(visualFor("m1"));
    const picked = new Set(Array.from({ length: 64 }, (_, i) => visualFor(`track-${i}`).id));
    expect(picked.size).toBeGreaterThan(1);
  });
});
