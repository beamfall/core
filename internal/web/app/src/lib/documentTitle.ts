import { useEffect } from "react";

// The browser tab names the page (CORE-2322): "<page> · Beamfall", or just the
// brand where a page has no heading of its own.

/** The document title for a page heading. */
export function pageDocumentTitle(heading: string | null | undefined, brand: string): string {
  const name = heading?.replace(/\s+/g, " ").trim() ?? "";
  return name && name !== brand ? `${name} · ${brand}` : brand;
}

/** Names the tab after a page title; null leaves the tab to whoever else set it. */
export function useDocumentTitle(heading: string | null | undefined, brand: string) {
  useEffect(() => {
    if (heading === null) return;
    document.title = pageDocumentTitle(heading, brand);
  }, [heading, brand]);
}

/**
 * Names the tab after the first h1 inside the element with this ID, following
 * it as the page loads and its heading changes. Every page draws its heading
 * as the first h1 of the shell's main region, so the shell can do this once.
 */
export function useHeadingDocumentTitle(containerID: string, brand: string, enabled = true) {
  useEffect(() => {
    if (!enabled) return;
    const container = document.getElementById(containerID);
    if (!container) return;
    const apply = () => {
      const next = pageDocumentTitle(container.querySelector("h1")?.textContent, brand);
      if (document.title !== next) document.title = next;
    };
    apply();
    const observer = new MutationObserver(apply);
    observer.observe(container, { childList: true, subtree: true, characterData: true });
    return () => observer.disconnect();
  }, [containerID, brand, enabled]);
}
