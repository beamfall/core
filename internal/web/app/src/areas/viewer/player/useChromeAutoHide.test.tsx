import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { act, fireEvent, render } from "@testing-library/react";
import { useRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { idleChromeClass, useChromeAutoHide } from "./useChromeAutoHide";

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

// CORE-3351: the music stage dims instead of hiding when idle.
function AudioHarness({ ambient = false }: { ambient?: boolean }) {
  const stageRef = useRef<HTMLDivElement>(null);
  const videoRef = useRef<HTMLVideoElement>(null);
  const { chromeHidden } = useChromeAutoHide({
    stageRef,
    videoRef,
    menuOpen: false,
  });
  return (
    <div
      ref={stageRef}
      data-testid="stage"
      className={
        "bf-player-stage" +
        idleChromeClass({
          playing: true,
          chromeHidden,
          audioStage: true,
          ambient,
        })
      }
    >
      <video ref={videoRef} />
      <button type="button">Play</button>
    </div>
  );
}

describe("music stage idle dim (CORE-3351)", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  function startPlaying(container: HTMLElement) {
    const video = playingVideo(container);
    act(() => {
      video.dispatchEvent(new Event("play"));
    });
    return video;
  }

  it("dims after the idle delay and wakes on activity", () => {
    const { container, getByTestId } = render(<AudioHarness />);
    startPlaying(container);
    const stage = getByTestId("stage");
    act(() => {
      vi.advanceTimersByTime(2500);
    });
    expect(stage.className).not.toContain("is-chrome-dim");
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(stage.className).toContain("is-chrome-dim");
    expect(stage.className).not.toContain("is-chrome-hidden");

    fireEvent.pointerMove(stage);
    expect(stage.className).not.toContain("is-chrome-dim");

    act(() => {
      vi.advanceTimersByTime(3500);
    });
    expect(stage.className).toContain("is-chrome-dim");
    fireEvent.keyDown(window, { key: "a" });
    expect(stage.className).not.toContain("is-chrome-dim");
  });

  it("never dims while paused", () => {
    const { container, getByTestId } = render(<AudioHarness />);
    const video = startPlaying(container);
    Object.defineProperty(video, "paused", {
      configurable: true,
      get: () => true,
    });
    act(() => {
      video.dispatchEvent(new Event("pause"));
    });
    act(() => {
      vi.advanceTimersByTime(10_000);
    });
    expect(getByTestId("stage").className).not.toContain("is-chrome-dim");
  });

  it("leaves Ambient mode to its own idle and video to hiding", () => {
    expect(
      idleChromeClass({
        playing: true,
        chromeHidden: true,
        audioStage: true,
        ambient: true,
      }),
    ).toBe("");
    expect(
      idleChromeClass({
        playing: true,
        chromeHidden: true,
        audioStage: false,
        ambient: false,
      }),
    ).toBe(" is-chrome-hidden");
    expect(
      idleChromeClass({
        playing: false,
        chromeHidden: true,
        audioStage: true,
        ambient: false,
      }),
    ).toBe("");
  });

  it("dims to translucent, stays up under the pointer, focus or an open menu, and skips the fade for reduced motion", () => {
    const css = readFileSync(
      resolve(process.cwd(), "src/areas/viewer/player/player-deferred.css"),
      "utf8",
    );
    const start = css.indexOf("main:has(> .bf-player-stage.is-chrome-dim):not(");
    expect(start).toBeGreaterThan(-1);
    const block = css.slice(start, css.indexOf("}", css.indexOf(") {", start)) + 1);
    for (const awake of [
      ".bf-transport:hover",
      ".bf-transport :focus-visible",
      ".bf-player-bar:hover",
      ".bf-player-bar :focus-visible",
      '[aria-expanded="true"]',
    ]) {
      expect(block).toContain(awake);
    }
    const chrome = Number(/--bf-idle-chrome:\s*([\d.]+)/.exec(block)?.[1]);
    const card = Number(/--bf-idle-card:\s*([\d.]+)/.exec(block)?.[1]);
    expect(chrome).toBeGreaterThan(0);
    expect(chrome).toBeLessThanOrEqual(0.3);
    expect(card).toBeGreaterThan(chrome);
    expect(card).toBeLessThan(1);

    const reduced = css.indexOf("@media (prefers-reduced-motion: reduce) {", start);
    const reducedBlock = css.slice(reduced, css.indexOf("}", reduced) + 1);
    expect(reducedBlock).toContain(".bf-player-stage.is-chrome-dim .bf-transport");
    expect(reducedBlock).toContain(".bf-player-stage.is-chrome-dim .bf-audio-artwork");
    expect(reducedBlock).toContain("transition: none;");
  });
});
