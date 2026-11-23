import { Film, ImageIcon, Music, Video } from "lucide-react";
import { libraryIcon } from "../../../lib/libraryIcons";

/**
 * The placeholder a poster frame draws when an item has no artwork: the icon
 * the item's plugin declares for artless items when it names one this build
 * knows (CORE-3336), else an audio glyph for audio Media, a picture glyph for
 * image Media (CORE-0542), a clip glyph for a camera-roll clip shown among
 * images (CORE-2977), the film glyph otherwise (CORE-1492). Decorative, so
 * it stays hidden from assistive technology; the title carries the name.
 */
export function PosterGlyph({
  mediaClass,
  clip = false,
  icon,
}: {
  mediaClass?: string;
  clip?: boolean;
  /** A Library icon name (CORE-1200 vocabulary) declared by the item's plugin. */
  icon?: string;
}) {
  const Declared = libraryIcon(icon);
  if (Declared && icon) {
    return (
      <Declared
        aria-hidden
        data-glyph={icon}
        strokeWidth={1.5}
        className="bf-poster-glyph size-[28%]"
      />
    );
  }
  const kind =
    mediaClass === "audio"
      ? "audio"
      : mediaClass === "image"
        ? "image"
        : clip && mediaClass === "video"
          ? "clip"
          : "film";
  const Glyph =
    kind === "audio" ? Music : kind === "image" ? ImageIcon : kind === "clip" ? Video : Film;
  return (
    <Glyph aria-hidden data-glyph={kind} strokeWidth={1.5} className="bf-poster-glyph size-[28%]" />
  );
}
