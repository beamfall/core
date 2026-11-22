import { afterEach, describe, expect, it, vi } from "vitest";
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
  shaderSource: async () => "",
}));

// A WebGL2 context that accepts every call: enough to drive the host's state, not to draw.
// `blocks` collects each SlideshowBlock uploaded.
function fakeGL(blocks: Float32Array[] = []): WebGL2RenderingContext {
  let lost = false;
  const overrides: Record<string, unknown> = {
    bufferSubData: (_: number, __: number, data: Float32Array) => {
      if (data.length === 44) blocks.push(data.slice());
    },
    isContextLost: () => lost,
    getExtension: (name: string) =>
      name === "WEBGL_lose_context" ? { loseContext: () => (lost = true) } : null,
    getParameter: () => 4096,
    getProgramParameter: () => true,
    checkFramebufferStatus: () => 0x8cd5,
    FRAMEBUFFER_COMPLETE: 0x8cd5,
    getError: () => 0,
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
