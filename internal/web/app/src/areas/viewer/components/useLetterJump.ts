import { type RefObject, useEffect, useRef, useState } from "react";
import type { LibraryItemsLetter } from "../lib/libraryBrowse";
import { letterRailOwner } from "./LibraryLetterRail";
import { virtualGridIndexAt, virtualGridScrollTop } from "./useVirtualGrid";

// The sticky header's height plus a little air, kept clear above a jumped-to row.
const LETTER_JUMP_HEADER_CLEARANCE = 72;
/** The letter rail is for long lists; a short one fits on a screen or two. */
export const LETTER_RAIL_MIN_ITEMS = 60;

// The scrolling behind a Library grid's letter rail (CORE-1685), shared by the
// Media view and each Entity kind's view (CORE-2440). A jump scrolls the grid
// to the row holding that offset; the window then loads its page, and the
// settle effect puts the row under the header once its card lands, focusing
// the card when the jump asked for it. While `enabled`, the current letter
// follows the row at the top of the grid. `titleAt` reads a loaded item's
// title; `loadedTick` changes when a page lands; a new `resultKey` (another
// query, or a retry) drops a jump still heading for a row.
export function useLetterJump({
  listRef,
  letters,
  enabled,
  columns,
  titleAt,
  loadedTick,
  resultKey,
}: {
  listRef: RefObject<HTMLUListElement | null>;
  letters: LibraryItemsLetter[];
  enabled: boolean;
  columns?: number;
  titleAt: (index: number) => string | undefined;
  loadedTick: number;
  resultKey: string;
}) {
  const pendingJumpRef = useRef<{ offset: number; focus: boolean } | null>(null);
  const [, setJumpTick] = useState(0);
  const titleAtRef = useRef(titleAt);
  titleAtRef.current = titleAt;

  useEffect(() => {
    pendingJumpRef.current = null;
  }, [resultKey]);

  function jumpTo(offset: number, focus: boolean) {
    const grid = listRef.current;
    if (!grid) return;
    pendingJumpRef.current = { offset, focus };
    const top = virtualGridScrollTop(grid, offset, columns);
    window.scrollTo({ top: Math.max(0, top - LETTER_JUMP_HEADER_CLEARANCE) });
    setJumpTick((tick) => tick + 1);
  }
  useEffect(() => {
    const jump = pendingJumpRef.current;
    if (jump === null) return;
    const cell = listRef.current?.querySelector<HTMLElement>(
      `[aria-posinset="${jump.offset + 1}"]`,
    );
    const card = cell?.querySelector<HTMLElement>(":scope > .bf-card");
    if (!cell || !card) return;
    pendingJumpRef.current = null;
    const top = cell.getBoundingClientRect().top + window.scrollY - LETTER_JUMP_HEADER_CLEARANCE;
    window.scrollTo({ top: Math.max(0, top) });
    if (jump.focus) card.focus({ preventScroll: true });
  });

  const [currentLetter, setCurrentLetter] = useState<string | null>(null);
  useEffect(() => {
    if (!enabled) return;
    let frame = 0;
    const measure = () => {
      frame = 0;
      const grid = listRef.current;
      if (!grid) return;
      const index = virtualGridIndexAt(
        grid,
        window.scrollY + LETTER_JUMP_HEADER_CLEARANCE,
        columns,
      );
      // The loaded item there names its own initial: "#" can hold titles at
      // both ends of the order, so the first offsets are not ranges. Before
      // its page lands, the last letter starting at or above it stands in.
      const title = titleAtRef.current(index);
      const at =
        (title !== undefined && letterRailOwner(letters, title, index)) ||
        letters.findLast((entry) => entry.offset <= index) ||
        letters[0];
      setCurrentLetter(at.letter);
    };
    const onScroll = () => {
      if (!frame) frame = window.requestAnimationFrame(measure);
    };
    measure();
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => {
      if (frame) window.cancelAnimationFrame(frame);
      window.removeEventListener("scroll", onScroll);
    };
    // loadedTick: re-read once the page at the top of the grid arrives.
  }, [enabled, letters, columns, loadedTick, listRef]);

  return { jumpTo, currentLetter };
}
