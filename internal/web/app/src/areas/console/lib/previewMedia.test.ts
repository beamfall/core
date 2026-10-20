import { afterEach, describe, expect, it, vi } from "vitest";
import { routeConsolePreview, webPlayerPath } from "./previewMedia";

type PostMessage = (message: { name: string; payload: Record<string, unknown> }) => boolean;

function installAppleHost(post: PostMessage): void {
  (window as unknown as { beamfallHost?: unknown }).beamfallHost = {
    kind: "apple-manager",
    platform: "macos",
    clientClass: "apple-manager-macos-direct",
    bridge: { postMessage: post },
  };
}

afterEach(() => {
  delete (window as unknown as { beamfallHost?: unknown }).beamfallHost;
  vi.restoreAllMocks();
});

describe("routeConsolePreview", () => {
  it("deep-links OUT to the native player inside the Apple host (never in-web)", () => {
    const post = vi.fn<PostMessage>(() => true);
    installAppleHost(post);

    const route = routeConsolePreview("media-123");

    expect(route).toEqual({ kind: "native" });
    expect(post).toHaveBeenCalledWith({
      name: "deepLink.openInNativePlayer",
      payload: { mediaID: "media-123" },
    });
  });

  it("falls back to the in-web player route in the browser Console (no host)", () => {
    const route = routeConsolePreview("media-123");
    expect(route).toEqual({ kind: "web", path: "/watch/media-123" });
  });

  it("falls back to the in-web player when the host bridge declines", () => {
    installAppleHost(() => false);
    const route = routeConsolePreview("media-123");
    expect(route).toEqual({ kind: "web", path: "/watch/media-123" });
  });

  it("does not deep-link an empty media id; uses the web route", () => {
    const post = vi.fn<PostMessage>(() => true);
    installAppleHost(post);

    const route = routeConsolePreview("");

    expect(post).not.toHaveBeenCalled();
    expect(route).toEqual({ kind: "web", path: "/watch/" });
  });
});

describe("webPlayerPath", () => {
  it("URL-encodes the media id", () => {
    expect(webPlayerPath("a/b c")).toBe("/watch/a%2Fb%20c");
  });
});
