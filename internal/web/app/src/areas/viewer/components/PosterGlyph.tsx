import { Film, Music } from "lucide-react";

/**
 * The placeholder a poster frame draws when an item has no artwork: an audio
 * glyph for audio Media, the film glyph otherwise (CORE-1492). Decorative, so
 * it stays hidden from assistive technology; the title carries the name.
 */
export function PosterGlyph({ mediaClass }: { mediaClass?: string }) {
  const Glyph = mediaClass === "audio" ? Music : Film;
  return (
    <Glyph
      aria-hidden
      data-glyph={mediaClass === "audio" ? "audio" : "film"}
      strokeWidth={1.5}
      className="bf-poster-glyph size-[28%]"
    />
  );
}
