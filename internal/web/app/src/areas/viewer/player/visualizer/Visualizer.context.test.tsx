import { act, render, waitFor } from "@testing-library/react";
import { createRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Visualizer } from "./Visualizer";

// A mocked renderer factory: each create hands out a fresh renderer, as a restored context does.
const made = vi.hoisted(() => ({
  renderers: [] as {
    setImage: ReturnType<typeof vi.fn>;
    use: ReturnType<typeof vi.fn>;
    prepare: ReturnType<typeof vi.fn>;
    draw: ReturnType<typeof vi.fn>;
    dispose: ReturnType<typeof vi.fn>;
  }[],
  fail: false,
}));

vi.mock("./oscillux", () => ({ loadOscillux: () => Promise.resolve({}) }));
vi.mock("./renderer", () => ({
  Renderer: {
    create: async () => {
      if (made.fail) return null;
      const r = {
        setImage: vi.fn(),
        use: vi.fn(() => Promise.resolve()),
        prepare: vi.fn(() => Promise.resolve()),
        draw: vi.fn(),
        dispose: vi.fn(),
      };
      made.renderers.push(r);
      return r;
    },
  },
}));

const direct = { kind: "direct-play", url: "/api/media/m1/stream" };
let pending: Set<number>;
let nextFrame: number;

beforeEach(() => {
  made.renderers.length = 0;
  made.fail = false;
  pending = new Set();
  nextFrame = 0;
  vi.stubGlobal("WebGL2RenderingContext", class {});
  vi.stubGlobal("OfflineAudioContext", class {});
  vi.stubGlobal("requestAnimationFrame", () => {
    pending.add(++nextFrame);
    return nextFrame;
  });
  vi.stubGlobal("cancelAnimationFrame", (id: number) => pending.delete(id));
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function mount() {
  const view = render(
    <Visualizer videoRef={createRef<HTMLVideoElement>()} stream={direct} trackKey="m1" />,
  );
  const canvas = view.container.querySelector("canvas") as HTMLCanvasElement;
  return { ...view, canvas };
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
