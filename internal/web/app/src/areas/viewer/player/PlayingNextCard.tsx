import { type CSSProperties, useState } from "react";
import { useT } from "@/lib/i18n";
import { PLAYING_NEXT_SECONDS } from "./usePlayingNext";

// The audio Playing next card (CORE-3216): the next title, its picture and
// artist when the next item's read has them, and a ring that empties over the
// item's final seconds. The region is named once for assistive technology;
// the ticking seconds are visual only, so a screen reader is not told each one.
export function PlayingNextCard({
  title,
  artist = "",
  artworkURL = "",
  remaining,
  onPlay,
  onDismiss,
}: {
  title: string;
  artist?: string;
  artworkURL?: string;
  remaining: number;
  onPlay?: () => void;
  onDismiss: () => void;
}) {
  const t = useT();
  // A picture that fails to load leaves no broken frame behind.
  const [failedArt, setFailedArt] = useState("");
  const left = Math.max(0, Math.min(1, remaining / PLAYING_NEXT_SECONDS));
  const showArt = artworkURL !== "" && failedArt !== artworkURL;
  return (
    <div
      className="bf-playing-next"
      role="region"
      aria-label={artist ? t("playingNextBy", { title, artist }) : t("playingNextIn", { title })}
      style={{ "--bf-left": left } as CSSProperties}
    >
      <span className="bf-playing-next-ring" aria-hidden="true">
        <span className="bf-playing-next-seconds">{Math.ceil(remaining)}</span>
      </span>
      {showArt && (
        <img
          className="bf-playing-next-art"
          src={artworkURL}
          alt=""
          decoding="async"
          onError={() => setFailedArt(artworkURL)}
        />
      )}
      <span className="bf-playing-next-text">
        <span className="bf-playing-next-eyebrow">{t("playingNext")}</span>
        <span className="bf-playing-next-title">{title}</span>
        {artist && <span className="bf-playing-next-artist">{artist}</span>}
      </span>
      <span className="bf-playing-next-actions">
        {onPlay && (
          <button type="button" className="bf-control is-primary" onClick={onPlay}>
            {t("playNow")}
          </button>
        )}
        <button type="button" className="bf-control" onClick={onDismiss}>
          {t("dismiss")}
        </button>
      </span>
    </div>
  );
}
