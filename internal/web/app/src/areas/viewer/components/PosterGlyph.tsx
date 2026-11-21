import { Film, ImageIcon, Music } from "lucide-react";

/**
 * The placeholder a poster frame draws when an item has no artwork: an audio
 * glyph for audio Media, a picture glyph for image Media (CORE-0542), the film
 * glyph otherwise (CORE-1492). Decorative, so
 * it stays hidden from assistive technology; the title carries the name.
 */
export function PosterGlyph({ mediaClass }: { mediaClass?: string }) {
  const kind = mediaClass === "audio" ? "audio" : mediaClass === "image" ? "image" : "film";
  const Glyph = kind === "audio" ? Music : kind === "image" ? ImageIcon : Film;
  return (
    <Glyph aria-hidden data-glyph={kind} strokeWidth={1.5} className="bf-poster-glyph size-[28%]" />
  );
}
