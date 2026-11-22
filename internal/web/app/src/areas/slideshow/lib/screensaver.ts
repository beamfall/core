// The web screensaver (CORE-2981, photos design §3.1): browsers have no
// third-party screensaver API, so, as on tvOS, an idle viewer opens the photo
// slideshow on the Source this device last played. Settings are per device.

import { parseSlideshowSource, type SlideshowSource, slideshowPath } from "./slideshow";

/** Input-free time on a browsing route before the screensaver opens. */
export const SCREENSAVER_IDLE_MS = 5 * 60_000;
/** The query flag that puts `/slideshow` in screensaver mode. */
export const SCREENSAVER_PARAM = "screensaver";

const ENABLED_KEY = "bf.screensaver.enabled";
const LAST_SOURCE_KEY = "bf.slideshow.lastSource";

/** Routes where a viewer is watching or doing something, so idle does not count. */
const BUSY_ROUTES = ["/watch", "/view", "/slideshow", "/profiles", "/remote"];
/** Fields a viewer types into; a focused checkbox or button is not mid-task. */
const TEXT_FIELDS =
  "input:not([type='checkbox']):not([type='radio']):not([type='range']):not([type='button']):not([type='submit']), textarea, [contenteditable='true']";

export function screensaverEnabled(): boolean {
  try {
    return localStorage.getItem(ENABLED_KEY) === "1";
  } catch {
    return false;
  }
}

export function saveScreensaverEnabled(on: boolean) {
  try {
    if (on) localStorage.setItem(ENABLED_KEY, "1");
    else localStorage.removeItem(ENABLED_KEY);
  } catch {
    // Without storage the option cannot be kept, and the screensaver stays off.
  }
}

/** The slideshow Source this device last played, or null. */
export function lastSlideshowSource(): SlideshowSource | null {
  try {
    const stored = localStorage.getItem(LAST_SOURCE_KEY);
    return stored ? parseSlideshowSource(stored) : null;
  } catch {
    return null;
  }
}

export function saveLastSlideshowSource(source: SlideshowSource) {
  try {
    localStorage.setItem(LAST_SOURCE_KEY, slideshowPath(source).replace(/^\/slideshow\?/, ""));
  } catch {
    // Storage is a convenience.
  }
}

/** The address that opens the screensaver on a Source. */
export function screensaverPath(source: SlideshowSource): string {
  return `${slideshowPath(source)}&${SCREENSAVER_PARAM}=1`;
}

export function isScreensaverSearch(params: URLSearchParams): boolean {
  return params.get(SCREENSAVER_PARAM) === "1";
}

/** Whether idle counts on this route: a browsing page, not the player or a full-screen surface. */
export function idleRoute(pathname: string): boolean {
  return !BUSY_ROUTES.some((route) => pathname === route || pathname.startsWith(`${route}/`));
}

/** Whether the page is mid-task: a dialog is open or a text field has focus. */
export function pageBusy(doc: Document): boolean {
  if (
    doc.querySelector("dialog[open], [role='dialog'], [role='alertdialog'], [aria-modal='true']")
  ) {
    return true;
  }
  const active = doc.activeElement;
  return active instanceof HTMLElement && !!active.closest(TEXT_FIELDS);
}
