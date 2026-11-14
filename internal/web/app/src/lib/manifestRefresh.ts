// Manifest refresh request (CORE-1200). A Console write that changes what the
// manifest publishes (a Library's icon) asks the session to re-read it, so the
// Viewer chrome is current when the owner goes back to it. The session
// registers the reader; with none registered (a test, a screen outside the
// session) a request does nothing.
let refresher: (() => void) | undefined;

export function setManifestRefresher(refresh?: () => void): void {
  refresher = refresh;
}

export function requestManifestRefresh(): void {
  refresher?.();
}
