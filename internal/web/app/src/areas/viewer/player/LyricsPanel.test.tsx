import "@testing-library/jest-dom/vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { createRef } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { fetchMediaLyrics, type MediaLyrics } from "../lib/mediaDetail";
import { LyricsPanel, lineAt } from "./LyricsPanel";

vi.mock("../lib/mediaDetail", () => ({ fetchMediaLyrics: vi.fn() }));
const fetchLyrics = vi.mocked(fetchMediaLyrics);

const synced: MediaLyrics = {
  synced: true,
  source: "sidecar-lrc",
  lines: [
    { startMs: 1000, text: "First" },
    { startMs: 4000, text: "Second" },
    { startMs: 8000, text: "Third" },
  ],
};

function pausedVideo(currentTime: number) {
  const video = document.createElement("video");
  Object.defineProperty(video, "paused", { value: true });
  video.currentTime = currentTime;
  return video;
}

async function renderPanel(lyrics: MediaLyrics | Error, video = pausedVideo(0), songScale = 1) {
  if (lyrics instanceof Error) fetchLyrics.mockRejectedValue(lyrics);
  else fetchLyrics.mockResolvedValue(lyrics);
  const videoRef = createRef<HTMLVideoElement>() as { current: HTMLVideoElement | null };
  videoRef.current = video;
  render(
    <I18nProvider>
      <LyricsPanel mediaID="media-1" videoRef={videoRef} songScale={songScale} />
    </I18nProvider>,
  );
  await act(async () => {});
  return video;
}

describe("LyricsPanel", () => {
  beforeEach(() => {
    fetchLyrics.mockReset();
  });

  it("brightens the line playing now and dims the rest", async () => {
    const video = await renderPanel(synced, pausedVideo(5));
    expect(fetchLyrics).toHaveBeenCalledWith("media-1", expect.any(AbortSignal));
    expect(screen.getByRole("button", { name: "First" })).toHaveAttribute("data-state", "past");
    expect(screen.getByRole("button", { name: "Second" })).toHaveAttribute("data-state", "current");
    expect(screen.getByRole("button", { name: "Second" })).toHaveAttribute("aria-current", "true");
    expect(screen.getByRole("button", { name: "Third" })).toHaveAttribute("data-state", "upcoming");

    video.currentTime = 9;
    act(() => {
      video.dispatchEvent(new Event("seeked"));
    });
    expect(screen.getByRole("button", { name: "Third" })).toHaveAttribute("data-state", "current");
  });

  it("seeks to a line when it is clicked, in stream time under a tempo", async () => {
    const video = await renderPanel(synced, pausedVideo(0), 1.25);
    fireEvent.click(screen.getByRole("button", { name: "Third" }));
    expect(video.currentTime).toBeCloseTo(8 / 1.25);
  });

  it("shows plain lyrics as text", async () => {
    await renderPanel({
      synced: false,
      source: "metadata",
      lines: [{ text: "Verse one" }, { text: "" }, { text: "Chorus" }],
    });
    expect(screen.getByText("Verse one").tagName).toBe("P");
    expect(screen.getByText("Chorus")).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it("says No lyrics when there are none", async () => {
    await renderPanel({ synced: false, source: "", lines: [] });
    expect(screen.getByText("No lyrics")).toBeInTheDocument();
  });

  it("says No lyrics when the read fails", async () => {
    await renderPanel(new Error("network"));
    expect(screen.getByText("No lyrics")).toBeInTheDocument();
  });
});

describe("lineAt", () => {
  it("finds the last line that has started", () => {
    expect(lineAt(synced.lines, 0)).toBe(-1);
    expect(lineAt(synced.lines, 1000)).toBe(0);
    expect(lineAt(synced.lines, 7999)).toBe(1);
    expect(lineAt(synced.lines, 60000)).toBe(2);
    expect(lineAt([], 5)).toBe(-1);
  });
});
