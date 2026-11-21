import {
  type KeyboardEvent,
  type MouseEvent,
  type PointerEvent,
  useEffect,
  useRef,
  useState,
} from "react";
import { Popover } from "@/components/ui/astryx-primitives";
import { Button } from "@/components/ui/button";
import { useT } from "@/lib/i18n";
import {
  clampSidebarWidth,
  SIDEBAR_WIDTH_DEFAULT,
  SIDEBAR_WIDTH_MAX,
  SIDEBAR_WIDTH_MIN,
  SIDEBAR_WIDTH_STEP,
  sidebarWidthForKey,
} from "./sidebarWidth";

/** Pointer travel below this is a click, not a drag. */
const CLICK_SLOP = 3;

// The drag handle on the expanded desktop sidebar's edge (CORE-2734): a
// focusable window splitter. Pointer drag follows the cursor, arrow keys step,
// Home and End jump to the limits, and a double-click restores the default.
// A click without dragging opens a small width control with step buttons, the
// single-pointer alternative WCAG 2.5.7 asks for (CORE-2736).
// The shell owns the width; `onChange` reports each move and `onCommit` the
// settled width to persist.
export function SidebarResizer({
  sidebarId,
  width,
  onChange,
  onCommit,
  onResizingChange,
}: {
  sidebarId: string;
  width: number;
  onChange: (width: number) => void;
  onCommit: (width: number) => void;
  /** True while a pointer drag is in progress, so the shell can drop its width transition. */
  onResizingChange: (resizing: boolean) => void;
}) {
  const t = useT();
  const drag = useRef<{ pointerId: number; startX: number; startWidth: number } | null>(null);
  const moved = useRef(false);
  const handle = useRef<HTMLDivElement>(null);
  // Where the popover opens: beside the handle at the height it was clicked.
  const clickY = useRef(0);
  const [open, setOpen] = useState(false);
  const latest = useRef(width);
  latest.current = width;
  const resizingChange = useRef(onResizingChange);
  resizingChange.current = onResizingChange;

  // Unmounting mid-drag (the window narrowing to the phone layout) must not
  // leave the shell stuck in its resizing state with text selection off.
  useEffect(
    () => () => {
      if (drag.current) resizingChange.current(false);
    },
    [],
  );

  function set(next: number, commit: boolean) {
    const clamped = clampSidebarWidth(next);
    latest.current = clamped;
    onChange(clamped);
    if (commit) onCommit(clamped);
  }

  function onPointerDown(event: PointerEvent<HTMLDivElement>) {
    // One drag at a time: a second finger must not take over the first one's.
    if (event.button !== 0 || drag.current) return;
    event.preventDefault();
    // preventDefault stops the click from focusing the separator; focus it so
    // arrow keys work after a drag.
    event.currentTarget.focus({ preventScroll: true });
    event.currentTarget.setPointerCapture?.(event.pointerId);
    drag.current = { pointerId: event.pointerId, startX: event.clientX, startWidth: width };
    moved.current = false;
    onResizingChange(true);
  }

  function onPointerMove(event: PointerEvent<HTMLDivElement>) {
    const current = drag.current;
    if (!current || current.pointerId !== event.pointerId) return;
    const dx = event.clientX - current.startX;
    if (Math.abs(dx) >= CLICK_SLOP) moved.current = true;
    if (moved.current) set(current.startWidth + dx, false);
  }

  function endDrag(event: PointerEvent<HTMLDivElement>) {
    const current = drag.current;
    if (!current || current.pointerId !== event.pointerId) return;
    drag.current = null;
    onResizingChange(false);
    if (moved.current) onCommit(latest.current);
  }

  function onClick(event: MouseEvent<HTMLDivElement>) {
    // A drag ends in a click too; only a press that stayed put opens the control.
    // The second click of a double-click is the reset's, below.
    if (moved.current || event.detail > 1) return;
    clickY.current = event.clientY;
    setOpen((was) => !was);
  }

  // Anchor at the click height rather than the full-height handle.
  const anchor = {
    getBoundingClientRect: () => {
      const rect = handle.current?.getBoundingClientRect() ?? new DOMRect();
      const y = clickY.current || rect.top + rect.height / 2;
      return new DOMRect(rect.left, y, rect.width, 0);
    },
  };

  function onKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    // Leave modified keys (Alt+Arrow is browser Back/Forward) to the browser.
    if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
    const next = sidebarWidthForKey(event.key, width);
    if (next == null) return;
    event.preventDefault();
    set(next, true);
  }

  const stepButton = (label: string, next: number, disabled: boolean) => (
    <Button
      type="button"
      variant="outline"
      size="sm"
      aria-disabled={disabled || undefined}
      className="aria-disabled:opacity-50"
      onClick={() => {
        if (!disabled) set(next, true);
      }}
    >
      {label}
    </Button>
  );

  return (
    <>
      <div
        ref={handle}
        role="separator"
        tabIndex={0}
        aria-orientation="vertical"
        aria-controls={sidebarId}
        aria-label={t("sidebarResize")}
        aria-valuenow={width}
        aria-valuemin={SIDEBAR_WIDTH_MIN}
        aria-valuemax={SIDEBAR_WIDTH_MAX}
        title={t("sidebarResizeHint")}
        className="bf-sidebar-resizer"
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={endDrag}
        onPointerCancel={endDrag}
        onLostPointerCapture={endDrag}
        onClick={onClick}
        onDoubleClick={() => {
          setOpen(false);
          set(SIDEBAR_WIDTH_DEFAULT, true);
        }}
        onKeyDown={onKeyDown}
      />
      <Popover.Root
        open={open}
        onOpenChange={(next, details) => {
          // Pressing the handle again is its own toggle, not an outside press.
          const target = details.event?.target;
          if (!next && target instanceof Node && handle.current?.contains(target)) return;
          setOpen(next);
        }}
      >
        <Popover.Portal>
          <Popover.Positioner
            anchor={anchor}
            side="inline-end"
            align="center"
            sideOffset={8}
            className="bf-card-menu-layer"
          >
            <Popover.Popup className="bf-sidebar-width-popover" finalFocus={handle}>
              <Popover.Title className="bf-filter-popover-title">
                {t("sidebarWidthTitle")}
              </Popover.Title>
              <div className="bf-sidebar-width-actions">
                {stepButton(
                  t("sidebarNarrower"),
                  width - SIDEBAR_WIDTH_STEP,
                  width <= SIDEBAR_WIDTH_MIN,
                )}
                {stepButton(
                  t("sidebarWider"),
                  width + SIDEBAR_WIDTH_STEP,
                  width >= SIDEBAR_WIDTH_MAX,
                )}
                {stepButton(
                  t("sidebarWidthDefault"),
                  SIDEBAR_WIDTH_DEFAULT,
                  width === SIDEBAR_WIDTH_DEFAULT,
                )}
              </div>
            </Popover.Popup>
          </Popover.Positioner>
        </Popover.Portal>
      </Popover.Root>
    </>
  );
}
