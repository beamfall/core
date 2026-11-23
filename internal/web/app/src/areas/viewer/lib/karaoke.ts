import { useSession } from "@/lib/session";

/** The plugin ID of the karaoke Library plugin. */
export const KARAOKE_PLUGIN_ID = "karaoke";

/**
 * Whether a Library is a karaoke Library (CORE-3362), so its page leads with
 * the songbook and the party rail instead of the generic hub. The manifest
 * already names each Library's plugin, so this needs no read of its own.
 */
export function useIsKaraokeLibrary(libraryID: string): boolean {
  return isKaraokeLibrary(useSession().manifest, libraryID);
}

/** {@link useIsKaraokeLibrary} for a manifest already in hand. */
export function isKaraokeLibrary(
  manifest: { libraries?: { id: string; pluginID: string | null }[] | null } | null | undefined,
  libraryID: string,
): boolean {
  if (!libraryID) return false;
  return (manifest?.libraries ?? []).some(
    (library) => library.id === libraryID && library.pluginID === KARAOKE_PLUGIN_ID,
  );
}

/** The jump-rail bucket of a heading: its first character upper-cased when a letter, else "#". */
export function karaokeInitial(text: string): string {
  const first = [...text.trim()][0] ?? "";
  return /^\p{L}$/u.test(first) ? first.toUpperCase() : "#";
}
