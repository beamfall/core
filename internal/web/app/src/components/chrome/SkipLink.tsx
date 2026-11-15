import type { MouseEvent } from "react";
import { useT } from "@/lib/i18n";
import "./skip-link.css";

/** The id of the region a page's skip link moves focus to. */
export const MAIN_CONTENT_ID = "bf-main-content";

/**
 * The first Tab stop on a page (CORE-1718): hidden until focused, it moves
 * focus past the header and navigation to the page's main content. The target
 * needs `id={MAIN_CONTENT_ID}` and `tabIndex={-1}` so it can take focus.
 */
export function SkipLink() {
  const t = useT();
  function skip(event: MouseEvent<HTMLAnchorElement>) {
    const target = document.getElementById(MAIN_CONTENT_ID);
    if (!target) return;
    // Focus directly: a hash change would fight the client-side router.
    event.preventDefault();
    target.focus();
    target.scrollIntoView?.({ block: "start" });
  }
  return (
    <a href={`#${MAIN_CONTENT_ID}`} className="bf-skip-link" onClick={skip}>
      {t("skipToContent")}
    </a>
  );
}
