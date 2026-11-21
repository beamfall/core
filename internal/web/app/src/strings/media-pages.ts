import { imageViewerStrings } from "./image-viewer";
import { karaokeStrings } from "./karaoke";
import { mediaDetailStrings } from "./media-detail";
import { slideshowStrings } from "./slideshow";

// Strings for the per-Media pages (detail, image viewer, slideshow, karaoke), bundled so
// catalog.ts registers them with one spread per locale. Add new media-page
// string modules here rather than to catalog.ts.
export const mediaPageStrings = {
  en: {
    ...mediaDetailStrings.en,
    ...karaokeStrings.en,
    ...imageViewerStrings.en,
    ...slideshowStrings.en,
  },
  fr: {
    ...mediaDetailStrings.fr,
    ...karaokeStrings.fr,
    ...imageViewerStrings.fr,
    ...slideshowStrings.fr,
  },
  de: {
    ...mediaDetailStrings.de,
    ...karaokeStrings.de,
    ...imageViewerStrings.de,
    ...slideshowStrings.de,
  },
  es: {
    ...mediaDetailStrings.es,
    ...karaokeStrings.es,
    ...imageViewerStrings.es,
    ...slideshowStrings.es,
  },
  pt: {
    ...mediaDetailStrings.pt,
    ...karaokeStrings.pt,
    ...imageViewerStrings.pt,
    ...slideshowStrings.pt,
  },
} as const;
