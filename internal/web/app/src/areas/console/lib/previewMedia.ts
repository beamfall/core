import { requestOpenInNativePlayer } from "@/lib/beamfallHost";

/// The outcome of routing a Console "preview this Media" affordance (MGRWEB-7).
///
/// Inside the Apple host all playback is native (apple ADR-0002 §2): the
/// preview deep-links **out** to the native Viewer/player and the in-web
/// hls.js player is never rendered (no two-player handoff / double audio).
/// In the browser-only Console the in-web player handles the preview.
export type PreviewRoute = { kind: "native" } | { kind: "web"; path: string };

/// The browser in-web player route for `mediaId` (the only path that mounts the
/// hls.js player). Used solely outside the Apple host.
export function webPlayerPath(mediaId: string): string {
  return `/watch/${encodeURIComponent(mediaId)}`;
}

/// Host-aware routing for the embedded Console's preview affordance.
///
/// When running inside the Apple host the preview is deep-linked out to the
/// native player via the bridge and `{ kind: "native" }` is returned, so the
/// caller must NOT mount the in-web player. Only when no host bridge handles it
/// (browser Console, or a host that declined) does this fall back to the in-web
/// player route. This is the single point that keeps hls.js out of the Apple
/// WebView.
export function routeConsolePreview(mediaId: string): PreviewRoute {
  if (requestOpenInNativePlayer(mediaId)) {
    return { kind: "native" };
  }
  return { kind: "web", path: webPlayerPath(mediaId) };
}
