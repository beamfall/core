import { imageViewerStrings } from "./image-viewer";
import { karaokeStrings } from "./karaoke";
import { mediaDetailStrings } from "./media-detail";

// Strings for the per-Media pages (detail, image viewer, karaoke), bundled so
// catalog.ts registers them with one spread per locale. Add new media-page
// string modules here rather than to catalog.ts.
export const mediaPageStrings = {
  en: { ...mediaDetailStrings.en, ...karaokeStrings.en, ...imageViewerStrings.en },
  fr: { ...mediaDetailStrings.fr, ...karaokeStrings.fr, ...imageViewerStrings.fr },
  de: { ...mediaDetailStrings.de, ...karaokeStrings.de, ...imageViewerStrings.de },
  es: { ...mediaDetailStrings.es, ...karaokeStrings.es, ...imageViewerStrings.es },
  pt: { ...mediaDetailStrings.pt, ...karaokeStrings.pt, ...imageViewerStrings.pt },
} as const;
