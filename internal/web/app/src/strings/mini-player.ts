import { nowPlayingStrings } from "./now-playing";

// Docked mini player copy: audio keeps playing while the listener browses (CORE-1633).
// The audio Now Playing copy (CORE-1637) rides along to keep catalog.ts under its size budget.
export const miniPlayerStrings = {
  en: {
    miniPlayerRegion: "Now playing",
    miniPlayerOpen: "Open player",
    miniPlayerClose: "Stop and close player",
    ...nowPlayingStrings.en,
  },
  fr: {
    miniPlayerRegion: "Lecture en cours",
    miniPlayerOpen: "Ouvrir le lecteur",
    miniPlayerClose: "Arrêter et fermer le lecteur",
    ...nowPlayingStrings.fr,
  },
  de: {
    miniPlayerRegion: "Wird gerade wiedergegeben",
    miniPlayerOpen: "Player öffnen",
    miniPlayerClose: "Stoppen und Player schließen",
    ...nowPlayingStrings.de,
  },
  es: {
    miniPlayerRegion: "Reproduciendo",
    miniPlayerOpen: "Abrir reproductor",
    miniPlayerClose: "Detener y cerrar el reproductor",
    ...nowPlayingStrings.es,
  },
  pt: {
    miniPlayerRegion: "A reproduzir",
    miniPlayerOpen: "Abrir leitor",
    miniPlayerClose: "Parar e fechar o leitor",
    ...nowPlayingStrings.pt,
  },
} as const;
