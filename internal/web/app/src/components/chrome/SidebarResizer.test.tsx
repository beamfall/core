import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
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
    fireEvent.pointerMove(handle, { pointerId: 1, clientX: 230 });
    fireEvent.lostPointerCapture(handle, { pointerId: 1 });
    expect(onResizingChange).toHaveBeenLastCalledWith(false);
    expect(onCommit).toHaveBeenCalledWith(230);
  });

  it("clears the resizing state when unmounted mid-drag", () => {
    const { handle, unmount, onResizingChange } = setup();
    fireEvent.pointerDown(handle, { button: 0, pointerId: 1, clientX: 200 });
    expect(onResizingChange).toHaveBeenLastCalledWith(true);
    unmount();
    expect(onResizingChange).toHaveBeenLastCalledWith(false);
  });

  describe("width control without dragging (CORE-2736)", () => {
    function press(handle: HTMLElement, moveTo?: number) {
      fireEvent.pointerDown(handle, { button: 0, pointerId: 1, clientX: 200 });
      if (moveTo != null) fireEvent.pointerMove(handle, { pointerId: 1, clientX: moveTo });
      fireEvent.pointerUp(handle, { pointerId: 1 });
      fireEvent.click(handle, { detail: 1, clientY: 120 });
    }

    it("opens on a click that stays put and steps the width", async () => {
      const { handle, onChange, onCommit } = setup();
      press(handle);
      const wider = await screen.findByRole("button", { name: "Wider" });
      expect(screen.getByText("Sidebar width")).toBeInTheDocument();
      expect(onCommit).not.toHaveBeenCalled();
      fireEvent.click(wider);
      expect(onChange).toHaveBeenLastCalledWith(216);
      expect(onCommit).toHaveBeenLastCalledWith(216);
      fireEvent.click(screen.getByRole("button", { name: "Narrower" }));
      expect(onCommit).toHaveBeenLastCalledWith(184);
    });

    it("disables the step past a limit and the reset at the default", async () => {
      const { handle, onCommit } = setup(360);
      press(handle);
      const wider = await screen.findByRole("button", { name: "Wider" });
      expect(wider).toHaveAttribute("aria-disabled", "true");
      fireEvent.click(wider);
      expect(onCommit).not.toHaveBeenCalled();
      fireEvent.click(screen.getByRole("button", { name: "Default width" }));
      expect(onCommit).toHaveBeenLastCalledWith(200);
    });

    it("does not open after a drag", async () => {
      const { handle, onCommit } = setup();
      press(handle, 260);
      expect(onCommit).toHaveBeenLastCalledWith(260);
      await act(async () => {});
      expect(screen.queryByRole("button", { name: "Wider" })).toBeNull();
    });

    it("closes on a second click and on a double-click reset", async () => {
      const { handle, onCommit } = setup(232);
      press(handle);
      await screen.findByRole("button", { name: "Wider" });
      press(handle);
      await act(async () => {});
      expect(screen.queryByRole("button", { name: "Wider" })).toBeNull();
      press(handle);
      await screen.findByRole("button", { name: "Wider" });
      fireEvent.click(handle, { detail: 2 });
      fireEvent.doubleClick(handle);
      expect(onCommit).toHaveBeenLastCalledWith(200);
      await act(async () => {});
      expect(screen.queryByRole("button", { name: "Wider" })).toBeNull();
    });
  });
});
