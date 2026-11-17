import { Images, Type } from "lucide-react";
import { NativeSelect } from "@/components/ui/select";
import { useT } from "@/lib/i18n";
import {
  ART_STYLES,
  type ArtStyle,
  setTrackChangeMotion,
  useTrackChangeMotion,
  WORD_STYLES,
  type WordStyle,
} from "./trackChangeMotion";

const WORD_LABELS = {
  rise: "trackMotionRise",
  flap: "trackMotionFlap",
  wipe: "trackMotionWipe",
} as const satisfies Record<WordStyle, string>;

const ART_LABELS = {
  zoom: "trackMotionZoom",
  flip: "trackMotionFlip",
  slide: "trackMotionSlide",
  dissolve: "trackMotionDissolve",
} as const satisfies Record<ArtStyle, string>;

/**
 * The track-change motion rows of the player settings sheet (CORE-2331,
 * CORE-2332): how the audio card's words and artwork change between items.
 * The choice belongs to this browser; "Mix" pairs the styles per item.
 */
export function TrackMotionSettings() {
  const t = useT();
  const motion = useTrackChangeMotion();
  return (
    <>
      <label className="bf-settings-row">
        <span aria-hidden="true" className="bf-settings-tile">
          <Type />
        </span>
        <span className="bf-settings-label">{t("trackMotionWords")}</span>
        <NativeSelect
          aria-label={t("trackMotionWords")}
          className="bf-settings-select"
          value={motion.words}
          onValueChange={(value) => setTrackChangeMotion({ words: value as WordStyle })}
        >
          <option value="mix">{t("trackMotionMix")}</option>
          {WORD_STYLES.map((style) => (
            <option key={style} value={style}>
              {t(WORD_LABELS[style])}
            </option>
          ))}
        </NativeSelect>
      </label>
      <label className="bf-settings-row">
        <span aria-hidden="true" className="bf-settings-tile">
          <Images />
        </span>
        <span className="bf-settings-label">{t("trackMotionArt")}</span>
        <NativeSelect
          aria-label={t("trackMotionArt")}
          className="bf-settings-select"
          value={motion.art}
          onValueChange={(value) => setTrackChangeMotion({ art: value as ArtStyle })}
        >
          <option value="mix">{t("trackMotionMix")}</option>
          {ART_STYLES.map((style) => (
            <option key={style} value={style}>
              {t(ART_LABELS[style])}
            </option>
          ))}
        </NativeSelect>
      </label>
    </>
  );
}
