import { listeningStrings } from "./listening";
import { playbackStrings } from "./playback";
import { playbackChapterStrings } from "./playback-chapters";
import { playerShortcutStrings } from "./player-shortcuts";
import { subtitleAppearanceStrings } from "./subtitle-appearance";
import { subtitleSearchStrings } from "./subtitle-search";
import { trackMenuStrings } from "./track-menu";

// Strings for the player surfaces (playback, chapters, shortcuts, subtitle appearance and
// search, listening, track menus), bundled so catalog.ts registers them with one spread per
// locale. Add new player string modules here rather than to catalog.ts.
export const playerSurfaceStrings = {
  en: {
    ...playbackStrings.en,
    ...playbackChapterStrings.en,
    ...playerShortcutStrings.en,
    ...subtitleAppearanceStrings.en,
    ...listeningStrings.en,
    ...trackMenuStrings.en,
    ...subtitleSearchStrings.en,
  },
  fr: {
    ...playbackStrings.fr,
    ...playbackChapterStrings.fr,
    ...playerShortcutStrings.fr,
    ...subtitleAppearanceStrings.fr,
    ...listeningStrings.fr,
    ...trackMenuStrings.fr,
    ...subtitleSearchStrings.fr,
  },
  de: {
    ...playbackStrings.de,
    ...playbackChapterStrings.de,
    ...playerShortcutStrings.de,
    ...subtitleAppearanceStrings.de,
    ...listeningStrings.de,
    ...trackMenuStrings.de,
    ...subtitleSearchStrings.de,
  },
  es: {
    ...playbackStrings.es,
    ...playbackChapterStrings.es,
    ...playerShortcutStrings.es,
    ...subtitleAppearanceStrings.es,
    ...listeningStrings.es,
    ...trackMenuStrings.es,
    ...subtitleSearchStrings.es,
  },
  pt: {
    ...playbackStrings.pt,
    ...playbackChapterStrings.pt,
    ...playerShortcutStrings.pt,
    ...subtitleAppearanceStrings.pt,
    ...listeningStrings.pt,
    ...trackMenuStrings.pt,
    ...subtitleSearchStrings.pt,
  },
} as const;
