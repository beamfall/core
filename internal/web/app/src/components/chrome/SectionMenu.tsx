import { ChevronDown } from "lucide-react";
import { type ReactNode, useEffect, useId, useRef, useState } from "react";
import { useLocation } from "react-router";
import { cn } from "@/lib/utils";

// An area's section rail. On a phone the rail would stack every link above the
// page, so it folds behind one button that names the current section; from
// 768px up the button is hidden and the rail shows in full (chrome.css). The
// menu itself is the area's navigation landmark, so a phone still exposes it
// while the links are folded away. `footer` renders outside the folding body,
// so it stays visible at every width.
export function SectionMenu({
  label,
  current,
  className,
  footer,
  children,
}: {
  label: string;
  current: string;
  className?: string;
  footer?: ReactNode;
  children: ReactNode;
}) {
  const { pathname } = useLocation();
  const bodyID = useId();
  const navRef = useRef<HTMLElement>(null);
  const toggleRef = useRef<HTMLButtonElement>(null);
  // Open for one route only, so following a link closes it without an effect.
  const [openAt, setOpenAt] = useState<string | null>(null);
  const open = openAt === pathname;

  // While open, Escape closes and returns focus to the toggle, and a press
  // anywhere outside the menu closes it.
  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      setOpenAt(null);
      toggleRef.current?.focus();
    };
    const onPointerDown = (event: PointerEvent) => {
      if (!navRef.current?.contains(event.target as Node)) setOpenAt(null);
    };
    document.addEventListener("keydown", onKeyDown);
    document.addEventListener("pointerdown", onPointerDown);
    return () => {
      document.removeEventListener("keydown", onKeyDown);
      document.removeEventListener("pointerdown", onPointerDown);
    };
  }, [open]);

  return (
    <nav
      ref={navRef}
      aria-label={label}
      className={cn("section-menu", className)}
      data-open={open || undefined}
    >
      <button
        ref={toggleRef}
        type="button"
        className="section-menu-toggle"
        aria-expanded={open}
        aria-controls={bodyID}
        onClick={() => setOpenAt(open ? null : pathname)}
      >
        <span className="min-w-0 text-left">{current}</span>
        <ChevronDown aria-hidden className="section-menu-chevron" />
      </button>
      {/* A link to the current page does not change the route, so any link
          press closes the menu here. */}
      <div
        id={bodyID}
        className="section-menu-body"
        onClick={(event) => {
          if ((event.target as Element).closest("a")) setOpenAt(null);
        }}
      >
        {children}
      </div>
      {footer}
    </nav>
  );
}
