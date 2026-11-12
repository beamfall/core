import { useCallback, useEffect, useRef, useState } from "react";

// Lightweight viewport windowing for large browse grids, ported in spirit from
// the vanilla VIRTUAL_GRID_* windowing (internal/web/static/app.js). NO
// dependency: the hook measures the live grid's real geometry (its width, the
// CSS gap, and the tallest card's height) and renders only the rows intersecting
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

// The tallest rendered card's height (0 before any card has laid out). Card
// copy varies (one- or two-line titles, an optional subtitle) and grid items
// stretch to their row, so a single sample card under-reads taller rows; the
// tallest one is the safest uniform row estimate.
function readCardHeight(grid: HTMLElement): number {
  let tallest = 0;
  for (const card of grid.querySelectorAll<HTMLElement>(":scope > li:not(.bf-grid-spacer)")) {
    tallest = Math.max(tallest, card.getBoundingClientRect().height);
  }
  return tallest;
}

export function useVirtualGrid({
  gridRef,
  itemCount,
  enabled,
  columns: fixedColumns,
  maxItems = VIRTUAL_GRID_MAX_ITEMS,
}: {
  gridRef: React.RefObject<HTMLUListElement | null>;
  itemCount: number;
  enabled: boolean;
  /** A fixed column count (1 for a stacked list) instead of the measured grid. */
  columns?: number;
  /** The rendered-item cap; raise it where a wide viewport shows more cards. */
  maxItems?: number;
}): VirtualGridWindow | null {
  // Tallest card seen while windowing. It only grows, so the spacer geometry
  // does not oscillate as rows of different heights scroll in and out.
  const cardHeightRef = useRef(0);
  const compute = useCallback((): VirtualGridWindow | null => {
    const grid = gridRef.current;
    if (!grid || !enabled || itemCount <= 0) return null;

    const gap = readGap(grid);
    const columns = fixedColumns ?? readColumns(grid, gap);
    cardHeightRef.current = Math.max(cardHeightRef.current, readCardHeight(grid));
    const rowHeight = (cardHeightRef.current || VIRTUAL_GRID_FALLBACK_ROW_HEIGHT) + gap;
    const totalRows = Math.ceil(itemCount / columns);

    const rect = grid.getBoundingClientRect();
    const viewportHeight = window.innerHeight || rect.height || rowHeight * 3;
    // Pixels of the grid scrolled above the viewport top (0 until it scrolls up).
    const scrolledPast = Math.max(0, -rect.top);

    const visibleRows = Math.ceil(viewportHeight / rowHeight) + VIRTUAL_GRID_OVERSCAN_ROWS * 2;
    // Clamped to the last rows: when the list shrinks under a deep scroll the
    // window must still hold items, and the smaller top spacer lets the
    // browser pull the scroll position back.
    const startRow = Math.min(
      Math.max(0, totalRows - visibleRows),
      Math.max(0, Math.floor(scrolledPast / rowHeight) - VIRTUAL_GRID_OVERSCAN_ROWS),
    );

    const startIndex = Math.min(itemCount, startRow * columns);
    const count = Math.min(itemCount - startIndex, visibleRows * columns, maxItems);
    const renderedRows = Math.ceil(count / columns);

    return {
      startIndex,
      endIndex: startIndex + count,
      topPad: startRow * rowHeight,
      bottomPad: Math.max(0, totalRows - startRow - renderedRows) * rowHeight,
    };
  }, [gridRef, itemCount, enabled, fixedColumns, maxItems]);

  const [windowState, setWindowState] = useState<VirtualGridWindow | null>(() => compute());

  useEffect(() => {
    if (!enabled) {
      cardHeightRef.current = 0;
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
