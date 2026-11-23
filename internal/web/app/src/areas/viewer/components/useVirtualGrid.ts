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
const VIRTUAL_GRID_WIDE_COLUMN_MIN = 220;
// Fallback card copy height before a card has laid out (title + subline + padding).
const VIRTUAL_GRID_DEFAULT_COPY_HEIGHT = 56;
const VIRTUAL_GRID_DEFAULT_ASPECT_RATIO = 2 / 3;
// A sane row-height fallback before a card or grid width has been measured.
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

function readColumnGap(grid: HTMLElement, rowGap: number): number {
  const styles = window.getComputedStyle(grid);
  const gap = parseFloat(styles.columnGap || styles.gap || "");
  return Number.isFinite(gap) ? gap : rowGap;
}

function readColumnMin(grid: HTMLElement): number {
  const styles = window.getComputedStyle(grid);
  const prop = grid.classList.contains("bf-grid-wide") ? "--bf-grid-wide-min" : "--bf-grid-min";
  const min = parseFloat(styles.getPropertyValue(prop));
  if (Number.isFinite(min) && min > 0) return min;
  return grid.classList.contains("bf-grid-wide")
    ? VIRTUAL_GRID_WIDE_COLUMN_MIN
    : VIRTUAL_GRID_COLUMN_MIN;
}

// Derives the live column count: prefer the actual rendered column template,
// else compute from the grid width and the CSS column floor (the vanilla path).
function readColumns(grid: HTMLElement, gap: number): number {
  const styles = window.getComputedStyle(grid);
  const template = styles.gridTemplateColumns;
  if (template && template !== "none" && !template.includes("(")) {
    const tracks = template.split(" ").filter(Boolean).length;
    if (tracks > 0) return tracks;
  }
  const colGap = readColumnGap(grid, gap);
  const colMin = readColumnMin(grid);
  const width = grid.clientWidth || window.innerWidth || colMin;
  return Math.max(1, Math.floor((width + colGap) / (colMin + colGap)));
}

function readColumnWidth(grid: HTMLElement, columns: number, gap: number): number {
  if (columns <= 0) return 0;
  const styles = window.getComputedStyle(grid);
  const template = styles.gridTemplateColumns;
  if (template && template !== "none" && !template.includes("(")) {
    const first = parseFloat(template.split(" ").filter(Boolean)[0] ?? "");
    if (Number.isFinite(first) && first > 0) return first;
  }
  const clientWidth = grid.clientWidth;
  if (clientWidth <= 0) return 0;
  const padding = (parseFloat(styles.paddingLeft) || 0) + (parseFloat(styles.paddingRight) || 0);
  const contentWidth = Math.max(0, clientWidth - padding);
  const colGap = readColumnGap(grid, gap);
  return Math.max(0, (contentWidth - colGap * (columns - 1)) / columns);
}

function parseAspectRatio(raw: string): number {
  const ratioMatch = raw.match(/(\d+(?:\.\d+)?)\s*\/\s*(\d+(?:\.\d+)?)/);
  if (ratioMatch) {
    const width = Number(ratioMatch[1]);
    const height = Number(ratioMatch[2]);
    if (width > 0 && height > 0) return width / height;
  }
  const numeric = parseFloat(raw);
  return Number.isFinite(numeric) && numeric > 0 ? numeric : 0;
}

function readCardAspectRatio(grid: HTMLElement, explicit?: number): number {
  if (explicit && explicit > 0) return explicit;
  if (grid.classList.contains("bf-grid-wide")) return 16 / 9;
  const cards = grid.querySelectorAll<HTMLElement>(":scope > li:not(.bf-grid-spacer)");
  let sawSquare = false;
  let sawNonSquare = false;
  for (const card of cards) {
    const poster = card.querySelector<HTMLElement>(".bf-poster");
    if (!poster) continue;
    if (poster.classList.contains("bf-poster-wide")) return 16 / 9;
    if (poster.classList.contains("bf-poster-intrinsic")) {
      const img = poster.querySelector<HTMLImageElement>("img");
      if (img && img.complete && img.naturalWidth > 0 && img.naturalHeight > 0) {
        return img.naturalWidth / img.naturalHeight;
      }
    }
    const inlineRatio = poster.style.aspectRatio ? parseAspectRatio(poster.style.aspectRatio) : 0;
    if (inlineRatio > 0) return inlineRatio;
    const computedRatio = parseAspectRatio(window.getComputedStyle(poster).aspectRatio);
    if (computedRatio > 0) return computedRatio;
    if (poster.classList.contains("bf-poster-square")) sawSquare = true;
    else sawNonSquare = true;
  }
  if (sawSquare && !sawNonSquare) return 1;
  return VIRTUAL_GRID_DEFAULT_ASPECT_RATIO;
}

function estimateCardHeight(
  grid: HTMLElement,
  columns: number,
  gap: number,
  fixedColumns?: number,
  explicitAspectRatio?: number,
): number {
  if (grid.classList.contains("bf-table-rows")) return 36;
  const columnWidth = readColumnWidth(grid, columns, gap);
  if (columnWidth <= 0) return VIRTUAL_GRID_FALLBACK_ROW_HEIGHT;
  if (fixedColumns === 1 || grid.classList.contains("bf-stack")) return 80;
  const ratio = readCardAspectRatio(grid, explicitAspectRatio);
  const copy = grid.querySelector<HTMLElement>(":scope > li:not(.bf-grid-spacer) .bf-card-copy");
  const measuredCopy = copy ? copy.getBoundingClientRect().height : 0;
  const copyHeight = measuredCopy > 0 ? measuredCopy : VIRTUAL_GRID_DEFAULT_COPY_HEIGHT;
  return Math.round(columnWidth / ratio + copyHeight);
}

function cardContentHeight(card: HTMLElement): number {
  const content = card.querySelector<HTMLElement>(":scope > .bf-card");
  const measured = content ? content.getBoundingClientRect().height : 0;
  return measured > 0 ? measured : card.getBoundingClientRect().height;
}

// The tallest rendered card's height (0 before any card has laid out). Card
// copy varies (one- or two-line titles, an optional subtitle) and grid items
// stretch to their row, so a single sample card under-reads taller rows; the
// tallest one is the safest uniform row estimate. Rows containing pending
// placeholders are excluded from real-card measurements because CSS grid
// stretches shorter real cards in that row to the placeholder's height.
function readCardHeight(
  grid: HTMLElement,
  columns: number,
): { height: number; pendingOnly: boolean } {
  const cards = grid.querySelectorAll<HTMLElement>(":scope > li:not(.bf-grid-spacer)");
  let realTallest = 0;
  let pendingTallest = 0;
  const stride = Math.max(1, columns);
  for (let rowStart = 0; rowStart < cards.length; rowStart += stride) {
    const rowEnd = Math.min(cards.length, rowStart + stride);
    let rowHasPending = false;
    let rowTallest = 0;
    for (let index = rowStart; index < rowEnd; index += 1) {
      const card = cards[index];
      if (card.getAttribute("data-pending") === "true") rowHasPending = true;
      rowTallest = Math.max(rowTallest, cardContentHeight(card));
    }
    if (rowHasPending) {
      pendingTallest = Math.max(pendingTallest, rowTallest);
    } else {
      realTallest = Math.max(realTallest, rowTallest);
    }
  }
  if (realTallest > 0) return { height: realTallest, pendingOnly: false };
  return { height: pendingTallest, pendingOnly: pendingTallest > 0 };
}

// The tallest card each windowed grid has measured, which its spacer rows
// use. The jump helpers read it too, so a jump lands on the row the window
// will render rather than on one computed from the cards now in view.
const retainedCardHeight = new WeakMap<HTMLElement, { height: number; pendingOnly: boolean }>();
const retainedAspectRatio = new WeakMap<HTMLElement, number>();

interface VariableRowMeasurements {
  geometry: string;
  items: readonly unknown[];
  heights: Map<number, number>;
}

// Computes the row range [startRow, endRow) capped to maxItems. A viewport
// taller than maxRows mounts exactly its visible rows, so no on-screen card is
// ever dropped; otherwise the window adds overscan within the cap.
function windowRows(
  totalRows: number,
  columns: number,
  maxItems: number,
  maxTop: number,
  firstRow: number,
  lastRow: number,
): { startRow: number; endRow: number } {
  // Nothing scrolls, so every row is on screen: the cap cannot drop any.
  if (maxTop <= 0) return { startRow: 0, endRow: totalRows };
  const maxRows = Math.max(1, Math.floor(maxItems / columns));
  // When more rows are on screen than the cap allows, mount exactly the rows
  // on screen: the cap bounds off-screen work and must never leave a visible
  // card unmounted (interpolating a capped window across a short scroll range
  // skips rows outright).
  if (lastRow - firstRow > maxRows)
    return { startRow: firstRow, endRow: Math.min(totalRows, lastRow) };
  const startRow = Math.max(0, firstRow - VIRTUAL_GRID_OVERSCAN_ROWS, lastRow - maxRows);
  const endRow = Math.min(totalRows, lastRow + VIRTUAL_GRID_OVERSCAN_ROWS, startRow + maxRows);
  return { startRow, endRow };
}

function variableGridWindow(
  grid: HTMLElement,
  items: readonly unknown[],
  columns: number,
  gap: number,
  maxItems: number,
  measurements: VariableRowMeasurements,
  aspectRatio?: number,
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

  const fallbackHeight = estimateCardHeight(grid, columns, gap, undefined, aspectRatio);
  let estimatedHeight = fallbackHeight;
  for (let row = 0; row < totalRows; row += 1) {
    const measured = heights.get(row);
    if (measured !== undefined) {
      estimatedHeight = measured;
      break;
    }
  }
  const offsets = [0];
  for (let row = 0; row < totalRows; row += 1) {
    const measured = heights.get(row);
    if (measured !== undefined) estimatedHeight = measured;
    offsets.push(offsets[row] + (measured ?? estimatedHeight) + gap);
  }
  const totalHeight = offsets[totalRows];
  const rect = grid.getBoundingClientRect();
  const paddingTop = parseFloat(window.getComputedStyle(grid).paddingTop) || 0;
  const viewport = window.innerHeight || rect.height || fallbackHeight * 3;
  const relativeTop = -rect.top - paddingTop;
  const maxTop = Math.max(0, totalHeight - gap - viewport);
  const top = Math.min(Math.max(0, relativeTop), maxTop);
  const bottom = Math.max(top, Math.min(relativeTop + viewport, top + viewport));

  let firstRow = 0;
  while (firstRow + 1 < totalRows && offsets[firstRow + 1] <= top) firstRow += 1;
  let lastRow = firstRow + 1;
  while (lastRow < totalRows && offsets[lastRow] < bottom) lastRow += 1;

  const { startRow, endRow } = windowRows(totalRows, columns, maxItems, maxTop, firstRow, lastRow);
  const startIndex = startRow * columns;
  return {
    startIndex,
    endIndex: Math.min(items.length, endRow * columns),
    // The grid already inserts one gap between each spacer and the cards.
    topPad: Math.max(0, offsets[startRow] - gap),
    bottomPad: Math.max(0, totalHeight - offsets[endRow] - gap),
  };
}

// The grid's column count, row pitch and page-space top, from the same
// geometry the window uses. Rows not yet measured derive their height from
// the card aspect ratio and column width (falling back to 300px off-layout).
function gridGeometry(grid: HTMLElement, fixedColumns?: number) {
  const gap = readGap(grid);
  const columns = fixedColumns ?? readColumns(grid, gap);
  const measured = readCardHeight(grid, columns);
  const retained = retainedCardHeight.get(grid);
  const cardHeight =
    measured.height > 0 && !measured.pendingOnly
      ? Math.max(retained && !retained.pendingOnly ? retained.height : 0, measured.height)
      : retained && retained.height > 0
        ? retained.height
        : measured.height;
  const fallback = estimateCardHeight(
    grid,
    columns,
    gap,
    fixedColumns,
    retainedAspectRatio.get(grid),
  );
  return {
    columns,
    rowHeight: (cardHeight || fallback) + gap,
    top: grid.getBoundingClientRect().top + window.scrollY,
  };
}

/**
 * The page scroll position that puts the row holding `index` at the top of the
 * viewport. Unmeasured rows use the aspect-ratio estimate, so the caller settles
 * the final position once the card renders.
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
  aspectRatio,
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
  /** Known card aspect ratio (width / height) used to estimate row height before layout. */
  aspectRatio?: number;
}): VirtualGridWindow | null {
  // Tallest card seen while windowing. It only grows within the same geometry,
  // so the spacer geometry does not oscillate as rows of different heights scroll in and out.
  const cardHeightRef = useRef(0);
  const hasRealCardRef = useRef(false);
  const variableRef = useRef<VariableRowMeasurements | null>(null);
  // The layout the retained height was measured in: a switch between grid
  // and list, or to another poster size (CORE-2468), starts the measurement
  // over, or the spacers and jumps would keep the old layout's row height.
  const [posterSize] = usePosterSize();
  const layoutKey = `${layout ?? ""}:${fixedColumns ?? ""}:${posterSize}:${aspectRatio ?? ""}`;
  const geometryRef = useRef("");
  const compute = useCallback((): VirtualGridWindow | null => {
    const grid = gridRef.current;
    if (!grid || !enabled || itemCount <= 0) return null;

    if (aspectRatio && aspectRatio > 0) retainedAspectRatio.set(grid, aspectRatio);
    else retainedAspectRatio.delete(grid);

    const gap = readGap(grid);
    const columns = fixedColumns ?? readColumns(grid, gap);
    const geometry = `${layoutKey}:${columns}:${grid.clientWidth}:${gap}`;
    if (geometryRef.current !== geometry) {
      geometryRef.current = geometry;
      cardHeightRef.current = 0;
      hasRealCardRef.current = false;
      retainedCardHeight.delete(grid);
    }

    if (variableRows) {
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
      return variableGridWindow(
        grid,
        variableRows,
        columns,
        gap,
        maxItems,
        measurements,
        aspectRatio,
      );
    }

    const measured = readCardHeight(grid, columns);
    if (measured.height > 0) {
      if (!measured.pendingOnly) {
        cardHeightRef.current = hasRealCardRef.current
          ? Math.max(cardHeightRef.current, measured.height)
          : measured.height;
        hasRealCardRef.current = true;
        retainedCardHeight.set(grid, { height: cardHeightRef.current, pendingOnly: false });
      } else if (!hasRealCardRef.current) {
        cardHeightRef.current = Math.max(cardHeightRef.current, measured.height);
        retainedCardHeight.set(grid, { height: cardHeightRef.current, pendingOnly: true });
      }
    }
    const fallbackHeight = estimateCardHeight(grid, columns, gap, fixedColumns, aspectRatio);
    const rowHeight = (cardHeightRef.current || fallbackHeight) + gap;
    const totalRows = Math.ceil(itemCount / columns);

    const rect = grid.getBoundingClientRect();
    const viewportHeight = window.innerHeight || rect.height || rowHeight * 3;
    // Pixels of the grid scrolled above the viewport top (0 until it scrolls up).
    const scrolledPast = Math.max(0, -rect.top);
    const maxTop = Math.max(0, totalRows * rowHeight - gap - viewportHeight);
    const top = Math.min(scrolledPast, maxTop);
    const firstRow = Math.min(totalRows - 1, Math.floor(top / rowHeight));
    const lastRow = Math.min(
      totalRows,
      Math.max(firstRow + 1, Math.ceil((top + viewportHeight) / rowHeight)),
    );
    const { startRow, endRow } = windowRows(
      totalRows,
      columns,
      maxItems,
      maxTop,
      firstRow,
      lastRow,
    );

    const startIndex = Math.min(itemCount, startRow * columns);
    const endIndex = Math.min(itemCount, endRow * columns);
    const renderedRows = Math.ceil((endIndex - startIndex) / columns);
    const trailingRows = Math.max(0, totalRows - startRow - renderedRows);

    return {
      startIndex,
      endIndex,
      // The grid already inserts one gap between each spacer and the cards.
      topPad: startRow > 0 ? Math.max(0, startRow * rowHeight - gap) : 0,
      bottomPad: trailingRows > 0 ? Math.max(0, trailingRows * rowHeight - gap) : 0,
    };
  }, [gridRef, itemCount, enabled, fixedColumns, layoutKey, maxItems, variableRows, aspectRatio]);

  const [windowState, setWindowState] = useState<VirtualGridWindow | null>(() => compute());
  // Measure the newly mounted rows even when their combined height matches
  // the estimates they replaced and the grid itself did not resize.
  const measuredStart = variableRows ? windowState?.startIndex : undefined;
  const measuredEnd = variableRows ? windowState?.endIndex : undefined;

  useEffect(() => {
    if (!enabled) {
      cardHeightRef.current = 0;
      hasRealCardRef.current = false;
      geometryRef.current = "";
      variableRef.current = null;
      if (gridRef.current) {
        retainedCardHeight.delete(gridRef.current);
        retainedAspectRatio.delete(gridRef.current);
      }
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
    const observer = typeof ResizeObserver !== "undefined" ? new ResizeObserver(recompute) : null;
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
