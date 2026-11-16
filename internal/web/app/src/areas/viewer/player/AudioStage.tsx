import { type CSSProperties, type RefObject, useState } from "react";
import type { MediaCredit } from "@/lib/graphql/types";
import type { PlaybackStream } from "../lib/playback";
import { useLayers } from "./useLayers";
import { type NextTrack, Visualizer } from "./visualizer/Visualizer";

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
  next = null,
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
  /** The item played next, which the visualizer prepares ahead. */
  next?: NextTrack | null;
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

  const meta = { position: queuePosition ?? "", title, people, collections, others };
  const metaLayers = useLayers(trackKey || title, meta, META_LEAVE_MS);
  const art = showArtwork ? artworkURL : "";
  // Tracks of one album name the same picture by different URLs: the visual keeps the first,
  // so it neither reloads the image nor switches visuals between them.
  const artKey = artworkKey(art);
  const [visualArt, setVisualArt] = useState({ key: artKey, url: art });
  if (visualArt.key !== artKey) setVisualArt({ key: artKey, url: art });
  const visualArtURL = visualArt.key === artKey ? visualArt.url : art;

  return (
    <div className="bf-audio-stage">
      <div aria-hidden="true" className="bf-audio-backdrop">
        <ArtworkLayers url={art} leaveMs={BACKDROP_LEAVE_MS} />
      </div>
      {/* The pinned Oscillux visualizer (CORE-1309). Visual effects come only
          from Oscillux (ADR-0228); the slot stays empty where it cannot draw. */}
      <div aria-hidden="true" className="bf-audio-visualizer" data-visualizer-slot="">
        {videoRef && (
          <Visualizer
            videoRef={videoRef}
            stream={stream}
            trackKey={trackKey || title}
            visualKey={artKey || trackKey || title}
            artworkURL={visualArtURL}
            next={next}
          />
        )}
      </div>
      <div className="bf-audio-card">
        <div className="bf-audio-artwork">
          <ArtworkLayers url={art} leaveMs={ART_LEAVE_MS} onError={setFailedURL} />
        </div>
        <div className="bf-audio-meta">
          {metaLayers.map(({ id, value: m, leaving }) => (
            <div
              key={id}
              className="bf-audio-meta-layer"
              data-state={leaving ? "out" : "in"}
              aria-hidden={leaving || undefined}
            >
              {m.position && (
                <p className="bf-audio-position" style={line(0)}>
                  {m.position}
                </p>
              )}
              <p className="bf-audio-title" style={line(1)}>
                {m.title}
              </p>
              {m.people && (
                <p className="bf-audio-people" style={line(2)}>
                  {m.people}
                </p>
              )}
              {m.collections && (
                <p className="bf-audio-collections" style={line(3)}>
                  {m.collections}
                </p>
              )}
              {m.others && (
                <p className="bf-audio-others" style={line(4)}>
                  {m.others}
                </p>
              )}
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

// Track-change motion (CORE-2218). The durations match the CSS transitions in
// player-deferred.css; the outgoing layer is dropped once its animation ends.
const ART_LEAVE_MS = 700;
const BACKDROP_LEAVE_MS = 900;
const META_LEAVE_MS = 320;

// Each line rises in a beat after the one above it.
function line(index: number): CSSProperties {
  return { "--bf-line": index } as CSSProperties;
}

// The artwork crossfade: the incoming image waits, hidden, until it has
// loaded, then settles in over the outgoing one, which stays underneath until
// then so the frame never flashes empty. No artwork fades the old one out.
function ArtworkLayers({
  url,
  leaveMs,
  onError,
}: {
  url: string;
  leaveMs: number;
  onError?: (url: string) => void;
}) {
  // Tracks of one album carry their own artwork URLs for the same picture; the
  // ?v= token is a content hash, so the same picture keeps its layer (and its
  // first URL, so nothing reloads) instead of crossfading to itself.
  const key = artworkKey(url);
  const [first, setFirst] = useState({ key, url });
  if (first.key !== key) setFirst({ key, url });
  const src = first.key === key ? first.url : url;
  const [shown, setShown] = useState<ReadonlySet<number>>(() => new Set());
  const layers = useLayers(key, src, leaveMs, (id) => shown.has(id));
  // A cached image can load before the hidden starting state is painted;
  // revealing it a frame later keeps the settle-in animation.
  const reveal = (id: number) =>
    requestAnimationFrame(() => requestAnimationFrame(() => setShown((s) => new Set(s).add(id))));
  const ready = layers.every((l) => l.leaving || shown.has(l.id));
  return layers.map(({ id, value, leaving }) => {
    // A layer that never loaded has nothing to show on its way out.
    if (leaving && !shown.has(id)) return null;
    let state = shown.has(id) ? "in" : "pending";
    if (leaving) state = ready ? "out" : "under";
    return (
      <img
        key={id}
        className="bf-audio-art-layer"
        src={value}
        alt=""
        data-state={state}
        onLoad={() => reveal(id)}
        onError={() => !leaving && onError?.(value)}
      />
    );
  });
}

// The picture an artwork URL names: its slot and content token when it has one
// (/api/media/artwork/<media>/<slot>?v=<hash>), else the URL itself.
export function artworkKey(url: string): string {
  if (!url) return "";
  try {
    const parsed = new URL(url, "http://local");
    const v = parsed.searchParams.get("v");
    return v ? `${parsed.pathname.split("/").pop()}?v=${v}` : url;
  } catch {
    return url;
  }
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
