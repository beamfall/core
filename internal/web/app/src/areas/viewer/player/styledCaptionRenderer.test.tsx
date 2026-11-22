import { act, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { publishSubtitleOffset } from "./playbackSync";
import { attachCaptionElements, captionElement, showCaptionElement } from "./streamTracks";
import { startStyledCaption } from "./styledCaptionRenderer";
import { useCaptionLoad } from "./useCaptionLoad";

const { draw, frame, instances, start, terminate } = vi.hoisted(() => ({
  draw: vi.fn<() => Promise<void>>(),
  instances: [] as { timeOffset: number }[],
  frame: { render: async () => {} },
  start: vi.fn(),
  terminate: vi.fn(),
}));

vi.mock("jassub", () => ({
  default: class {
    ready = Promise.resolve();
    timeOffset = 0;
    renderer = new Proxy(
      { _draw: draw },
      {
        set: () => {
          throw new Error("Assignments on the RPC proxy reach the worker");
        },
      },
    );
    _worker = Object.assign(new EventTarget(), { terminate });

    constructor(private readonly options: { canvas: HTMLCanvasElement; subContent: string }) {
      start(options);
      instances.push(this);
    }

    async setVideo() {
      frame.render = async () => {
        // JASSUB 2.5.36 catches the rejected RPC and only logs the failure.
        await this.renderer._draw().catch(console.error);
      };
    }

    async destroy() {
      this.options.canvas.remove();
    }
  },
}));

const track = { id: "styled", format: "ass", url: "/api/media/sidecar/grant/0.ass" };

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("styled caption renderer", () => {
  it.each(["UTF-8", "Windows-1252"])("preserves %s sidecar text", async (encoding) => {
    // The ASS payload from TestSidecarsRawASSDoesNotClaimUTF8ForCP1252Bytes.
    const prefix =
      "[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nDialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,caf";
    const encoder = new TextEncoder();
    const bytes =
      encoding === "UTF-8"
        ? encoder.encode(`${prefix}é\n`)
        : new Uint8Array([...encoder.encode(prefix), 0xe9, 0x0a]);
    vi.stubGlobal("fetch", async () => new Response(bytes));

    const controller = new AbortController();
    await startStyledCaption(
      document.createElement("video"),
      track.url,
      controller.signal,
      vi.fn(),
    );
    controller.abort();
    expect(start).toHaveBeenLastCalledWith(expect.objectContaining({ subContent: `${prefix}é\n` }));
  });

  it("follows the published subtitle offset (CORE-1671)", async () => {
    vi.stubGlobal("fetch", async () => new Response("[Events]"));
    const video = document.createElement("video");
    publishSubtitleOffset(video, 1.5);
    const controller = new AbortController();
    await startStyledCaption(video, track.url, controller.signal, vi.fn());
    const renderer = instances[instances.length - 1];
    // JASSUB draws at mediaTime + timeOffset: showing cues 1.5 s later looks back 1.5 s.
    expect(renderer.timeOffset).toBe(-1.5);
    publishSubtitleOffset(video, -0.3);
    expect(renderer.timeOffset).toBe(0.3);
    controller.abort();
    publishSubtitleOffset(video, 2);
    expect(renderer.timeOffset).toBe(0.3);
  });

  it("switches to WebVTT when a frame draw rejects after startup", async () => {
    vi.stubGlobal("Worker", class {});
    vi.stubGlobal("fetch", async () => new Response("[Events]"));
    vi.stubGlobal(
      "HTMLCanvasElement",
      class {
        transferControlToOffscreen() {}
      },
    );
    const logged = vi.spyOn(console, "error").mockImplementation(() => {});
    const video = document.createElement("video");
    function Status() {
      return <output>{useCaptionLoad(video).status}</output>;
    }
    const { container } = render(<Status />);
    container.appendChild(video);
    attachCaptionElements(video, [track], "en");
    await act(async () => showCaptionElement(video, track.id));
    expect(screen.getByRole("status").textContent).toBe("loaded");
    const element = captionElement(video, track.id);
    expect(element?.getAttribute("src")).toBeNull();

    const error = new Error("Frame draw failed");
    draw.mockRejectedValueOnce(error);
    terminate.mockClear();
    await act(frame.render);
    expect(logged).toHaveBeenCalledWith(error);
    expect(terminate).toHaveBeenCalledOnce();
    expect(element?.getAttribute("src")).toBe("/api/media/sidecar/grant/0.vtt");
    expect(screen.getByRole("status").textContent).toBe("loading");
    act(() => element?.dispatchEvent(new Event("load")));
    expect(screen.getByRole("status").textContent).toBe("loaded");
  });
});
