import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { createRef, type RefObject } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { MiniPlayer } from "./MiniPlayer";
import { loadWaveform, resetWaveformCache, type WaveformInput } from "./useWaveform";
import * as viewerIntent from "./viewerIntent";

beforeEach(() => resetWaveformCache());
afterEach(() => vi.restoreAllMocks());

function mockWaveformFetch(byURL: Record<string, number[]>) {
  return vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
    const points = byURL[String(input)];
    if (!points) return new Response("missing", { status: 404 });
    return new Response(JSON.stringify({ version: 1, points }), { status: 200 });
  });
}

function Mini({
  mediaID,
  waveform,
  durationSeconds,
  videoRef = createRef<HTMLVideoElement>(),
}: {
  mediaID: string;
  waveform?: Omit<WaveformInput, "mediaID">;
  durationSeconds?: number;
  videoRef?: RefObject<HTMLVideoElement | null>;
}) {
  return (
    <I18nProvider initialLocale="en">
      <video ref={videoRef} />
      <MiniPlayer
        videoRef={videoRef}
        title="Long Weekend"
        artist=""
        artworkURL=""
        mediaID={mediaID}
        songScale={1}
        durationSeconds={durationSeconds}
        onOpen={() => {}}
        onStop={() => {}}
        waveform={waveform}
      />
    </I18nProvider>
  );
}

describe("MiniPlayer waveform strip (CORE-2404)", () => {
  it("shows peaks the full player already cached without fetching again", async () => {
    const fetchMock = mockWaveformFetch({ "/w/a": [0.2, 0.8] });
    await loadWaveform("a", "/w/a");
    render(<Mini mediaID="a" waveform={{ infoMediaID: "a", url: "/w/a" }} />);

    const bar = screen.getByRole("slider", { name: "Playback position" });
    expect(bar).toHaveClass("has-waveform");
    expect(screen.getByTestId("scrubber-waveform")).not.toHaveClass("is-placeholder");
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("falls back to the plain strip when the waveform cannot load", async () => {
    mockWaveformFetch({});
    render(<Mini mediaID="a" waveform={{ infoMediaID: "a", url: "/w/a" }} />);
    await waitFor(() => expect(screen.queryByTestId("scrubber-waveform")).not.toBeInTheDocument());
    expect(screen.getByRole("slider", { name: "Playback position" })).not.toHaveClass(
      "has-waveform",
    );
  });

  it("shows the prefetched next track's peaks at once after advancing while docked", async () => {
    mockWaveformFetch({ "/w/a": [0.2, 0.5], "/w/b": [0.7, 0.3] });
    const waveform = { infoMediaID: "a", url: "/w/a", nextMediaID: "b", nextURL: "/w/b" };
    const view = render(<Mini mediaID="a" waveform={waveform} />);
    await waitFor(() =>
      expect(screen.getByTestId("scrubber-waveform")).not.toHaveClass("is-placeholder"),
    );
    await waitFor(() => expect(globalThis.fetch).toHaveBeenCalledTimes(2));

    view.rerender(<Mini mediaID="b" waveform={{}} />);
    expect(screen.getByTestId("scrubber-waveform")).not.toHaveClass("is-placeholder");
  });

  it("keeps the plain strip without a waveform", () => {
    render(<Mini mediaID="v" />);
    expect(screen.queryByTestId("scrubber-waveform")).not.toBeInTheDocument();
  });
});

describe("MiniPlayer seek strip (CORE-3340)", () => {
  it("seeks within the server's length, not a live transcode's encoded prefix, and marks viewer intent", () => {
    const intent = vi.spyOn(viewerIntent, "markViewerIntent");
    const videoRef = createRef<HTMLVideoElement>();
    render(<Mini mediaID="a" durationSeconds={200} videoRef={videoRef} />);
    const video = videoRef.current as HTMLVideoElement;
    Object.defineProperty(video, "duration", { configurable: true, value: 20 });
    fireEvent(video, new Event("durationchange"));

    const bar = screen.getByRole("slider", { name: "Playback position" });
    expect(bar).toHaveAttribute("aria-valuemax", "200");
    fireEvent.keyDown(bar, { key: "End" });
    expect(video.currentTime).toBe(200);
    expect(intent).toHaveBeenCalled();
  });
});
