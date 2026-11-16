import { type RefObject, useState } from "react";
import type { MediaCredit } from "@/lib/graphql/types";
import type { PlaybackStream } from "../lib/playback";
import { Visualizer } from "./visualizer/Visualizer";

// The audio surface (CORE-1143): an audio item has no picture, so the stage
// shows its artwork, title and linked Entities instead of a black <video>.
// Generic by invariant #1 — credits are grouped by the kind's fixed archetype,
// never by domain names: "person" credits form one line, "container" credits
// another, everything else a third, each in plugin declaration order. The
// artwork frame keeps its square footprint with a quiet fallback when the item
// has no artwork or it fails to load, so the layout never shifts.
export function AudioStage({
  title,
  artworkURL,
  credits,
  queuePosition,
  videoRef,
  stream = null,
  trackKey = "",
}: {
  title: string;
  artworkURL: string;
  credits: readonly MediaCredit[];
  /** "3 of 12" when the item plays from a queue. */
  queuePosition?: string;
  /** The playing element and its stream, which the visualizer analyses. */
  videoRef?: RefObject<HTMLVideoElement | null>;
  stream?: PlaybackStream | null;
  /** Stable per item: picks the visual. */
  trackKey?: string;
}) {
  const [failedURL, setFailedURL] = useState("");
  const showArtwork = !!artworkURL && failedURL !== artworkURL;
  const people = creditLine(credits, (c) => c.archetype === "person", ", ");
  const collections = creditLine(credits, (c) => c.archetype === "container", " · ");
  const others = creditLine(
    credits,
    (c) => c.archetype !== "person" && c.archetype !== "container",
    " · ",
  );

  return (
    <div className="bf-audio-stage">
      {showArtwork && (
        <div
          aria-hidden="true"
          className="bf-audio-backdrop"
          style={{ backgroundImage: `url("${cssURL(artworkURL)}")` }}
        />
      )}
      {/* The pinned Oscillux visualizer (CORE-1309). Visual effects come only
          from Oscillux (ADR-0228); the slot stays empty where it cannot draw. */}
      <div aria-hidden="true" className="bf-audio-visualizer" data-visualizer-slot="">
        {videoRef && (
          <Visualizer videoRef={videoRef} stream={stream} trackKey={trackKey || title} />
        )}
      </div>
      <div className="bf-audio-card">
        <div className="bf-audio-artwork">
          {showArtwork && <img src={artworkURL} alt="" onError={() => setFailedURL(artworkURL)} />}
        </div>
        <div className="bf-audio-meta">
          {queuePosition && <p className="bf-audio-position">{queuePosition}</p>}
          <p className="bf-audio-title">{title}</p>
          {people && <p className="bf-audio-people">{people}</p>}
          {collections && <p className="bf-audio-collections">{collections}</p>}
          {others && <p className="bf-audio-others">{others}</p>}
        </div>
      </div>
    </div>
  );
}

function creditLine(
  credits: readonly MediaCredit[],
  keep: (credit: MediaCredit) => boolean,
  separator: string,
): string {
  return credits
    .filter(keep)
    .map((credit) => credit.title)
    .filter(Boolean)
    .join(separator);
}

// The artwork URL lands inside a CSS url("…"); escape what would end it.
function cssURL(url: string): string {
  return url.replace(/["\\\n]/g, (ch) => `\\${ch === "\n" ? "a " : ch}`);
}
