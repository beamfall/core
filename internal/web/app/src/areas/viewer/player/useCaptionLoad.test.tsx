import "@testing-library/jest-dom/vitest";
import { act, render, screen } from "@testing-library/react";
import { createRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  attachCaptionElements,
  captionElement,
  showCaptionElement,
  shownCaptionElement,
} from "./streamTracks";
import { type CaptionProbe, probeCaptionSource, useCaptionLoad } from "./useCaptionLoad";

const { loadStyledCaptionModule, startStyledCaption } = vi.hoisted(() => ({
  loadStyledCaptionModule: vi.fn(),
  startStyledCaption: vi.fn(
    async (
      _video: HTMLVideoElement,
      _url: string,
      _signal: AbortSignal,
      _onError: () => void,
    ) => {},
  ),
}));

vi.mock("./styledCaptionRenderer", () => {
  loadStyledCaptionModule();
  return { startStyledCaption };
});

const videoRef = createRef<HTMLVideoElement>();
// The server's answer to the failed track: seconds to wait while it is still
// extracting, or null for a real failure.
let probeAnswer: number | null = null;
const probe = vi.fn<CaptionProbe>(async () => probeAnswer);
const OPTIONS = { timeoutMS: 1000, maxRetries: 2, probe };

function Harness() {
  const { status, retry } = useCaptionLoad(videoRef, OPTIONS);
  return (
    <>
      <video ref={videoRef} />
      <output data-testid="status">{status}</output>
      <button type="button" onClick={retry}>
        retry
      </button>
    </>
  );
}

function setup() {
  render(<Harness />);
  const video = videoRef.current as HTMLVideoElement;
  attachCaptionElements(
    video,
    [
      { id: "en", format: "subrip", language: "en", url: "/api/media/subtitle/en" },
      { id: "fr", format: "subrip", language: "fr", url: "/api/media/subtitle/fr" },
    ],
    "en",
  );
  return video;
}

const status = () => screen.getByTestId("status").textContent;

describe("useCaptionLoad (CORE-2315)", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    probeAnswer = null;
    probe.mockClear();
  });
  afterEach(() => vi.useRealTimers());

  it("reports a loaded track", () => {
    const video = setup();
    act(() => showCaptionElement(video, "en"));
    expect(status()).toBe("loading");
    act(() => {
      captionElement(video, "en")?.dispatchEvent(new Event("load"));
    });
    expect(status()).toBe("loaded");
    act(() => vi.advanceTimersByTime(5000));
    expect(status()).toBe("loaded");
  });

  it("reports a track that errors, and retries it with a fresh element", async () => {
    const video = setup();
    act(() => showCaptionElement(video, "en"));
    const first = captionElement(video, "en");
    await act(async () => {
      first?.dispatchEvent(new Event("error"));
    });
    expect(probe).toHaveBeenCalledWith(expect.stringContaining("/api/media/subtitle/en"));
    expect(status()).toBe("failed");

    act(() => screen.getByRole("button", { name: "retry" }).click());
    expect(captionElement(video, "en")).not.toBe(first);
    expect(status()).toBe("loading");
  });

  it("reports a track that never arrives once the timeout passes", () => {
    const video = setup();
    act(() => showCaptionElement(video, "en"));
    act(() => vi.advanceTimersByTime(999));
    expect(status()).toBe("loading");
    act(() => vi.advanceTimersByTime(1));
    expect(status()).toBe("failed");
  });

  it("ignores another track's events and clears when subtitles go off", () => {
    const video = setup();
    act(() => showCaptionElement(video, "en"));
    act(() => {
      captionElement(video, "fr")?.dispatchEvent(new Event("error"));
    });
    expect(status()).toBe("loading");
    act(() => showCaptionElement(video, null));
    act(() => vi.advanceTimersByTime(5000));
    expect(status()).toBe("idle");
  });

  it("waits and fetches again while the server is still extracting the track", async () => {
    probeAnswer = 5;
    const video = setup();
    act(() => showCaptionElement(video, "en"));
    const first = captionElement(video, "en");
    await act(async () => {
      first?.dispatchEvent(new Event("error"));
    });
    expect(status()).toBe("preparing");
    act(() => vi.advanceTimersByTime(4999));
    expect(captionElement(video, "en")).toBe(first);
    act(() => vi.advanceTimersByTime(1));
    const second = captionElement(video, "en");
    expect(second).not.toBe(first);
    expect(status()).toBe("preparing");
    act(() => {
      second?.dispatchEvent(new Event("load"));
    });
    expect(status()).toBe("loaded");
  });

  it("stops retrying after the cap and reports the failure", async () => {
    probeAnswer = 1;
    const video = setup();
    act(() => showCaptionElement(video, "en"));
    for (let attempt = 0; attempt < 2; attempt++) {
      await act(async () => {
        captionElement(video, "en")?.dispatchEvent(new Event("error"));
      });
      expect(status()).toBe("preparing");
      act(() => vi.advanceTimersByTime(1000));
    }
    await act(async () => {
      captionElement(video, "en")?.dispatchEvent(new Event("error"));
    });
    expect(probe).toHaveBeenCalledTimes(2);
    expect(status()).toBe("failed");
  });

  it("drops a pending retry when the viewer picks another track", async () => {
    probeAnswer = 5;
    const video = setup();
    act(() => showCaptionElement(video, "en"));
    const first = captionElement(video, "en");
    await act(async () => {
      first?.dispatchEvent(new Event("error"));
    });
    act(() => showCaptionElement(video, "fr"));
    act(() => vi.advanceTimersByTime(5000));
    expect(captionElement(video, "en")).toBe(first);
    expect(status()).toBe("failed");
  });
});

describe("probeCaptionSource (CORE-2315)", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("reads the server's still-extracting answer and its Retry-After", async () => {
    const fetchMock = vi.fn(
      async () =>
        new Response("subtitle extraction in progress; retry", {
          status: 503,
          headers: { "Retry-After": "5" },
        }),
    );
    vi.stubGlobal("fetch", fetchMock);
    await expect(probeCaptionSource("/api/media/subtitle/en")).resolves.toBe(5);
  });

  it("treats any other failure as a real error", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("nope", { status: 404 })),
    );
    await expect(probeCaptionSource("/s")).resolves.toBeNull();
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        throw new TypeError("offline");
      }),
    );
    await expect(probeCaptionSource("/s")).resolves.toBeNull();
  });

  it("treats a 503 without Retry-After as the server's hard failure", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("subtitle unavailable", { status: 503 })),
    );
    await expect(probeCaptionSource("/s")).resolves.toBeNull();
  });

  it("reloads at once when the track became ready while the probe waited", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("WEBVTT\n", { status: 200 })),
    );
    await expect(probeCaptionSource("/s")).resolves.toBe(0);
  });
});

describe("styled caption selection", () => {
  const styledTrack = {
    id: "muxed-ass",
    format: "ass",
    streamIndex: 2,
    url: "/api/media/subtitle/grant/2.vtt",
  };
  const sidecarTrack = {
    id: "sidecar-ssa",
    format: "ssa",
    url: "/api/media/sidecar/grant/0.ass",
  };

  beforeEach(() => {
    vi.stubGlobal("Worker", class {});
    Object.defineProperty(HTMLCanvasElement.prototype, "transferControlToOffscreen", {
      configurable: true,
      value: vi.fn(),
    });
    startStyledCaption.mockClear();
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    Reflect.deleteProperty(HTMLCanvasElement.prototype, "transferControlToOffscreen");
  });

  it("does not evaluate the renderer module for a non-ASS pick", async () => {
    const video = setup();
    attachCaptionElements(
      video,
      [styledTrack, { id: "plain", format: "srt", url: "/plain.vtt" }],
      "en",
    );
    await act(async () => showCaptionElement(video, "plain"));
    expect(loadStyledCaptionModule).not.toHaveBeenCalled();
    expect(startStyledCaption).not.toHaveBeenCalled();
    expect(captionElement(video, "plain")?.getAttribute("src")).toBe("/plain.vtt");
    expect(captionElement(video, styledTrack.id)?.getAttribute("src")).toBeNull();
  });

  it("loads on ASS selection and aborts the renderer when captions are turned off", async () => {
    const video = setup();
    attachCaptionElements(video, [styledTrack], "en");
    const track = captionElement(video, styledTrack.id);
    // jsdom does not expose TextTrack on HTMLTrackElement.
    Object.defineProperty(track, "track", { value: { mode: "disabled" } });
    expect(startStyledCaption).not.toHaveBeenCalled();
    await act(async () => showCaptionElement(video, styledTrack.id));
    expect(loadStyledCaptionModule).toHaveBeenCalledOnce();
    expect(startStyledCaption).toHaveBeenCalledWith(
      video,
      "/api/media/subtitle/grant/2.ass",
      expect.any(AbortSignal),
      expect.any(Function),
    );
    expect(captionElement(video, styledTrack.id)?.getAttribute("src")).toBeNull();
    expect(shownCaptionElement(video)).toBe(styledTrack.id);
    expect(status()).toBe("loaded");
    const signal = startStyledCaption.mock.calls[0]?.[2];
    act(() => showCaptionElement(video, null));
    expect(signal?.aborted).toBe(true);
  });

  it("uses WebVTT when SSA initialization rejects", async () => {
    const video = setup();
    attachCaptionElements(video, [sidecarTrack], "en");
    startStyledCaption.mockRejectedValueOnce(new Error("WASM failed"));
    await act(async () => showCaptionElement(video, sidecarTrack.id));
    expect(startStyledCaption.mock.calls[0]?.[1]).toBe(sidecarTrack.url);
    expect(captionElement(video, sidecarTrack.id)?.getAttribute("src")).toBe(
      "/api/media/sidecar/grant/0.vtt",
    );
    expect(status()).toBe("loading");
  });

  it("uses WebVTT after a started worker fails", async () => {
    const video = setup();
    attachCaptionElements(video, [styledTrack], "en");
    await act(async () => showCaptionElement(video, styledTrack.id));
    const onError = startStyledCaption.mock.calls[0]?.[3];
    expect(onError).toBeTypeOf("function");
    act(() => onError?.());
    expect(captionElement(video, styledTrack.id)?.getAttribute("src")).toBe(styledTrack.url);
  });

  it("uses WebVTT without asking for the renderer when workers are unavailable", async () => {
    vi.stubGlobal("Worker", undefined);
    const video = setup();
    attachCaptionElements(video, [styledTrack], "en");
    await act(async () => showCaptionElement(video, styledTrack.id));
    expect(startStyledCaption).not.toHaveBeenCalled();
    expect(captionElement(video, styledTrack.id)?.getAttribute("src")).toBe(styledTrack.url);
  });
});
