import { afterEach, describe, expect, it, vi } from "vitest";
import { FrameLimiter, type Limiter } from "../../viewer/player/visualizer/limiter";
import { createSlideshowDriver, MOTION_CANVAS_CLASS } from "./slideshowDriver";

vi.mock("../../viewer/player/visualizer/oscillux", () => ({
  loadOscillux: async () => ({
    OverlayConfig: class {
      free() {}
    },
    OverlayRegions: class {
      free() {}
      pack() {
        return 0;
      }
      uniformsPtr() {
        return 0;
      }
    },
    statusString: () => "",
    wasmMemory: () => ({ buffer: new ArrayBuffer(4096) }),
  }),
  // Each shader's source is its path, so the fake context can tell the passes apart.
  shaderSource: async (path: string) => path,
}));

const READ_FRAMEBUFFER = 0x8ca8;

// A WebGL2 context that accepts every call: enough to drive the host's state, not to draw.
// `blocks` collects each SlideshowBlock uploaded and `blits` the framebuffer each canvas blit
// read. A program links unless `refuse` refuses one of its shader sources; getError reports
// `errors` first.
function fakeGL(
  blocks: Float32Array[] = [],
  blits: unknown[] = [],
  refuse: (source: string) => boolean = () => false,
  errors: number[] = [],
): WebGL2RenderingContext {
  let lost = false;
  let read: unknown = null;
  let lastClear = new Uint32Array(4);
  const sources = new WeakMap<object, string>();
  const linked = new WeakMap<object, string[]>();
  const overrides: Record<string, unknown> = {
    bufferSubData: (_: number, __: number, data: Float32Array) => {
      if (data.length === 44) blocks.push(data.slice());
    },
    createShader: () => ({}),
    createProgram: () => ({}),
    createFramebuffer: () => ({}),
    shaderSource: (shader: object, source: string) => sources.set(shader, source),
    attachShader: (program: object, shader: object) =>
      linked.set(program, [...(linked.get(program) ?? []), sources.get(shader) ?? ""]),
    getProgramParameter: (program: object) => !(linked.get(program) ?? []).some(refuse),
    bindFramebuffer: (target: number, f: unknown) => {
      if (target === READ_FRAMEBUFFER) read = f;
    },
    blitFramebuffer: () => blits.push(read),
    // The tracker format check reads back the bits it cleared: a 32-bit store keeps them.
    clearBufferuiv: (_: number, __: number, v: Uint32Array) => (lastClear = v.slice()),
    readPixels: (...args: unknown[]) => (args[6] as Uint32Array).set(lastClear),
    READ_FRAMEBUFFER,
    DRAW_FRAMEBUFFER: 0x8ca9,
    FRAMEBUFFER: 0x8d40,
    isContextLost: () => lost,
    getExtension: (name: string) =>
      name === "WEBGL_lose_context" ? { loseContext: () => (lost = true) } : null,
    getParameter: () => 4096,
    checkFramebufferStatus: () => 0x8cd5,
    FRAMEBUFFER_COMPLETE: 0x8cd5,
    getError: () => errors.shift() ?? 0,
    OUT_OF_MEMORY: 0x0505,
    CONTEXT_LOST_WEBGL: 0x9242,
    NO_ERROR: 0,
  };
  return new Proxy({} as WebGL2RenderingContext, {
    get: (_, key) => {
      if (typeof key !== "string") return undefined;
      if (key in overrides) return overrides[key];
      if (/^[A-Z0-9_]+$/.test(key)) return 1;
      return () => ({});
    },
  });
}

function photoSlide(): HTMLElement {
  const slide = document.createElement("div");
  const image = document.createElement("img");
  image.className = "bf-slideshow-media";
  Object.defineProperties(image, {
    complete: { value: true },
    naturalWidth: { value: 300 },
    naturalHeight: { value: 200 },
  });
  slide.append(image);
  return slide;
}

const options = { intervalMs: () => 12_000, reducedMotion: () => false };

// Animation frames driven by hand on a mocked clock.
function frames() {
  let frame: FrameRequestCallback | null = null;
  vi.spyOn(window, "requestAnimationFrame").mockImplementation((cb) => {
    frame = cb;
    return 1;
  });
  let clock = 1000;
  vi.spyOn(performance, "now").mockImplementation(() => clock);
  const tick = (ms: number) => {
    clock += ms;
    const cb = frame;
    frame = null;
    cb?.(clock);
  };
  // Runs ms of 60 Hz frames: BLACK_S of black, credited a frame at a time, takes about 80.
  const run = (ms: number) => {
    for (let t = 0; t < ms; t += 16) tick(16);
  };
  return { tick, run };
}

afterEach(() => {
  vi.restoreAllMocks();
  document.body.replaceChildren();
});

describe("createSlideshowDriver", () => {
  it("is null without WebGL2, so the page hard-cuts", () => {
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null);
    expect(createSlideshowDriver(options)).toBeNull();
  });

  it("covers the stage with the pass, and steps aside for a clip and on a context loss", async () => {
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(
      fakeGL() as unknown as RenderingContext,
    );
    vi.spyOn(window, "requestAnimationFrame").mockReturnValue(1);
    const driver = createSlideshowDriver(options);
    if (!driver) throw new Error("no driver");
    const stage = document.createElement("div");
    document.body.append(stage);
    await new Promise((resolve) => setTimeout(resolve));

    const first = photoSlide();
    stage.append(first);
    driver.advance(null, first);
    const canvas = stage.querySelector<HTMLCanvasElement>(`canvas.${MOTION_CANVAS_CLASS}`);
    expect(canvas?.getAttribute("aria-hidden")).toBe("true");
    await vi.waitFor(() => expect(canvas?.hidden).toBe(false));

    // A clip plays in the DOM: the canvas hides.
    const clip = document.createElement("div");
    clip.append(document.createElement("video"));
    stage.append(clip);
    driver.advance(first, clip);
    expect(canvas?.hidden).toBe(true);

    const next = photoSlide();
    stage.append(next);
    driver.advance(clip, next);
    await vi.waitFor(() => expect(canvas?.hidden).toBe(false));

    // A lost context drops the canvas for good: every later change is a hard cut.
    canvas?.dispatchEvent(new Event("webglcontextlost", { cancelable: true }));
    expect(canvas?.hidden).toBe(true);
    const last = photoSlide();
    stage.append(last);
    driver.advance(next, last);
    await new Promise((resolve) => setTimeout(resolve));
    expect(canvas?.hidden).toBe(true);

    driver.dispose();
    expect(stage.querySelector("canvas")).toBeNull();
  });

  it("waits for the pass, holds k while paused and drops the photo when its slide goes", async () => {
    const blocks: Float32Array[] = [];
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(
      fakeGL(blocks) as unknown as RenderingContext,
    );
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue(
      new DOMRect(0, 0, 160, 90),
    );
    const { tick, run } = frames();
    const driver = createSlideshowDriver(options);
    if (!driver) throw new Error("no driver");
    const stage = document.createElement("div");
    document.body.append(stage);

    // Decoded before the pass is built: it still comes in once the pass is ready.
    const slide = photoSlide();
    stage.append(slide);
    driver.advance(null, slide);
    const canvas = stage.querySelector<HTMLCanvasElement>(`canvas.${MOTION_CANVAS_CLASS}`);
    await vi.waitFor(() => expect(canvas?.hidden).toBe(false));
    // Black for BLACK_S before the first photo.
    await new Promise((resolve) => setTimeout(resolve));
    run(1600);
    expect(blocks.length).toBeGreaterThan(0);

    tick(1200);
    const k = blocks.at(-1)?.[12] ?? -1;
    expect(k).toBeGreaterThan(0);
    driver.setRunning(false);
    tick(3000);
    expect(blocks.at(-1)?.[12]).toBeCloseTo(k);
    driver.setRunning(true);
    tick(1000);
    expect(blocks.at(-1)?.[12]).toBeGreaterThan(k);

    // The slide leaves the page with no change after it: the canvas goes with it.
    slide.remove();
    tick(16);
    expect(canvas?.hidden).toBe(true);
    driver.dispose();
  });

  it("uncovers the DOM slide when its photo fails to decode", async () => {
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(
      fakeGL() as unknown as RenderingContext,
    );
    vi.spyOn(window, "requestAnimationFrame").mockReturnValue(1);
    const driver = createSlideshowDriver(options);
    if (!driver) throw new Error("no driver");
    const stage = document.createElement("div");
    document.body.append(stage);
    const first = photoSlide();
    stage.append(first);
    driver.advance(null, first);
    const canvas = stage.querySelector<HTMLCanvasElement>(`canvas.${MOTION_CANVAS_CLASS}`);
    await vi.waitFor(() => expect(canvas?.hidden).toBe(false));

    const broken = document.createElement("div");
    const image = document.createElement("img");
    image.className = "bf-slideshow-media";
    image.decode = () => Promise.reject(new Error("broken"));
    broken.append(image);
    stage.append(broken);
    driver.advance(first, broken);
    await vi.waitFor(() => expect(canvas?.hidden).toBe(true));
    driver.dispose();
  });
});

describe("SlideshowDriver flash limiter (CORE-2980)", () => {
  const mount = (blits: unknown[], refuse?: (source: string) => boolean, errors?: number[]) => {
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(
      fakeGL([], blits, refuse, errors) as unknown as RenderingContext,
    );
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue(
      new DOMRect(0, 0, 160, 90),
    );
    const clock = frames();
    const driver = createSlideshowDriver(options);
    if (!driver) throw new Error("no driver");
    const stage = document.createElement("div");
    document.body.append(stage);
    const slide = photoSlide();
    stage.append(slide);
    driver.advance(null, slide);
    const canvas = stage.querySelector<HTMLCanvasElement>(`canvas.${MOTION_CANVAS_CLASS}`);
    return { driver, canvas, stage, slide, ...clock };
  };

  it("shows black for BLACK_S, then only the limiter's output of each frame", async () => {
    const blits: unknown[] = [];
    const runs: [Limiter, WebGLTexture][] = [];
    const run = FrameLimiter.prototype.run;
    vi.spyOn(FrameLimiter.prototype, "run").mockImplementation(function (this: FrameLimiter, ...a) {
      runs.push([a[0], a[1]]);
      run.apply(this, a);
    });
    const { driver, canvas, run: play } = mount(blits);
    // The canvas covers the DOM slide in black at once.
    expect(canvas?.hidden).toBe(false);
    await new Promise((resolve) => setTimeout(resolve));
    play(1000);
    expect(runs).toEqual([]);
    expect(blits).toEqual([]);

    play(600);
    expect(runs.length).toBeGreaterThan(0);
    expect(blits).toHaveLength(runs.length);
    // Every blit to the canvas reads the limiter's output of that frame.
    const [L] = runs.at(-1) as [Limiter, WebGLTexture];
    expect(blits.at(-1)).toBe(L.fOut[L.cur]);
    expect(L.fOut).toContain(blits[0]);
    driver.dispose();
  });

  it("fades through the limiter when the stage changes size, then starts after black", async () => {
    const blits: unknown[] = [];
    const spend = vi.spyOn(FrameLimiter.prototype, "spend");
    const { driver, canvas, run, tick } = mount(blits);
    await new Promise((resolve) => setTimeout(resolve));
    run(1600);
    const starts = spend.mock.calls.length;
    // The canvas keeps the screen pixels its frame started on.
    expect(canvas?.style.width).toBe("160px");
    vi.mocked(HTMLElement.prototype.getBoundingClientRect).mockReturnValue(
      new DOMRect(0, 0, 320, 180),
    );
    tick(16);
    // The fade writes the budgets spent and feeds the limiter black, still blitting its output.
    expect(spend.mock.calls.length).toBe(starts + 1);
    const [L] = spend.mock.calls.at(-1) as [Limiter];
    expect(L.window.limit).toBe(0);
    const shown = blits.length;
    run(1000);
    expect(blits.length).toBeGreaterThan(shown);
    expect(blits.at(-1)).toBe(L.fOut[L.cur]);
    expect(canvas?.style.width).toBe("160px");

    // FADE_S of credited time, then BLACK_S of black, then a frame at the new size.
    run(1200);
    const faded = blits.length;
    run(1000);
    expect(blits.length).toBe(faded);
    expect(canvas?.style.width).toBe("");
    run(1500);
    const [next] = spend.mock.calls.at(-1) as [Limiter];
    expect(next).not.toBe(L);
    expect(next.window.limit).toBeGreaterThan(0);
    expect(blits.at(-1)).toBe(next.fOut[next.cur]);
    expect(canvas?.style.width).toBe("320px");
    driver.dispose();
  });

  it("hard-cuts for good, never showing a frame, when the limiter fails to build", async () => {
    const blits: unknown[] = [];
    const { driver, canvas, stage, slide, run } = mount(blits, (s) => s.startsWith("limiter/"));
    await vi.waitFor(() => expect(canvas?.hidden).toBe(true));
    run(2000);
    const next = photoSlide();
    stage.append(next);
    driver.advance(slide, next);
    run(2000);
    expect(canvas?.hidden).toBe(true);
    expect(blits).toEqual([]);
    driver.dispose();
  });

  it("hard-cuts when the limiter's uniform buffer runs out of memory", async () => {
    const blits: unknown[] = [];
    const { driver, canvas, run } = mount(blits, undefined, [0x0505]);
    await vi.waitFor(() => expect(canvas?.hidden).toBe(true));
    run(2000);
    expect(blits).toEqual([]);
    driver.dispose();
  });

  it("hard-cuts when the limiter's targets are incomplete", async () => {
    const blits: unknown[] = [];
    const allocate = FrameLimiter.prototype.allocate;
    vi.spyOn(FrameLimiter.prototype, "allocate").mockImplementation(function (
      this: FrameLimiter,
      ...a
    ) {
      return { ...allocate.apply(this, a), complete: false };
    });
    const free = vi.spyOn(FrameLimiter.prototype, "free");
    const { driver, canvas, run } = mount(blits);
    await new Promise((resolve) => setTimeout(resolve));
    run(1600);
    expect(canvas?.hidden).toBe(true);
    expect(free).toHaveBeenCalledTimes(1);
    expect(blits).toEqual([]);
    driver.dispose();
  });
});
