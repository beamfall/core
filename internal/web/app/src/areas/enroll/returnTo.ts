// Where a successful enrollment lands (ADR-0240 D9). A player display's QR
// code opens /play-on before the phone is enrolled; the enroll gate carries
// that path as `next` so the phone resolves the display straight after
// enrolling, without a second scan. Only the QR route is carried, and only as
// a same-origin path, so `next` can never send the browser elsewhere.

const RETURNABLE = /^\/play-on(\?[^#]*)?$/;

/** The `next` value to put on /enroll for a Viewer path, or null. */
export function enrollReturnPath(pathname: string, search: string): string | null {
  const path = pathname + search;
  return RETURNABLE.test(path) ? path : null;
}

/** The URL to load once enrollment succeeds: BASE_URL, or BASE_URL + `next`. */
export function afterEnrollURL(search: string = window.location.search): string {
  const base = import.meta.env.BASE_URL;
  const next = new URLSearchParams(search).get("next");
  if (!next || !RETURNABLE.test(next)) return base;
  return base.replace(/\/$/, "") + next;
}
