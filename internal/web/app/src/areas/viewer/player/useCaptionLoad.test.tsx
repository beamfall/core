import "@testing-library/jest-dom/vitest";
import { act, render, screen } from "@testing-library/react";
import { createRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { attachCaptionElements, captionElement, showCaptionElement } from "./streamTracks";
import { useCaptionLoad } from "./useCaptionLoad";

const videoRef = createRef<HTMLVideoElement>();

function Harness() {
  const { status, retry } = useCaptionLoad(videoRef, 1000);
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
  beforeEach(() => vi.useFakeTimers());
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

  it("reports a track that errors, and retries it with a fresh element", () => {
    const video = setup();
    act(() => showCaptionElement(video, "en"));
    const first = captionElement(video, "en");
    act(() => {
      first?.dispatchEvent(new Event("error"));
    });
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
});
