// "Who's watching?" is asked once per browser tab (CORE-1196): a server with
// more than one Profile opens on the picker, and choosing a Profile (or
// confirming the current one) lets the rest of the tab's visit through. The
// flag is per-tab session storage, so a new tab or window asks again, as a
// shared living-room browser should.

const PICKED_KEY = "beamfall.profile-picked";

export function profilePickedThisTab(): boolean {
  try {
    return window.sessionStorage.getItem(PICKED_KEY) === "1";
  } catch {
    // Storage is blocked: never trap the visitor on the picker.
    return true;
  }
}

export function markProfilePicked(): void {
  try {
    window.sessionStorage.setItem(PICKED_KEY, "1");
  } catch {
    // Nothing to remember; profilePickedThisTab already lets the visit through.
  }
}
