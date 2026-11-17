import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { act, fireEvent, render } from "@testing-library/react";
import { useRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useChromeAutoHide } from "./useChromeAutoHide";

function Harness() {
  const stageRef = useRef<HTMLDivElement>(null);
  const videoRef = useRef<HTMLVideoElement>(null);
  const { chromeHidden } = useChromeAutoHide({ stageRef, videoRef, menuOpen: false });
  return (
    <div ref={stageRef} data-testid="stage" data-hidden={String(chromeHidden)}>
      <video ref={videoRef} />
      <button type="button">Settings</button>
    </div>
  );
}

function playingVideo(container: HTMLElement): HTMLVideoElement {
  const video = container.querySelector("video") as HTMLVideoElement;
  Object.defineProperty(video, "paused", { configurable: true, get: () => false });
  return video;
}

describe("useChromeAutoHide (CORE-2322)", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("reveals the hidden chrome on a press with no pointer move before it", () => {
    const { container, getByTestId, getByRole } = render(<Harness />);
    const video = playingVideo(container);
    act(() => {
      video.dispatchEvent(new Event("play"));
    });
    act(() => {
      vi.advanceTimersByTime(3500);
    });
    expect(getByTestId("stage").dataset.hidden).toBe("true");
    fireEvent.pointerDown(getByRole("button", { name: "Settings" }));
    expect(getByTestId("stage").dataset.hidden).toBe("false");
  });

  it("leaves hidden controls clickable for a mouse and blocks only touch", () => {
    for (const [file, selector] of [
      [
        "src/areas/viewer/player/player-deferred.css",
        ".bf-player-stage.is-chrome-hidden .bf-transport *",
      ],
      [
        "src/components/chrome/chrome.css",
        "main:has(> .bf-player-stage.is-chrome-hidden) > .bf-player-bar > *",
      ],
    ]) {
      const css = readFileSync(resolve(process.cwd(), file), "utf8");
      const rule = css.indexOf(`${selector} {\n    pointer-events: none;`);
      expect(rule, file).toBeGreaterThan(-1);
      const guard = css.lastIndexOf("@media not all and (pointer: fine) {", rule);
      expect(guard, file).toBeGreaterThan(-1);
      expect(css.slice(guard, rule).includes("}"), file).toBe(false);
    }
  });
});
