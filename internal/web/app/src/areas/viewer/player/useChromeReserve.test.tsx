import { act, render } from "@testing-library/react";
import { useRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useChromeReserve } from "./useChromeReserve";

// CORE-3373: a karaoke picture is laid out between the top bar and the transport.

const observers: Array<() => void> = [];

beforeEach(() => {
  vi.stubGlobal(
    "ResizeObserver",
    class {
      constructor(private readonly cb: () => void) {
        observers.push(() => this.cb());
      }
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  );
});

afterEach(() => {
  observers.length = 0;
  vi.unstubAllGlobals();
});

function rect(top: number, bottom: number): DOMRect {
  return {
    top,
    bottom,
    left: 0,
    right: 0,
    width: 0,
    height: bottom - top,
    x: 0,
    y: top,
  } as DOMRect;
}

function Player({
  active,
  transport,
  skip = false,
}: {
  active: boolean;
  transport: boolean;
  skip?: boolean;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const reserve = useChromeReserve(ref, active);
  return (
    <main>
      <div className="bf-player-bar">
        <button type="button" data-testid="back">
          Back
        </button>
      </div>
      <div
        ref={ref}
        data-testid="stage"
        data-reserve={reserve ? `${reserve.top}/${reserve.bottom}` : "none"}
      >
        {transport && skip && <button type="button" className="bf-control is-skip-segment" />}
        {transport && <div className="bf-transport-bottom" />}
      </div>
    </main>
  );
}

function layout(stage: HTMLElement, bottomTop: number, skipTop?: number) {
  stage.getBoundingClientRect = () => rect(0, 720);
  const back = stage.ownerDocument.querySelector("[data-testid=back]") as HTMLElement;
  back.getBoundingClientRect = () => rect(12, 56.2);
  const bottom = stage.querySelector(".bf-transport-bottom") as HTMLElement | null;
  if (bottom) bottom.getBoundingClientRect = () => rect(bottomTop, 720);
  const skip = stage.querySelector(".is-skip-segment") as HTMLElement | null;
  if (skip && skipTop !== undefined) skip.getBoundingClientRect = () => rect(skipTop, skipTop + 40);
}

function fire() {
  act(() => {
    for (const cb of observers) cb();
  });
}

describe("useChromeReserve", () => {
  it("reserves the top bar's controls and the transport, and keeps both after the transport unmounts", () => {
    const { getByTestId, rerender } = render(<Player active transport />);
    const stage = getByTestId("stage");
    layout(stage, 580.4);
    fire();
    expect(stage.dataset.reserve).toBe("57/140");

    rerender(<Player active transport={false} />);
    fire();
    expect(stage.dataset.reserve).toBe("57/140");
  });

  it("reserves a segment-skip button above the transport", () => {
    const { getByTestId } = render(<Player active transport skip />);
    const stage = getByTestId("stage");
    layout(stage, 580, 530);
    fire();
    expect(stage.dataset.reserve).toBe("57/190");
  });

  it("drops the top bar's room while the stage is fullscreen", () => {
    const { getByTestId } = render(<Player active transport />);
    const stage = getByTestId("stage");
    layout(stage, 600);
    fire();
    expect(stage.dataset.reserve).toBe("57/120");
    Object.defineProperty(document, "fullscreenElement", { configurable: true, get: () => stage });
    try {
      act(() => {
        document.dispatchEvent(new Event("fullscreenchange"));
      });
      expect(stage.dataset.reserve).toBe("0/120");
    } finally {
      Object.defineProperty(document, "fullscreenElement", { configurable: true, get: () => null });
    }
    act(() => {
      document.dispatchEvent(new Event("fullscreenchange"));
    });
    expect(stage.dataset.reserve).toBe("57/120");
  });

  it("reports nothing while inactive", () => {
    const { getByTestId, rerender } = render(<Player active transport />);
    const stage = getByTestId("stage");
    layout(stage, 600);
    fire();
    expect(stage.dataset.reserve).toBe("57/120");
    rerender(<Player active={false} transport />);
    expect(stage.dataset.reserve).toBe("none");
  });
});
