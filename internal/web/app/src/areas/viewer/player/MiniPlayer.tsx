import { Maximize2, Pause, Play, SkipForward, X } from "lucide-react";
import { type RefObject, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { formatAriaTime, formatClockTime } from "../lib/transport";
import { useRemotePlayerTarget } from "../remote/remoteControl";
import { useWaveform, type WaveformInput } from "./useWaveform";
import { WaveformBars } from "./WaveformBars";

// The docked mini player (CORE-1633): the audio Media playing while the
// listener browses, with its artwork, title and credited people, play/pause,
// next, a progress bar, and a way back to the full player. It drives the same
// engine-owned media element the full player does, so progress beacons, the
// Media Session and relock handling are unchanged. It is a labelled region of
// native buttons, so it is reachable by keyboard and announced by name.
export function MiniPlayer({
  videoRef,
  title,
  artist,
  artworkURL,
  mediaID,
  songScale,
  onNext,
  onPrevious,
  onOpen,
  onStop,
  onRestart,
  waveform,
}: {
  videoRef: RefObject<HTMLVideoElement | null>;
  title: string;
  artist: string;
  artworkURL: string;
  mediaID: string;
  /** Song seconds per stream second (CORE-1659): times read in song time. */
  songScale: number;
  onNext?: () => void;
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

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    const sync = () => {
      setPaused(video.paused);
      setTime({
        current: video.currentTime * songScale,
        duration: Number.isFinite(video.duration) ? video.duration * songScale : 0,
      });
    };
    sync();
    const events = ["timeupdate", "durationchange", "loadedmetadata", "play", "pause", "emptied"];
    for (const name of events) video.addEventListener(name, sync);
    return () => {
      for (const name of events) video.removeEventListener(name, sync);
    };
  }, [videoRef, songScale]);

  // Commands from the Profile's other devices keep driving the docked player
  // (CORE-1678), as they drive the full player's transport.
  useRemotePlayerTarget({
    video: () => videoRef.current,
    mediaID,
    next: onNext,
    previous: onPrevious,
    stop: onStop,
    restart: onRestart,
  });

  function togglePlay() {
    const video = videoRef.current;
    if (!video) return;
    if (video.paused) video.play()?.catch(() => {});
    else video.pause();
  }

  const peaks = useWaveform({ mediaID, ...waveform });
  const showWaveform = !!peaks.points || peaks.pending;
  const percent = time.duration > 0 ? Math.min(100, (time.current / time.duration) * 100) : 0;
  const showArtwork = !!artworkURL && failedArtwork !== artworkURL;

  return (
    <section className="bf-mini-player" aria-label={t("miniPlayerRegion")}>
      <div
        className={`bf-mini-progress${showWaveform ? " has-waveform" : ""}`}
        role="progressbar"
        aria-label={t("playbackPosition")}
        aria-valuemin={0}
        aria-valuemax={Math.round(time.duration)}
        aria-valuenow={Math.round(time.current)}
        aria-valuetext={formatAriaTime(time.current, time.duration, t)}
      >
        {showWaveform ? (
          <WaveformBars points={peaks.points} progressPercent={percent} />
        ) : (
          <div className="bf-mini-progress-fill" style={{ width: `${percent}%` }} />
        )}
      </div>
      <div className="bf-mini-artwork" aria-hidden="true">
        {showArtwork && (
          <img src={artworkURL} alt="" onError={() => setFailedArtwork(artworkURL)} />
        )}
      </div>
      <div className="bf-mini-meta">
        <p className="bf-mini-title">{title}</p>
        {artist && <p className="bf-mini-artist">{artist}</p>}
      </div>
      <span className="bf-mini-time" aria-hidden="true">
        {formatClockTime(time.current)} / {formatClockTime(time.duration)}
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
        <button
          type="button"
          className="bf-mini-button"
          aria-label={t("nextInQueue")}
          disabled={!onNext}
          onClick={onNext}
        >
          <SkipForward aria-hidden />
        </button>
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
