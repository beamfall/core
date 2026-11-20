import { describe, expect, it, vi } from "vitest";
import { silentFrame } from "./analysis";
import { GPU_BUDGET_MS } from "./cadence";
import {
  BLACK_S,
  FADE_S,
  frameSize,
  limiterWindow,
  pickFeedbackFormat,
  QUALITY_LADDER,
  Renderer,
  renderQuality,
  sameFrame,
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

// A WebGL2 stand-in that hands out numbered objects, reports every framebuffer complete (but the
// status check numbered `count.incompleteAt`) and records deletions, blits, clears and uniform blocks; enough to drive the renderer's frames.
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
  // The grant word readPixels returns: `grant` unless a harness picks another.
  const pick = { word: () => grant };
  // Reads into the pixel pack buffer, by the framebuffer read, and reads into an array, which
  // wait for the GPU. A fence signals at the next poll unless the GPU is busy.
  const reads: unknown[] = [];
  const blocking: unknown[] = [];
  const packed = new Uint32Array(4);
  const gpu = { busy: false };
  const waits: number[] = [];
  // The order of the frames' blits to the canvas, the fences and the pack buffer reads; the
  // framebuffer status checks; and the textures created.
  const order: string[] = [];
  const count = { checks: 0, textures: 0, incompleteAt: 0 };
  const TIMEOUT_EXPIRED = 0x911b;
  const CONDITION_SATISFIED = 0x911c;
  const canvas = { width: 0, height: 0, clientWidth: 0 };
  const gl = new Proxy({} as Record<string | symbol, unknown>, {
    get(_, key) {
      if (key === "canvas") return canvas;
      if (key === "FRAMEBUFFER_COMPLETE") return 1;
      if (key === "TIMEOUT_EXPIRED") return TIMEOUT_EXPIRED;
      if (key === "WAIT_FAILED") return 0x911d;
      if (key === "checkFramebufferStatus")
        return () => (++count.checks === count.incompleteAt ? 0 : 1);
      if (typeof key === "string" && /^[A-Z0-9_]+$/.test(key)) return 0;
      if (key === "createTexture")
        return () => {
          count.textures++;
          return { id: ++next };
        };
      if (typeof key === "string" && key.startsWith("create")) return () => ({ id: ++next });
      if (typeof key === "string" && key.startsWith("delete"))
        return (x: unknown) => deleted.add(x);
      if (key === "blitFramebuffer")
        return (...a: number[]) => {
          order.push("blit");
          blits.push(a.slice(0, 8));
        };
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
        return (
          ...args: [number, number, number, number, number, number, Uint32Array | number]
        ) => {
          if (typeof args[6] !== "number") return blocking.push(bound.f);
          reads.push(bound.f);
          packed.set(pick.word());
        };
      if (key === "fenceSync")
        return () => {
          order.push("fence");
          return { id: ++next };
        };
      if (key === "clientWaitSync")
        return (_: unknown, __: unknown, timeout: number) => {
          waits.push(timeout);
          return gpu.busy ? TIMEOUT_EXPIRED : CONDITION_SATISFIED;
        };
      if (key === "getBufferSubData")
        return (_: unknown, __: unknown, into: Uint32Array) => {
          order.push("sub");
          into.set(packed);
        };
      return () => undefined;
    },
  });
  return {
    gl,
    deleted,
    blits,
    canvas,
    spent,
    clears,
    fills,
    blocks,
    grant,
    pick,
    reads,
    blocking,
    gpu,
    waits,
    order,
    count,
  };
}

// One rule covers every change to what the viewer sees (CORE-2516): the frame on screen fades to
// black through the limiter, black is presented, and a frame starts at the geometry asked for
// then, black with every budget spent. Nothing is carried from one geometry to the next.
describe("Renderer geometry (CORE-2516)", () => {
  type Limiter = { fG: unknown[]; fOut: unknown[]; cur: number };
  type Texels = [width: number, height: number, texel: number];
  const SPENT = [256, 65025, 65025, 0];
  // A frame starts only at a geometry asked for twice running. The first draw of a test is
  // therefore preceded by the same request, which presents black and starts nothing, unless the
  // renderer is `fresh`: a restored one, whose black comes first anyway, or a mount under test.
  const make = (restored = false, fresh = restored) => {
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
      grantPbo: {},
      grantSync: null,
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
      want: { width: 0, height: 0, texel: 0, visibleWidth: 0, visibleHeight: 0 },
      asked: { width: 0, height: 0, texel: 0, visibleWidth: 0, visibleHeight: 0 },
      incomplete: false,
      fixedChecked: false,
      building: 0,
    });
    const renderer = r as unknown as Renderer;
    // The grant of a frame of a fade holds a direction, as while a lit frame waits for its fall,
    // until a test clears fadeGrant: the fade then runs to FADE_S.
    const fadeGrant = new Uint32Array([0x1f, 0, 0, 0]);
    recorded.pick.word = () => ((r.fadeLeft as number) > 0 ? fadeGrant : recorded.grant);
    let asked = fresh;
    const ask = (width: number, height: number, texel: number, slot: [number, number]) => {
      if (asked) return;
      asked = true;
      renderer.draw(width, height, 0, silentFrame(), false, 0, texel, 0, ...slot);
      recorded.clears.length = 0;
    };
    const draw = (width: number, height: number, dt = 1 / 60, frameMs = dt * 1000) => {
      ask(width, height, 1, [width, height]);
      renderer.draw(width, height, dt, silentFrame(), false, frameMs);
    };
    // A frame of texels in a slot of slotWidth x slotHeight physical pixels, shown for shownMs.
    const drawIn = (
      [width, height, texel]: Texels,
      [slotWidth, slotHeight]: [number, number],
      shownMs = 1000 / 60,
    ) => {
      ask(width, height, texel, [slotWidth, slotHeight]);
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
    };
    // Draws of a changed geometry an eighth of a second apart: one starts the fade, sixteen run
    // it for FADE_S and end in black, and one more, the black shown for BLACK_S, starts the frame.
    const through = (frame: (shownMs: number) => void) => {
      for (let i = 0; i < 18; i++) frame(125);
    };
    const limiter = () => (r.size as { limiter: Limiter }).limiter;
    return { r, renderer, draw, drawIn, through, limiter, fadeGrant, ...recorded };
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
    // The frame is held for the tick its grant is read in, then drawn again.
    expect(renderer.settled(640, 360, 1)).toBe(true);
    expect(reads).toEqual([L.fG[L.cur]]);
    expect(renderer.settled(640, 360, 1)).toBe(false);
    expect(reads).toHaveLength(1);

    grant[0] = 0x01;
    draw(640, 360);
    expect(reads).toHaveLength(1);
    expect(renderer.settled(640, 360, 1)).toBe(true);
    expect(reads.at(-1)).toBe(L.fG[L.cur]);
    expect(renderer.settled(640, 360, 1)).toBe(true);
    expect(renderer.settled(640, 360, 1)).toBe(true);
    expect(reads).toHaveLength(2);
  });

  // Codex round 27: a read into an array waits for the GPU on the main thread. The word goes to
  // a pixel pack buffer behind a fence, polled with no timeout.
  it("holds a paused frame while the GPU reads its grant, without waiting", () => {
    const { renderer, draw, grant, reads, blocking, gpu, waits } = make();
    draw(640, 360);
    grant[0] = 0x1e;
    draw(640, 360);
    gpu.busy = true;
    for (let i = 0; i < 4; i++) expect(renderer.settled(640, 360, 1)).toBe(true);
    expect(reads).toHaveLength(1);
    gpu.busy = false;
    expect(renderer.settled(640, 360, 1)).toBe(false);
    expect(blocking).toEqual([]);
    expect(waits.every((timeout) => timeout === 0)).toBe(true);
  });

  it("reads the grant again after a draw, not the one in flight", () => {
    const { renderer, draw, grant, reads } = make();
    draw(640, 360);
    draw(640, 360);
    expect(renderer.settled(640, 360, 1)).toBe(true);
    // The frame drawn since holds a direction: the read in flight, of a clear word, is dropped.
    grant[0] = 0x1e;
    draw(640, 360);
    expect(renderer.settled(640, 360, 1)).toBe(true);
    expect(reads).toHaveLength(2);
    expect(renderer.settled(640, 360, 1)).toBe(false);
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
    expect(renderer.shown).toMatchObject({
      width: 64,
      height: 36,
      texel: 2,
      visibleWidth: 2,
      visibleHeight: 35,
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

    // The black is over, but the slot is still moving: nothing starts (Codex round 28).
    draw(1200, 675, BLACK_S / 2);
    expect(r.size).toBe(null);
    expect(blits).toEqual([]);
    draw(1200, 675);
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
    expect(odd.shown).toMatchObject({ texel: 3, visibleWidth: 1, visibleHeight: 40 });
    const divisor = drop([1, 38, 2], [2, 76], [1, 75, 1], [1, 75]);
    expect(divisor.blits).toEqual([[0, 0, 1, 38, 0, 0, 1, 38]]);
    expect(divisor.shown).toMatchObject({ texel: 2, visibleWidth: 1, visibleHeight: 38 });
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
    expect(r.size).toBe(null);
    expect(renderer.settled(64, 36, 1)).toBe(false);
    drawIn([64, 36, 1], [64, 36]);
    expect(r.size).not.toBe(size);
    const L = limiter();
    expect(spent.at(-1)).toEqual([L.fG[L.cur ^ 1], SPENT]);
    expect(blocks.at(-1)?.[3]).toBe(0);
  });

  // Codex round 24: at two physical pixels per texel, a slot narrowed from 4 to 3 pixels and
  // widened again showed the fourth pixel column in its texel's colour with no tracker event.
  // Codex round 29: the slot's layout could do the same with no size ever observed. The frame
  // on screen keeps the texels it started with, whatever the slot does, for the host to show
  // (shown): it fades whole, and the frame that starts takes the slot's size then.
  it("keeps the view a frame started with until it is black", () => {
    const { r, renderer, drawIn, limiter, spent } = make();
    const at = (slotWidth: number, shownMs?: number) =>
      drawIn([64, 36, 2], [slotWidth, 72], shownMs);
    at(4);
    const size = r.size;
    const shown = renderer.shown;
    expect(shown).toMatchObject({ width: 64, texel: 2, visibleWidth: 2, visibleHeight: 36 });
    at(3);
    expect(r.fadeLeft).toBe(FADE_S);
    // The same object, unchanged: reading it allocates nothing, and the host refits nothing.
    expect(renderer.shown).toBe(shown);
    for (let i = 0; i < 15; i++) at(i % 2 ? 4 : 1, 125);
    expect(r.size).toBe(size);
    expect(renderer.shown).toBe(shown);
    expect(shown).toMatchObject({ visibleWidth: 2, visibleHeight: 36 });
    at(3, 125);
    expect(renderer.shown).toBe(null);
    at(3, BLACK_S * 1000);
    expect(r.size).not.toBe(null);
    expect(r.size).not.toBe(size);
    expect(renderer.shown).toMatchObject({ visibleWidth: 1, visibleHeight: 36 });
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

  // The owner's round 26 requirement: the fade lasts as long as the fall takes to be granted, not
  // FADE_S. The first frame of the fade reads a grant the host wrote, so it decides nothing.
  it("ends the fade at the first frame after one whose grant held nothing", () => {
    const { r, renderer, draw, limiter, fadeGrant, reads, clears, deleted } = make();
    draw(640, 360);
    draw(640, 360);
    const old = limiter();
    fadeGrant[0] = 0x02;
    draw(800, 450);
    expect(r.fadeLeft).toBe(FADE_S);
    // The read is of the grant the fade's first frame was judged with.
    expect(reads).toEqual([old.fG[old.cur]]);
    clears.length = 0;
    draw(800, 450);
    expect(reads).toHaveLength(1);
    expect(r.fadeLeft).toBe(0);
    expect(r.size).toBe(null);
    expect(renderer.shown).toBe(null);
    expect(r.black).toBe(BLACK_S);
    expect(clears).toEqual([null]);
    expect([...old.fOut, ...old.fG].every((f) => deleted.has(f))).toBe(true);
  });

  it("keeps fading while the grant holds a direction, then goes to black", () => {
    const { r, draw, fadeGrant, reads } = make();
    draw(640, 360);
    draw(800, 450);
    const size = r.size;
    for (let i = 0; i < 5; i++) draw(800, 450);
    expect(r.size).toBe(size);
    expect(r.fadeLeft).toBeCloseTo(FADE_S - 5 / 60);
    expect(reads).toHaveLength(6);
    // The red token is back and the fall is granted: the frame drawn now is black, and the fade
    // ends at the next frame, which sees its grant.
    fadeGrant[0] = 0x08;
    draw(800, 450);
    expect(r.size).toBe(size);
    draw(800, 450);
    expect(r.size).toBe(null);
    expect(r.black).toBe(BLACK_S);
  });

  // Codex round 27: each frame of the fade read its grant into an array, which waits for the
  // GPU on the main thread: about 160 waits through a red fall at 60 frames a second. The read
  // is polled, so a busy GPU only ends the fade later.
  // Codex round 28: a read left in flight while later frames drew was taken, once its fence
  // passed, behind those frames' commands, and the pack buffer read waited for them. Each frame
  // of the fade reads its own grant, after everything else it issues, and the next draw takes
  // the word, before it issues anything, only if the GPU has finished that frame.
  it("never waits for the GPU through a fade", () => {
    const { r, draw, fadeGrant, reads, blocking, gpu, waits, order } = make();
    draw(640, 360);
    draw(640, 360);
    fadeGrant[0] = 0x02;
    gpu.busy = true;
    order.length = 0;
    draw(800, 450);
    const size = r.size;
    for (let i = 0; i < 3; i++) draw(800, 450);
    // One read a frame, each in place of the last; the fade goes on, and no word is taken. The
    // frame's own fence (CORE-2720) comes before the read's, which stays last.
    expect(reads).toHaveLength(4);
    expect(order).toEqual(Array(4).fill(["blit", "fence", "fence"]).flat());
    expect(r.size).toBe(size);
    expect(r.fadeLeft).toBeCloseTo(FADE_S - 3 / 60);
    gpu.busy = false;
    draw(800, 450);
    // The word is taken straight after the last frame's fence, behind no later command.
    expect(order.slice(-2)).toEqual(["fence", "sub"]);
    expect(order.filter((x) => x === "sub")).toHaveLength(1);
    expect(r.size).toBe(null);
    expect(r.black).toBe(BLACK_S);
    expect(blocking).toEqual([]);
    // Four polls of the grant and, with the frame fence polled at each draw (CORE-2720), six of
    // frames: none waits.
    expect(waits).toHaveLength(10);
    expect(waits.every((timeout) => timeout === 0)).toBe(true);
  });

  it("ends the fade at FADE_S when no read of the grant ever finishes", () => {
    const { r, draw, through, fadeGrant, gpu, blocking, canvas } = make();
    draw(640, 360);
    fadeGrant[0] = 0x02;
    gpu.busy = true;
    draw(800, 450);
    for (let i = 0; i < 15; i++) draw(800, 450, 0.125);
    expect(r.size).not.toBe(null);
    draw(800, 450, 0.125);
    expect(r.size).toBe(null);
    through((ms) => draw(800, 450, ms / 1000));
    expect([canvas.width, canvas.height]).toEqual([800, 450]);
    expect(blocking).toEqual([]);
  });

  // A read started before the fade is of a frame that was not black.
  it("does not end the fade on a grant read before it started", () => {
    const { r, renderer, draw, reads } = make();
    draw(640, 360);
    draw(640, 360);
    expect(renderer.settled(640, 360, 1)).toBe(true);
    expect(reads).toHaveLength(1);
    draw(800, 450);
    const size = r.size;
    draw(800, 450);
    expect(r.size).toBe(size);
    expect(r.fadeLeft).toBeCloseTo(FADE_S - 1 / 60);
  });

  // Codex round 29: a mount's first request counted as asked for twice, so a slot already
  // moving allocated every target for a size it left at the next draw.
  it("starts a mounted frame only at a geometry asked for twice running", () => {
    const { r, draw, count, clears, canvas } = make(false, true);
    draw(800, 450);
    draw(801, 450);
    draw(802, 450);
    expect(r.size).toBe(null);
    expect(count.textures).toBe(0);
    expect(clears).toEqual([null, null, null]);
    draw(802, 450);
    expect(r.size).not.toBe(null);
    expect([canvas.width, canvas.height]).toEqual([802, 450]);
  });

  // Codex round 29: the comparison built a geometry at every draw. The request is written into
  // one of two objects the renderer keeps.
  it("compares the geometry asked for without allocating", () => {
    const { r, renderer, draw } = make();
    draw(640, 360);
    const kept = new Set([r.asked, r.want]);
    expect(kept.size).toBe(2);
    for (let i = 0; i < 6; i++) {
      draw(640 + (i % 3), 360);
      renderer.settled(640, 360, 1);
      expect(kept.has(r.asked) && kept.has(r.want) && r.asked !== r.want).toBe(true);
    }
  });

  // Codex round 26: the slot changed after the last black frame was painted, so the frame that
  // started was withdrawn. It was never on screen: the black is, and a frame starts at the new
  // geometry with no second fade. Codex round 28: after BLACK_S more of black, once two draws
  // running ask for that geometry.
  it("starts at the new geometry when the frame that ended the black is withdrawn", () => {
    const { r, renderer, draw, limiter, spent, clears, blits, canvas, deleted } = make();
    draw(640, 360);
    draw(800, 450);
    for (let i = 0; i < 16; i++) draw(800, 450, 0.125);
    expect(r.size).toBe(null);
    draw(800, 450, 0.125);
    const started = limiter();
    expect([canvas.width, canvas.height]).toEqual([800, 450]);
    const clock = r.clockMs;
    clears.length = 0;
    blits.length = 0;

    renderer.withdraw();
    expect(r.size).toBe(null);
    expect(renderer.shown).toBe(null);
    expect(r.fadeLeft).toBe(0);
    expect(r.black).toBe(BLACK_S);
    expect(r.clockMs).toBe((clock as number) - 125);
    expect(clears).toEqual([null]);
    expect(blits).toEqual([]);
    expect([...started.fOut, ...started.fG].every((f) => deleted.has(f))).toBe(true);
    expect(renderer.settled(900, 500, 1)).toBe(false);

    draw(900, 500, BLACK_S);
    expect(r.size).toBe(null);
    expect(r.black).toBe(0);
    expect(blits).toEqual([]);
    draw(900, 500);
    const L = limiter();
    expect([canvas.width, canvas.height]).toEqual([900, 500]);
    expect(r.fadeLeft).toBe(0);
    expect(spent.at(-1)).toEqual([L.fG[L.cur ^ 1], SPENT]);
    expect(blits).toEqual([[0, 0, 900, 500, 0, 0, 900, 500]]);
    // A second withdrawal of the same frame does nothing more.
    renderer.withdraw();
    renderer.withdraw();
    expect(r.size).toBe(null);
  });

  // Codex round 26: at DPR 4 a slot alternating between 128 and 129 physical pixels shows the
  // same 64 two-pixel texels. The host compares geometries, so it withdraws no frame and the
  // limiter's clock runs.
  it("advances the limiter's clock while the slot gains and loses an unfilled pixel", () => {
    const { r, renderer, drawIn } = make();
    const slots: [number, number][] = [
      [128, 72],
      [129, 72],
    ];
    drawIn([64, 36, 2], slots[0]);
    const size = r.size;
    const clock = r.clockMs as number;
    for (let i = 1; i <= 120; i++) {
      drawIn([64, 36, 2], slots[(i - 1) % 2]);
      // The observer's callback: the slot it reports against the frame just drawn.
      if (!sameFrame([64, 36, 2, ...slots[i % 2]], [64, 36, 2, ...slots[(i - 1) % 2]]))
        renderer.withdraw();
    }
    expect(r.size).toBe(size);
    expect(r.fadeLeft).toBe(0);
    expect((r.clockMs as number) - clock).toBeCloseTo(120 * (1000 / 60));
    // A slot that cuts a texel it showed is another geometry.
    expect(sameFrame([64, 36, 2, 127, 72], [64, 36, 2, 128, 72])).toBe(false);
    expect(sameFrame([64, 36, 2, 129, 72], [64, 36, 2, 128, 72])).toBe(true);
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
    draw(64, 36, BLACK_S);
    expect(renderer.settled(64, 36, 1)).toBe(true);
    expect(renderer.settled(64, 36, 1)).toBe(true);
    expect(reads).toHaveLength(1);
  });

  // Codex round 28: a slot dragged after the black started a frame at every draw and withdrew
  // it at every observation, freeing and allocating every full-resolution target each time.
  it("allocates nothing while the slot moves, and once in BLACK_S when it stops between", () => {
    const { r, renderer, draw, count } = make();
    draw(640, 360);
    draw(800, 450);
    for (let i = 0; i < 16; i++) draw(800, 450, 0.125);
    expect(r.size).toBe(null);
    const textures = count.textures;
    // The drag outlasts the black: a new size at every draw, each withdrawn at the observation
    // after it.
    for (let i = 0; i < 60; i++) {
      draw(801 + i, 450);
      renderer.withdraw();
    }
    expect(r.black).toBe(0);
    expect(r.size).toBe(null);
    expect(count.textures).toBe(textures);
    // The slot rests for two draws and moves again before the paint: one start, withdrawn.
    draw(860, 450);
    expect(r.size).not.toBe(null);
    const started = count.textures;
    expect(started).toBeGreaterThan(textures);
    renderer.withdraw();
    expect(r.black).toBe(BLACK_S);
    for (let i = 0; i < 5; i++) {
      draw(860, 450);
      renderer.withdraw();
    }
    expect(r.size).toBe(null);
    expect(count.textures).toBe(started);
  });

  // Codex round 29: only the first start checked its framebuffers, so a target whose storage
  // failed at a later size was drawn into anyway. The limiter read its zeros, counted nothing
  // over the area limit and passed a full-screen red frame on a spent grant.
  // Codex round 30: each check is a wait on the GPU process, and the limiter's fixed-size
  // targets are the same at every start, so they are checked at the first only. Codex round
  // 31: either target of a pair can fail alone, so the feedback, history and output pairs and
  // the row counts, seven targets, are all checked at every start.
  it("checks every target of every start, and the fixed-size ones at the first", () => {
    const { r, draw, through, count, canvas } = make();
    draw(640, 360);
    expect(count.checks).toBe(12);
    through((ms) => draw(800, 450, ms / 1000));
    expect([canvas.width, canvas.height]).toEqual([800, 450]);
    expect(r.size).not.toBe(null);
    expect(count.checks).toBe(19);
  });

  // Codex round 30: a framebuffer check waits for a shader compiling in parallel, on the page's
  // thread. No frame starts until the compile is done; the canvas stays black meanwhile.
  it("starts no frame while a visual compiles", () => {
    const { r, draw, count, clears } = make();
    r.building = 1;
    for (let i = 0; i < 30; i++) draw(640, 360);
    expect(r.size).toBe(null);
    expect(count.checks).toBe(0);
    expect(count.textures).toBe(0);
    expect(clears.at(-1)).toBe(null);
    r.building = 0;
    draw(640, 360);
    expect(r.size).not.toBe(null);
  });

  // Codex round 30: a docked player's slot has no pixels. A fade held the frame, with the clip
  // it started with, and showed it again when the player reopened before the fall was granted.
  it("drops the frame of a slot with no pixels and starts again as a first frame", () => {
    const { r, renderer, draw, limiter, spent, clears, blits, deleted } = make();
    draw(640, 360);
    draw(640, 360);
    const first = limiter();
    renderer.drop();
    expect(r.size).toBe(null);
    expect(renderer.shown).toBe(null);
    expect(r.fadeLeft).toBe(0);
    expect(r.black).toBe(BLACK_S);
    expect(clears.at(-1)).toBe(null);
    expect(first.fOut.every((f) => deleted.has(f))).toBe(true);
    // Dropping again does nothing more, and nothing is withdrawn.
    const cleared = clears.length;
    renderer.drop();
    renderer.withdraw();
    expect(clears.length).toBe(cleared);
    // The same geometry again: one ask is not two running, and the black runs first.
    blits.length = 0;
    renderer.draw(640, 360, 1 / 60, silentFrame(), false);
    expect(r.size).toBe(null);
    renderer.draw(640, 360, BLACK_S, silentFrame(), false);
    expect(r.size).not.toBe(null);
    const L = limiter();
    expect(L).not.toBe(first);
    expect(spent.at(-1)).toEqual([L.fG[L.cur ^ 1], SPENT]);
    expect(blits).toHaveLength(1);
  });

  // The fade of a frame whose slot emptied is dropped with it.
  it("drops a frame that is fading", () => {
    const { r, renderer, draw } = make();
    draw(640, 360);
    draw(800, 450);
    expect(r.fadeLeft).toBe(FADE_S);
    renderer.drop();
    expect(r.size).toBe(null);
    expect(r.fadeLeft).toBe(0);
  });

  it("presents black and fails when a later start's framebuffer is incomplete", () => {
    const { r, renderer, draw, count, clears, blits, blocks, deleted } = make();
    draw(640, 360);
    const checks = count.checks;
    const first = (r.size as { limiter: Limiter }).limiter;
    // The fade and the black of a resize, up to the draw that starts the second frame.
    draw(800, 450);
    for (let i = 0; i < 16; i++) draw(800, 450, 0.125);
    expect(r.size).toBe(null);
    const textures = count.textures;
    blits.length = 0;
    blocks.length = 0;
    clears.length = 0;
    // The limiter's row-count target, the last framebuffer a later start checks.
    count.incompleteAt = checks + 7;
    expect(() => draw(800, 450, 0.125)).toThrow("incomplete framebuffer");
    expect(count.checks).toBe(checks + 7);
    // No frame, nothing drawn through the limiter, black on the canvas, every target freed.
    expect(r.size).toBe(null);
    expect(renderer.shown).toBe(null);
    expect(blits).toEqual([]);
    expect(blocks).toEqual([]);
    expect(clears.at(-1)).toBe(null);
    expect(count.textures).toBeGreaterThan(textures);
    expect(first.fOut.every((f) => deleted.has(f))).toBe(true);
    expect(deleted.size).toBeGreaterThanOrEqual(2 * (count.textures - textures));
  });

  // Codex round 31: a start checked only the first of each pair of targets, and the frames
  // alternate between the two outputs, so an incomplete second one cleared every other frame to
  // black and the next blit brought the picture back. Every target a start builds is checked.
  it("fails a start whose second output framebuffer is incomplete", () => {
    const { r, draw, count, blits } = make();
    draw(640, 360);
    const checks = count.checks;
    draw(800, 450);
    for (let i = 0; i < 16; i++) draw(800, 450, 0.125);
    expect(r.size).toBe(null);
    blits.length = 0;
    // Feedback, history and output targets come in pairs; the second output is the sixth.
    count.incompleteAt = checks + 6;
    expect(() => draw(800, 450, 0.125)).toThrow("incomplete framebuffer");
    expect(r.size).toBe(null);
    expect(blits).toEqual([]);
  });

  // CORE-2720: a slow interval after a frame the GPU had finished is a throttled page, not an
  // overloaded render; the gate is told which.
  it("tells the cadence gate whether the GPU finished the last frame", () => {
    const { renderer, r, draw, gpu } = make();
    const idle: boolean[] = [];
    r.cadence = {
      sample: (_: number, gpuMs: number | null) => (idle.push(gpuMs !== null), null),
    };
    draw(64, 36);
    draw(64, 36);
    gpu.busy = true;
    draw(64, 36);
    gpu.busy = false;
    renderer.draw(64, 36, 0.2, silentFrame(), false);
    expect(idle).toEqual([false, true, false, true]);
  });

  // Codex review of CORE-2720: a browser that holds the next callback until the GPU is done
  // would find every fence signalled at the callback. The fence is checked at the budget.
  it("checks the frame fence at the budget, not at the next callback", () => {
    vi.useFakeTimers();
    try {
      const { renderer, r, draw, gpu } = make();
      const idle: boolean[] = [];
      r.cadence = {
        sample: (_: number, gpuMs: number | null) => (idle.push(gpuMs !== null), null),
      };
      draw(64, 36);
      gpu.busy = true;
      vi.advanceTimersByTime(GPU_BUDGET_MS + 1);
      gpu.busy = false;
      renderer.draw(64, 36, 0.05, silentFrame(), false);
      vi.advanceTimersByTime(GPU_BUDGET_MS + 1);
      renderer.draw(64, 36, 0.2, silentFrame(), false);
      expect(idle).toEqual([false, false, true]);
    } finally {
      vi.useRealTimers();
    }
  });

  // Codex review of CORE-2720: a check held back by a busy main thread finds the fence signalled
  // but proves only that the GPU finished by then, so it reports that time.
  it("reports a late fence check as the time it ran", () => {
    vi.useFakeTimers();
    const now = vi.spyOn(performance, "now").mockReturnValue(1000);
    try {
      const { renderer, r, draw } = make();
      const seen: (number | null)[] = [];
      r.cadence = { sample: (_: number, gpuMs: number | null) => (seen.push(gpuMs), null) };
      draw(64, 36);
      now.mockReturnValue(1045);
      renderer.draw(64, 36, 0.045, silentFrame(), false);
      expect(seen).toEqual([null, 45]);
    } finally {
      now.mockRestore();
      vi.useRealTimers();
    }
  });

  // Codex round 18: the limiter ages evidence by the time frames are shown apart, which a late
  // callback's timestamp overstates, while the governor keeps the raw interval.
  it("times the limiter by the shown interval", () => {
    const { renderer, draw, blocks } = make();
    draw(64, 36);
    renderer.draw(64, 36, 0.1, silentFrame(), false, 183, 1, 33);
    expect(blocks.at(-1)?.[5]).toBe(33);
  });

  // CORE-2744 (Codex P1): deltaMs clears every stamp over the coverage interval, so it is the
  // least the frame can have been shown: draws 70 ms apart on a 100 Hz display may paint 60 ms
  // apart, and the stamps of the frame before stay recent.
  it("ages the stamps by the interval less the paint lag", () => {
    const { renderer, draw, blocks } = make();
    draw(64, 36);
    renderer.draw(64, 36, 0.07, silentFrame(), false, 70, 1, 70, 64, 36, 10);
    expect(blocks.at(-1)?.[5]).toBe(60);
  });

  // CORE-2744 (Codex P2): the clock runs slower by the lag a second, so a span it counts as a
  // second (six refills) was a second on screen however late the first frame of it was painted.
  it("slows the limiter's clock by the paint lag a second", () => {
    const { renderer, draw } = make();
    draw(640, 360);
    const r = renderer as unknown as { clockMs: number };
    const start = r.clockMs;
    const lag = 1000 / 59.94;
    for (let i = 0; i < 30; i++) {
      renderer.draw(
        640,
        360,
        1 / 29.97,
        silentFrame(),
        false,
        1000 / 29.97,
        1,
        1000 / 29.97,
        640,
        360,
        lag,
      );
    }
    const drawnMs = 30 * (1000 / 29.97);
    expect(r.clockMs - start).toBeCloseTo((drawnMs * 1000) / (1000 + lag), 6);
  });

  // A lag over one recency unit slows the clock further, so a stamp under the coverage interval
  // old on screen is never more than the 5 units recency reaches on the clock.
  it("keeps a stamp the coverage interval old within reach at a slow refresh", () => {
    const { renderer, draw } = make();
    draw(640, 360);
    const r = renderer as unknown as { clockMs: number };
    const start = r.clockMs;
    const lag = 1000 / 30;
    renderer.draw(
      640,
      360,
      0.1,
      silentFrame(),
      false,
      1000 / 15 + lag,
      1,
      1000 / 15 + lag,
      640,
      360,
      lag,
    );
    expect(r.clockMs - start).toBeCloseTo(1000 / 15 + 1000 / 60, 6);
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

// CORE-2724: the incoming visual's feedback starts black, so the outgoing visual shows only
// through the switch pass and leaves nothing in the incoming one's trails.
describe("Renderer switch feedback (CORE-2724)", () => {
  const make = (reducedMotion: boolean) => {
    const { gl, ...recorded } = recordingGl();
    const r = Object.create(Renderer.prototype) as Record<string, unknown>;
    const feedback = [
      { tex: { id: "a" }, fbo: { id: "fa" } },
      { tex: { id: "b" }, fbo: { id: "fb" } },
    ];
    const size = { renderWidth: 64, renderHeight: 36, feedback, outgoing: null };
    Object.assign(r, {
      gl,
      size,
      reducedMotion,
      visual: { visual: { id: "aurora" }, program: {}, locations: new Map(), image: null },
      outgoing: null,
      image: null,
      feedbackFormat: { internal: 0, type: 0, label: "float" },
      transition: { request: () => 0 },
      endSwitch: () => undefined,
    });
    const apply = (r.apply as (v: unknown, p: unknown) => void).bind(r);
    return { apply, size, feedback, recorded };
  };

  it("blends into a fresh black pair without copying the outgoing feedback", () => {
    const { apply, size, feedback, recorded } = make(false);
    apply({ id: "nebula" }, {});
    expect(recorded.blits).toHaveLength(0);
    expect(size.outgoing).toBe(feedback);
    expect(size.feedback).not.toBe(feedback);
    const fresh = size.feedback as unknown as { fbo: unknown }[];
    expect(recorded.fills.map(([f, i, v]) => [f, i, v.join()])).toEqual([
      [fresh[0].fbo, 0, "0,0,0,1"],
      [fresh[1].fbo, 0, "0,0,0,1"],
    ]);
  });

  it("clears the feedback when the visual changes at once", () => {
    const { apply, size, feedback, recorded } = make(true);
    apply({ id: "nebula" }, {});
    expect(size.feedback).toBe(feedback);
    expect(recorded.fills.map(([f, i, v]) => [f, i, v.join()])).toEqual([
      [feedback[0].fbo, 0, "0,0,0,1"],
      [feedback[1].fbo, 0, "0,0,0,1"],
    ]);
  });
});
