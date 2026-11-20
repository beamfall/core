import { act, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Visualizer } from "./Visualizer";

// A mocked renderer factory: each create hands out a fresh renderer, as a restored context does.
const made = vi.hoisted(() => ({
  renderers: [] as {
    setImage: ReturnType<typeof vi.fn>;
    use: ReturnType<typeof vi.fn>;
    prepare: ReturnType<typeof vi.fn>;
    draw: ReturnType<typeof vi.fn>;
    settled: ReturnType<typeof vi.fn>;
    withdraw: ReturnType<typeof vi.fn>;
    drop: ReturnType<typeof vi.fn>;
    shown: {
      width: number;
      height: number;
      texel: number;
      visibleWidth: number;
      visibleHeight: number;
    } | null;
    dispose: ReturnType<typeof vi.fn>;
    contextLost: boolean;
  }[],
  restores: [] as boolean[],
  fail: false,
  // Geometries compared, and what the comparisons returned that was not a boolean.
  compares: 0,
  results: [] as unknown[],
  // When set, the next create waits on it, as a create still compiling when the context goes.
  gate: null as Promise<void> | null,
}));

vi.mock("./oscillux", () => ({ loadOscillux: () => Promise.resolve({}) }));
vi.mock("./renderer", async (importOriginal) => {
  const { sameFrame } = await importOriginal<typeof import("./renderer")>();
  return {
    MAX_SIDE: 8192,
    TRACKED_BLACK: "#000",
    sameFrame: (...args: Parameters<typeof sameFrame>) => {
      made.compares += 1;
      const same = sameFrame(...args);
      if (typeof same !== "boolean") made.results.push(same);
      return same;
    },
    Renderer: {
      create: async (_canvas: HTMLCanvasElement, _osc: unknown, restored: boolean) => {
        made.restores.push(restored);
        const gate = made.gate;
        made.gate = null;
        if (gate) await gate;
        if (made.fail) return null;
        const r = {
          setImage: vi.fn(),
          use: vi.fn(() => Promise.resolve()),
          prepare: vi.fn(() => Promise.resolve()),
          draw: vi.fn(),
          settled: vi.fn(() => false),
          withdraw: vi.fn(),
          drop: vi.fn(),
          shown: null as (typeof made.renderers)[number]["shown"],
          dispose: vi.fn(),
          contextLost: false,
        };
        made.renderers.push(r);
        return r;
      },
    },
  };
});

const direct = { kind: "direct-play", url: "/api/media/m1/stream" };
let pending: Set<number>;
let callbacks: Map<number, FrameRequestCallback>;
let nextFrame: number;

beforeEach(() => {
  made.renderers.length = 0;
  made.restores.length = 0;
  made.fail = false;
  made.compares = 0;
  made.results.length = 0;
  made.gate = null;
  pending = new Set();
  callbacks = new Map();
  nextFrame = 0;
  vi.stubGlobal("WebGL2RenderingContext", class {});
  vi.stubGlobal("OfflineAudioContext", class {});
  // An observer that never reports: the loop measures the slot.
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  );
  vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) => {
    pending.add(++nextFrame);
    callbacks.set(nextFrame, cb);
    return nextFrame;
  });
  vi.stubGlobal("cancelAnimationFrame", (id: number) => pending.delete(id));
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function mount() {
  const videoRef = { current: document.createElement("video") };
  const view = render(<Visualizer videoRef={videoRef} stream={direct} trackKey="m1" />);
  const canvas = view.container.querySelector("canvas") as HTMLCanvasElement;
  // jsdom lays nothing out, and a slot with no pixels has nothing to show.
  vi.spyOn(canvas.parentElement!, "getBoundingClientRect").mockReturnValue({
    width: 640,
    height: 360,
  } as DOMRect);
  return { ...view, canvas, videoRef };
}

function step(now: number) {
  const [id] = pending;
  const callback = callbacks.get(id)!;
  pending.delete(id);
  callbacks.delete(id);
  act(() => callback(now));
}

describe("Visualizer context loss (CORE-2482)", () => {
  it("suspends on loss, then rebuilds the renderer and resumes on restore", async () => {
    const { canvas } = mount();
    await waitFor(() => expect(pending.size).toBe(1));
    const first = made.renderers[0];
    vi.stubGlobal("devicePixelRatio", 1);
    first.shown = { width: 640, height: 360, texel: 1, visibleWidth: 600, visibleHeight: 360 };
    step(16);
    expect([canvas.style.width, canvas.style.clipPath]).toEqual(["640px", "inset(0 40px 0px 0)"]);

    const lost = new Event("webglcontextlost", { cancelable: true });
    act(() => void canvas.dispatchEvent(lost));
    expect(lost.defaultPrevented).toBe(true);
    expect(pending.size).toBe(0);
    expect(first.dispose).toHaveBeenCalled();
    // Codex round 30: the lost frame's box, 640 pixels wide, stayed in the page until the
    // restore and overflowed a window narrowed meanwhile.
    expect([canvas.style.width, canvas.style.height, canvas.style.clipPath]).toEqual([
      "0px",
      "0px",
      "",
    ]);

    act(() => void canvas.dispatchEvent(new Event("webglcontextrestored")));
    await waitFor(() => expect(pending.size).toBe(1));
    expect(made.renderers).toHaveLength(2);
    expect(made.restores).toEqual([false, true]);
    expect(made.renderers[1].use).toHaveBeenCalled();
    expect(made.renderers[1].setImage).toHaveBeenCalled();
    expect(canvas.isConnected).toBe(true);
  });

  it("falls back to the failed state when the restore fails", async () => {
    const { canvas, container } = mount();
    await waitFor(() => expect(pending.size).toBe(1));
    act(() => void canvas.dispatchEvent(new Event("webglcontextlost", { cancelable: true })));
    made.fail = true;
    act(() => void canvas.dispatchEvent(new Event("webglcontextrestored")));
    await waitFor(() => expect(container.querySelector("canvas")).toBeNull());
  });

  it("ignores a superseded create that fails when the context is lost mid-create", async () => {
    let reject!: (err: Error) => void;
    made.gate = new Promise((_, no) => {
      reject = no;
    });
    const { canvas } = mount();
    act(() => void canvas.dispatchEvent(new Event("webglcontextlost", { cancelable: true })));
    await act(async () => reject(new Error("context lost")));
    expect(canvas.isConnected).toBe(true);
    act(() => void canvas.dispatchEvent(new Event("webglcontextrestored")));
    await waitFor(() => expect(pending.size).toBe(1));
    expect(made.renderers).toHaveLength(1);
    expect(canvas.isConnected).toBe(true);
  });

  it("ignores a visual switch that fails on a renderer a context loss replaced", async () => {
    const { canvas, rerender, videoRef } = mount();
    await waitFor(() => expect(pending.size).toBe(1));
    let reject!: (err: Error) => void;
    made.renderers[0].use.mockImplementation(
      () =>
        new Promise<void>((_, no) => {
          reject = no;
        }),
    );
    rerender(<Visualizer videoRef={videoRef} stream={direct} trackKey="m2" />);
    expect(reject).toBeDefined();
    act(() => void canvas.dispatchEvent(new Event("webglcontextlost", { cancelable: true })));
    act(() => void canvas.dispatchEvent(new Event("webglcontextrestored")));
    await waitFor(() => expect(pending.size).toBe(1));
    await act(async () => reject(new Error("shader import failed")));
    expect(made.renderers).toHaveLength(2);
    expect(canvas.isConnected).toBe(true);
  });

  it("waits for the loss event when a create fails on a context already lost", async () => {
    let reject!: (err: Error) => void;
    made.gate = new Promise((_, no) => {
      reject = no;
    });
    const { canvas } = mount();
    const lostContext = { isContextLost: () => true } as unknown as RenderingContext;
    vi.spyOn(canvas, "getContext").mockReturnValue(lostContext);
    await act(async () => reject(new Error("createBuffer returned null")));
    expect(canvas.isConnected).toBe(true);
    act(() => void canvas.dispatchEvent(new Event("webglcontextlost", { cancelable: true })));
    act(() => void canvas.dispatchEvent(new Event("webglcontextrestored")));
    await waitFor(() => expect(pending.size).toBe(1));
    expect(made.renderers).toHaveLength(1);
    expect(canvas.isConnected).toBe(true);
  });

  it("waits for the loss event when a draw throws on a context already lost", async () => {
    const { canvas } = mount();
    await waitFor(() => expect(pending.size).toBe(1));
    const first = made.renderers[0];
    first.contextLost = true;
    first.draw.mockImplementation(() => {
      throw new Error("FRAMEBUFFER_UNSUPPORTED");
    });
    step(16);
    expect(first.draw).toHaveBeenCalled();
    expect(canvas.isConnected).toBe(true);
    act(() => void canvas.dispatchEvent(new Event("webglcontextlost", { cancelable: true })));
    act(() => void canvas.dispatchEvent(new Event("webglcontextrestored")));
    await waitFor(() => expect(made.renderers).toHaveLength(2));
    expect(canvas.isConnected).toBe(true);
  });

  it("removes its listeners on unmount", async () => {
    const { canvas, unmount } = mount();
    await waitFor(() => expect(pending.size).toBe(1));
    unmount();
    const lost = new Event("webglcontextlost", { cancelable: true });
    canvas.dispatchEvent(lost);
    expect(lost.defaultPrevented).toBe(false);
    canvas.dispatchEvent(new Event("webglcontextrestored"));
    await Promise.resolve();
    expect(made.renderers).toHaveLength(1);
  });
});

it("passes the time since the last draw after a playback pause", async () => {
  const { videoRef } = mount();
  await waitFor(() => expect(pending.size).toBe(1));
  const renderer = made.renderers[0];
  Object.defineProperty(videoRef.current, "readyState", { value: 3 });
  const paused = vi.spyOn(videoRef.current, "paused", "get").mockReturnValue(false);
  step(16);

  paused.mockReturnValue(true);
  renderer.settled.mockReturnValue(true);
  for (let now = 32; now < 316; now += 16) step(now);
  expect(renderer.draw).toHaveBeenCalledTimes(1);

  paused.mockReturnValue(false);
  step(316);
  expect(renderer.draw).toHaveBeenCalledTimes(2);
  expect(renderer.draw.mock.lastCall?.[5]).toBe(300);
});

// Oscillux's visuals are tuned per 60 Hz step: a 120 Hz display stepped their feedback and
// trails twice as fast, smearing them.
it("steps the visual at 60 Hz at most on a faster display", async () => {
  const { videoRef } = mount();
  await waitFor(() => expect(pending.size).toBe(1));
  const renderer = made.renderers[0];
  Object.defineProperty(videoRef.current, "readyState", { value: 3 });
  vi.spyOn(videoRef.current, "paused", "get").mockReturnValue(false);
  const tick = 1000 / 120;
  for (let i = 1; i <= 120; i++) step(i * tick);
  expect(renderer.draw).toHaveBeenCalledTimes(60);
  for (const call of renderer.draw.mock.calls.slice(1)) expect(call[5]).toBeCloseTo(1000 / 60);

  // A 60 Hz display, jitter and all, steps on every tick.
  renderer.draw.mockClear();
  let now = 120 * tick;
  for (let i = 0; i < 60; i++) step((now += i % 2 ? 15.9 : 17.4));
  expect(renderer.draw).toHaveBeenCalledTimes(60);
});

describe("Visualizer canvas fitting", () => {
  function slotSize(canvas: HTMLCanvasElement, width: number, height: number) {
    return vi.spyOn(canvas.parentElement!, "getBoundingClientRect").mockReturnValue({
      width,
      height,
    } as DOMRect);
  }

  it("fits and filters the frame on screen until its fade ends", async () => {
    const { canvas } = mount();
    await waitFor(() => expect(pending.size).toBe(1));
    const renderer = made.renderers[0];
    vi.stubGlobal("devicePixelRatio", 3);
    const box = slotSize(canvas, 640, 360);
    renderer.shown = { width: 960, height: 540, texel: 2, visibleWidth: 960, visibleHeight: 540 };
    step(16);
    expect(canvas.style.imageRendering).toBe("pixelated");

    vi.stubGlobal("devicePixelRatio", 2);
    box.mockReturnValue({ width: 960, height: 540 } as DOMRect);
    step(32);
    expect(renderer.draw.mock.lastCall?.[6]).toBe(1);
    expect(canvas.style.imageRendering).toBe("pixelated");
    expect(canvas.style.width).toBe("960px");

    renderer.shown = {
      width: 1920,
      height: 1080,
      texel: 1,
      visibleWidth: 1920,
      visibleHeight: 1080,
    };
    step(48);
    expect(canvas.style.imageRendering).toBe("");
    expect(canvas.style.width).toBe("960px");
  });

  // The texels the slot cut when the frame started were never in view: the canvas is clipped to
  // the rest for as long as that frame is on screen.
  it("clips the canvas to the texels its frame started with", async () => {
    const { canvas } = mount();
    await waitFor(() => expect(pending.size).toBe(1));
    const renderer = made.renderers[0];
    vi.stubGlobal("devicePixelRatio", 4);
    slotSize(canvas, 1, 30);
    renderer.shown = { width: 64, height: 36, texel: 2, visibleWidth: 1, visibleHeight: 36 };
    step(16);
    expect(canvas.style.clipPath).toBe("inset(0 31.5px 0px 0)");

    renderer.shown = { width: 64, height: 36, texel: 2, visibleWidth: 64, visibleHeight: 36 };
    step(32);
    expect(canvas.style.clipPath).toBe("");
  });

  it("refits a paused frame when DPR changes at the same physical size", async () => {
    const { canvas, videoRef } = mount();
    await waitFor(() => expect(pending.size).toBe(1));
    Object.defineProperty(videoRef.current, "readyState", { value: 3 });
    vi.stubGlobal("devicePixelRatio", 1);
    const box = slotSize(canvas, 640, 360);
    const renderer = made.renderers[0];
    renderer.shown = { width: 640, height: 360, texel: 1, visibleWidth: 640, visibleHeight: 360 };
    step(16);
    expect(canvas.style.width).toBe("640px");

    renderer.settled.mockReturnValue(true);
    vi.stubGlobal("devicePixelRatio", 1.25);
    box.mockReturnValue({ width: 512, height: 288 } as DOMRect);
    step(32);
    expect(renderer.settled).toHaveBeenLastCalledWith(640, 360, 1, 640, 360);
    expect(renderer.draw).toHaveBeenCalledTimes(1);
    expect(canvas.style.width).toBe("512px");
    expect(canvas.style.height).toBe("288px");
  });

  it("keeps drawing a paused geometry change until the renderer settles", async () => {
    const { canvas, videoRef } = mount();
    await waitFor(() => expect(pending.size).toBe(1));
    Object.defineProperty(videoRef.current, "readyState", { value: 3 });
    vi.stubGlobal("devicePixelRatio", 1);
    slotSize(canvas, 640, 360);
    const renderer = made.renderers[0];
    renderer.shown = { width: 320, height: 180, texel: 2, visibleWidth: 320, visibleHeight: 180 };
    // The frame starts at the new geometry, black, and has not been judged yet.
    renderer.draw.mockImplementationOnce(() => {
      renderer.shown = { width: 640, height: 360, texel: 1, visibleWidth: 640, visibleHeight: 360 };
    });
    step(16);
    step(32);
    expect(renderer.draw).toHaveBeenCalledTimes(2);

    renderer.settled.mockReturnValue(true);
    step(48);
    expect(renderer.draw).toHaveBeenCalledTimes(2);
  });
});

// Codex round 18: an observer runs after the frame's draw and before its paint.
describe("Visualizer resize before paint", () => {
  type Observed = (entries: unknown[]) => void;
  let observed: Observed | null;
  let observes: number;
  // As Chromium's observer: a target is reported once after it is first observed, and observing
  // it again while it is registered does nothing.
  let registered: boolean;
  let fresh: boolean;
  const report = (width: number, height: number) =>
    act(() =>
      observed?.([
        {
          contentRect: { width, height },
          devicePixelContentBoxSize: [{ inlineSize: width, blockSize: height }],
        },
      ]),
    );
  // A slot of `device` physical pixels laid out as `css` CSS pixels.
  const reportBox = (css: [number, number], device: [number, number]) =>
    act(() =>
      observed?.([
        {
          contentRect: { width: css[0], height: css[1] },
          devicePixelContentBoxSize: [{ inlineSize: device[0], blockSize: device[1] }],
        },
      ]),
    );
  // What the browser does between a frame and its paint, with no size change: it reports a
  // target observed afresh, and nothing otherwise.
  const paint = (css: [number, number], device: [number, number]) => {
    if (!fresh) return;
    fresh = false;
    reportBox(css, device);
  };
  // A browser without device-pixel-content-box reports CSS pixels only.
  const reportCss = (width: number, height: number) =>
    act(() => observed?.([{ contentRect: { width, height } }]));
  const setRatio = (value: number) =>
    Object.defineProperty(window, "devicePixelRatio", { configurable: true, value });
  beforeEach(() => {
    observed = null;
    observes = 0;
    registered = false;
    fresh = false;
    vi.stubGlobal(
      "ResizeObserver",
      class {
        constructor(cb: Observed) {
          observed = cb;
        }
        observe() {
          observes += 1;
          if (registered) return;
          registered = true;
          fresh = true;
        }
        unobserve() {
          registered = false;
          fresh = false;
        }
        disconnect() {}
      },
    );
    vi.stubGlobal("devicePixelRatio", 1);
  });

  it("withdraws a drawn frame when the slot changes size before the paint", async () => {
    mount();
    await waitFor(() => expect(pending.size).toBe(1));
    const renderer = made.renderers[0];
    report(640, 360);
    step(16);
    expect(renderer.draw.mock.lastCall?.slice(0, 2)).toEqual([640, 360]);
    report(640, 360);
    expect(renderer.withdraw).not.toHaveBeenCalled();

    step(32);
    report(320, 360);
    expect(renderer.withdraw).toHaveBeenCalledOnce();
    report(300, 360);
    expect(renderer.withdraw).toHaveBeenCalledOnce();
  });

  // Codex round 26: red in an 800 x 600 slot at DPR 2, then a zoom to DPR 1 that keeps the
  // slot's physical pixels. The CSS box cached from DPR 2 times the new ratio is a 400 x 300 slot
  // that never existed; drawing for it faded and clipped a lit frame with no grant.
  it("holds the frame after a ratio change until the slot is observed at the new ratio", async () => {
    const ratio = window.devicePixelRatio;
    setRatio(2);
    try {
      const { canvas } = mount();
      await waitFor(() => expect(pending.size).toBe(1));
      const renderer = made.renderers[0];
      reportCss(400, 300);
      step(16);
      expect(renderer.draw.mock.lastCall?.slice(0, 2)).toEqual([800, 600]);
      expect(renderer.draw.mock.lastCall?.slice(8)).toEqual([800, 600]);
      renderer.shown = { width: 800, height: 600, texel: 1, visibleWidth: 800, visibleHeight: 600 };
      const asked = observes;

      setRatio(1);
      step(32);
      step(48);
      // No frame is drawn for the guessed slot; the one on screen keeps its physical pixels,
      // unclipped, and the slot is observed afresh, once.
      expect(renderer.draw).toHaveBeenCalledOnce();
      expect(renderer.settled).not.toHaveBeenCalled();
      expect(observes).toBe(asked + 1);
      expect(canvas.style.width).toBe("800px");
      expect(canvas.style.height).toBe("600px");
      expect(canvas.style.clipPath).toBe("");

      reportCss(800, 600);
      expect(renderer.withdraw).not.toHaveBeenCalled();
      step(64);
      expect(renderer.draw).toHaveBeenCalledTimes(2);
      expect(renderer.draw.mock.lastCall?.slice(0, 2)).toEqual([800, 600]);
      expect(renderer.draw.mock.lastCall?.[6]).toBe(1);
      expect(renderer.draw.mock.lastCall?.slice(8)).toEqual([800, 600]);
    } finally {
      setRatio(ratio);
    }
  });

  // Codex round 27: the same zoom, with nothing handed to the observer. The device-pixel box is
  // unchanged, so the observation in place reports nothing, and observing the slot again while
  // it is registered is a no-op in Chromium: the loop held the frame for good. Unobserving first
  // makes the observation fresh, and a fresh one is reported before the paint.
  it("draws again after a zoom that keeps the slot's physical pixels", async () => {
    const ratio = window.devicePixelRatio;
    setRatio(2);
    try {
      mount();
      await waitFor(() => expect(pending.size).toBe(1));
      const renderer = made.renderers[0];
      paint([400, 300], [800, 600]);
      step(16);
      paint([400, 300], [800, 600]);
      expect(renderer.draw).toHaveBeenCalledOnce();
      expect(renderer.draw.mock.lastCall?.slice(8)).toEqual([800, 600]);

      setRatio(1);
      step(32);
      expect(renderer.draw).toHaveBeenCalledOnce();
      paint([800, 600], [800, 600]);
      step(48);
      paint([800, 600], [800, 600]);
      expect(renderer.draw).toHaveBeenCalledTimes(2);
      expect(renderer.draw.mock.lastCall?.slice(0, 2)).toEqual([800, 600]);
      expect(renderer.draw.mock.lastCall?.[6]).toBe(1);
      expect(renderer.draw.mock.lastCall?.slice(8)).toEqual([800, 600]);
    } finally {
      setRatio(ratio);
    }
  });

  // An observer that reports nothing at the new ratio: the device-pixel box it last reported is
  // still the slot's, so after a few held frames the loop draws from it, with the CSS size read
  // from layout. Nothing is guessed from the old CSS size.
  it("draws from the device-pixel box it has when no report comes at the new ratio", async () => {
    const ratio = window.devicePixelRatio;
    setRatio(2);
    try {
      const { canvas } = mount();
      await waitFor(() => expect(pending.size).toBe(1));
      const renderer = made.renderers[0];
      reportBox([400, 300], [800, 600]);
      step(16);
      renderer.shown = { width: 800, height: 600, texel: 1, visibleWidth: 800, visibleHeight: 600 };

      setRatio(1);
      vi.spyOn(canvas.parentElement!, "getBoundingClientRect").mockReturnValue({
        width: 800,
        height: 600,
      } as DOMRect);
      for (const now of [32, 48, 64]) step(now);
      expect(renderer.draw).toHaveBeenCalledOnce();
      expect(canvas.style.width).toBe("800px");
      step(80);
      expect(renderer.draw).toHaveBeenCalledTimes(2);
      expect(renderer.draw.mock.lastCall?.slice(0, 2)).toEqual([800, 600]);
      expect(renderer.draw.mock.lastCall?.[6]).toBe(1);
      expect(renderer.draw.mock.lastCall?.slice(8)).toEqual([800, 600]);
      // It goes on drawing: the ratio counts as reported.
      step(96);
      expect(renderer.draw).toHaveBeenCalledTimes(3);
    } finally {
      setRatio(ratio);
    }
  });

  // Codex rounds 24, 27, 28 and 29: the slot clipped the canvas, so a slot that shrank and grew
  // hid lit pixels and showed them again, at a painted size no observation need report. The
  // canvas takes its size and clip from the frame the renderer shows and from nothing else: a
  // slot of any size, at any ratio, leaves the frame's physical pixels where they are.
  it("leaves the canvas of a frame alone whatever the slot does", async () => {
    const ratio = window.devicePixelRatio;
    setRatio(1);
    try {
      const { canvas } = mount();
      await waitFor(() => expect(pending.size).toBe(1));
      const renderer = made.renderers[0];
      reportBox([800, 600], [800, 600]);
      step(16);
      renderer.shown = { width: 800, height: 600, texel: 1, visibleWidth: 800, visibleHeight: 600 };
      step(32);
      const fit = () => [canvas.style.width, canvas.style.height, canvas.style.clipPath];
      expect(fit()).toEqual(["800px", "600px", ""]);

      step(48);
      reportBox([400, 600], [400, 600]);
      expect(fit()).toEqual(["800px", "600px", ""]);
      reportBox([800, 600], [800, 600]);
      expect(fit()).toEqual(["800px", "600px", ""]);
      step(64);
      reportBox([1, 1], [1, 1]);
      step(80);
      expect(fit()).toEqual(["800px", "600px", ""]);

      // The same 800 x 600 physical pixels at twice the ratio, with the slot not yet observed.
      setRatio(2);
      step(96);
      expect(fit()).toEqual(["400px", "300px", ""]);
    } finally {
      setRatio(ratio);
    }
  });

  // With no frame the canvas has no size: the black it holds is under no neighbour, and it
  // makes no scrollable overflow. A frame withdrawn at an observation goes at that observation.
  it("gives the canvas no size while the renderer shows no frame", async () => {
    const { canvas } = mount();
    await waitFor(() => expect(pending.size).toBe(1));
    const renderer = made.renderers[0];
    report(800, 600);
    step(16);
    expect([canvas.style.width, canvas.style.height]).toEqual(["0px", "0px"]);

    renderer.draw.mockImplementationOnce(() => {
      renderer.shown = { width: 800, height: 600, texel: 1, visibleWidth: 800, visibleHeight: 600 };
    });
    renderer.withdraw.mockImplementationOnce(() => {
      renderer.shown = null;
    });
    step(32);
    expect([canvas.style.width, canvas.style.height]).toEqual(["800px", "600px"]);
    report(400, 600);
    expect(renderer.withdraw).toHaveBeenCalledOnce();
    expect([canvas.style.width, canvas.style.height]).toEqual(["0px", "0px"]);
  });

  // Codex round 28: a request for a report at DPR 2 timed out on an empty slot and stayed on
  // record. After a context loss, a report at DPR 1 and a restore, the first frame back at DPR 2
  // matched the old request: it observed nothing and, its frames long since counted, drew from
  // the DPR 1 box.
  it("observes afresh and waits again at a ratio it asked at before", async () => {
    const ratio = window.devicePixelRatio;
    setRatio(1);
    try {
      const { canvas } = mount();
      await waitFor(() => expect(pending.size).toBe(1));
      reportBox([800, 600], [800, 600]);
      step(16);
      setRatio(2);
      const rect = vi
        .spyOn(canvas.parentElement!, "getBoundingClientRect")
        .mockReturnValue({ width: 0, height: 0 } as DOMRect);
      for (const now of [32, 48, 64, 80]) step(now);
      // The wait over, the slot measures empty: its frame is dropped, not drawn.
      expect(made.renderers[0].draw).toHaveBeenCalledOnce();
      expect(made.renderers[0].drop).toHaveBeenCalledOnce();
      reportBox([400, 300], [800, 600]);

      act(() => void canvas.dispatchEvent(new Event("webglcontextlost", { cancelable: true })));
      setRatio(1);
      reportBox([800, 600], [800, 600]);
      act(() => void canvas.dispatchEvent(new Event("webglcontextrestored")));
      await waitFor(() => expect(pending.size).toBe(1));
      const renderer = made.renderers[1];
      rect.mockReturnValue({ width: 400, height: 300 } as DOMRect);

      setRatio(2);
      const asked = observes;
      for (const now of [96, 112, 128]) step(now);
      expect(observes).toBe(asked + 1);
      expect(renderer.draw).not.toHaveBeenCalled();
      reportBox([400, 300], [800, 600]);
      step(144);
      expect(renderer.draw).toHaveBeenCalledOnce();
      expect(renderer.draw.mock.lastCall?.slice(0, 2)).toEqual([800, 600]);
      expect(renderer.draw.mock.lastCall?.slice(8)).toEqual([800, 600]);

      // A ratio left before its report and come back to is asked for again, from the start.
      setRatio(1);
      step(160);
      setRatio(2);
      step(176);
      expect(renderer.draw).toHaveBeenCalledTimes(2);
      setRatio(1);
      for (const now of [192, 208, 224]) step(now);
      expect(observes).toBe(asked + 3);
      expect(renderer.draw).toHaveBeenCalledTimes(2);
    } finally {
      setRatio(ratio);
    }
  });

  // Codex round 28: the loop built a geometry key, an array and a string, at every frame.
  // Codex round 30: so did every observation that followed a draw. Geometries are compared for
  // such an observation only, as numbers.
  it("compares no geometry for a frame nothing observes, and builds no key", async () => {
    mount();
    await waitFor(() => expect(pending.size).toBe(1));
    const renderer = made.renderers[0];
    report(640, 360);
    for (let i = 1; i <= 60; i++) step(16 * i);
    expect(renderer.draw).toHaveBeenCalledTimes(60);
    expect(made.compares).toBe(0);
    report(320, 360);
    expect(made.compares).toBe(1);
    expect(made.results).toEqual([]);
    expect(renderer.withdraw).toHaveBeenCalledOnce();
  });

  // Codex round 26: at DPR 4 a slot alternating between 128 and 129 physical pixels shows the
  // same 64 x 36 two-pixel texels. The observer compared slot pixels and withdrew every frame,
  // which stopped the limiter's clock for as long as the slot jittered.
  it("withdraws nothing while the slot gains and loses a pixel no texel fills", async () => {
    const ratio = window.devicePixelRatio;
    setRatio(4);
    try {
      mount();
      await waitFor(() => expect(pending.size).toBe(1));
      const renderer = made.renderers[0];
      report(128, 72);
      for (let i = 1; i <= 120; i++) {
        step(16 * i);
        report(i % 2 ? 129 : 128, 72);
      }
      expect(renderer.draw).toHaveBeenCalledTimes(120);
      expect(renderer.withdraw).not.toHaveBeenCalled();
      // A slot that cuts a texel it showed is another frame.
      step(16 * 121);
      report(127, 72);
      expect(renderer.withdraw).toHaveBeenCalledOnce();
    } finally {
      setRatio(ratio);
    }
  });

  it("withdraws nothing once the next frame has run", async () => {
    const { videoRef } = mount();
    await waitFor(() => expect(pending.size).toBe(1));
    const renderer = made.renderers[0];
    report(640, 360);
    step(16);
    Object.defineProperty(videoRef.current, "readyState", { value: 3 });
    renderer.settled.mockReturnValue(true);
    step(32);
    report(320, 360);
    expect(renderer.withdraw).not.toHaveBeenCalled();
  });

  // Codex round 20: a collapsed slot and a one-pixel slot share a backing size, so the slot's
  // pixels tell them apart. Codex round 30: a slot with no pixels (a docked player's) shows
  // nothing of the frame, and fading it held the frame, clipped as it started, to show again
  // when the slot reopened before the fall was granted. The frame is dropped at the observation
  // and nothing is drawn until the slot has pixels.
  it("drops the frame of a slot that has no pixels and draws nothing for it", async () => {
    const { canvas } = mount();
    await waitFor(() => expect(pending.size).toBe(1));
    const renderer = made.renderers[0];
    report(640, 360);
    renderer.draw.mockImplementationOnce(() => {
      renderer.shown = { width: 640, height: 360, texel: 1, visibleWidth: 600, visibleHeight: 360 };
    });
    renderer.drop.mockImplementation(() => {
      renderer.shown = null;
    });
    step(16);
    expect(renderer.draw).toHaveBeenCalledOnce();
    expect(canvas.style.width).toBe("640px");

    // Docked: the frame goes at the observation, with its box.
    report(0, 0);
    expect(renderer.drop).toHaveBeenCalledOnce();
    expect(renderer.withdraw).not.toHaveBeenCalled();
    expect([canvas.style.width, canvas.style.height]).toEqual(["0px", "0px"]);
    step(32);
    step(48);
    expect(renderer.draw).toHaveBeenCalledOnce();
    expect(renderer.drop).toHaveBeenCalledTimes(3);

    // A slot with one dimension is as empty.
    report(0, 360);
    step(64);
    expect(renderer.draw).toHaveBeenCalledOnce();

    // Reopened: drawing goes on, timed as a first frame, and nothing is fitted until the
    // renderer shows a frame again.
    report(640, 360);
    step(80);
    expect(renderer.draw).toHaveBeenCalledTimes(2);
    expect(renderer.draw.mock.lastCall?.[5]).toBeCloseTo(1000 / 60);
    expect([canvas.style.width, canvas.style.height]).toEqual(["0px", "0px"]);
  });

  // Codex round 31: a slot collapsed to no width while still displayed lets its canvas
  // overflow and show the frame, which a drop would turn black in one paint without a grant.
  // Only a canvas that is not rendered has its frame dropped; a rendered one fades it.
  it("fades, not drops, the frame of an empty slot whose canvas is still rendered", async () => {
    const { canvas } = mount();
    await waitFor(() => expect(pending.size).toBe(1));
    const renderer = made.renderers[0];
    report(640, 360);
    renderer.draw.mockImplementation(() => {
      renderer.shown = { width: 640, height: 360, texel: 1, visibleWidth: 640, visibleHeight: 360 };
    });
    step(16);
    expect(renderer.draw).toHaveBeenCalledOnce();
    const rects = vi
      .spyOn(canvas, "getClientRects")
      .mockReturnValue([new DOMRect(0, 0, 640, 360)] as unknown as DOMRectList);
    try {
      report(0, 360);
      expect(renderer.drop).not.toHaveBeenCalled();
      expect(renderer.withdraw).toHaveBeenCalledOnce();
      step(32);
      expect(renderer.drop).not.toHaveBeenCalled();
      expect(renderer.draw).toHaveBeenCalledTimes(2);
      expect(canvas.style.width).toBe("640px");
    } finally {
      rects.mockRestore();
    }
  });

  // Codex rounds 21 and 22: at DPR 4 a texel is two device pixels. A one-pixel slot would cut
  // it and show nothing, so it takes one-pixel texels.
  it("gives a slot narrower than one texel smaller texels", async () => {
    const ratio = window.devicePixelRatio;
    Object.defineProperty(window, "devicePixelRatio", { configurable: true, value: 4 });
    try {
      mount();
      await waitFor(() => expect(pending.size).toBe(1));
      const renderer = made.renderers[0];
      report(2, 120);
      step(16);
      expect(renderer.draw.mock.lastCall?.slice(0, 2)).toEqual([1, 60]);
      expect(renderer.draw.mock.lastCall?.[6]).toBe(2);
      report(1, 120);
      step(32);
      expect(renderer.draw.mock.lastCall?.slice(0, 2)).toEqual([1, 120]);
      expect(renderer.draw.mock.lastCall?.[6]).toBe(1);
      expect(renderer.draw.mock.lastCall?.slice(8)).toEqual([1, 120]);
    } finally {
      Object.defineProperty(window, "devicePixelRatio", { configurable: true, value: ratio });
    }
  });

  it("times the limiter by the shorter of the frame and draw intervals", async () => {
    mount();
    await waitFor(() => expect(pending.size).toBe(1));
    const renderer = made.renderers[0];
    const clock = vi.spyOn(performance, "now").mockReturnValue(150);
    step(0);
    clock.mockReturnValue(183);
    step(183);
    expect(renderer.draw.mock.lastCall?.[5]).toBe(183);
    expect(renderer.draw.mock.lastCall?.[7]).toBe(16.5);
    clock.mockRestore();
  });

  // Codex round 19: a frame painted late and the next on time are shown one refresh apart.
  it("never times the limiter past the shortest draw interval seen", async () => {
    mount();
    await waitFor(() => expect(pending.size).toBe(1));
    const renderer = made.renderers[0];
    const clock = vi.spyOn(performance, "now").mockReturnValue(0);
    step(0);
    clock.mockReturnValue(16);
    step(16);
    expect(renderer.draw.mock.lastCall?.[7]).toBe(8);
    clock.mockReturnValue(132);
    step(132);
    expect(renderer.draw.mock.lastCall?.[5]).toBe(116);
    expect(renderer.draw.mock.lastCall?.[7]).toBe(16);
    // A callback burst closer than any display refreshes does not shrink the floor below 4 ms.
    clock.mockReturnValue(133);
    step(133);
    clock.mockReturnValue(149);
    step(149);
    expect(renderer.draw.mock.lastCall?.[7]).toBe(4);
    clock.mockRestore();
  });

  // Codex round 28: a bound measured again from the last draws over-credits a late frame once
  // the page draws slower than the display refreshes, so the bound is the shortest interval
  // since the loop started. A cadence that slows from 60 Hz to 30 Hz keeps the 60 Hz bound.
  it("keeps the shortest draw interval after the cadence slows from 60 Hz to 30 Hz", async () => {
    mount();
    await waitFor(() => expect(pending.size).toBe(1));
    const renderer = made.renderers[0];
    const clock = vi.spyOn(performance, "now").mockReturnValue(0);
    let now = 0;
    const frames = (count: number, period: number) => {
      for (let i = 0; i < count; i++) {
        now += period;
        clock.mockReturnValue(now);
        step(now);
      }
    };
    step(0);
    frames(100, 1000 / 60);
    expect(renderer.draw.mock.lastCall?.[7]).toBeCloseTo(500 / 60);
    frames(200, 1000 / 30);
    expect(renderer.draw.mock.lastCall?.[7]).toBeCloseTo(1000 / 60);
    // A frame three refreshes late was on screen for one refresh at least.
    frames(1, 4000 / 60);
    frames(1, 1000 / 60);
    expect(renderer.draw.mock.lastCall?.[7]).toBeCloseTo(500 / 60);
    clock.mockRestore();
  });

  // Codex round 21: animation frames at every other refresh (59.94 Hz drawn at 29.97 Hz) look
  // like a 29.97 Hz display, and a frame painted one refresh late is shown for one refresh.
  it("times the limiter by half the interval when the refresh may be half of it", async () => {
    mount();
    await waitFor(() => expect(pending.size).toBe(1));
    const renderer = made.renderers[0];
    const period = 2000 / 59.94;
    const clock = vi.spyOn(performance, "now").mockReturnValue(0);
    step(0);
    clock.mockReturnValue(period);
    step(period);
    clock.mockReturnValue(2 * period);
    step(2 * period);
    expect(renderer.draw.mock.lastCall?.[7]).toBeCloseTo(1000 / 59.94);
    clock.mockRestore();
  });

  // Codex round 20: a host that only ever draws slower than its display cannot measure the
  // refresh, so a late paint could follow its predecessor by one refresh; no frame is assumed shown
  // for longer than a 24 Hz refresh.
  it("never times the limiter past a 24 Hz refresh when it draws slowly", async () => {
    mount();
    await waitFor(() => expect(pending.size).toBe(1));
    const renderer = made.renderers[0];
    const clock = vi.spyOn(performance, "now").mockReturnValue(0);
    step(0);
    clock.mockReturnValue(100);
    step(100);
    expect(renderer.draw.mock.lastCall?.[5]).toBe(100);
    expect(renderer.draw.mock.lastCall?.[7]).toBeCloseTo(1000 / 24);
    clock.mockRestore();
  });
});
