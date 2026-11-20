import { useCallback, useEffect, useRef, useState } from "react";
import { usePosterSize } from "@/components/chrome/posterSize";

// Lightweight viewport windowing for large browse grids, ported in spirit from
// the vanilla VIRTUAL_GRID_* windowing (internal/web/static/app.js). NO
// dependency: the hook measures the live grid's real geometry (its width, the
// CSS gap, and card heights) and renders only the rows intersecting
// the viewport, padded above and below with spacer rows so the scroll height and
// scrollbar stay correct. Small grids skip windowing entirely (the caller gates
// on VIRTUAL_GRID_MIN_ITEMS) and keep their plain CSS grid.
//
// Thresholds mirror the vanilla constants: virtualize only past ~40 items, two
// overscan rows above and below, and a hard cap on rendered items so a giant
// grid never mounts an unbounded card count in one frame.
export const VIRTUAL_GRID_MIN_ITEMS = 40;
// Sized for the Small poster setting (110px columns), which fits about twice
// as many cards per row as Large.
const VIRTUAL_GRID_MAX_ITEMS = 120;
const VIRTUAL_GRID_OVERSCAN_ROWS = 2;
// Fallback column floor before the grid template is measured: the Large poster
// size (chrome.css --bf-grid-min). Smaller sizes are read from the template.
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

// The tallest card each windowed grid has measured, which its spacer rows
// use. The jump helpers read it too, so a jump lands on the row the window
// will render rather than on one computed from the cards now in view.
const retainedCardHeight = new WeakMap<HTMLElement, number>();

interface VariableRowMeasurements {
  geometry: string;
  items: readonly unknown[];
  heights: Map<number, number>;
}

function variableGridWindow(
  grid: HTMLElement,
  items: readonly unknown[],
  columns: number,
  gap: number,
  maxItems: number,
  measurements: VariableRowMeasurements,
): VirtualGridWindow {
  const { heights } = measurements;
  const totalRows = Math.ceil(items.length / columns);
  const cards = grid.querySelectorAll<HTMLElement>(":scope > li[aria-posinset]");
  const firstIndex = Number(cards[0]?.getAttribute("aria-posinset")) - 1;
  // A resize can leave the old window starting partway through a new row.
  // Wait for the realigned window before recording any of its measurements.
  if (firstIndex >= 0 && firstIndex % columns === 0) {
    for (let offset = 0; offset < cards.length; offset += columns) {
      const index = firstIndex + offset;
      const count = Math.min(columns, items.length - index);
      if (count <= 0 || offset + count > cards.length) break;
      let height = 0;
      for (let column = 0; column < count; column += 1) {
        height = Math.max(height, cards[offset + column].getBoundingClientRect().height);
      }
      if (height > 0) heights.set(index / columns, height);
    }
  }

  const offsets = [0];
  for (let row = 0; row < totalRows; row += 1) {
    const height = heights.get(row) ?? VIRTUAL_GRID_FALLBACK_ROW_HEIGHT;
    offsets.push(offsets[row] + height + gap);
  }
  const totalHeight = offsets[totalRows];
  const rect = grid.getBoundingClientRect();
  const paddingTop = parseFloat(window.getComputedStyle(grid).paddingTop) || 0;
  const viewport = window.innerHeight || rect.height || VIRTUAL_GRID_FALLBACK_ROW_HEIGHT * 3;
  const relativeTop = -rect.top - paddingTop;
  const top = Math.min(Math.max(0, relativeTop), Math.max(0, totalHeight - gap - viewport));
  const bottom = Math.max(top, Math.min(relativeTop + viewport, top + viewport));

  let firstRow = 0;
  while (firstRow + 1 < totalRows && offsets[firstRow + 1] <= top) firstRow += 1;
  let lastRow = firstRow + 1;
  while (lastRow < totalRows && offsets[lastRow] < bottom) lastRow += 1;

  const startRow = Math.max(0, firstRow - VIRTUAL_GRID_OVERSCAN_ROWS);
  const maxRows = Math.floor(maxItems / columns);
  const endRow = Math.min(
    totalRows,
    lastRow + VIRTUAL_GRID_OVERSCAN_ROWS,
    Math.max(lastRow, startRow + maxRows),
  );
  return {
    startIndex: startRow * columns,
    endIndex: Math.min(items.length, endRow * columns),
    // The grid already inserts one gap between each spacer and the cards.
    topPad: Math.max(0, offsets[startRow] - gap),
    bottomPad: Math.max(0, totalHeight - offsets[endRow] - gap),
  };
}

// The grid's column count, row pitch and page-space top, from the same
// geometry the window uses. Rows not yet measured use the fallback height.
function gridGeometry(grid: HTMLElement, fixedColumns?: number) {
  const gap = readGap(grid);
  const cardHeight = Math.max(retainedCardHeight.get(grid) ?? 0, readCardHeight(grid));
  return {
    columns: fixedColumns ?? readColumns(grid, gap),
    rowHeight: (cardHeight || VIRTUAL_GRID_FALLBACK_ROW_HEIGHT) + gap,
    top: grid.getBoundingClientRect().top + window.scrollY,
  };
}

/**
 * The page scroll position that puts the row holding `index` at the top of the
 * viewport. Unmeasured rows use the fallback height, so the caller settles the
 * final position once the card renders.
 */
export function virtualGridScrollTop(
  grid: HTMLElement,
  index: number,
  fixedColumns?: number,
): number {
  const { columns, rowHeight, top } = gridGeometry(grid, fixedColumns);
  return Math.max(0, top + Math.floor(index / columns) * rowHeight);
}

/** The index of the first item in the row at page offset `pageY`. */
export function virtualGridIndexAt(
  grid: HTMLElement,
  pageY: number,
  fixedColumns?: number,
): number {
  const { columns, rowHeight, top } = gridGeometry(grid, fixedColumns);
  return Math.max(0, Math.floor((pageY - top) / rowHeight)) * columns;
}

export function useVirtualGrid({
  gridRef,
  itemCount,
  enabled,
  columns: fixedColumns,
  layout,
  maxItems = VIRTUAL_GRID_MAX_ITEMS,
  variableRows,
}: {
  gridRef: React.RefObject<HTMLUListElement | null>;
  itemCount: number;
  enabled: boolean;
  /** A fixed column count (1 for a stacked list) instead of the measured grid. */
  columns?: number;
  /**
   * Names the card layout when one column count draws more than one (a list
   * and a table are both one column); a change measures the rows afresh.
   */
  layout?: string;
  /** The rendered-item cap; raise it where a wide viewport shows more cards. */
  maxItems?: number;
  /** Items in intrinsic-height grids, used to keep measurements across page appends. */
  variableRows?: readonly unknown[];
}): VirtualGridWindow | null {
  // Tallest card seen while windowing. It only grows, so the spacer geometry
  // does not oscillate as rows of different heights scroll in and out.
  const cardHeightRef = useRef(0);
  const variableRef = useRef<VariableRowMeasurements | null>(null);
  // The layout the retained height was measured in: a switch between grid
  // and list, or to another poster size (CORE-2468), starts the measurement
  // over, or the spacers and jumps would keep the old layout's row height.
  const [posterSize] = usePosterSize();
  const layoutKey = `${layout ?? ""}:${fixedColumns ?? ""}:${posterSize}`;
  const layoutRef = useRef(layoutKey);
  const compute = useCallback((): VirtualGridWindow | null => {
    const grid = gridRef.current;
    if (!grid || !enabled || itemCount <= 0) return null;
    if (layoutRef.current !== layoutKey) {
      layoutRef.current = layoutKey;
      cardHeightRef.current = 0;
    }

    const gap = readGap(grid);
    const columns = fixedColumns ?? readColumns(grid, gap);
    if (variableRows) {
      const geometry = `${layoutKey}:${columns}:${grid.clientWidth}:${gap}`;
      let measurements = variableRef.current;
      if (!measurements || measurements.geometry !== geometry) {
        measurements = { geometry, heights: new Map(), items: variableRows };
        variableRef.current = measurements;
      } else if (measurements.items !== variableRows) {
        const previous = measurements.items;
        const sharedLength = Math.min(previous.length, variableRows.length);
        let index = 0;
        while (index < sharedLength && previous[index] === variableRows[index]) index += 1;
        for (const row of measurements.heights.keys()) {
          if (row >= Math.floor(index / columns)) measurements.heights.delete(row);
        }
        measurements.items = variableRows;
      }
      return variableGridWindow(grid, variableRows, columns, gap, maxItems, measurements);
    }
    cardHeightRef.current = Math.max(cardHeightRef.current, readCardHeight(grid));
    retainedCardHeight.set(grid, cardHeightRef.current);
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
  }, [gridRef, itemCount, enabled, fixedColumns, layoutKey, maxItems, variableRows]);

  const [windowState, setWindowState] = useState<VirtualGridWindow | null>(() => compute());
  // Measure the newly mounted rows even when their combined height matches
  // the estimates they replaced and the grid itself did not resize.
  const measuredStart = variableRows ? windowState?.startIndex : undefined;
  const measuredEnd = variableRows ? windowState?.endIndex : undefined;

  useEffect(() => {
    if (!enabled) {
      cardHeightRef.current = 0;
      variableRef.current = null;
      if (gridRef.current) retainedCardHeight.delete(gridRef.current);
      setWindowState(null);
      return;
    }
    let frame = 0;
    const recompute = () => {
      if (frame) return;
      frame = window.requestAnimationFrame(() => {
        frame = 0;
        const next = compute();
        setWindowState((current) =>
          current &&
          next &&
          current.startIndex === next.startIndex &&
          current.endIndex === next.endIndex &&
          current.topPad === next.topPad &&
          current.bottomPad === next.bottomPad
            ? current
            : next,
        );
      });
    };
    const grid = gridRef.current;
    const observer =
      variableRows && typeof ResizeObserver !== "undefined" ? new ResizeObserver(recompute) : null;
    if (grid && observer) observer.observe(grid);
    if (variableRows) {
      grid?.addEventListener("load", recompute, true);
      grid?.addEventListener("error", recompute, true);
    }
    recompute();
    window.addEventListener("scroll", recompute, { passive: true });
    window.addEventListener("resize", recompute);
    return () => {
      if (frame) window.cancelAnimationFrame(frame);
      observer?.disconnect();
      grid?.removeEventListener("load", recompute, true);
      grid?.removeEventListener("error", recompute, true);
      window.removeEventListener("scroll", recompute);
      window.removeEventListener("resize", recompute);
    };
  }, [enabled, compute, gridRef, variableRows, measuredStart, measuredEnd]);

  return enabled ? windowState : null;
}
