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
const BUSY_ROUTES = ["/watch", "/view", "/slideshow", "/profiles", "/remote", "/party/stage"];
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

// Text fields the viewer has typed into since their form last reset. React keeps
// a controlled field's defaultValue equal to its value, so value against
// defaultValue cannot tell unsaved text on its own. A submit does not clear the
// mark: the save can fail and leave the text in place, so the text counts until
// the page empties the field or unmounts it. A search field outside a form
// filters as it is typed and holds no unsaved work; one inside a form waits
// for its submit, which runs the search, so the submit clears it.
const typed = new WeakSet<Element>();
const SEARCH_FIELDS = "input[type='search'], [role='searchbox']";

/** Follows typing in doc's text fields for pageBusy; returns the cleanup. */
export function trackTyping(doc: Document): () => void {
  const onInput = (event: Event) => {
    const target = event.target;
    if (
      target instanceof Element &&
      target.matches(TEXT_FIELDS) &&
      (!target.matches(SEARCH_FIELDS) || target.closest("form"))
    ) {
      typed.add(target);
    }
  };
  const onReset = (event: Event) => {
    const form = event.target;
    if (!(form instanceof HTMLFormElement)) return;
    for (const field of form.querySelectorAll(TEXT_FIELDS)) typed.delete(field);
  };
  const onSubmit = (event: Event) => {
    const form = event.target;
    if (!(form instanceof HTMLFormElement)) return;
    for (const field of form.querySelectorAll(SEARCH_FIELDS)) typed.delete(field);
  };
  doc.addEventListener("input", onInput, true);
  doc.addEventListener("reset", onReset, true);
  doc.addEventListener("submit", onSubmit, true);
  return () => {
    doc.removeEventListener("input", onInput, true);
    doc.removeEventListener("reset", onReset, true);
    doc.removeEventListener("submit", onSubmit, true);
  };
}

/**
 * Whether the page is mid-task: a dialog is open, media is playing, or a text
 * field has focus or unsaved text.
 */
export function pageBusy(doc: Document): boolean {
  if (
    doc.querySelector("dialog[open], [role='dialog'], [role='alertdialog'], [aria-modal='true']")
  ) {
    return true;
  }
  // Audible playback only: a muted, looping hover preview is not a viewer watching.
  for (const media of doc.querySelectorAll("video, audio")) {
    if (media instanceof HTMLMediaElement && !media.paused && !media.ended && !media.muted) {
      return true;
    }
  }
  const active = doc.activeElement;
  if (active instanceof HTMLElement && active.closest(TEXT_FIELDS)) return true;
  // Typed-in text the viewer has not submitted would be lost when the screensaver
  // replaces the page, even after focus moves on to the form's button.
  for (const field of doc.querySelectorAll(TEXT_FIELDS)) {
    if (
      (field instanceof HTMLInputElement || field instanceof HTMLTextAreaElement) &&
      (field.value !== field.defaultValue || (typed.has(field) && field.value !== ""))
    ) {
      return true;
    }
  }
  return false;
}
