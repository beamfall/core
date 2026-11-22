import { useDisplayLocale, useT } from "@/lib/i18n";
import type { AudioQuality } from "../lib/playback";
import { audioQualityLabel } from "./nowPlaying";

export interface UpNextItem {
  id: string;
  title: string;
}

// Under the audio stage's words (CORE-1637): the quality badge and the next
// items of the queue already driving playback. Choosing an item plays it
// through the player's own advance, so the queue stays the one queue.
export function NowPlayingDetails({
  quality,
  upNext,
  onPlay,
}: {
  quality?: AudioQuality | null;
  upNext: readonly UpNextItem[];
  onPlay: (item: UpNextItem) => void;
}) {
  const t = useT();
  const locale = useDisplayLocale();
  const rate = new Intl.NumberFormat(locale, { maximumFractionDigits: 2 });
  const label = quality
    ? audioQualityLabel(quality, {
        direct: t("audioQualityDirect"),
        transcode: t("audioQualityTranscode"),
        bits: (bits) => t("audioQualityBits", { bits }),
        rate: (kHz) => t("audioQualityRate", { rate: rate.format(kHz) }),
      })
    : "";
  const items = upNext.filter((item) => item.title);
  if (!label && items.length === 0) return null;
  return (
    <>
      {label && (
        <p className="bf-audio-quality">
          <span className="sr-only">{t("nowPlayingQuality", { quality: label })}</span>
          <span aria-hidden="true">{label}</span>
        </p>
      )}
      {items.length > 0 && (
        <section className="bf-audio-upnext" aria-labelledby="bf-audio-upnext-heading">
          <h3 id="bf-audio-upnext-heading">{t("nowPlayingUpNext")}</h3>
          <ol>
            {items.map((item) => (
              <li key={item.id}>
                <button
                  type="button"
                  onClick={() => onPlay(item)}
                  aria-label={t("nowPlayingPlayItem", { title: item.title })}
                >
                  {item.title}
                </button>
              </li>
            ))}
          </ol>
        </section>
      )}
    </>
  );
}
