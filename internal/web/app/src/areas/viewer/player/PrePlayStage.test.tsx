import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PrePlayItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { fetchPrePlayStream } from "../lib/prePlay";
import { PrePlayStage } from "./PrePlayStage";

vi.mock("../lib/prePlay", () => ({ fetchPrePlayStream: vi.fn() }));

function item(mediaID: string, title: string): PrePlayItem {
  return { mediaID, title, durationSeconds: 30, role: "clip", label: "" };
}

function renderStage(items: PrePlayItem[], onDone = vi.fn()) {
  render(
    <I18nProvider>
      <PrePlayStage items={items} onDone={onDone} />
    </I18nProvider>,
  );
  return onDone;
}

function video(): HTMLVideoElement {
  const el = document.querySelector("video");
  if (!el) throw new Error("no video element");
  return el;
}

describe("PrePlayStage", () => {
  let play: ReturnType<typeof vi.fn<() => Promise<void>>>;

  beforeEach(() => {
    play = vi.fn<() => Promise<void>>().mockResolvedValue(undefined);
    vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(play);
    vi.spyOn(HTMLMediaElement.prototype, "load").mockImplementation(() => {});
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.mocked(fetchPrePlayStream).mockReset();
  });

  it("plays a direct-play item from its stream url", async () => {
    vi.mocked(fetchPrePlayStream).mockResolvedValue({ kind: "direct-play", url: "/clip-a.mp4" });
    renderStage([item("clip-a", "Studio intro")]);
    await waitFor(() => expect(play).toHaveBeenCalled());
    expect(video().getAttribute("src")).toBe("/clip-a.mp4");
    expect(video()).not.toHaveAttribute("controls");
  });

  it("passes over an item that would need a server transcode", async () => {
    vi.mocked(fetchPrePlayStream).mockImplementation(async (mediaID) =>
      mediaID === "clip-a"
        ? { kind: "hls", url: "/api/media/hls/clip-a/master.m3u8" }
        : { kind: "direct-play", url: "/clip-b.mp4" },
    );
    renderStage([item("clip-a", "First clip"), item("clip-b", "Second clip")]);
    await waitFor(() => expect(screen.getByText("Second clip")).toBeInTheDocument());
    await waitFor(() => expect(video().getAttribute("src")).toBe("/clip-b.mp4"));
    expect(play).toHaveBeenCalledTimes(1);
  });

  it("finishes when every item needs a transcode", async () => {
    vi.mocked(fetchPrePlayStream).mockResolvedValue({ kind: "hls", url: "/x.m3u8" });
    const onDone = renderStage([item("clip-a", "Only clip")]);
    await waitFor(() => expect(onDone).toHaveBeenCalled());
    expect(play).not.toHaveBeenCalled();
  });

  it("shows native controls when the browser refuses autoplay", async () => {
    play.mockRejectedValue(new DOMException("blocked", "NotAllowedError"));
    vi.mocked(fetchPrePlayStream).mockResolvedValue({ kind: "direct-play", url: "/clip-a.mp4" });
    const onDone = renderStage([item("clip-a", "Studio intro")]);
    await waitFor(() => expect(video()).toHaveAttribute("controls"));
    expect(onDone).not.toHaveBeenCalled();
    expect(screen.getByText("Studio intro")).toBeInTheDocument();
  });

  it("advances when the clip ends", async () => {
    vi.mocked(fetchPrePlayStream).mockResolvedValue({ kind: "direct-play", url: "/clip.mp4" });
    const onDone = renderStage([item("clip-a", "Studio intro")]);
    await waitFor(() => expect(play).toHaveBeenCalled());
    act(() => {
      fireEvent.ended(video());
    });
    await waitFor(() => expect(onDone).toHaveBeenCalled());
  });
});
