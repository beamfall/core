import { listeningControlsStrings } from "./listening-controls";
import { nowPlayingStrings } from "./now-playing";

// Docked mini player copy: audio keeps playing while the listener browses (CORE-1633).
// The audio Now Playing (CORE-1637) and listening controls (CORE-2960) copy rides
// along to keep catalog.ts under its size budget.
export const miniPlayerStrings = {
  en: {
    miniPlayerRegion: "Now playing",
    miniPlayerOpen: "Open player",
    miniPlayerClose: "Stop and close player",
    ...nowPlayingStrings.en,
    ...listeningControlsStrings.en,
  },
  fr: {
    miniPlayerRegion: "Lecture en cours",
    miniPlayerOpen: "Ouvrir le lecteur",
    miniPlayerClose: "Arrêter et fermer le lecteur",
    ...nowPlayingStrings.fr,
    ...listeningControlsStrings.fr,
  },
  de: {
    miniPlayerRegion: "Wird gerade wiedergegeben",
    miniPlayerOpen: "Player öffnen",
    miniPlayerClose: "Stoppen und Player schließen",
    ...nowPlayingStrings.de,
    ...listeningControlsStrings.de,
  },
  es: {
    miniPlayerRegion: "Reproduciendo",
    miniPlayerOpen: "Abrir reproductor",
    miniPlayerClose: "Detener y cerrar el reproductor",
    ...nowPlayingStrings.es,
    ...listeningControlsStrings.es,
  },
  pt: {
    miniPlayerRegion: "A reproduzir",
    miniPlayerOpen: "Abrir leitor",
    miniPlayerClose: "Parar e fechar o leitor",
    ...nowPlayingStrings.pt,
    ...listeningControlsStrings.pt,
  },
} as const;
