import { Music } from "lucide-react";
import { type CSSProperties, type ReactNode, type RefObject, useEffect, useState } from "react";
import { Link, useInRouterContext } from "react-router";
import type { MediaCredit } from "@/lib/graphql/types";
import { mediaPath } from "../lib/mediaAddress";
import type { PlaybackStream } from "../lib/playback";
import {
  type ChangeMotion,
  motionForChange,
  usePrefersReducedMotion,
  useTrackChangeMotion,
} from "./trackChangeMotion";
import { useLayers } from "./useLayers";
import { type NextTrack, Visualizer } from "./visualizer/Visualizer";
import { useVisualChoice } from "./visualizer/visualChoice";

// The audio surface (CORE-1143): an audio item has no picture, so the stage
// shows its artwork, title and linked Entities instead of a black <video>.
// Generic by invariant #1 — credits are grouped by the kind's fixed archetype,
// never by domain names: "person" credits form one line, "container" credits
// another, everything else a third, each in plugin declaration order. The
// artwork frame keeps its square footprint with a quiet fallback when the item
// has no artwork or it fails to load, so the layout never shifts.
// Now Playing (CORE-1637): inside a router the title links to the item's page
// and each credit to its Entity page; `children` (Up Next, the quality badge)
// sit under the words.
export function AudioStage({
  title,
  artworkURL,
  credits,
  queuePosition,
  videoRef,
  stream = null,
  trackKey = "",
  next = null,
  mediaID = "",
  children,
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
  /** The playing item, whose page the title links to. */
  mediaID?: string;
  /** Shown under the words: Up Next and the quality badge. */
  children?: ReactNode;
}) {
  const [failedURL, setFailedURL] = useState("");
  const showArtwork = !!artworkURL && failedURL !== artworkURL;
  const people = credits.filter((c) => c.archetype === "person" && c.title);
  const collections = credits.filter((c) => c.archetype === "container" && c.title);
  const others = credits.filter(
    (c) => c.archetype !== "person" && c.archetype !== "container" && c.title,
  );
  const linked = useInRouterContext();

  const changeKey = trackKey || title;
  const motion = motionForChange(changeKey, useTrackChangeMotion(), usePrefersReducedMotion());
  const meta = { position: queuePosition ?? "", title, mediaID, people, collections, others };
  // A further change cancels an unfinished one: only the latest outgoing words stay.
  const metaLayers = useLayers(changeKey, meta, META_LEAVE_MS, undefined, 1);
  const art = showArtwork ? artworkURL : "";
  // Tracks of one album name the same picture by different URLs: the visual keeps the first,
  // so it neither reloads the image nor switches visuals between them.
  const artKey = artworkKey(art);
  const visualChoice = useVisualChoice();
  const [visualArt, setVisualArt] = useState({ key: artKey, url: art });
  if (visualArt.key !== artKey) setVisualArt({ key: artKey, url: art });
  const visualArtURL = visualArt.key === artKey ? visualArt.url : art;

  return (
    <div className="bf-audio-stage">
      <div aria-hidden="true" className="bf-audio-backdrop">
        <ArtworkLayers url={art} leaveMs={BACKDROP_LEAVE_MS} />
      </div>
      {/* The pinned Oscillux visualizer (CORE-1309). Visual effects come only
          from Oscillux (ADR-0228); the slot stays empty where it cannot draw,
          and when the viewer turns it off (CORE-2814). */}
      <div aria-hidden="true" className="bf-audio-visualizer" data-visualizer-slot="">
        {videoRef && visualChoice !== "off" && (
          <Visualizer
            videoRef={videoRef}
            stream={stream}
            trackKey={trackKey || title}
            visualKey={artKey || trackKey || title}
            choice={visualChoice}
            artworkURL={visualArtURL}
            next={next}
          />
        )}
      </div>
      <div className="bf-audio-card">
        <div className="bf-audio-artwork" data-art={motion.art}>
          <ArtworkLayers url={art} leaveMs={ART_LEAVE_MS} onError={setFailedURL} placeholder />
        </div>
        <div className="bf-audio-info">
          {/* One polite region: each incoming item is announced once, as whole
            words; leaving words and per-letter pieces are hidden from it. */}
          <div className="bf-audio-meta" data-words={motion.words} aria-live="polite">
            {metaLayers.map(({ id, value: m, leaving }) => (
              <div
                key={id}
                className="bf-audio-meta-layer"
                data-state={leaving ? "out" : "in"}
                aria-hidden={leaving || undefined}
              >
                {m.position && (
                  <Line beat={3} className="bf-audio-position">
                    {m.position}
                  </Line>
                )}
                <Line
                  beat={0}
                  className="bf-audio-title"
                  spoken={!leaving && splitsLetters(m.title, motion) ? m.title : undefined}
                  to={linked && !leaving && m.mediaID ? mediaPath("media", m.mediaID) : undefined}
                >
                  {leaving ? m.title : <Letters text={m.title} motion={motion} />}
                </Line>
                {m.people.length > 0 && (
                  <Line beat={1} className="bf-audio-people">
                    <Credits credits={m.people} separator=", " linked={linked && !leaving} />
                  </Line>
                )}
                {m.collections.length > 0 && (
                  <Line beat={2} className="bf-audio-collections">
                    <Credits credits={m.collections} separator=" · " linked={linked && !leaving} />
                  </Line>
                )}
                {m.others.length > 0 && (
                  <Line beat={3} className="bf-audio-others">
                    <Credits credits={m.others} separator=" · " linked={linked && !leaving} />
                  </Line>
                )}
              </div>
            ))}
          </div>
          {children}
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

// One line of words. Its wrapper is the mask a wipe sweeps and the frame a
// flap rolls in; the beat orders the arrival: title, then who made it, then
// what it belongs to and the queue position (CORE-2331).
// A line drawn letter by letter is read from `spoken`, a visually hidden copy,
// and its letters are hidden from assistive technology. A linked line keeps
// the spoken copy inside its link, so the link has a name.
function Line({
  beat,
  className,
  spoken,
  to,
  children,
}: {
  beat: number;
  className: string;
  spoken?: string;
  to?: string;
  children: ReactNode;
}) {
  if (to) {
    return (
      <div className="bf-audio-line" style={{ "--bf-line": beat } as CSSProperties}>
        <p className={className}>
          <Link to={to} className="bf-audio-link">
            {spoken && <span className="sr-only">{spoken}</span>}
            <span aria-hidden={spoken ? true : undefined}>{children}</span>
          </Link>
        </p>
      </div>
    );
  }
  return (
    <div className="bf-audio-line" style={{ "--bf-line": beat } as CSSProperties}>
      {spoken && <span className="sr-only">{spoken}</span>}
      <p className={className} aria-hidden={spoken ? true : undefined}>
        {children}
      </p>
    </div>
  );
}

// A credit line: each credit links to its Entity page when it names one.
function Credits({
  credits,
  separator,
  linked,
}: {
  credits: readonly MediaCredit[];
  separator: string;
  linked: boolean;
}) {
  if (!linked) return credits.map((credit) => credit.title).join(separator);
  return credits.map((credit, index) => (
    <span key={`${credit.entityID}-${index}`}>
      {index > 0 && separator}
      {credit.entityID ? (
        <Link to={`/entity/${encodeURIComponent(credit.entityID)}`} className="bf-audio-link">
          {credit.title}
        </Link>
      ) : (
        credit.title
      )}
    </span>
  ));
}

/** Titles longer than this rise as one line; per-letter motion would cost frames. */
const MAX_LETTERS = 80;

/** Whether the title is drawn letter by letter for this change. */
function splitsLetters(text: string, motion: ChangeMotion): boolean {
  return (
    (motion.words === "rise" || motion.words === "flap") && graphemes(text).length <= MAX_LETTERS
  );
}

// The title split into letters for the per-letter styles; words stay unbroken
// so the title wraps where the plain text would.
function Letters({ text, motion }: { text: string; motion: ChangeMotion }) {
  if (!splitsLetters(text, motion)) return text;
  let index = 0;
  return (
    <span className="bf-audio-letters">
      {text.split(/(\s+)/).map((word, w) => {
        if (!word) return null;
        if (/^\s+$/.test(word)) return word;
        return (
          <span key={w} className="bf-audio-word">
            {graphemes(word).map((letter, l) => (
              <span
                key={l}
                className="bf-audio-char"
                style={{ "--bf-char": index++ } as CSSProperties}
              >
                {letter}
              </span>
            ))}
          </span>
        );
      })}
    </span>
  );
}

function graphemes(text: string): string[] {
  if (typeof Intl !== "undefined" && "Segmenter" in Intl) {
    return [...new Intl.Segmenter(undefined, { granularity: "grapheme" }).segment(text)].map(
      (s) => s.segment,
    );
  }
  return Array.from(text);
}

// The artwork transition: the incoming image waits, hidden, until it has
// loaded and decoded, then settles in over the outgoing one, which stays
// underneath until then so the frame never flashes empty. With `placeholder`
// an item without artwork transitions to the audio placeholder the same way
// (CORE-2332); without it, no artwork fades the old one out.
function ArtworkLayers({
  url,
  leaveMs,
  onError,
  placeholder = false,
}: {
  url: string;
  leaveMs: number;
  onError?: (url: string) => void;
  placeholder?: boolean;
}) {
  // Tracks of one album carry their own artwork URLs for the same picture; the
  // ?v= token is a content hash, so the same picture keeps its layer (and its
  // first URL, so nothing reloads) instead of crossfading to itself.
  const key = artworkKey(url) || (placeholder ? PLACEHOLDER_KEY : "");
  const [first, setFirst] = useState({ key, url });
  if (first.key !== key) setFirst({ key, url });
  const src = first.key === key ? first.url : url;
  const [shown, setShown] = useState<ReadonlySet<number>>(() => new Set());
  // A further change cancels an unfinished one: only the latest outgoing picture stays.
  const layers = useLayers(key, src, leaveMs, (id) => shown.has(id), 1);
  // A cached image can load before the hidden starting state is painted;
  // revealing it a frame later keeps the settle-in animation.
  const reveal = (id: number) =>
    requestAnimationFrame(() => requestAnimationFrame(() => setShown((s) => new Set(s).add(id))));
  // The swap waits for the decoded picture, so it never paints half-decoded.
  const loaded = (id: number, img: HTMLImageElement) => {
    if (typeof img.decode !== "function") return reveal(id);
    img.decode().then(
      () => reveal(id),
      () => reveal(id),
    );
  };
  const ready = layers.every((l) => l.leaving || shown.has(l.id));
  return layers.map(({ id, value, leaving }) => {
    // A layer that never loaded has nothing to show on its way out.
    if (leaving && !shown.has(id)) return null;
    let state = shown.has(id) ? "in" : "pending";
    if (leaving) state = ready ? "out" : "under";
    if (!value) {
      return <PlaceholderLayer key={id} state={state} onMount={() => reveal(id)} />;
    }
    return (
      <img
        key={id}
        className="bf-audio-art-layer"
        src={value}
        alt=""
        data-state={state}
        onLoad={(event) => loaded(id, event.currentTarget)}
        onError={() => !leaving && onError?.(value)}
      />
    );
  });
}

const PLACEHOLDER_KEY = "placeholder";

// The quiet frame an item without artwork shows: nothing to load, so it is
// ready as soon as it is drawn.
function PlaceholderLayer({ state, onMount }: { state: string; onMount: () => void }) {
  useEffect(() => {
    onMount();
  }, []);
  return (
    <div className="bf-audio-art-layer bf-audio-art-placeholder" data-state={state}>
      <Music aria-hidden="true" strokeWidth={1.5} className="bf-audio-art-glyph" />
    </div>
  );
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
