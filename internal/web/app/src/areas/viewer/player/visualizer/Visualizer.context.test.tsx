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
    shown: { width: number; height: number; texel: number } | null;
    dispose: ReturnType<typeof vi.fn>;
    contextLost: boolean;
  }[],
  restores: [] as boolean[],
  fail: false,
  // When set, the next create waits on it, as a create still compiling when the context goes.
  gate: null as Promise<void> | null,
}));

vi.mock("./oscillux", () => ({ loadOscillux: () => Promise.resolve({}) }));
vi.mock("./renderer", () => ({
  MAX_SIDE: 8192,
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
        shown: null,
        dispose: vi.fn(),
        contextLost: false,
      };
      made.renderers.push(r);
      return r;
    },
  },
}));

const direct = { kind: "direct-play", url: "/api/media/m1/stream" };
let pending: Set<number>;
let callbacks: Map<number, FrameRequestCallback>;
let nextFrame: number;

beforeEach(() => {
  made.renderers.length = 0;
  made.restores.length = 0;
  made.fail = false;
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

    const lost = new Event("webglcontextlost", { cancelable: true });
    act(() => void canvas.dispatchEvent(lost));
    expect(lost.defaultPrevented).toBe(true);
    expect(pending.size).toBe(0);
    expect(first.dispose).toHaveBeenCalled();

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

describe("Visualizer canvas fitting", () => {
  function slotSize(canvas: HTMLCanvasElement, width: number, height: number) {
    return vi.spyOn(canvas.parentElement!, "getBoundingClientRect").mockReturnValue({
      width,
      height,
    } as DOMRect);
  }

  it("filters at the shown scale until the old surface finishes fading", async () => {
    const { canvas } = mount();
    await waitFor(() => expect(pending.size).toBe(1));
    const renderer = made.renderers[0];
    vi.stubGlobal("devicePixelRatio", 3);
    const box = slotSize(canvas, 640, 360);
    renderer.shown = { width: 960, height: 540, texel: 2 };
    step(16);
    expect(canvas.style.imageRendering).toBe("pixelated");

    vi.stubGlobal("devicePixelRatio", 2);
    box.mockReturnValue({ width: 960, height: 540 } as DOMRect);
    step(32);
    expect(renderer.draw.mock.lastCall?.[6]).toBe(1);
    expect(canvas.style.imageRendering).toBe("pixelated");
    expect(canvas.style.width).toBe("960px");

    renderer.shown = { width: 1920, height: 1080, texel: 1 };
    step(48);
    expect(canvas.style.imageRendering).toBe("");
    expect(canvas.style.width).toBe("960px");
  });

  it("refits a paused frame when DPR changes at the same physical size", async () => {
    const { canvas, videoRef } = mount();
    await waitFor(() => expect(pending.size).toBe(1));
    Object.defineProperty(videoRef.current, "readyState", { value: 3 });
    vi.stubGlobal("devicePixelRatio", 1);
    const box = slotSize(canvas, 640, 360);
    const renderer = made.renderers[0];
    renderer.shown = { width: 640, height: 360, texel: 1 };
    step(16);
    expect(canvas.style.width).toBe("640px");

    renderer.settled.mockReturnValue(true);
    vi.stubGlobal("devicePixelRatio", 1.25);
    box.mockReturnValue({ width: 512, height: 288 } as DOMRect);
    step(32);
    expect(renderer.settled).toHaveBeenLastCalledWith(640, 360, 1);
    expect(renderer.draw).toHaveBeenCalledTimes(1);
    expect(canvas.style.width).toBe("512px");
    expect(canvas.style.height).toBe("288px");
  });

  it("keeps drawing a paused scale switch until the renderer settles", async () => {
    const { canvas, videoRef } = mount();
    await waitFor(() => expect(pending.size).toBe(1));
    Object.defineProperty(videoRef.current, "readyState", { value: 3 });
    vi.stubGlobal("devicePixelRatio", 1);
    slotSize(canvas, 640, 360);
    const renderer = made.renderers[0];
    renderer.shown = { width: 320, height: 180, texel: 2 };
    // The new size is shown as black while the scale switch is still in progress.
    renderer.draw.mockImplementationOnce(() => {
      renderer.shown = { width: 640, height: 360, texel: 1 };
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
  const report = (width: number, height: number) =>
    act(() =>
      observed?.([
        {
          contentRect: { width, height },
          devicePixelContentBoxSize: [{ inlineSize: width, blockSize: height }],
        },
      ]),
    );
  beforeEach(() => {
    observed = null;
    vi.stubGlobal(
      "ResizeObserver",
      class {
        constructor(cb: Observed) {
          observed = cb;
        }
        observe() {}
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

  it("times the limiter by the shorter of the frame and draw intervals", async () => {
    mount();
    await waitFor(() => expect(pending.size).toBe(1));
    const renderer = made.renderers[0];
    const clock = vi.spyOn(performance, "now").mockReturnValue(150);
    step(0);
    clock.mockReturnValue(183);
    step(183);
    expect(renderer.draw.mock.lastCall?.[5]).toBe(183);
    expect(renderer.draw.mock.lastCall?.[7]).toBe(33);
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
    expect(renderer.draw.mock.lastCall?.[7]).toBe(16);
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
});
