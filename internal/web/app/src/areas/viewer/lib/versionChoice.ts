import type { PlaybackVersion } from "./playback";

// The Version a profile last picked for a Media (ADR-0009 Versions, CORE-1676).
// It is a per-viewer convenience kept in this browser only: a blocked or
// cleared store means the server's own choice. The stored value is the opaque
// Version id playbackInfo reports, which carries no path or file identity.
const VERSION_CHOICE_KEY = "beamfall.version.";

function choiceKey(profileID: string, mediaID: string): string {
  return `${VERSION_CHOICE_KEY}${profileID}\u0000${mediaID}`;
}

export function readVersionChoice(profileID: string | undefined, mediaID: string): string {
  if (!profileID || !mediaID) return "";
  try {
    return window.localStorage.getItem(choiceKey(profileID, mediaID)) ?? "";
  } catch {
    return "";
  }
}

export function writeVersionChoice(
  profileID: string | undefined,
  mediaID: string,
  versionID: string,
): void {
  if (!profileID || !mediaID) return;
  try {
    window.localStorage.setItem(choiceKey(profileID, mediaID), versionID);
  } catch {
    // Remembering the choice is optional.
  }
}

/** A Version's picker label: its stored label, or the client's name for the default. */
export function versionLabel(version: PlaybackVersion, defaultLabel: string): string {
  return version.label || defaultLabel;
}
