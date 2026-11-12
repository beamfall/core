import { useLayoutEffect } from "react";
import { useLocation, useNavigationType } from "react-router";

// Per-history-entry window scroll, keyed by location.key. Module-scoped so a
// position survives the Viewer area unmounting (e.g. a trip into the console).
const positions = new Map<string, number>();

// How long a Back/Forward restore waits for hydrating Sections to grow the page
// tall enough to reach the saved offset before settling for the furthest reach.
export const SCROLL_RESTORE_TIMEOUT_MS = 3000;

const USER_SCROLL_INTENT = ["wheel", "touchstart", "keydown", "pointerdown"] as const;

// A new navigation (PUSH) starts at the top; Back/Forward (POP) returns to the
// entry's saved offset once the content is tall enough, giving up early if the
// user starts scrolling themselves. REPLACE (query edits, redirects) keeps the
// current position.
export function useScrollRestoration() {
  const location = useLocation();
  const navigationType = useNavigationType();

  useLayoutEffect(() => {
    if (!("scrollRestoration" in window.history)) return;
    const previous = window.history.scrollRestoration;
    window.history.scrollRestoration = "manual";
    return () => {
      window.history.scrollRestoration = previous;
    };
  }, []);

  useLayoutEffect(() => {
    const key = location.key;
    let restoring = false;
    let frame = 0;

    const stopRestoring = () => {
      restoring = false;
      if (frame) window.cancelAnimationFrame(frame);
      frame = 0;
      for (const type of USER_SCROLL_INTENT) window.removeEventListener(type, stopRestoring);
    };
    const save = () => {
      if (!restoring) positions.set(key, window.scrollY);
    };

    const target = navigationType === "POP" ? positions.get(key) : undefined;
    if (navigationType === "PUSH") {
      window.scrollTo(0, 0);
    } else if (target !== undefined && target > 0) {
      restoring = true;
      const deadline = Date.now() + SCROLL_RESTORE_TIMEOUT_MS;
      const attempt = () => {
        frame = 0;
        if (!restoring) return;
        const reachable = document.documentElement.scrollHeight - window.innerHeight;
        if (reachable >= target || Date.now() >= deadline) {
          window.scrollTo(0, Math.max(0, Math.min(target, reachable)));
          stopRestoring();
          return;
        }
        frame = window.requestAnimationFrame(attempt);
      };
      for (const type of USER_SCROLL_INTENT) {
        window.addEventListener(type, stopRestoring, { passive: true });
      }
      attempt();
    }

    window.addEventListener("scroll", save, { passive: true });
    return () => {
      stopRestoring();
      window.removeEventListener("scroll", save);
    };
  }, [location.key, navigationType]);
}

export function resetScrollPositionsForTests() {
  positions.clear();
}
