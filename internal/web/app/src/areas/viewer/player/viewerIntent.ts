// When the viewer last asked for a play, pause or seek: a pointer or key in
// the page, or an OS media control routed through the Media Session. A Watch
// Party sends a media event as transport only if it follows this input, so the
// browser's autoplay and the party's own corrections stay local (CORE-1677).

let last = Number.NEGATIVE_INFINITY;

/** Record that the viewer just asked for a transport change. */
export function markViewerIntent(): void {
  last = performance.now();
}

/** performance.now() of the viewer's last transport input. */
export function lastViewerIntent(): number {
  return last;
}
