import { type KeyboardEvent, type PointerEvent, useEffect, useRef } from "react";
import { useT } from "@/lib/i18n";
import {
  clampSidebarWidth,
  SIDEBAR_WIDTH_DEFAULT,
  SIDEBAR_WIDTH_MAX,
  SIDEBAR_WIDTH_MIN,
  sidebarWidthForKey,
} from "./sidebarWidth";

// The drag handle on the expanded desktop sidebar's edge (CORE-2734): a
// focusable window splitter. Pointer drag follows the cursor, arrow keys step,
// Home and End jump to the limits, and a double-click restores the default.
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
    onResizingChange(true);
  }

  function onPointerMove(event: PointerEvent<HTMLDivElement>) {
    const current = drag.current;
    if (!current || current.pointerId !== event.pointerId) return;
    set(current.startWidth + event.clientX - current.startX, false);
  }

  function endDrag(event: PointerEvent<HTMLDivElement>) {
    const current = drag.current;
    if (!current || current.pointerId !== event.pointerId) return;
    drag.current = null;
    onResizingChange(false);
    onCommit(latest.current);
  }

  function onKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    // Leave modified keys (Alt+Arrow is browser Back/Forward) to the browser.
    if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
    const next = sidebarWidthForKey(event.key, width);
    if (next == null) return;
    event.preventDefault();
    set(next, true);
  }

  return (
    <div
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
      onDoubleClick={() => set(SIDEBAR_WIDTH_DEFAULT, true)}
      onKeyDown={onKeyDown}
    />
  );
}
