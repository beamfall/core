import { useEffect, useLayoutEffect, useSyncExternalStore } from "react";
import { getIdentityEpochSignal } from "@/lib/api";
import { fetchClientPrefs, setClientPref } from "@/lib/graphql/viewer";

// How large posters draw across the viewer (CORE-2468, CORE-2893), like Plex's
// grid size control. One choice for the whole app. The browser keeps the value
// it draws: localStorage when the browser allows it, and an in-memory value for
// the rest of the session when it does not, the same as the Library display
// mode. The chosen size lands on the root element as `data-poster-size`, and
// chrome.css turns it into every surface's size, scaled separately for phones.
//
// The choice also roams with the signed-in Profile (CORE-2914): a pick is saved
// to the Profile's client preferences, and a Profile's saved size becomes the
// browser's value once its preferences load. A Profile with no saved size keeps
// the browser's value, and nothing is saved to a Profile until a pick is made
// under it.
export const POSTER_SIZES = ["small", "medium", "large"] as const;
export type PosterSize = (typeof POSTER_SIZES)[number];
/** Large is the size the grids had before the control existed. */
export const DEFAULT_POSTER_SIZE: PosterSize = "large";
export const POSTER_SIZE_KEY = "beamfall.posterSize";
/** The per-Profile client preference that carries the choice between browsers. */
export const POSTER_SIZE_PREF_KEY = "web.poster-size";

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

function emit() {
  for (const listener of listeners) listener();
}

function onStorage(event: StorageEvent) {
  if (event.key !== null && event.key !== POSTER_SIZE_KEY) return;
  // Another tab's pick (or its adoption of the Profile's size) is newer than
  // a read this tab still has in flight: that read must not undo it.
  if (sync?.state === "loading") sync.picked = true;
  emit();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  if (listeners.size === 1) globalThis.addEventListener?.("storage", onStorage);
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0) globalThis.removeEventListener?.("storage", onStorage);
  };
}

/** Make `size` this browser's value, without saving it to the Profile. */
function setBrowserPosterSize(size: PosterSize) {
  try {
    globalThis.localStorage.setItem(POSTER_SIZE_KEY, size);
    sessionValue = undefined;
  } catch {
    sessionValue = size;
  }
  emit();
}

// The signed-in Profile the choice syncs with, held outside React so it
// survives AppShell remounts (every route mounts its own shell) and a visit
// to the Console.
interface ProfileSync {
  profileID: string;
  state: "loading" | "loaded" | "failed";
  // A Viewer shell for this Profile is mounted: only then does a pick save.
  active: boolean;
  // The browser's value changed since this Profile's read began (a pick here
  // or in another tab): the read is older and must not undo it.
  picked: boolean;
  // Saves run one after another, in pick order, so the Profile ends on the
  // last pick.
  writes: Promise<unknown>;
}

let sync: ProfileSync | null = null;

function loadProfile(profileID: string) {
  const same = sync?.profileID === profileID ? sync : null;
  if (same && same.state !== "failed") {
    same.active = true;
    return;
  }
  const current: ProfileSync = {
    profileID,
    state: "loading",
    active: true,
    // A retry after a failed read keeps this Profile's picks and their saves.
    picked: same?.picked ?? false,
    writes: same?.writes ?? Promise.resolve(),
  };
  sync = current;
  fetchClientPrefs()
    .then((prefs) => {
      if (sync !== current) return;
      current.state = "loaded";
      const saved = prefs.find((pref) => pref.key === POSTER_SIZE_PREF_KEY)?.value;
      // An unknown or missing value leaves the browser's value alone.
      const size = POSTER_SIZES.find((candidate) => candidate === saved);
      if (size && !current.picked && size !== readPosterSize()) setBrowserPosterSize(size);
    })
    .catch(() => {
      // Keep the browser's value; the next shell mount reads again.
      if (sync === current && current.state === "loading") current.state = "failed";
    });
}

/** Test seam: forget the Profile sync. */
export function resetPosterSizeSync() {
  sync = null;
  sessionValue = undefined;
}

export function setPosterSize(size: PosterSize) {
  setBrowserPosterSize(size);
  const current = sync;
  if (!current?.active) return;
  current.picked = true;
  const { profileID } = current;
  // A queued save must not run under a later identity: once a Profile switch,
  // reveal or relock starts a new epoch, the session it would carry belongs
  // to someone else. A retried read or a Console visit for the same Profile
  // keeps the queue.
  const epoch = getIdentityEpochSignal();
  current.writes = current.writes
    .then(() => {
      if (sync?.profileID !== profileID) return;
      if (getIdentityEpochSignal() !== epoch || epoch?.aborted) return;
      return setClientPref(POSTER_SIZE_PREF_KEY, size);
    })
    // A failed save keeps the browser's value; the next pick saves again.
    .catch(() => {});
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

/**
 * Keeps the root element's `data-poster-size` on the chosen size, and syncs
 * the choice with the signed-in Profile. With no Profile (signed out, enrolling,
 * the Console) the choice stays the browser's own.
 */
export function usePosterSizeRoot(profileID: string | null) {
  const [size] = usePosterSize();
  useLayoutEffect(() => {
    document.documentElement.dataset.posterSize = size;
  }, [size]);
  useEffect(() => {
    if (profileID) loadProfile(profileID);
    // Signed out, enrolling or in the Console: picks stay the browser's own,
    // and saves already queued for the Profile still run.
    else if (sync) sync.active = false;
  }, [profileID]);
}
