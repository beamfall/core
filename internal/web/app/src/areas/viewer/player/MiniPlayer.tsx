import { Maximize2, Pause, Play, SkipForward, X } from "lucide-react";
import {
  Fragment,
  type KeyboardEvent,
  type PointerEvent,
  type RefObject,
  useEffect,
  useState,
} from "react";
import { Link, useInRouterContext } from "react-router";
import type { MediaCredit } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { formatAriaTime, formatClockTime, playbackDurationSeconds } from "../lib/transport";
import { useRemotePlayerTarget } from "../remote/remoteControl";
import { NeighbourTip } from "./NeighbourTip";
import { useWaveform, type WaveformInput } from "./useWaveform";
import { markViewerIntent } from "./viewerIntent";
import { WaveformBars } from "./WaveformBars";

// The docked mini player (CORE-1633): the audio Media playing while the
// listener browses, with its artwork, title and credited people, play/pause,
// next, a progress bar, and a way back to the full player. It drives the same
// engine-owned media element the full player does, so progress beacons, the
// Media Session and relock handling are unchanged. It is a labelled region of
// native buttons, so it is reachable by keyboard and announced by name.
// Every credited person links to their page and the artwork and album name to
// the album's (CORE-3349); the player host sits above the routes, so following
// a link keeps the audio playing.
export function MiniPlayer({
  videoRef,
  title,
  credits,
  artworkURL,
  mediaID,
  songScale,
  durationSeconds,
  onNext,
  nextTitle,
  onPrevious,
  onOpen,
  onStop,
  onRestart,
  waveform,
}: {
  videoRef: RefObject<HTMLVideoElement | null>;
  title: string;
  /** The stage's credits: person credits form the artist line, the first container is the album. */
  credits: readonly MediaCredit[];
  artworkURL: string;
  mediaID: string;
  /** Song seconds per stream second (CORE-1659): times read in song time. */
  songScale: number;
  /**
   * The server's probed length in song seconds: a live transcode's
   * `video.duration` only covers what has been encoded so far.
   */
  durationSeconds?: number;
  onNext?: () => void;
  /** The queue's next title, named on Next as the full player does. */
  nextTitle?: string;
  onPrevious?: () => void;
  onOpen: () => void;
  onStop: () => void;
  onRestart?: (atSeconds?: number) => void;
  /**
   * The playback info's waveform for this Media and the next one (CORE-2404).
   * Peaks come from the same per-Media cache the full player fills, so docking
   * shows them at once.
   */
  waveform?: Omit<WaveformInput, "mediaID">;
}) {
  const t = useT();
  const [paused, setPaused] = useState(true);
  const [time, setTime] = useState({ current: 0, duration: 0 });
  const [failedArtwork, setFailedArtwork] = useState("");
  // Song seconds a drag on the seek strip is previewing; it seeks on release.
  const [scrub, setScrub] = useState<number | null>(null);

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    const sync = () => {
      setPaused(video.paused);
      setTime({
        current: video.currentTime * songScale,
        duration: playbackDurationSeconds(durationSeconds, video.duration * songScale),
      });
    };
    sync();
    const events = ["timeupdate", "durationchange", "loadedmetadata", "play", "pause", "emptied"];
    for (const name of events) video.addEventListener(name, sync);
    return () => {
      for (const name of events) video.removeEventListener(name, sync);
    };
  }, [videoRef, songScale, durationSeconds]);

  // Commands from the Profile's other devices keep driving the docked player
  // (CORE-1678), as they drive the full player's transport.
  useRemotePlayerTarget({
    video: () => videoRef.current,
    mediaID,
    next: onNext,
    previous: onPrevious,
    stop: onStop,
    restart: onRestart,
    songScale,
  });

  function togglePlay() {
    const video = videoRef.current;
    if (!video) return;
    if (video.paused) video.play()?.catch(() => {});
    else video.pause();
  }

  function seekTo(songSeconds: number) {
    const video = videoRef.current;
    if (!video || time.duration <= 0) return;
    const clamped = Math.max(0, Math.min(time.duration, songSeconds));
    // A Watch Party keeps this seek rather than correcting it back (CORE-1677).
    markViewerIntent();
    video.currentTime = clamped / songScale;
    setTime((prev) => ({ ...prev, current: clamped }));
  }

  function pointerSeconds(event: PointerEvent<HTMLDivElement>) {
    const rect = event.currentTarget.getBoundingClientRect();
    const fraction = rect.width > 0 ? (event.clientX - rect.left) / rect.width : 0;
    return Math.max(0, Math.min(1, fraction)) * time.duration;
  }

  function onSeekKey(event: KeyboardEvent<HTMLDivElement>) {
    const steps: Record<string, number> = {
      ArrowLeft: time.current - 5,
      ArrowRight: time.current + 5,
      PageDown: time.current - 30,
      PageUp: time.current + 30,
      Home: 0,
      End: time.duration,
    };
    const target = steps[event.key];
    if (target === undefined) return;
    event.preventDefault();
    seekTo(target);
  }

  const peaks = useWaveform({ mediaID, ...waveform });
  const showWaveform = !!peaks.points || peaks.pending;
  const shown = scrub ?? time.current;
  const percent = time.duration > 0 ? Math.min(100, (shown / time.duration) * 100) : 0;
  const seekable = time.duration > 0;
  const showArtwork = !!artworkURL && failedArtwork !== artworkURL;
  const linked = useInRouterContext();
  const people = credits.filter((credit) => credit.archetype === "person" && credit.title);
  const album = credits.find((credit) => credit.archetype === "container" && credit.title);
  const albumPath = linked && album?.entityID ? creditPath(album) : "";
  const artwork = showArtwork && (
    <img src={artworkURL} alt="" onError={() => setFailedArtwork(artworkURL)} />
  );

  return (
    <section className="bf-mini-player" aria-label={t("miniPlayerRegion")}>
      <div
        className={`bf-mini-progress${showWaveform ? " has-waveform" : ""}${scrub !== null ? " is-scrubbing" : ""}`}
        role="slider"
        tabIndex={seekable ? 0 : -1}
        aria-label={t("playbackPosition")}
        aria-disabled={!seekable || undefined}
        aria-valuemin={0}
        aria-valuemax={Math.round(time.duration)}
        aria-valuenow={Math.round(shown)}
        aria-valuetext={formatAriaTime(shown, time.duration, t)}
        onKeyDown={onSeekKey}
        onPointerDown={(event) => {
          if (!seekable || event.button !== 0) return;
          event.currentTarget.setPointerCapture?.(event.pointerId);
          setScrub(pointerSeconds(event));
        }}
        onPointerMove={(event) => {
          if (scrub !== null) setScrub(pointerSeconds(event));
        }}
        onPointerUp={(event) => {
          if (scrub === null) return;
          seekTo(pointerSeconds(event));
          setScrub(null);
        }}
        onPointerCancel={() => setScrub(null)}
      >
        <div className="bf-mini-progress-track">
          {showWaveform ? (
            <WaveformBars points={peaks.points} progressPercent={percent} />
          ) : (
            <div className="bf-mini-progress-fill" style={{ width: `${percent}%` }} />
          )}
        </div>
      </div>
      {albumPath ? (
        // The album name below is the keyboard route to the same page; the
        // artwork is a pointer shortcut, so it stays out of the tab order.
        <Link to={albumPath} className="bf-mini-artwork" tabIndex={-1} aria-label={album?.title}>
          {artwork}
        </Link>
      ) : (
        <div className="bf-mini-artwork" aria-hidden="true">
          {artwork}
        </div>
      )}
      <div className="bf-mini-meta">
        <p className="bf-mini-title">{title}</p>
        {(people.length > 0 || album) && (
          <p className="bf-mini-artist">
            {people.map((credit, index) => (
              <Fragment key={`${credit.entityID}-${index}`}>
                {index > 0 && <span className="bf-mini-sep">, </span>}
                <CreditLink credit={credit} linked={linked} />
              </Fragment>
            ))}
            {album && (
              <>
                {people.length > 0 && <span className="bf-mini-sep"> · </span>}
                <CreditLink credit={album} linked={linked} />
              </>
            )}
          </p>
        )}
      </div>
      <span className="bf-mini-time" aria-hidden="true">
        {formatClockTime(shown)} / {formatClockTime(time.duration)}
      </span>
      <div className="bf-mini-controls">
        <button
          type="button"
          className="bf-mini-button"
          aria-label={paused ? t("play") : t("pause")}
          onClick={togglePlay}
        >
          {paused ? <Play aria-hidden /> : <Pause aria-hidden />}
        </button>
        <span className="bf-neighbour">
          <button
            type="button"
            className="bf-mini-button"
            aria-label={nextTitle ? t("nextInQueueNamed", { title: nextTitle }) : t("nextInQueue")}
            disabled={!onNext}
            onClick={onNext}
          >
            <SkipForward aria-hidden />
          </button>
          {onNext && nextTitle && <NeighbourTip eyebrow={t("nextInQueue")} title={nextTitle} />}
        </span>
        <button
          type="button"
          className="bf-mini-button"
          aria-label={t("miniPlayerOpen")}
          onClick={onOpen}
        >
          <Maximize2 aria-hidden />
        </button>
        <button
          type="button"
          className="bf-mini-button"
          aria-label={t("miniPlayerClose")}
          onClick={onStop}
        >
          <X aria-hidden />
        </button>
      </div>
    </section>
  );
}

function creditPath(credit: MediaCredit): string {
  return `/entity/${encodeURIComponent(credit.entityID)}`;
}

// A credit's name, linked to its Entity page when it names one. Each credit is
// its own shrinking flex item, so a long name cannot push later links out of reach.
function CreditLink({ credit, linked }: { credit: MediaCredit; linked: boolean }) {
  if (!linked || !credit.entityID) return <span className="bf-mini-credit">{credit.title}</span>;
  return (
    <Link to={creditPath(credit)} className="bf-mini-credit bf-mini-link">
      {credit.title}
    </Link>
  );
}
