import { cleanup, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useVirtualGrid } from "./useVirtualGrid";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

// A one-column grid of 100px rows (jsdom reports no card height, so the row
// height is pinned through the fallback plus a zero gap).
function gridScrolledTo(scrolledPast: number) {
  const grid = document.createElement("ul");
  grid.style.rowGap = "0px";
  document.body.append(grid);
  vi.spyOn(grid, "getBoundingClientRect").mockReturnValue({ top: -scrolledPast } as DOMRect);
  return { current: grid };
}

describe("useVirtualGrid", () => {
  it("keeps items in the window when the list shrinks under a deep scroll", () => {
    const gridRef = gridScrolledTo(300 * 500);
    const deep = renderHook(() =>
      useVirtualGrid({ gridRef, itemCount: 1000, enabled: true, columns: 1 }),
    ).result.current;
    expect(deep?.startIndex).toBe(498);

    const shrunk = renderHook(() =>
      useVirtualGrid({ gridRef, itemCount: 60, enabled: true, columns: 1 }),
    ).result.current;
    expect(shrunk).not.toBeNull();
    expect(shrunk?.endIndex).toBe(60);
    expect(shrunk?.startIndex).toBeLessThan(60);
    expect(shrunk?.topPad).toBe((shrunk?.startIndex ?? 0) * 300);
    expect(shrunk?.bottomPad).toBe(0);
  });
});
