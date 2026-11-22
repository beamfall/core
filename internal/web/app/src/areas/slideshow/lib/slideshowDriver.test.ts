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
function fakeGL(): WebGL2RenderingContext {
  let lost = false;
  const overrides: Record<string, unknown> = {
    isContextLost: () => lost,
    getExtension: (name: string) =>
      name === "WEBGL_lose_context" ? { loseContext: () => (lost = true) } : null,
    getParameter: () => 4096,
    getProgramParameter: () => true,
    checkFramebufferStatus: () => 0x8cd5,
    FRAMEBUFFER_COMPLETE: 0x8cd5,
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
    driver.advance(clip, next);
    await vi.waitFor(() => expect(canvas?.hidden).toBe(false));

    // A lost context drops the canvas for good: every later change is a hard cut.
    canvas?.dispatchEvent(new Event("webglcontextlost", { cancelable: true }));
    expect(canvas?.hidden).toBe(true);
    driver.advance(next, photoSlide());
    await new Promise((resolve) => setTimeout(resolve));
    expect(canvas?.hidden).toBe(true);

    driver.dispose();
    expect(stage.querySelector("canvas")).toBeNull();
  });
});
