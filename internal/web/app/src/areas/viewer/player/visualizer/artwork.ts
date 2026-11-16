// The visual's image input (CORE-2217): the track's artwork, fetched only from this origin so the
// pixels can be uploaded to WebGL, and decoded small, as the visuals sample it as a soft tint.
const SIZE = 256;

/** True for a non-empty URL served from this page's origin. */
export function sameOrigin(url: string): boolean {
  if (!url) return false;
  try {
    return new URL(url, window.location.href).origin === window.location.origin;
  } catch {
    return false;
  }
}

/** The artwork as a downscaled bitmap, or null where it is not same-origin. */
export async function loadArtwork(url: string, signal: AbortSignal): Promise<ImageBitmap | null> {
  if (!sameOrigin(url) || typeof createImageBitmap === "undefined") return null;
  const res = await fetch(url, { signal, credentials: "same-origin" });
  if (!res.ok) throw new Error(`visualizer: artwork ${res.status}`);
  const blob = await res.blob();
  signal.throwIfAborted();
  return createImageBitmap(blob, {
    resizeWidth: SIZE,
    resizeHeight: SIZE,
    resizeQuality: "medium",
  });
}
