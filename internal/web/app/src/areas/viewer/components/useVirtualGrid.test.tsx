import { cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useVirtualGrid, virtualGridScrollTop } from "./useVirtualGrid";

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

  it("measures rows afresh when the layout switches between grid and list", async () => {
    const gridRef = gridScrolledTo(0);
    const card = document.createElement("li");
    gridRef.current.append(card);
    const height = vi.spyOn(card, "getBoundingClientRect");
    height.mockReturnValue({ height: 400 } as DOMRect);
    const view = renderHook(
      ({ columns }: { columns?: number }) =>
        useVirtualGrid({ gridRef, itemCount: 1000, enabled: true, columns }),
      { initialProps: {} as { columns?: number } },
    );
    expect(virtualGridScrollTop(gridRef.current, 6)).toBeGreaterThan(0);

    // The list's rows are shorter than the posters were.
    height.mockReturnValue({ height: 80 } as DOMRect);
    view.rerender({ columns: 1 });
    await waitFor(() =>
      expect(view.result.current?.bottomPad).toBe(
        (1000 - (view.result.current?.endIndex ?? 0)) * 80,
      ),
    );
    expect(virtualGridScrollTop(gridRef.current, 10, 1)).toBe(800);
  });
});
