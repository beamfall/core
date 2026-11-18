import { useLayoutEffect, useSyncExternalStore } from "react";

// How large posters draw in the viewer's grids (CORE-2468), like Plex's grid
// size control. One choice for every grid, kept per browser because the right
// size depends on the screen: localStorage when the browser allows it, and an
// in-memory value for the rest of the session when it does not, the same as the
// Library display mode. The chosen size lands on the root element as
// `data-poster-size`, and chrome.css turns it into the grids' column floor.
export const POSTER_SIZES = ["small", "medium", "large"] as const;
export type PosterSize = (typeof POSTER_SIZES)[number];
/** Large is the size the grids had before the control existed. */
export const DEFAULT_POSTER_SIZE: PosterSize = "large";
export const POSTER_SIZE_KEY = "beamfall.posterSize";

// The image `sizes` hint for a card at each size. Small cards stay under 160px,
// so a 2x screen fetches the w320 derivative instead of w640.
const CARD_IMAGE_SIZES: Record<PosterSize, string> = {
  small: "160px",
  medium: "300px",
  large: "300px",
};

const listeners = new Set<() => void>();
// Set only when storage refused a write, so the control still works this session.
let sessionValue: PosterSize | undefined;

function parsePosterSize(raw: string | null | undefined): PosterSize {
  return POSTER_SIZES.find((size) => size === raw) ?? DEFAULT_POSTER_SIZE;
}

export function readPosterSize(): PosterSize {
  if (sessionValue !== undefined) return sessionValue;
  try {
    return parsePosterSize(globalThis.localStorage?.getItem(POSTER_SIZE_KEY));
  } catch {
    return DEFAULT_POSTER_SIZE;
  }
}

function onStorage(event: StorageEvent) {
  if (event.key !== null && event.key !== POSTER_SIZE_KEY) return;
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  if (listeners.size === 1) globalThis.addEventListener?.("storage", onStorage);
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0) globalThis.removeEventListener?.("storage", onStorage);
  };
}

export function setPosterSize(size: PosterSize) {
  try {
    globalThis.localStorage.setItem(POSTER_SIZE_KEY, size);
    sessionValue = undefined;
  } catch {
    sessionValue = size;
  }
  for (const listener of listeners) listener();
}

export function usePosterSize(): [PosterSize, (size: PosterSize) => void] {
  return [
    useSyncExternalStore(subscribe, readPosterSize, () => DEFAULT_POSTER_SIZE),
    setPosterSize,
  ];
}

/** The `sizes` hint a card image gives at the chosen poster size. */
export function useCardImageSizes(): string {
  return CARD_IMAGE_SIZES[usePosterSize()[0]];
}

/** Keeps the root element's `data-poster-size` on the chosen size. */
export function usePosterSizeRoot() {
  const [size] = usePosterSize();
  useLayoutEffect(() => {
    document.documentElement.dataset.posterSize = size;
  }, [size]);
}
