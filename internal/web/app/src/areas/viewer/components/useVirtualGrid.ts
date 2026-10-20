import { useCallback, useEffect, useState } from "react";

// Lightweight viewport windowing for large browse grids, ported in spirit from
// the vanilla VIRTUAL_GRID_* windowing (internal/web/static/app.js). NO
// dependency: the hook measures the live grid's real geometry (its width, the
// CSS gap, and a sample card's height) and renders only the rows intersecting
// the viewport, padded above and below with spacer rows so the scroll height and
// scrollbar stay correct. Small grids skip windowing entirely (the caller gates
// on VIRTUAL_GRID_MIN_ITEMS) and keep their plain CSS grid.
//
// Thresholds mirror the vanilla constants: virtualize only past ~40 items, two
// overscan rows above and below, and a hard cap on rendered items so a giant
// grid never mounts an unbounded card count in one frame.
export const VIRTUAL_GRID_MIN_ITEMS = 40;
const VIRTUAL_GRID_MAX_ITEMS = 72;
const VIRTUAL_GRID_OVERSCAN_ROWS = 2;
// The grid's CSS column floor (chrome.css .bf-grid minmax(150px, 1fr)).
const VIRTUAL_GRID_COLUMN_MIN = 150;
// A sane row-height fallback before a card has been measured.
const VIRTUAL_GRID_FALLBACK_ROW_HEIGHT = 300;

export interface VirtualGridWindow {
  startIndex: number;
  endIndex: number;
  topPad: number;
  bottomPad: number;
}

function readGap(grid: HTMLElement): number {
  const styles = window.getComputedStyle(grid);
  const gap = parseFloat(styles.rowGap || styles.gap || "");
  return Number.isFinite(gap) ? gap : 14;
}

// Derives the live column count: prefer the actual rendered column template,
// else compute from the grid width and the CSS column floor (the vanilla path).
function readColumns(grid: HTMLElement, gap: number): number {
  const styles = window.getComputedStyle(grid);
  const template = styles.gridTemplateColumns;
  if (template && template !== "none") {
    const tracks = template.split(" ").filter(Boolean).length;
    if (tracks > 0) return tracks;
  }
  const width = grid.clientWidth || window.innerWidth || VIRTUAL_GRID_COLUMN_MIN;
  return Math.max(1, Math.floor((width + gap) / (VIRTUAL_GRID_COLUMN_MIN + gap)));
}

// The on-screen row height: a measured card's height plus the row gap, falling
// back to a constant before any card has laid out.
function readRowHeight(grid: HTMLElement, gap: number): number {
  const card = grid.querySelector<HTMLElement>(":scope > li:not(.bf-grid-spacer)");
  const cardHeight = card ? card.getBoundingClientRect().height : 0;
  const base = cardHeight > 0 ? cardHeight : VIRTUAL_GRID_FALLBACK_ROW_HEIGHT;
  return base + gap;
}

export function useVirtualGrid({
  gridRef,
  itemCount,
  enabled,
}: {
  gridRef: React.RefObject<HTMLUListElement | null>;
  itemCount: number;
  enabled: boolean;
}): VirtualGridWindow | null {
  const compute = useCallback((): VirtualGridWindow | null => {
    const grid = gridRef.current;
    if (!grid || !enabled || itemCount <= 0) return null;

    const gap = readGap(grid);
    const columns = readColumns(grid, gap);
    const rowHeight = readRowHeight(grid, gap);
    const totalRows = Math.ceil(itemCount / columns);

    const rect = grid.getBoundingClientRect();
    const viewportHeight = window.innerHeight || rect.height || rowHeight * 3;
    // Pixels of the grid scrolled above the viewport top (0 until it scrolls up).
    const scrolledPast = Math.max(0, -rect.top);

    const startRow = Math.max(0, Math.floor(scrolledPast / rowHeight) - VIRTUAL_GRID_OVERSCAN_ROWS);
    const visibleRows = Math.ceil(viewportHeight / rowHeight) + VIRTUAL_GRID_OVERSCAN_ROWS * 2;

    const startIndex = Math.min(itemCount, startRow * columns);
    const count = Math.min(itemCount - startIndex, visibleRows * columns, VIRTUAL_GRID_MAX_ITEMS);
    const renderedRows = Math.ceil(count / columns);

    return {
      startIndex,
      endIndex: startIndex + count,
      topPad: startRow * rowHeight,
      bottomPad: Math.max(0, totalRows - startRow - renderedRows) * rowHeight,
    };
  }, [gridRef, itemCount, enabled]);

  const [windowState, setWindowState] = useState<VirtualGridWindow | null>(() => compute());

  useEffect(() => {
    if (!enabled) {
      setWindowState(null);
      return;
    }
    let frame = 0;
    const recompute = () => {
      if (frame) return;
      frame = window.requestAnimationFrame(() => {
        frame = 0;
        setWindowState(compute());
      });
    };
    recompute();
    window.addEventListener("scroll", recompute, { passive: true });
    window.addEventListener("resize", recompute);
    return () => {
      if (frame) window.cancelAnimationFrame(frame);
      window.removeEventListener("scroll", recompute);
      window.removeEventListener("resize", recompute);
    };
  }, [enabled, compute]);

  return enabled ? windowState : null;
}
