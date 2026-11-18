import { useState } from "react";
import { useT } from "@/lib/i18n";
import { artworkBucketURL, artworkSrcSet } from "../lib/artwork";

// The detail stage's backdrop and overview (media-detail.css), shared by the
// Entity page hero (CORE-2441). The Media detail page keeps its own copies
// for now; both draw the same classes.

const BACKDROP_BUCKETS = ["w640", "w1280"] as const;
/** An overview longer than this is clamped with a More toggle. */
const OVERVIEW_CLAMP_CHARS = 240;

/** The backdrop slot behind a scrim; renders nothing once the image fails. */
export function DetailBackdrop({ url }: { url: string }) {
  const [loaded, setLoaded] = useState(false);
  const [failed, setFailed] = useState(false);
  if (failed) return null;
  return (
    <div className="bf-detail-backdrop" aria-hidden>
      <img
        alt=""
        decoding="async"
        src={artworkBucketURL(url, "w1280")}
        srcSet={artworkSrcSet(url, BACKDROP_BUCKETS)}
        sizes="100vw"
        data-loaded={loaded ? "" : undefined}
        ref={(img) => {
          // A cached image can finish before React attaches onLoad.
          if (img?.complete && img.naturalWidth > 0 && !loaded) setLoaded(true);
        }}
        onLoad={() => setLoaded(true)}
        onError={() => setFailed(true)}
      />
      <div className="bf-detail-scrim" />
    </div>
  );
}

/** The overview text, clamped to three lines with a More toggle when long. */
export function DetailOverview({ text, id }: { text: string; id: string }) {
  const t = useT();
  const [expanded, setExpanded] = useState(false);
  const clampable = text.length > OVERVIEW_CLAMP_CHARS;
  return (
    <div className="bf-detail-overview">
      <p
        id={id}
        className="bf-detail-summary"
        data-clamped={clampable && !expanded ? "" : undefined}
      >
        {text}
      </p>
      {clampable && (
        <button
          type="button"
          className="bf-detail-more"
          aria-expanded={expanded}
          aria-controls={id}
          onClick={() => setExpanded((open) => !open)}
        >
          {expanded ? t("mediaDetailOverviewLess") : t("mediaDetailOverviewMore")}
        </button>
      )}
    </div>
  );
}
