import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setPosterSize } from "@/components/chrome/posterSize";
import { useVirtualGrid, virtualGridScrollTop } from "./useVirtualGrid";

afterEach(() => {
  cleanup();
  setPosterSize("large");
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

  it("measures rows afresh when one column count switches layout, list to table (CORE-2443)", async () => {
    const gridRef = gridScrolledTo(0);
    const card = document.createElement("li");
    gridRef.current.append(card);
    const height = vi.spyOn(card, "getBoundingClientRect");
    height.mockReturnValue({ height: 80 } as DOMRect);
    const view = renderHook(
      ({ layout }: { layout: string }) =>
        useVirtualGrid({ gridRef, itemCount: 1000, enabled: true, columns: 1, layout }),
      { initialProps: { layout: "list" } },
    );
    await waitFor(() => expect(virtualGridScrollTop(gridRef.current, 10, 1)).toBe(800));

    // The table's rows are shorter than the list's, under the same one column.
    height.mockReturnValue({ height: 36 } as DOMRect);
    view.rerender({ layout: "table" });
    await waitFor(() =>
      expect(view.result.current?.bottomPad).toBe(
        (1000 - (view.result.current?.endIndex ?? 0)) * 36,
      ),
    );
    expect(virtualGridScrollTop(gridRef.current, 10, 1)).toBe(360);
  });

  it("measures rows afresh when the poster size shrinks (CORE-2468)", async () => {
    const gridRef = gridScrolledTo(0);
    const card = document.createElement("li");
    gridRef.current.append(card);
    const height = vi.spyOn(card, "getBoundingClientRect");
    height.mockReturnValue({ height: 290 } as DOMRect);
    const view = renderHook(() =>
      useVirtualGrid({ gridRef, itemCount: 1000, enabled: true, columns: 1 }),
    );
    await waitFor(() => expect(virtualGridScrollTop(gridRef.current, 10, 1)).toBe(2900));

    // Small posters draw shorter cards, so the retained Large height must go.
    height.mockReturnValue({ height: 190 } as DOMRect);
    act(() => setPosterSize("small"));
    await waitFor(() =>
      expect(view.result.current?.bottomPad).toBe(
        (1000 - (view.result.current?.endIndex ?? 0)) * 190,
      ),
    );
    expect(virtualGridScrollTop(gridRef.current, 10, 1)).toBe(1900);
  });

  it("derives initial row height from card aspect ratio for landscape and square grids", () => {
    // 16:9 landscape grid (5 columns of 256px + 24px row gap -> 144px art + 56px copy = 200px)
    const wideGrid = document.createElement("ul");
    wideGrid.className = "bf-grid bf-grid-wide";
    wideGrid.style.gridTemplateColumns = "256px ".repeat(5).trim();
    wideGrid.style.rowGap = "24px";
    document.body.append(wideGrid);
    vi.spyOn(wideGrid, "getBoundingClientRect").mockReturnValue({ top: 0 } as DOMRect);

    const wide = renderHook(() =>
      useVirtualGrid({ gridRef: { current: wideGrid }, itemCount: 100, enabled: true }),
    ).result.current;
    // 20 rows total, row height 200 + 24 = 224 (vs fixed 300 + 24 = 324).
    const wideRenderedRows = Math.ceil((wide?.endIndex ?? 0) / 5);
    expect(wide?.bottomPad).toBe((20 - wideRenderedRows) * 224 - 24);
    expect(virtualGridScrollTop(wideGrid, 10)).toBe(2 * 224);

    // 1:1 square grid (4 columns of 150px + 24px row gap -> 150px art + 56px copy = 206px)
    const squareGrid = document.createElement("ul");
    squareGrid.className = "bf-grid";
    squareGrid.style.gridTemplateColumns = "150px ".repeat(4).trim();
    squareGrid.style.rowGap = "24px";
    const squareItem = document.createElement("li");
    const squarePoster = document.createElement("div");
    squarePoster.className = "bf-poster bf-poster-square";
    squareItem.append(squarePoster);
    squareGrid.append(squareItem);
    document.body.append(squareGrid);
    vi.spyOn(squareGrid, "getBoundingClientRect").mockReturnValue({ top: 0 } as DOMRect);

    const square = renderHook(() =>
      useVirtualGrid({ gridRef: { current: squareGrid }, itemCount: 80, enabled: true }),
    ).result.current;
    const squareRenderedRows = Math.ceil((square?.endIndex ?? 0) / 4);
    expect(square?.bottomPad).toBe((20 - squareRenderedRows) * (206 + 24) - 24);
    expect(virtualGridScrollTop(squareGrid, 8)).toBe(2 * (206 + 24));
  });

  it("subtracts row gap from top and bottom spacers and remeasures when width shrinks", async () => {
    const gridRef = gridScrolledTo(0);
    const grid = gridRef.current;
    grid.style.gridTemplateColumns = "200px 200px 200px 200px";
    grid.style.rowGap = "24px";
    let width = 900;
    Object.defineProperty(grid, "clientWidth", { configurable: true, get: () => width });
    const card = document.createElement("li");
    grid.append(card);
    const cardRect = vi.spyOn(card, "getBoundingClientRect");
    cardRect.mockReturnValue({ height: 220 } as DOMRect);

    let scroll = 0;
    vi.spyOn(grid, "getBoundingClientRect").mockImplementation(() => ({ top: -scroll }) as DOMRect);

    const view = renderHook(() => useVirtualGrid({ gridRef, itemCount: 200, enabled: true }));
    await waitFor(() => expect(view.result.current?.bottomPad).toBeGreaterThan(0));

    // Scroll to row 5 (5 * (220 + 24) = 1220); startRow = 3 so topPad = 3 * 244 - 24 = 708.
    scroll = 5 * 244 + 10;
    act(() => {
      window.dispatchEvent(new Event("scroll"));
    });
    await waitFor(() => {
      expect(view.result.current?.startIndex).toBe(12);
      expect(view.result.current?.topPad).toBe(3 * 244 - 24);
    });

    // Narrowing the grid shrinks square/landscape cards; retained height must shrink too.
    scroll = 0;
    width = 680;
    grid.style.gridTemplateColumns = "150px 150px 150px 150px";
    cardRect.mockReturnValue({ height: 170 } as DOMRect);
    act(() => {
      window.dispatchEvent(new Event("resize"));
    });
    await waitFor(() => expect(virtualGridScrollTop(grid, 12)).toBe(3 * (170 + 24)));
  });

  it("does not latch onto taller pending cards once real landscape cards load", async () => {
    const gridRef = gridScrolledTo(0);
    const grid = gridRef.current;
    grid.className = "bf-grid bf-grid-wide";
    grid.style.gridTemplateColumns = "256px 256px 256px 256px";
    grid.style.rowGap = "24px";

    const pending = document.createElement("li");
    pending.setAttribute("data-pending", "true");
    vi.spyOn(pending, "getBoundingClientRect").mockReturnValue({ height: 400 } as DOMRect);
    grid.append(pending);

    const view = renderHook(() => useVirtualGrid({ gridRef, itemCount: 80, enabled: true }));
    await waitFor(() => expect(view.result.current).not.toBeNull());

    // Real 16:9 cards mount and replace pending placeholders.
    pending.remove();
    const real = document.createElement("li");
    vi.spyOn(real, "getBoundingClientRect").mockReturnValue({ height: 190 } as DOMRect);
    grid.append(real);
    act(() => {
      window.dispatchEvent(new Event("resize"));
    });

    await waitFor(() => {
      const renderedRows = Math.ceil((view.result.current?.endIndex ?? 0) / 4);
      expect(view.result.current?.bottomPad).toBe((20 - renderedRows) * (190 + 24) - 24);
    });
  });

  it("sizes unmeasured landscape rows from measured rows in variable-row grids", async () => {
    const grid = document.createElement("ul");
    grid.className = "bf-grid bf-grid-wide";
    grid.style.gridTemplateColumns = "256px 256px 256px 256px";
    grid.style.rowGap = "20px";
    document.body.append(grid);
    vi.stubGlobal("innerHeight", 320);
    vi.spyOn(grid, "getBoundingClientRect").mockReturnValue({ top: 0 } as DOMRect);

    const items = Array.from({ length: 80 }, (_, index) => ({ id: index }));
    for (let index = 0; index < 16; index += 1) {
      const card = document.createElement("li");
      card.setAttribute("aria-posinset", String(index + 1));
      vi.spyOn(card, "getBoundingClientRect").mockReturnValue({ height: 140 } as DOMRect);
      grid.append(card);
    }

    const view = renderHook(() =>
      useVirtualGrid({
        gridRef: { current: grid },
        itemCount: items.length,
        enabled: true,
        variableRows: items,
      }),
    );

    // 20 rows total at 140px + 20px gap = 160px pitch; unmeasured trailing rows must
    // use 140px rather than the fixed 300px fallback.
    await waitFor(() => {
      const renderedRows = Math.ceil((view.result.current?.endIndex ?? 0) / 4);
      expect(view.result.current?.bottomPad).toBe((20 - renderedRows) * 160 - 20);
    });
    vi.unstubAllGlobals();
  });

  it("ignores stretched real cards sharing a row with a pending placeholder", async () => {
    const gridRef = gridScrolledTo(0);
    const grid = gridRef.current;
    grid.className = "bf-grid bf-grid-wide";
    grid.style.gridTemplateColumns = "256px 256px 256px 256px";
    grid.style.rowGap = "24px";

    const realA = document.createElement("li");
    const realB = document.createElement("li");
    const pendingA = document.createElement("li");
    pendingA.setAttribute("data-pending", "true");
    const pendingB = document.createElement("li");
    pendingB.setAttribute("data-pending", "true");
    // CSS grid stretches all items in the mixed row to the pending card's height.
    const rectA = vi.spyOn(realA, "getBoundingClientRect").mockReturnValue({ height: 400 } as DOMRect);
    const rectB = vi.spyOn(realB, "getBoundingClientRect").mockReturnValue({ height: 400 } as DOMRect);
    vi.spyOn(pendingA, "getBoundingClientRect").mockReturnValue({ height: 400 } as DOMRect);
    vi.spyOn(pendingB, "getBoundingClientRect").mockReturnValue({ height: 400 } as DOMRect);
    grid.append(realA, realB, pendingA, pendingB);

    const view = renderHook(() => useVirtualGrid({ gridRef, itemCount: 80, enabled: true }));
    await waitFor(() => expect(view.result.current).not.toBeNull());

    // Once the placeholders load, the row settles to the real 190px height.
    pendingA.remove();
    pendingB.remove();
    rectA.mockReturnValue({ height: 190 } as DOMRect);
    rectB.mockReturnValue({ height: 190 } as DOMRect);
    const loadedA = document.createElement("li");
    const loadedB = document.createElement("li");
    vi.spyOn(loadedA, "getBoundingClientRect").mockReturnValue({ height: 190 } as DOMRect);
    vi.spyOn(loadedB, "getBoundingClientRect").mockReturnValue({ height: 190 } as DOMRect);
    grid.append(loadedA, loadedB);
    act(() => {
      window.dispatchEvent(new Event("resize"));
    });

    await waitFor(() => {
      const renderedRows = Math.ceil((view.result.current?.endIndex ?? 0) / 4);
      expect(view.result.current?.bottomPad).toBe((20 - renderedRows) * (190 + 24) - 24);
    });
    expect(virtualGridScrollTop(grid, 8)).toBe(2 * (190 + 24));
  });

  it("mounts every on-screen row when they outnumber the 120-card cap", async () => {
    const grid = document.createElement("ul");
    grid.className = "bf-grid";
    grid.style.gridTemplateColumns = "110px ".repeat(12).trim();
    grid.style.rowGap = "20px";
    document.body.append(grid);
    // 11+ visible rows at 200px + 20px gap -> 132+ visible items on 12 columns.
    vi.stubGlobal("innerHeight", 2500);
    vi.spyOn(grid, "getBoundingClientRect").mockReturnValue({ top: 0 } as DOMRect);

    const items = Array.from({ length: 240 }, (_, index) => ({ id: index }));
    for (let index = 0; index < 144; index += 1) {
      const card = document.createElement("li");
      card.setAttribute("aria-posinset", String(index + 1));
      vi.spyOn(card, "getBoundingClientRect").mockReturnValue({ height: 200 } as DOMRect);
      grid.append(card);
    }

    const view = renderHook(() =>
      useVirtualGrid({
        gridRef: { current: grid },
        itemCount: items.length,
        enabled: true,
        variableRows: items,
      }),
    );

    await waitFor(() => {
      const win = view.result.current;
      expect(win).not.toBeNull();
      // Rows 0-11 reach 2640px, past the 2500px viewport: 12 rows of 12 cards.
      expect((win?.endIndex ?? 0) - (win?.startIndex ?? 0)).toBe(144);
    });
    vi.unstubAllGlobals();
  });

  it("keeps every on-screen row mounted at both ends of a short scroll range", async () => {
    // 200 cards on 12 columns = 17 rows (200px + 20px gap -> 3720px total height).
    // On a 2500px viewport the maximum scroll offset is 3720 - 2500 = 1220px.
    const grid = document.createElement("ul");
    grid.className = "bf-grid";
    grid.style.gridTemplateColumns = "110px ".repeat(12).trim();
    grid.style.rowGap = "20px";
    document.body.append(grid);
    vi.stubGlobal("innerHeight", 2500);

    let scroll = 0;
    vi.spyOn(grid, "getBoundingClientRect").mockImplementation(() => ({ top: -scroll }) as DOMRect);

    const items = Array.from({ length: 200 }, (_, index) => ({ id: index }));
    for (let index = 0; index < 120; index += 1) {
      const card = document.createElement("li");
      card.setAttribute("aria-posinset", String(index + 1));
      vi.spyOn(card, "getBoundingClientRect").mockReturnValue({ height: 200 } as DOMRect);
      grid.append(card);
    }

    try {
      const variable = renderHook(() =>
        useVirtualGrid({
          gridRef: { current: grid },
          itemCount: items.length,
          enabled: true,
          variableRows: items,
        }),
      );
      await waitFor(() => expect(variable.result.current?.endIndex).toBe(144));

      scroll = 17 * 220 - 20 - 2500;
      act(() => {
        window.dispatchEvent(new Event("scroll"));
      });
      await waitFor(() => {
        const win = variable.result.current;
        expect(win?.endIndex).toBe(200);
        expect(win?.startIndex).toBe(60);
        expect(win?.topPad).toBe(5 * 220 - 20);
        expect(win?.bottomPad).toBe(0);
      });

      scroll = 0;
      const fixed = renderHook(() =>
        useVirtualGrid({
          gridRef: { current: grid },
          itemCount: items.length,
          enabled: true,
        }),
      );
      await waitFor(() => expect(fixed.result.current?.endIndex).toBe(144));

      scroll = 17 * 220 - 20 - 2500;
      act(() => {
        window.dispatchEvent(new Event("scroll"));
      });
      await waitFor(() => {
        const win = fixed.result.current;
        expect(win?.endIndex).toBe(200);
        expect(win?.startIndex).toBe(60);
        expect(win?.topPad).toBe(5 * 220 - 20);
        expect(win?.bottomPad).toBe(0);
      });
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("mounts every card when a capped grid scrolls by a single pixel", async () => {
    // 252 cards on 12 columns = 21 rows of 96px + 24px gap (2496px) on a 2495px viewport.
    const grid = document.createElement("ul");
    grid.className = "bf-grid";
    grid.style.gridTemplateColumns = "110px ".repeat(12).trim();
    grid.style.rowGap = "24px";
    document.body.append(grid);
    vi.stubGlobal("innerHeight", 2495);
    let scroll = 0;
    vi.spyOn(grid, "getBoundingClientRect").mockImplementation(() => ({ top: -scroll }) as DOMRect);
    const items = Array.from({ length: 252 }, (_, index) => ({ id: index }));
    for (let index = 0; index < 120; index += 1) {
      const card = document.createElement("li");
      card.setAttribute("aria-posinset", String(index + 1));
      vi.spyOn(card, "getBoundingClientRect").mockReturnValue({ height: 96 } as DOMRect);
      grid.append(card);
    }
    try {
      const view = renderHook(() =>
        useVirtualGrid({ gridRef: { current: grid }, itemCount: items.length, enabled: true, variableRows: items }),
      );
      for (const offset of [0, 1]) {
        scroll = offset;
        act(() => {
          window.dispatchEvent(new Event("scroll"));
        });
        await waitFor(() => {
          expect(view.result.current?.startIndex).toBe(0);
          expect(view.result.current?.endIndex).toBe(252);
        });
      }
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("mounts every card when the whole capped grid fits the viewport", async () => {
    // 132 cards on 12 columns = 11 rows (11 * 220 - 20 = 2400px) on a 2500px viewport: nothing scrolls.
    const grid = document.createElement("ul");
    grid.className = "bf-grid";
    grid.style.gridTemplateColumns = "110px ".repeat(12).trim();
    grid.style.rowGap = "20px";
    document.body.append(grid);
    vi.stubGlobal("innerHeight", 2500);
    vi.spyOn(grid, "getBoundingClientRect").mockReturnValue({ top: 0 } as DOMRect);
    const items = Array.from({ length: 132 }, (_, index) => ({ id: index }));
    for (let index = 0; index < 120; index += 1) {
      const card = document.createElement("li");
      card.setAttribute("aria-posinset", String(index + 1));
      vi.spyOn(card, "getBoundingClientRect").mockReturnValue({ height: 200 } as DOMRect);
      grid.append(card);
    }
    try {
      for (const variableRows of [items, undefined]) {
        const view = renderHook(() =>
          useVirtualGrid({ gridRef: { current: grid }, itemCount: items.length, enabled: true, variableRows }),
        );
        await waitFor(() => {
          expect(view.result.current?.startIndex).toBe(0);
          expect(view.result.current?.endIndex).toBe(132);
        });
      }
    } finally {
      vi.unstubAllGlobals();
    }
  });
});
