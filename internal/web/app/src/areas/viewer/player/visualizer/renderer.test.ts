import { describe, expect, it } from "vitest";
import { silentFrame } from "./analysis";
import {
  BLACK_S,
  FADE_S,
  frameSize,
  limiterWindow,
  pickFeedbackFormat,
  QUALITY_LADDER,
  Renderer,
  renderQuality,
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
// records deletions, blits, clears and uniform blocks; enough to drive the renderer's frames.
function recordingGl() {
  let next = 0;
  const deleted = new Set<unknown>();
  const blits: number[][] = [];
  const bound: { f: unknown } = { f: null };
  const spent: [unknown, number[]][] = [];
  const clears: unknown[] = [];
  const fills: [unknown, number, number[]][] = [];
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
      if (key === "clearBufferfv")
        return (_: unknown, i: number, v: ArrayLike<number>) =>
          fills.push([bound.f, i, Array.from(v)]);
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
  return { gl, deleted, blits, canvas, spent, clears, fills, blocks, grant, reads };
}

// One rule covers every change to what the viewer sees (CORE-2516): the frame on screen fades to
// black through the limiter, black is presented, and a frame starts at the geometry asked for
// then, black with every budget spent. Nothing is carried from one geometry to the next.
describe("Renderer geometry (CORE-2516)", () => {
  type Limiter = { fG: unknown[]; fOut: unknown[]; cur: number };
  type Texels = [width: number, height: number, texel: number];
  const SPENT = [256, 65025, 65025, 0];
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
      limiterSpent: false,
      refillCarry: 0,
      undo: null,
      limiter: new Array(6),
      level: QUALITY_LADDER.length - 1,
      size: null,
      feedbackFormat: { internal: 0, type: 0, label: "float" },
      endSwitch: () => undefined,
      fadeLeft: 0,
      black: restored ? BLACK_S : 0,
      blackShown: false,
    });
    const renderer = r as unknown as Renderer;
    const draw = (width: number, height: number, dt = 1 / 60, frameMs = dt * 1000) =>
      renderer.draw(width, height, dt, silentFrame(), false, frameMs);
    // A frame of texels in a slot of slotWidth x slotHeight physical pixels, shown for shownMs.
    const drawIn = (
      [width, height, texel]: Texels,
      [slotWidth, slotHeight]: [number, number],
      shownMs = 1000 / 60,
    ) =>
      renderer.draw(
        width,
        height,
        shownMs / 1000,
        silentFrame(),
        false,
        shownMs,
        texel,
        shownMs,
        slotWidth,
        slotHeight,
      );
    // Draws of a changed geometry an eighth of a second apart: one starts the fade, sixteen run
    // it for FADE_S and end in black, and one more, the black shown for BLACK_S, starts the frame.
    const through = (frame: (shownMs: number) => void) => {
      for (let i = 0; i < 18; i++) frame(125);
    };
    const limiter = () => (r.size as { limiter: Limiter }).limiter;
    return { r, renderer, draw, drawIn, through, limiter, ...recorded };
  };

  // Codex round 24: the host cannot know what the canvas replaced on screen, so the first frame
  // starts with every budget spent, like any other.
  it("starts the output black, the trackers tracked black and every budget spent", () => {
    const { draw, limiter, spent, canvas, blocks } = make();
    draw(640, 360);
    const L = limiter();
    expect([canvas.width, canvas.height]).toEqual([640, 360]);
    for (const f of L.fOut) expect(spent).toContainEqual([f, [0, 0, 0, 0]]);
    // The grant state the first frame read.
    expect(spent.at(-1)).toEqual([L.fG[L.cur ^ 1], SPENT]);
    expect(spent.slice(0, -1).every(([, v]) => v.join() === "0,0,0,0")).toBe(true);
    // Codex round 17: the first draw earns no credit before a budget write it could repay.
    expect(blocks.at(-1)?.[3]).toBe(0);
  });

  it("presents black after a restored context, timed from the first black frame shown", () => {
    const { r, renderer, draw, limiter, blits, spent, clears, blocks } = make(true);
    // This interval preceded the first black frame shown and cannot count toward the hold.
    draw(640, 360, BLACK_S);
    expect(r.size).toBe(null);
    expect(clears).toEqual([null]);
    expect(renderer.settled(640, 360, 1)).toBe(false);

    draw(640, 360, BLACK_S / 2);
    expect(r.size).toBe(null);
    expect(clears).toEqual([null, null]);
    expect(blits).toEqual([]);
    expect(blocks).toEqual([]);
    expect(renderer.settled(640, 360, 1)).toBe(false);

    draw(640, 360, BLACK_S / 2);
    const L = limiter();
    for (const f of L.fOut) expect(spent).toContainEqual([f, [0, 0, 0, 0]]);
    expect(spent.at(-1)).toEqual([L.fG[L.cur ^ 1], SPENT]);
    expect(clears).toHaveLength(3); // the canvas is cleared before the visible blit
    expect(blits).toEqual([[0, 0, 640, 360, 0, 0, 640, 360]]);
    expect(blocks.at(-1)?.[3]).toBe(0);
    expect(renderer.settled(640, 360, 1)).toBe(true);
    expect(BLACK_S).toBeGreaterThan(1 / 15);
  });

  // Codex round 18: bits 4 to 7 of the grant are the directions held. A grant that admits one
  // direction and holds none is settled, though the other admission bits are clear.
  it("keeps a paused restore drawing until the grant holds no direction", () => {
    const { renderer, draw, limiter, grant, reads } = make(true);
    draw(640, 360, BLACK_S);
    expect(renderer.settled(640, 360, 1)).toBe(false);
    expect(reads).toHaveLength(0);

    grant.set([0x1e, 65025, 0, 0]);
    draw(640, 360, BLACK_S);
    const L = limiter();
    expect(reads).toHaveLength(0);
    expect(renderer.settled(640, 360, 1)).toBe(false);
    expect(reads).toEqual([L.fG[L.cur]]);

    grant[0] = 0x01;
    draw(640, 360);
    expect(reads).toHaveLength(1);
    expect(renderer.settled(640, 360, 1)).toBe(true);
    expect(reads.at(-1)).toBe(L.fG[L.cur]);
    expect(renderer.settled(640, 360, 1)).toBe(true);
    expect(reads).toHaveLength(2);
  });

  it("settles only for the geometry on screen", () => {
    const { renderer, draw } = make();
    draw(60, 36);
    expect(renderer.settled(60, 36, 1)).toBe(true);
    expect(renderer.settled(65, 36, 1)).toBe(false);
    expect(renderer.settled(60, 36, 2)).toBe(false);
    expect(renderer.settled(60, 36, 1, 59, 36)).toBe(false);
  });

  it("limits a clipped 60x36 slot by its visible field at the minimum backing size", () => {
    const full = make();
    full.draw(64, 36);
    expect(full.blocks.at(-1)?.[2]).toBe(63);
    const { draw, canvas, blocks } = make();
    draw(60, 36);
    expect([canvas.width, canvas.height]).toEqual([64, 36]);
    expect(blocks.at(-1)?.[2]).toBe(60);
  });

  // Codex round 17: the canvas showed the whole backing, so a slot growing between draws revealed
  // hidden pixels for a paint. GL rows run bottom-up, so the visible rows are the top ones.
  it("presents only the texels in view over black", () => {
    const { draw, blits, clears } = make();
    draw(32, 30);
    expect(blits.at(-1)).toEqual([0, 6, 32, 36, 0, 6, 32, 36]);
    expect(clears.at(-1)).toBe(null);
  });

  // Codex rounds 21 to 24: a texel the slot cuts is partly on screen, and its hidden pixels would
  // appear unjudged when the slot grows. It is never shown.
  it("shows no texel the slot cuts", () => {
    const { renderer, drawIn, blits, blocks } = make();
    drawIn([64, 36, 2], [5, 71]);
    expect(blits.at(-1)).toEqual([0, 1, 2, 36, 0, 1, 2, 36]);
    expect(blocks.at(-1)?.[2]).toBe(limiterWindow(64, 36, 2, 2, 35).limit);
    expect(renderer.shown).toEqual({
      width: 64,
      height: 36,
      texel: 2,
      viewWidth: 5,
      viewHeight: 71,
    });
    // A slot that moves within the texel it cut has the same texels in view.
    expect(renderer.settled(64, 36, 2, 4, 70)).toBe(true);
    expect(renderer.settled(64, 36, 2, 6, 71)).toBe(false);
  });

  // The fade: the frame on screen stays as it is, the limiter is fed black with its budgets
  // spent and an area limit of zero, and nothing is reallocated or copied.
  it("fades the frame on screen to black through the limiter when the geometry changes", () => {
    const { r, renderer, draw, limiter, spent, blits, blocks, fills, canvas } = make();
    draw(640, 360);
    draw(640, 360);
    const size = r.size;
    const L = limiter();
    const cur = L.cur;
    const before = spent.length;
    blits.length = 0;
    fills.length = 0;
    draw(800, 450);
    expect(r.size).toBe(size);
    expect(r.fadeLeft).toBe(FADE_S);
    expect([canvas.width, canvas.height]).toEqual([640, 360]);
    expect(blits).toEqual([[0, 0, 640, 360, 0, 0, 640, 360]]);
    // Black replaces the limiter's input.
    expect(fills.map(([, i, v]) => [i, v])).toEqual([[0, [0, 0, 0, 1]]]);
    // One spend, in the grant state the frame read; area limit and refill of zero.
    expect(spent.slice(before)).toEqual([[L.fG[cur], SPENT]]);
    expect(blocks.at(-1)?.slice(2, 4)).toEqual([0, 0]);
    expect(renderer.settled(800, 450, 1)).toBe(false);
    expect(renderer.settled(640, 360, 1)).toBe(false);
  });

  // Codex rounds 24 and 25: a resize during the fade spent the budgets again while the fade ran
  // on, and a draw back at the old geometry cancelled it. A change during the fade moves the
  // target and nothing else.
  it("only moves the target when the geometry changes during the fade", () => {
    const { r, draw, spent, blocks, canvas } = make();
    draw(640, 360);
    draw(800, 450, 0.125);
    const size = r.size;
    const before = spent.length;
    draw(640, 360, 0.125);
    expect(r.fadeLeft).toBe(FADE_S - 0.125);
    draw(700, 400, 0.125);
    draw(640, 360, 0.125);
    expect(r.fadeLeft).toBe(FADE_S - 0.375);
    expect(r.size).toBe(size);
    expect(spent).toHaveLength(before);
    expect(blocks.at(-1)?.[2]).toBe(0);
    expect(blocks.at(-1)?.[3]).toBeGreaterThan(0);
    expect([canvas.width, canvas.height]).toEqual([640, 360]);
  });

  it("starts once, at the geometry asked for when the black ends", () => {
    const { r, renderer, draw, limiter, spent, clears, blits, blocks, canvas, deleted } = make();
    draw(640, 360);
    const old = limiter();
    draw(800, 450);
    for (let i = 0; i < 15; i++) draw(900 + i, 500, 0.125);
    expect(r.fadeLeft).toBe(0.125);
    blits.length = 0;
    clears.length = 0;
    blocks.length = 0;
    // The fade is over: the frame is freed and black is presented.
    draw(1000, 560, 0.125);
    expect(r.size).toBe(null);
    expect(renderer.shown).toBe(null);
    expect([...old.fOut, ...old.fG].every((f) => deleted.has(f))).toBe(true);
    expect([canvas.width, canvas.height]).toEqual([640, 360]);
    draw(1100, 620, BLACK_S / 2);
    expect(r.size).toBe(null);
    expect(clears).toEqual([null, null]);
    expect(blits).toEqual([]);
    expect(blocks).toEqual([]);

    draw(1200, 675, BLACK_S / 2);
    const L = limiter();
    expect([canvas.width, canvas.height]).toEqual([1200, 675]);
    for (const f of L.fOut) expect(spent).toContainEqual([f, [0, 0, 0, 0]]);
    expect(spent.at(-1)).toEqual([L.fG[L.cur ^ 1], SPENT]);
    expect(blocks.at(-1)?.slice(2, 4)).toEqual([limiterWindow(1200, 675).limit, 0]);
    expect(blits).toEqual([[0, 0, 1200, 675, 0, 0, 1200, 675]]);
    expect(r.fadeLeft).toBe(0);
  });

  // Codex rounds 18 and 22: the fade runs on the time the limiter is credited with, not the
  // clamped step or the frame interval, so the fall's grant has come by its end.
  it("fades on the credited time", () => {
    const { r, renderer, draw } = make();
    draw(64, 36);
    renderer.draw(64, 36, 0.1, silentFrame(), false, 200, 2, 33);
    expect(r.fadeLeft).toBe(FADE_S);
    renderer.draw(64, 36, 0.1, silentFrame(), false, 200, 2, 33);
    expect(r.fadeLeft).toBeCloseTo(FADE_S - 0.033);
    // A frame credits the limiter one token's refill at most, however long it was shown.
    renderer.draw(64, 36, 0.1, silentFrame(), false, 1000, 2, 1000);
    expect(r.fadeLeft).toBeCloseTo(FADE_S - 0.033 - 1 / 6);
  });

  it("fades for FADE_S, longer than the red token's refill", () => {
    const { r, draw } = make();
    draw(64, 36);
    draw(80, 36);
    let frames = 0;
    while (r.size) {
      draw(80, 36);
      frames++;
    }
    expect(frames).toBeGreaterThanOrEqual(FADE_S * 60);
    expect(frames).toBeLessThanOrEqual(FADE_S * 60 + 1);
    expect(FADE_S).toBeGreaterThan(8 / 6);
  });

  // Codex round 22: shrinking a clipped slot from 1 x 120 to 1 x 100 kept an area limit from the
  // old view. The frame that starts takes its limit from the texels in view then.
  it("limits a clipped slot that shrank by the texels in view", () => {
    const { drawIn, through, blocks } = make();
    drawIn([1, 120, 1], [1, 120]);
    expect(blocks.at(-1)?.[2]).toBe(limiterWindow(64, 120, 1, 1, 120).limit);
    drawIn([1, 100, 1], [1, 100]);
    expect(blocks.at(-1)?.[2]).toBe(0);
    through((ms) => drawIn([1, 100, 1], [1, 100], ms));
    expect(blocks.at(-1)?.[2]).toBe(8);
    expect(limiterWindow(64, 100, 1, 1, 100).limit).toBe(8);
  });

  // Codex round 22: a drop in the display scale cleared pixels in view (DPR 5, a 3 x 120 slot to
  // 2 x 120) or dropped a texel the new slot cut (DPR 4, 2 x 76 to 1 x 75). The frame on screen
  // fades whole at its own scale instead, whatever the new one.
  it("fades the frame on screen at its own scale when the display scale drops", () => {
    const drop = (from: Texels, slot: [number, number], to: Texels, next: [number, number]) => {
      const { r, renderer, drawIn, blits, canvas } = make();
      drawIn(from, slot);
      const size = r.size;
      const backing = [canvas.width, canvas.height];
      blits.length = 0;
      drawIn(to, next);
      expect(r.size).toBe(size);
      expect(r.fadeLeft).toBe(FADE_S);
      expect([canvas.width, canvas.height]).toEqual(backing);
      return { blits, shown: renderer.shown };
    };
    const odd = drop([1, 40, 3], [3, 120], [1, 60, 2], [2, 120]);
    expect(odd.blits).toEqual([[0, 0, 1, 40, 0, 0, 1, 40]]);
    expect(odd.shown).toMatchObject({ texel: 3, viewWidth: 2, viewHeight: 120 });
    const divisor = drop([1, 38, 2], [2, 76], [1, 75, 1], [1, 75]);
    expect(divisor.blits).toEqual([[0, 0, 1, 38, 0, 0, 1, 38]]);
    expect(divisor.shown).toMatchObject({ texel: 2, viewWidth: 1, viewHeight: 75 });
  });

  // Codex round 22: a 1 x 8192 slot grown to 8193 needs 2-pixel texels and has no whole one in
  // view. The frame on screen fades through the limiter; the slot then stays black.
  it("fades to black with no frame when the slot has no whole texel", () => {
    const { r, renderer, drawIn, through, clears } = make();
    drawIn([1, 8192, 1], [1, 8192]);
    const size = r.size;
    drawIn([1, 4096, 2], [1, 8193]);
    expect(r.size).toBe(size);
    expect(r.fadeLeft).toBe(FADE_S);
    for (let i = 0; i < 16; i++) drawIn([1, 4096, 2], [1, 8193], 125);
    expect(r.size).toBe(null);
    expect(renderer.settled(1, 4096, 2, 1, 8193)).toBe(false);
    drawIn([1, 4096, 2], [1, 8193], BLACK_S * 1000);
    expect(r.size).toBe(null);
    expect(clears.at(-1)).toBe(null);
    expect(renderer.settled(1, 4096, 2, 1, 8193)).toBe(true);
    expect(renderer.settled(1, 8192, 1, 1, 8192)).toBe(false);
    through((ms) => drawIn([1, 4096, 2], [1, 8193], ms));
    expect(r.size).toBe(null);
  });

  // Codex round 20: a slot collapsed to no pixels and reopened at the old size must not reveal
  // the held frame against an unspent budget.
  it("starts again from black when a collapsed slot reopens", () => {
    const { r, renderer, drawIn, through, limiter, spent, blocks } = make();
    drawIn([64, 36, 1], [64, 36]);
    drawIn([64, 36, 1], [64, 36]);
    const size = r.size;
    through((ms) => drawIn([1, 1, 1], [0, 0], ms));
    expect(r.size).toBe(null);
    expect(renderer.settled(1, 1, 1, 0, 0)).toBe(true);
    expect(renderer.settled(64, 36, 1)).toBe(false);
    drawIn([64, 36, 1], [64, 36]);
    expect(r.size).not.toBe(size);
    const L = limiter();
    expect(spent.at(-1)).toEqual([L.fG[L.cur ^ 1], SPENT]);
    expect(blocks.at(-1)?.[3]).toBe(0);
  });

  // Codex round 24: at two physical pixels per texel, a slot narrowed from 4 to 3 pixels and
  // widened again showed the fourth pixel column in its texel's colour with no tracker event.
  // The view of a frame only shrinks: the column comes back with the frame that starts, black.
  it("keeps a pixel the slot hid out of view until a frame starts", () => {
    const { r, renderer, drawIn, limiter, spent } = make();
    const at = (slotWidth: number, shownMs?: number) =>
      drawIn([64, 36, 2], [slotWidth, 72], shownMs);
    at(4);
    const size = r.size;
    expect(renderer.shown).toMatchObject({ width: 64, texel: 2, viewWidth: 4, viewHeight: 72 });
    at(3);
    expect(r.fadeLeft).toBe(FADE_S);
    expect(renderer.shown).toMatchObject({ viewWidth: 3, viewHeight: 72 });
    for (let i = 0; i < 15; i++) at(4, 125);
    expect(r.size).toBe(size);
    expect(renderer.shown).toMatchObject({ viewWidth: 3, viewHeight: 72 });
    at(4, 125);
    expect(renderer.shown).toBe(null);
    at(4, BLACK_S * 1000);
    expect(r.size).not.toBe(size);
    expect(renderer.shown).toMatchObject({ viewWidth: 4, viewHeight: 72 });
    const L = limiter();
    expect(spent.at(-1)).toEqual([L.fG[L.cur ^ 1], SPENT]);
  });

  // Codex round 18: a slot resized between a draw and its paint withdraws the frame, so nothing
  // judged against the old area limit is shown in the new one.
  it("withdraws the last frame to the limiter's previous output and state", () => {
    const { r, renderer, draw, limiter, blits, blocks } = make();
    draw(64, 36);
    draw(64, 36, 1 / 60, 50);
    const L = limiter();
    const cur = L.cur;
    const clock = r.clockMs;
    const carry = r.refillCarry;
    draw(64, 36, 1 / 60, 40);
    const shown = blits.length;
    renderer.withdraw();
    expect(L.cur).toBe(cur);
    expect(r.clockMs).toBe(clock);
    expect(r.refillCarry).toBe(carry);
    expect(blits).toHaveLength(shown + 1);
    expect(blits.at(-1)).toEqual([0, 0, 64, 36, 0, 0, 64, 36]);
    renderer.withdraw();
    expect(blits).toHaveLength(shown + 1);
    draw(64, 36, 1 / 60, 30);
    expect(blocks.at(-1)?.[5]).toBe(30);
    expect(L.cur).toBe(cur ^ 1);
  });

  // Codex rounds 23 and 25: withdrawn draws ran the fade down without crediting the limiter, and
  // a withdrawn draw that had cancelled the fade resumed too short a one. A frame of the fade is
  // safe in any slot, so it stands, with its fade time and its credit.
  it("lets a frame of the fade stand when it is withdrawn", () => {
    const { r, renderer, draw, limiter, blits } = make();
    draw(64, 36);
    draw(80, 36, 0.125);
    draw(80, 36, 0.125);
    const cur = limiter().cur;
    const clock = r.clockMs;
    const shown = blits.length;
    renderer.withdraw();
    expect(limiter().cur).toBe(cur);
    expect(r.clockMs).toBe(clock);
    expect(r.fadeLeft).toBe(FADE_S - 0.125);
    expect(blits).toHaveLength(shown);
  });

  it("withdraws nothing after a draw that presented black", () => {
    const { r, renderer, draw, blits } = make(true);
    draw(640, 360, BLACK_S / 2);
    renderer.withdraw();
    expect(r.size).toBe(null);
    expect(blits).toEqual([]);
  });

  // Codex round 24: a withdrawn first draw left the grant word the host wrote spent. Its held
  // bits are clear because it has judged no frame, so a paused player held black.
  it("does not settle on a grant the host wrote spent", () => {
    const { renderer, draw, reads } = make();
    draw(64, 36);
    renderer.withdraw();
    expect(renderer.settled(64, 36, 1)).toBe(false);
    expect(reads).toHaveLength(0);
    draw(64, 36);
    expect(renderer.settled(64, 36, 1)).toBe(true);
  });

  // Codex round 18: the limiter ages evidence by the time frames are shown apart, which a late
  // callback's timestamp overstates, while the governor keeps the raw interval.
  it("times the limiter by the shown interval", () => {
    const { renderer, draw, blocks } = make();
    draw(64, 36);
    renderer.draw(64, 36, 0.1, silentFrame(), false, 183, 1, 33);
    expect(blocks.at(-1)?.[5]).toBe(33);
  });

  it("refills from the intervals after the frame that started", () => {
    const { draw, blocks } = make();
    draw(800, 450, 0.1);
    expect(blocks.at(-1)?.[3]).toBe(0);
    draw(800, 450, 0.05);
    expect(blocks.at(-1)?.[3]).toBe(19507);
    draw(800, 450, 0.1, 300);
    expect(blocks.at(-1)?.[3]).toBe(65024);
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

  it("takes the area limit from the texels in view", () => {
    expect(limiterWindow(64, 100, 1, 1, 100).limit).toBe(Math.floor((1 * 33) / 4));
    expect(limiterWindow(64, 40, 3, 1, 40).limit).toBe(Math.floor((1 * 40) / 4 / 9));
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
