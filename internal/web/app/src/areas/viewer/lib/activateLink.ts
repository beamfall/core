import type { MouseEvent } from "react";

/** Build navigation state on activation, preserving the anchor's new-tab gestures. */
export function activateLink(event: MouseEvent<HTMLAnchorElement>, navigate: () => void) {
  if (
    event.defaultPrevented ||
    event.button !== 0 ||
    event.metaKey ||
    event.altKey ||
    event.ctrlKey ||
    event.shiftKey
  ) {
    return;
  }
  event.preventDefault();
  navigate();
}
