import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { SidebarResizer } from "./SidebarResizer";

// jsdom has no PointerEvent, so fireEvent would drop button and pointerId.
if (typeof window.PointerEvent === "undefined") {
  class TestPointerEvent extends MouseEvent {
    pointerId: number;
    constructor(type: string, init: PointerEventInit = {}) {
      super(type, init);
      this.pointerId = init.pointerId ?? 0;
    }
  }
  window.PointerEvent = TestPointerEvent as unknown as typeof PointerEvent;
}

afterEach(cleanup);

function setup(width = 200) {
  const props = {
    sidebarId: "side",
    width,
    onChange: vi.fn(),
    onCommit: vi.fn(),
    onResizingChange: vi.fn(),
  };
  const view = render(
    <I18nProvider>
      <SidebarResizer {...props} />
    </I18nProvider>,
  );
  return { ...props, ...view, handle: screen.getByRole("separator") };
}

describe("SidebarResizer (CORE-2734)", () => {
  it("leaves modified arrow keys to the browser", () => {
    const { handle, onChange } = setup();
    const altLeft = fireEvent.keyDown(handle, { key: "ArrowLeft", altKey: true });
    expect(altLeft).toBe(true); // not prevented
    expect(onChange).not.toHaveBeenCalled();
    fireEvent.keyDown(handle, { key: "ArrowLeft" });
    expect(onChange).toHaveBeenCalledWith(184);
  });

  it("focuses the separator when a drag starts", () => {
    const { handle } = setup();
    fireEvent.pointerDown(handle, { button: 0, pointerId: 1, clientX: 200 });
    expect(handle).toHaveFocus();
  });

  it("ignores a second pointer while one drag is active", () => {
    const { handle, onChange, onResizingChange } = setup();
    fireEvent.pointerDown(handle, { button: 0, pointerId: 1, clientX: 200 });
    fireEvent.pointerDown(handle, { button: 0, pointerId: 2, clientX: 300 });
    fireEvent.pointerMove(handle, { pointerId: 1, clientX: 240 });
    expect(onChange).toHaveBeenLastCalledWith(240);
    fireEvent.pointerUp(handle, { pointerId: 2 });
    expect(onResizingChange).not.toHaveBeenCalledWith(false);
    fireEvent.pointerUp(handle, { pointerId: 1 });
    expect(onResizingChange).toHaveBeenLastCalledWith(false);
  });

  it("ends the drag when pointer capture is lost", () => {
    const { handle, onCommit, onResizingChange } = setup();
    fireEvent.pointerDown(handle, { button: 0, pointerId: 1, clientX: 200 });
    fireEvent.lostPointerCapture(handle, { pointerId: 1 });
    expect(onResizingChange).toHaveBeenLastCalledWith(false);
    expect(onCommit).toHaveBeenCalledWith(200);
  });

  it("clears the resizing state when unmounted mid-drag", () => {
    const { handle, unmount, onResizingChange } = setup();
    fireEvent.pointerDown(handle, { button: 0, pointerId: 1, clientX: 200 });
    expect(onResizingChange).toHaveBeenLastCalledWith(true);
    unmount();
    expect(onResizingChange).toHaveBeenLastCalledWith(false);
  });
});
