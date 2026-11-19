import { describe, expect, it, vi } from "vitest";
import { silentFrame } from "./analysis";
import {
  frameSize,
  limiterWindow,
  pickFeedbackFormat,
  QUALITY_LADDER,
  Renderer,
  renderQuality,
  SCALE_BLACK_S,
  SCALE_FADE_S,
  switchAction,
} from "./renderer";

describe("switchAction (CORE-2219)", () => {
  it("blends from one visual to another", () => {
    expect(switchAction("aurora", false, "nebula", false)).toBe("blend");
  });

  it("keeps the visual when it does not change", () => {
    expect(switchAction("aurora", false, "aurora", false)).toBe("same");
  });

  it("starts the first visual at once", () => {
    expect(switchAction(null, false, "aurora", false)).toBe("instant");
  });

  it("changes at once under reduced motion, even mid-switch", () => {
    expect(switchAction("aurora", false, "nebula", true)).toBe("instant");
    expect(switchAction("aurora", true, "nebula", true)).toBe("instant");
  });

  it("queues a request made while a switch runs", () => {
    expect(switchAction("aurora", true, "nebula", false)).toBe("queue");
    expect(switchAction("aurora", true, "aurora", false)).toBe("queue");
  });
});

describe("renderQuality (CORE-2215)", () => {
  it("follows the Oscillux reference host's ladder", () => {
    expect(QUALITY_LADDER.map((s) => s.scale)).toEqual([0.5, 0.67, 0.83, 1]);
    expect(QUALITY_LADDER.map((s) => s.reduce)).toEqual([1, 0.6, 0.25, 0]);
  });

  it("renders at the canvas size and full detail at the top level", () => {
    expect(renderQuality(3840, 2160, 3)).toEqual({ width: 3840, height: 2160, reduce: 0 });
  });

  it("scales the render size down with the level", () => {
    expect(renderQuality(3840, 2160, 0)).toEqual({ width: 1920, height: 1080, reduce: 1 });
    expect(renderQuality(3840, 2160, 1)).toEqual({ width: 2573, height: 1447, reduce: 0.6 });
  });

  it("clamps the level and never renders below one pixel", () => {
    expect(renderQuality(100, 100, 9).width).toBe(100);
    expect(renderQuality(1, 1, -1)).toEqual({ width: 1, height: 1, reduce: 1 });
  });
});

// A GL that offers the named extensions and completes the probe framebuffer only for those in
// `complete`, recording the order the extensions were asked for and what was deleted.
function probeGl(offered: string[], complete: string[]) {
  const asked: string[] = [];
  const deleted: string[] = [];
  let enabled = "";
  const gl = {
    RGBA16F: 0x881a,
    RGBA8: 0x8058,
    HALF_FLOAT: 0x140b,
    UNSIGNED_BYTE: 0x1401,
    TEXTURE_2D: 0xde1,
    FRAMEBUFFER: 0x8d40,
    COLOR_ATTACHMENT0: 0x8ce0,
    FRAMEBUFFER_COMPLETE: 0x8cd5,
    getExtension: (name: string) => {
      asked.push(name);
      if (!offered.includes(name)) return null;
      enabled = name;
      return {};
    },
    createTexture: () => ({ kind: "texture" }),
    createFramebuffer: () => ({ kind: "framebuffer" }),
    bindTexture: () => {},
    bindFramebuffer: () => {},
    texStorage2D: () => {},
    framebufferTexture2D: () => {},
    checkFramebufferStatus: () => (complete.includes(enabled) ? 0x8cd5 : 0x8cd6),
    deleteTexture: () => deleted.push("texture"),
    deleteFramebuffer: () => deleted.push("framebuffer"),
  };
  return { gl: gl as unknown as WebGL2RenderingContext, asked, deleted };
}

describe("pickFeedbackFormat (CORE-2481)", () => {
  const FLOAT = "EXT_color_buffer_float";
  const HALF = "EXT_color_buffer_half_float";

  it("takes RGBA16F through EXT_color_buffer_float first", () => {
    const { gl, asked, deleted } = probeGl([FLOAT, HALF], [FLOAT, HALF]);
    expect(pickFeedbackFormat(gl)).toMatchObject({ internal: 0x881a, label: "float" });
    expect(asked).toEqual([FLOAT]);
    expect(deleted).toEqual(["framebuffer", "texture"]);
  });

  it("falls to EXT_color_buffer_half_float when the float framebuffer is incomplete", () => {
    const { gl, asked, deleted } = probeGl([FLOAT, HALF], [HALF]);
    expect(pickFeedbackFormat(gl)).toMatchObject({ internal: 0x881a, label: "half-float" });
    expect(asked).toEqual([FLOAT, HALF]);
    expect(deleted).toHaveLength(4);
  });

  it("uses the half-float extension when the float one is absent", () => {
    const { gl } = probeGl([HALF], [HALF]);
    expect(pickFeedbackFormat(gl).label).toBe("half-float");
  });

  it("falls to RGBA8 when neither probe completes, releasing both probes", () => {
    const { gl, deleted } = probeGl([FLOAT, HALF], []);
    expect(pickFeedbackFormat(gl)).toEqual({ internal: 0x8058, type: 0x1401, label: "8-bit" });
    expect(deleted).toHaveLength(4);
  });

  it("falls to RGBA8 without either extension", () => {
    const { gl, deleted } = probeGl([], []);
    expect(pickFeedbackFormat(gl).label).toBe("8-bit");
    expect(deleted).toHaveLength(0);
  });
});

// A WebGL2 stand-in that hands out numbered objects, reports every framebuffer complete and
// records deletions; enough to drive the renderer's size bookkeeping.
function recordingGl() {
  let next = 0;
  const deleted = new Set<unknown>();
  const blits: number[][] = [];
  const bound: { f: unknown } = { f: null };
  const spent: [unknown, number[]][] = [];
  const clears: unknown[] = [];
  const blocks: number[][] = [];
  const grant = new Uint32Array([15, 0, 0, 0]);
  const reads: unknown[] = [];
  const canvas = { width: 0, height: 0, clientWidth: 0 };
  const gl = new Proxy({} as Record<string | symbol, unknown>, {
    get(_, key) {
      if (key === "canvas") return canvas;
      if (key === "FRAMEBUFFER_COMPLETE") return 1;
      if (key === "checkFramebufferStatus") return () => 1;
      if (typeof key === "string" && /^[A-Z0-9_]+$/.test(key)) return 0;
      if (typeof key === "string" && key.startsWith("create")) return () => ({ id: ++next });
      if (typeof key === "string" && key.startsWith("delete"))
        return (x: unknown) => deleted.add(x);
      if (key === "blitFramebuffer") return (...a: number[]) => blits.push(a.slice(0, 8));
      if (key === "bindFramebuffer") return (_: unknown, f: unknown) => (bound.f = f);
      if (key === "clearBufferuiv")
        return (_: unknown, __: unknown, v: Uint32Array) => spent.push([bound.f, [...v]]);
      if (key === "clear") return () => clears.push(bound.f);
      if (key === "bufferSubData")
        return (_: unknown, __: unknown, values: Float32Array) => blocks.push([...values]);
      if (key === "readPixels")
        return (...args: [number, number, number, number, number, number, Uint32Array]) => {
          reads.push(bound.f);
          args[6].set(grant);
        };
      return () => undefined;
    },
  });
  return { gl, deleted, blits, canvas, spent, clears, blocks, grant, reads };
}

describe("Renderer resize (CORE-2516)", () => {
  type Sized = {
    width: number;
    height: number;
    limiter: Record<string, unknown> & {
      rows: unknown;
      out: unknown[];
      g: unknown[];
      window: unknown;
    };
  };
  const make = (restored = false) => {
    const { gl, ...recorded } = recordingGl();
    const r = Object.create(Renderer.prototype) as Record<string, unknown>;
    Object.assign(r, {
      gl,
      visual: {},
      time: 0,
      clockMs: 0,
      switchProgress: () => null,
      cadence: { sample: () => null },
      governor: { level: QUALITY_LADDER.length - 1 },
      drawVisual: (_: unknown, feedback: unknown[]) => feedback[1],
      packPresent: () => undefined,
      presentBlock: new Float32Array(92),
      limiterBlock: new Float32Array(8),
      grantWord: new Uint32Array(4),
      grantSettled: true,
      refillCarry: 0,
      limiter: new Array(6),
      level: QUALITY_LADDER.length - 1,
      size: null,
      feedbackFormat: { internal: 0, type: 0, label: "float" },
      endSwitch: () => undefined,
      displayScale: 0,
      black: 0,
      blackShown: false,
      fadeLeft: -1,
      restored,
    });
    const resize = (w: number, h: number, texel = 1) =>
      (r.resize as (w: number, h: number, texel: number) => Sized).call(r, w, h, texel);
    const renderer = r as unknown as Renderer;
    const draw = (width: number, height: number, dt = 1 / 60, frameMs = dt * 1000) =>
      renderer.draw(width, height, dt, silentFrame(), false, frameMs);
    return { r, renderer, resize, draw, ...recorded };
  };

  // Codex round 12: the display scale is the texel's side in physical pixels. A change switches
  // at once, never stretching the old frame, with the output and trackers tracked black, every
  // budget spent and black presented for longer than the coverage interval.
  it("switches a display-scale change at once to black with every budget spent", () => {
    const { r, renderer, resize, blits, spent } = make();
    resize(1200, 900, 1);
    blits.length = 0;
    spent.length = 0;
    // A zoom from DPR 2 to 3 on 1200 physical pixels: 600 texels of 2 pixels each.
    const L = resize(600, 450, 2).limiter as unknown as { fG: unknown[]; cur: number };
    expect(blits).toEqual([]);
    expect(spent.at(-1)).toEqual([L.fG[L.cur], [256, 65025, 65025, 0]]);
    expect(spent.slice(0, -1).every(([, v]) => v.join() === "0,0,0,0")).toBe(true);
    expect(r.black).toBe(SCALE_BLACK_S);
    expect(renderer.settled(600, 450, 2)).toBe(false);
    expect(SCALE_BLACK_S).toBeGreaterThan(1 / 15);
  });

  it("switches when only the texel size changes", () => {
    const { r, resize, spent } = make();
    const a = resize(800, 600, 1);
    spent.length = 0;
    const b = resize(800, 600, 2);
    expect(b).not.toBe(a);
    expect(spent.length).toBeGreaterThan(0);
    expect(r.black).toBe(SCALE_BLACK_S);
  });

  it("presents no black for a resize at one display scale", () => {
    const { r, resize } = make();
    resize(640, 360);
    resize(800, 450);
    expect(r.black).toBe(0);
  });

  // Oscillux shaders/limiter/README.md, "Ping-pong and reset": the limiter's history follows
  // what is on screen, so at one display scale a resize copies the overlap 1:1 from the top-left.
  // The exposed area starts tracked black and the budgets are written spent, so a strobe in
  // the area a resize exposes is not handed a fresh budget (Codex round 11).
  it("copies the limiter's history 1:1 from the top-left and spends when the display scale is unchanged", () => {
    const { resize, blits, spent } = make();
    resize(640, 360);
    blits.length = 0;
    spent.length = 0;
    const L = resize(800, 300).limiter as unknown as { fG: unknown[]; cur: number };
    // Output and trackers: the top 300 rows of the old 640 columns, at the top of the new frame.
    expect(blits).toEqual([
      [0, 60, 640, 360, 0, 0, 640, 300],
      [0, 60, 640, 360, 0, 0, 640, 300],
    ]);
    expect(spent.at(-1)).toEqual([L.fG[L.cur], [256, 65025, 65025, 0]]);
    // The new targets' trackers start tracked black (zero), never "untracked".
    expect(spent.slice(0, -1).every(([, v]) => v.join() === "0,0,0,0")).toBe(true);
  });

  it("starts the output black and the trackers tracked black", () => {
    const { resize, spent } = make();
    resize(640, 360);
    expect(spent.length).toBeGreaterThan(0);
    expect(spent.every(([, v]) => v.join() === "0,0,0,0")).toBe(true);
  });

  it("restores with tracked black and spent grants, timing black from the first shown frame", () => {
    const { r, renderer, resize, draw, blits, spent, clears } = make(true);
    const limit = vi.fn();
    r.limit = limit;

    // This interval preceded the first displayed black frame and cannot count toward the hold.
    draw(640, 360, SCALE_BLACK_S);
    const L = resize(640, 360).limiter as unknown as {
      fG: unknown[];
      fOut: unknown[];
      cur: number;
    };
    for (const f of L.fOut) expect(spent).toContainEqual([f, [0, 0, 0, 0]]);
    expect(spent.at(-1)).toEqual([L.fG[L.cur], [256, 65025, 65025, 0]]);
    expect(clears).toEqual([null]);
    expect(renderer.settled(640, 360, 1)).toBe(false);

    draw(640, 360, SCALE_BLACK_S / 2);
    expect(clears).toEqual([null, null]);
    expect(blits).toEqual([]);
    expect(limit).not.toHaveBeenCalled();
    expect(renderer.settled(640, 360, 1)).toBe(false);

    draw(640, 360, SCALE_BLACK_S / 2);
    expect(clears).toHaveLength(2);
    expect(limit).toHaveBeenCalledOnce();
    expect(blits).toEqual([[0, 0, 640, 360, 0, 0, 640, 360]]);
    expect(renderer.settled(640, 360, 1)).toBe(true);
  });

  it("keeps a paused restore drawing until the grant admits every direction", () => {
    const { renderer, resize, draw, grant, reads, blocks } = make(true);
    draw(640, 360, SCALE_BLACK_S);
    expect(renderer.settled(640, 360, 1)).toBe(false);
    expect(reads).toHaveLength(0);

    grant.set([0x1e, 65025, 0, 0]);
    draw(640, 360, SCALE_BLACK_S);
    const L = resize(640, 360).limiter as unknown as { fG: unknown[]; cur: number };
    expect(reads).toHaveLength(0);
    expect(renderer.settled(640, 360, 1)).toBe(false);
    expect(reads).toEqual([L.fG[L.cur]]);
    expect(blocks.at(-1)?.[3]).toBe(0);

    grant[0] = 0x1f;
    draw(640, 360);
    expect(reads).toHaveLength(1);
    expect(renderer.settled(640, 360, 1)).toBe(true);
    expect(reads.at(-1)).toBe(L.fG[L.cur]);
    expect(renderer.settled(640, 360, 1)).toBe(true);
    expect(reads).toHaveLength(2);
  });

  it("settles at the minimum backing for an undersized slot", () => {
    const { renderer, draw } = make();
    draw(60, 36);
    expect(renderer.settled(60, 36, 1)).toBe(true);
    expect(renderer.settled(65, 36, 1)).toBe(false);
    expect(renderer.settled(60, 36, 2)).toBe(false);
  });

  it("limits a clipped 60x36 slot by its visible field at the minimum backing size", () => {
    const { renderer, draw, canvas, blocks } = make();
    draw(64, 36);
    expect(blocks.at(-1)?.[2]).toBe(63);
    draw(60, 36);
    expect([canvas.width, canvas.height]).toEqual([64, 36]);
    expect(blocks.at(-1)?.[2]).toBe(60);
    expect(renderer.settled(64, 36, 1)).toBe(false);
  });

  it("starts refill at zero on a same-scale resize and uses only subsequent draw intervals", () => {
    const { draw, blocks } = make();
    draw(640, 360);
    draw(800, 450, 0.1);
    expect(blocks.at(-1)?.[3]).toBe(0);

    draw(800, 450, 0.05);
    expect(blocks.at(-1)?.[3]).toBe(19507);
    draw(800, 450, 0.1, 300);
    expect(blocks.at(-1)?.[3]).toBe(65025);
    expect(blocks.at(-1)?.[5]).toBe(300);
  });

  // Codex round 15: flooring each frame's refill lost its fraction, so at 60 Hz ten frames refilled
  // 65020 of a 65025 token and a tiny maxFlashHz at a high frame rate never refilled at all.
  it("carries the fraction of a refill unit to the next frame", () => {
    const { draw, blocks } = make();
    draw(640, 360);
    const refills: number[] = [];
    for (let i = 0; i < 10; i++) {
      draw(640, 360, 1 / 60, 1000 / 60);
      refills.push(blocks.at(-1)?.[3] ?? -1);
    }
    expect(new Set(refills)).toEqual(new Set([6502, 6503]));
    expect(refills.reduce((a, b) => a + b, 0)).toBeGreaterThanOrEqual(65024);
  });

  it("keeps the limiter's budget, carries its trackers and rebuilds its size-dependent targets", () => {
    const { resize, deleted } = make();
    const before = resize(640, 360).limiter;
    const after = resize(800, 450).limiter;
    for (const key of ["s", "c", "w", "g", "fG"]) {
      expect(before[key]).toBeDefined();
      expect(after[key]).toBe(before[key]);
    }
    expect(after.rows).not.toBe(before.rows);
    expect(after.out[0]).not.toBe(before.out[0]);
    expect((after.track as unknown[])[0]).not.toBe((before.track as unknown[])[0]);
    expect(deleted.has(before.rows)).toBe(true);
    expect(before.out.every((t) => deleted.has(t))).toBe(true);
    expect((before.track as unknown[]).every((t) => deleted.has(t))).toBe(true);
    expect(before.g.some((t) => deleted.has(t))).toBe(false);
    expect(after.window).toEqual(limiterWindow(800, 450));
  });
});

describe("limiterWindow", () => {
  // windowOf("wcag", W, H) in Oscillux v0.2.9 tools/shader/limiter-geometry.mjs.
  it.each([
    [320, 180, 7, 4, 1605],
    [331, 193, 7, 4, 1760],
    [1920, 1080, 7, 4, 57600],
    [390, 844, 7, 4, 9132],
    [64, 36, 6, 4, 63],
    [3840, 2160, 7, 4, 230400],
  ])("%i x %i", (w, h, cols, rows, limit) => {
    expect(limiterWindow(w, h)).toEqual({ cols, rows, limit });
  });

  // Codex round 13: 65 x 36 texels shown at 2x is 130 x 72 physical pixels, whose field is
  // 43 x 24 with a limit of 258 pixels; an 11 x 6 patch covers 264, so it is over in texels too.
  it("limits the physical field's area at a display scale", () => {
    const w = limiterWindow(65, 36, 2);
    expect(w.limit).toBe(Math.floor(258 / 4));
    expect(11 * 6).toBeGreaterThan(w.limit);
    expect(limiterWindow(65, 36, 1)).toEqual(limiterWindow(65, 36));
  });
});

describe("Renderer display-scale fade (Codex round 13)", () => {
  const make = () => {
    const r = Object.create(Renderer.prototype) as Record<string, unknown>;
    Object.assign(r, {
      size: { width: 640, height: 360, visibleWidth: 640, visibleHeight: 360 },
      displayScale: 1,
      fadeLeft: -1,
      grantSettled: true,
      refillCarry: 0,
    });
    return {
      r,
      fading: (texel: number, dt: number) =>
        (r.fading as (t: number, d: number) => boolean).call(r, texel, dt),
    };
  };

  it("fades for SCALE_FADE_S at the old scale before switching", () => {
    const { fading } = make();
    expect(fading(1, 1 / 60)).toBe(false);
    let frames = 0;
    while (fading(2, 1 / 60)) frames++;
    expect(frames).toBeGreaterThanOrEqual(SCALE_FADE_S * 60);
    expect(frames).toBeLessThanOrEqual(SCALE_FADE_S * 60 + 1);
    expect(SCALE_FADE_S).toBeGreaterThan(8 / 6);
  });

  it("needs no fade before the first frame or back at the current scale", () => {
    const { r, fading } = make();
    const renderer = r as unknown as Renderer;
    expect(fading(2, 1 / 60)).toBe(true);
    expect(renderer.settled(640, 360, 1)).toBe(false);
    expect(fading(1, 1 / 60)).toBe(false);
    expect(renderer.settled(640, 360, 1)).toBe(true);
    expect(r.fadeLeft).toBe(-1);
    r.displayScale = 0;
    expect(fading(2, 1 / 60)).toBe(false);
  });
});

describe("frameSize", () => {
  it.each([
    [1920, 1080, 1920, 1080],
    [10, 10, 64, 36],
    [16384, 9216, 8192, 4608],
    [9000, 3000, 8192, 2731],
    [3000, 20000, 1229, 8192],
  ])("%ix%i renders at %ix%i", (w, h, ew, eh) => {
    expect(frameSize(w, h)).toEqual([ew, eh]);
  });
});
