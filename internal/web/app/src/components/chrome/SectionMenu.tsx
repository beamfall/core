import { ChevronDown } from "lucide-react";
import { type ReactNode, useId, useState } from "react";
import { useLocation } from "react-router";
import { cn } from "@/lib/utils";

// An area's section rail. On a phone the rail would stack every link above the
// page, so it folds behind one button that names the current section; from
// 768px up the button is hidden and the rail shows in full (chrome.css). The
// menu itself is the area's navigation landmark, so a phone still exposes it
// while the links are folded away.
export function SectionMenu({
  label,
  current,
  className,
  children,
}: {
  label: string;
  current: string;
  className?: string;
  children: ReactNode;
}) {
  const { pathname } = useLocation();
  const bodyID = useId();
  // Open for one route only, so following a link closes it without an effect.
  const [openAt, setOpenAt] = useState<string | null>(null);
  const open = openAt === pathname;
  return (
    <nav aria-label={label} className={cn("section-menu", className)} data-open={open || undefined}>
      <button
        type="button"
        className="section-menu-toggle"
        aria-expanded={open}
        aria-controls={bodyID}
        onClick={() => setOpenAt(open ? null : pathname)}
      >
        <span className="min-w-0 text-left">{current}</span>
        <ChevronDown aria-hidden className="section-menu-chevron" />
      </button>
      <div id={bodyID} className="section-menu-body">
        {children}
      </div>
    </nav>
  );
}
