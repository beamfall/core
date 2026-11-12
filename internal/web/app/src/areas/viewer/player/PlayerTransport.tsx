import type Hls from "hls.js";
import {
  AudioLines,
  Captions,
  ChevronRight,
  ChevronsLeft,
  ChevronsRight,
  Gauge,
  Maximize,
  Minimize,
  MonitorPlay,
  Pause,
  PictureInPicture2,
  Play,
  RotateCcw,
  RotateCw,
  Settings,
  Share2,
  SkipBack,
  SkipForward,
  Timer,
  Volume1,
  Volume2,
  VolumeX,
} from "lucide-react";
import {
  type PointerEvent as ReactPointerEvent,
  useCallback,
  useEffect,
  useId,
  useRef,
  useState,
} from "react";
import { Slider } from "@/components/ui/astryx-primitives";
import { NativeSelect } from "@/components/ui/select";
import { useT } from "@/lib/i18n";
import { SKIP_INTERVALS, type SkipInterval } from "../lib/preferences";
import {
  buildMarkerStripModel,
  buildRenditionMenuModel,
  chapterSeekTarget,
  formatAriaTime,
  formatClockTime,
  formatRemainingTime,
  resolveTrickplayTile,
} from "../lib/transport";
import { InfoOverlay } from "./InfoOverlay";
import { SubtitleStylePanel } from "./SubtitleStylePanel";
import { TransportMenu } from "./TransportMenu";
import { useLiveTracks } from "./useLiveTracks";
import type { PlayerState, SwitchResult } from "./usePlayer";
import { useSubtitleStyling } from "./useSubtitleStyling";

// The player's control overlay (CORE-1141): a gradient scrim with the title,
// route summary, slim scrubber (buffered range, marker strip, hover tooltip or
// trickplay) and one transport row. Speed, audio, subtitles, subtitle styling,
// quality, skip interval and playback details live in one settings sheet
// behind the gear; its listboxes portal into the transport layer so they stay
// inside the stage in fullscreen. Exactly one menu/overlay is open at a time
// (openKey), and the open state is reported up so the player stage can
// suppress idle auto-hide while settings or a menu is up. Every control is
// keyboard-operable and labelled (the a11y gate).
type OpenKey = "audio" | "subtitle" | "quality" | "info" | "subtitleStyle" | null;
type GestureFeedback =
  | { kind: "seek"; side: "left" | "right"; amount: number }
  | { kind: "volume"; value: number };

const SPEEDS = [0.5, 0.75, 1, 1.25, 1.5, 2];
const GESTURE_THRESHOLD = 24;

export function PlayerTransport({
  videoRef,
  hlsRef,
  state,
  mediaTitle = "",
  stageRef,
  skipInterval = 10,
  showSkipInterval = "",
  showSkipIntervalLabel = "",
  showSkipIntervalCollection = "",
  onShowSkipIntervalChange,
  onShare,
  onPreviousInQueue,
  onNextInQueue,
  switchToHls,
  onMenuOpenChange,
}: {
  videoRef: React.RefObject<HTMLVideoElement | null>;
  hlsRef: React.RefObject<Hls | null>;
  state: PlayerState;
  /** The current Media's title (from nav state) — names the info overlay. */
  mediaTitle?: string;
  /** The player stage element — subtitle styling rides a CSS var/class on it. */
  stageRef: React.RefObject<HTMLElement | null>;
  /** Seconds used by the transport's back/forward skip controls. */
  skipInterval?: number;
  /** Optional per-show override value; empty means use the Profile default. */
  showSkipInterval?: SkipInterval | "";
  /** The per-collection skip-interval row's label; the row is hidden without one. */
  showSkipIntervalLabel?: string;
  /** The collection the row belongs to, shown under the row's title. */
  showSkipIntervalCollection?: string;
  onShowSkipIntervalChange?: (value: SkipInterval | "") => void;
  onShare?: (seconds: number) => void;
  /** Play the queue's previous item; the control is hidden when absent. */
  onPreviousInQueue?: () => void;
  /** Play the queue's next item; the control is hidden when absent. */
  onNextInQueue?: () => void;
  /** Switches direct play to its HLS rendition stream at the current position. */
  switchToHls?: (renditionKey?: string) => SwitchResult;
  /** Reports whether any menu/overlay is open (drives auto-hide suppression). */
  onMenuOpenChange?: (open: boolean) => void;
}) {
  const t = useT();
  const [current, setCurrent] = useState(0);
  const [duration, setDuration] = useState(0);
  const [paused, setPaused] = useState(true);
  const [volume, setVolumeState] = useState(1);
  const [muted, setMuted] = useState(false);
  const [playbackRate, setPlaybackRate] = useState(1);
  const [fullscreen, setFullscreen] = useState(false);
  const [pipSupported, setPipSupported] = useState(false);
  const [pipActive, setPipActive] = useState(false);
  const [pipFailure, setPipFailure] = useState<"request" | "exit" | null>(null);
  const [gestureFeedback, setGestureFeedback] = useState<GestureFeedback | null>(null);
  const [openKey, setOpenKey] = useState<OpenKey>(null);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [buffered, setBuffered] = useState(0);
  const [hover, setHover] = useState<{ left: number; time: number } | null>(null);
  const narrow = useNarrowViewport();
  const settingsAnchorRef = useRef<HTMLDivElement | null>(null);
  const settingsButtonRef = useRef<HTMLButtonElement | null>(null);
  const settingsPanelRef = useRef<HTMLDivElement | null>(null);
  const layerRef = useRef<HTMLDivElement | null>(null);
  const settingsId = useId();
  const [manualRenditionID, setManualRenditionID] = useState("");
  const gestureTimerRef = useRef<number | null>(null);
  const pipStatusId = useId();
  const [trickplay, setTrickplay] = useState<{
    left: number;
    time: number;
    sheetURL: string;
    spriteW: number;
    spriteH: number;
    x: number;
    y: number;
  } | null>(null);

  // Report open/closed up so the stage can keep chrome visible while a menu is up.
  useEffect(() => {
    onMenuOpenChange?.(settingsOpen || openKey !== null);
  }, [openKey, settingsOpen, onMenuOpenChange]);

  const closeSettings = useCallback((restoreFocus: boolean) => {
    setSettingsOpen(false);
    setOpenKey(null);
    if (restoreFocus) settingsButtonRef.current?.focus();
  }, []);

  // Opening settings moves focus to its first row; a press outside the sheet
  // (and outside its portalled listboxes) closes it.
  useEffect(() => {
    if (!settingsOpen) return;
    settingsPanelRef.current?.querySelector<HTMLElement>("button, select")?.focus();
    const down = (event: PointerEvent) => {
      const target = event.target;
      if (!(target instanceof Node)) return;
      if (settingsAnchorRef.current?.contains(target) || layerRef.current?.contains(target)) {
        return;
      }
      closeSettings(false);
    };
    document.addEventListener("pointerdown", down);
    return () => document.removeEventListener("pointerdown", down);
  }, [settingsOpen, closeSettings]);
  // Unmounting with a menu open must release the auto-hide suppression.
  const onMenuOpenChangeRef = useRef(onMenuOpenChange);
  onMenuOpenChangeRef.current = onMenuOpenChange;
  useEffect(() => () => onMenuOpenChangeRef.current?.(false), []);

  const setOpen = useCallback((key: Exclude<OpenKey, null>, open: boolean) => {
    setOpenKey((prev) => (open ? key : prev === key ? null : prev));
  }, []);

  const subtitle = useSubtitleStyling({
    stageRef,
    videoRef,
    hlsRef,
    tracksVersion: state.tracksVersion,
  });

  const tracks = useLiveTracks({
    videoRef,
    hlsRef,
    stream: state.stream,
    streams: state.streams,
    switchToHls,
    manualRenditionID,
    setManualRenditionID,
    onSubtitleChanged: subtitle.applySync,
  });

  // Mirror the live <video> time into React state so the chrome re-renders.
  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    const sync = () => {
      setCurrent(Number.isFinite(video.currentTime) ? video.currentTime : 0);
      setDuration(Number.isFinite(video.duration) ? video.duration : 0);
      setPaused(video.paused);
      setVolumeState(video.volume);
      setMuted(video.muted);
      setPlaybackRate(video.playbackRate);
      setBuffered(bufferedEnd(video));
    };
    sync();
    video.addEventListener("timeupdate", sync);
    video.addEventListener("durationchange", sync);
    video.addEventListener("play", sync);
    video.addEventListener("pause", sync);
    video.addEventListener("loadedmetadata", sync);
    video.addEventListener("volumechange", sync);
    video.addEventListener("ratechange", sync);
    video.addEventListener("progress", sync);
    return () => {
      video.removeEventListener("timeupdate", sync);
      video.removeEventListener("durationchange", sync);
      video.removeEventListener("play", sync);
      video.removeEventListener("pause", sync);
      video.removeEventListener("loadedmetadata", sync);
      video.removeEventListener("volumechange", sync);
      video.removeEventListener("ratechange", sync);
      video.removeEventListener("progress", sync);
    };
  }, [videoRef]);

  useEffect(() => {
    const sync = () => {
      setFullscreen(document.fullscreenElement === stageRef.current);
    };
    sync();
    document.addEventListener("fullscreenchange", sync);
    return () => document.removeEventListener("fullscreenchange", sync);
  }, [stageRef]);

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    const supported =
      document.pictureInPictureEnabled !== false &&
      typeof video.requestPictureInPicture === "function" &&
      typeof document.exitPictureInPicture === "function";
    const enter = () => {
      setPipActive(true);
      setPipFailure(null);
    };
    const leave = () => setPipActive(false);
    setPipSupported(supported);
    setPipActive(document.pictureInPictureElement === video);
    video.addEventListener("enterpictureinpicture", enter);
    video.addEventListener("leavepictureinpicture", leave);
    return () => {
      video.removeEventListener("enterpictureinpicture", enter);
      video.removeEventListener("leavepictureinpicture", leave);
    };
  }, [videoRef]);

  const seekBy = useCallback(
    (seconds: number) => {
      const video = videoRef.current;
      if (!video) return;
      const dur = Number.isFinite(video.duration) ? video.duration : Infinity;
      video.currentTime = Math.max(0, Math.min(dur, video.currentTime + seconds));
    },
    [videoRef],
  );

  const setVolume = useCallback(
    (next: number) => {
      const video = videoRef.current;
      if (!video) return;
      const clamped = Math.max(0, Math.min(1, next));
      video.volume = clamped;
      if (clamped > 0) video.muted = false;
      setVolumeState(clamped);
      setMuted(video.muted);
    },
    [videoRef],
  );

  const showGestureFeedback = useCallback((feedback: GestureFeedback) => {
    setGestureFeedback((previous) => {
      if (
        feedback.kind === "seek" &&
        previous?.kind === "seek" &&
        previous.side === feedback.side
      ) {
        return { ...feedback, amount: previous.amount + feedback.amount };
      }
      return feedback;
    });
    if (gestureTimerRef.current) window.clearTimeout(gestureTimerRef.current);
    gestureTimerRef.current = window.setTimeout(() => setGestureFeedback(null), 700);
  }, []);

  useEffect(() => {
    const stage = stageRef.current;
    if (!stage) return;
    let pointer:
      | {
          id: number;
          x: number;
          y: number;
          right: boolean;
          volume: number;
          moved: boolean;
        }
      | undefined;
    let lastTap: { time: number; side: "left" | "right" } | undefined;
    const excluded = (target: EventTarget | null) =>
      target instanceof Element && Boolean(target.closest(".bf-transport, .bf-scrubber"));
    const down = (event: PointerEvent) => {
      if (event.pointerType !== "touch" || excluded(event.target)) return;
      const rect = stage.getBoundingClientRect();
      pointer = {
        id: event.pointerId,
        x: event.clientX,
        y: event.clientY,
        right: event.clientX >= rect.left + rect.width / 2,
        volume: videoRef.current?.volume ?? 1,
        moved: false,
      };
    };
    const move = (event: PointerEvent) => {
      if (!pointer || event.pointerId !== pointer.id || !pointer.right) return;
      const dx = event.clientX - pointer.x;
      const dy = event.clientY - pointer.y;
      if (Math.abs(dy) < GESTURE_THRESHOLD || Math.abs(dy) <= Math.abs(dx)) return;
      pointer.moved = true;
      event.preventDefault();
      const rect = stage.getBoundingClientRect();
      const next = pointer.volume - dy / Math.max(rect.height, 1);
      setVolume(next);
      showGestureFeedback({ kind: "volume", value: Math.max(0, Math.min(1, next)) });
    };
    const up = (event: PointerEvent) => {
      if (!pointer || event.pointerId !== pointer.id) return;
      const currentPointer = pointer;
      pointer = undefined;
      if (currentPointer.moved) return;
      const distance = Math.hypot(
        event.clientX - currentPointer.x,
        event.clientY - currentPointer.y,
      );
      if (distance >= GESTURE_THRESHOLD) return;
      const side = currentPointer.right ? "right" : "left";
      const now = Date.now();
      if (lastTap && lastTap.side === side && now - lastTap.time <= 350) {
        const amount = side === "left" ? -skipInterval : skipInterval;
        seekBy(amount);
        showGestureFeedback({ kind: "seek", side, amount: Math.abs(amount) });
      }
      lastTap = { time: now, side };
    };
    const cancel = () => {
      pointer = undefined;
    };
    stage.addEventListener("pointerdown", down);
    stage.addEventListener("pointermove", move, { passive: false });
    stage.addEventListener("pointerup", up);
    stage.addEventListener("pointercancel", cancel);
    return () => {
      stage.removeEventListener("pointerdown", down);
      stage.removeEventListener("pointermove", move);
      stage.removeEventListener("pointerup", up);
      stage.removeEventListener("pointercancel", cancel);
      if (gestureTimerRef.current) window.clearTimeout(gestureTimerRef.current);
    };
  }, [seekBy, setVolume, showGestureFeedback, skipInterval, stageRef, videoRef]);

  const togglePlay = useCallback(() => {
    const video = videoRef.current;
    if (!video) return;
    if (video.paused) {
      video.play().catch(() => {});
    } else {
      video.pause();
    }
  }, [videoRef]);

  const toggleFullscreen = useCallback(() => {
    const stage = stageRef.current;
    if (!stage) return;
    if (document.fullscreenElement === stage) {
      document.exitFullscreen?.().catch(() => {});
    } else {
      stage.requestFullscreen?.().catch(() => {});
    }
  }, [stageRef]);

  const toggleMute = useCallback(() => {
    const video = videoRef.current;
    if (!video) return;
    video.muted = !video.muted;
    setMuted(video.muted);
  }, [videoRef]);

  useEffect(() => {
    const interactive = (target: EventTarget | null) => {
      const element = target instanceof Element ? target : document.activeElement;
      if (!(element instanceof HTMLElement)) return false;
      if (element.isContentEditable) return true;
      return Boolean(
        element.closest(
          'button, a, input, select, textarea, menu, dialog, [role="menu"], [role="dialog"], [contenteditable]:not([contenteditable="false"])',
        ),
      );
    };
    const keydown = (event: KeyboardEvent) => {
      if (
        event.altKey ||
        event.ctrlKey ||
        event.metaKey ||
        event.shiftKey ||
        interactive(event.target)
      ) {
        return;
      }
      const key = event.key.toLowerCase();
      const actions: Record<string, () => void> = {
        " ": togglePlay,
        k: togglePlay,
        f: toggleFullscreen,
        m: toggleMute,
        arrowleft: () => seekBy(-skipInterval),
        arrowright: () => seekBy(skipInterval),
        arrowup: () => setVolume((videoRef.current?.volume ?? 0) + 0.05),
        arrowdown: () => setVolume((videoRef.current?.volume ?? 0) - 0.05),
      };
      const action = actions[key];
      if (!action) return;
      event.preventDefault();
      action();
    };
    window.addEventListener("keydown", keydown);
    return () => window.removeEventListener("keydown", keydown);
  }, [seekBy, setVolume, skipInterval, toggleFullscreen, toggleMute, togglePlay, videoRef]);

  async function togglePictureInPicture() {
    const video = videoRef.current;
    if (!video || !pipSupported) return;
    const exiting = pipActive || document.pictureInPictureElement === video;
    setPipFailure(null);
    try {
      if (exiting) {
        await document.exitPictureInPicture();
        setPipActive(document.pictureInPictureElement === video);
      } else {
        await video.requestPictureInPicture();
        setPipActive(document.pictureInPictureElement === video);
      }
    } catch {
      setPipFailure(exiting ? "exit" : "request");
      setPipActive(document.pictureInPictureElement === video);
    }
  }

  function setPlaybackPosition(value: number) {
    const video = videoRef.current;
    if (video && Number.isFinite(value)) {
      video.currentTime = value;
      setCurrent(value);
    }
  }

  function onScrubberKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
    e.preventDefault();
    const amount = e.shiftKey ? 30 : 5;
    seekBy(e.key === "ArrowLeft" ? -amount : amount);
  }

  // Trickplay preview on scrubber hover, ported from updateTrickplayPreview.
  function onScrubberPointerMove(e: ReactPointerEvent<HTMLElement>) {
    const rect = e.currentTarget.getBoundingClientRect();
    const ratio = Math.max(0, Math.min(1, (e.clientX - rect.left) / Math.max(rect.width, 1)));
    const time = ratio * duration;
    setHover(duration > 0 ? { left: ratio * 100, time } : null);
    if (!state.trickplay) {
      setTrickplay(null);
      return;
    }
    const tile = resolveTrickplayTile(state.trickplay, time);
    if (!tile) {
      setTrickplay(null);
      return;
    }
    let sheetURL = tile.sheet;
    try {
      sheetURL = new URL(tile.sheet, state.trickplayBaseURL).href;
    } catch {
      // leave the server-provided URL as-is
    }
    setTrickplay({
      left: ratio * 100,
      time,
      sheetURL,
      spriteW: tile.width * 10,
      spriteH: tile.height * 10,
      x: tile.x,
      y: tile.y,
    });
  }

  const markerModel = buildMarkerStripModel(state.markers, {
    durationSeconds: duration,
    currentTimeSeconds: current,
  });
  const previousChapter = chapterSeekTarget(state.markers, current, -1);
  const nextChapter = chapterSeekTarget(state.markers, current, 1);
  const skipBackLabel = t("seekBackSeconds", { seconds: skipInterval });
  const skipForwardLabel = t("seekForwardSeconds", { seconds: skipInterval });

  function seekTo(seconds: number | null) {
    const video = videoRef.current;
    if (!video || seconds === null) return;
    video.currentTime = Math.max(0, Math.min(duration || Infinity, seconds));
  }

  const volumeLevel = muted ? 0 : volume;
  const VolumeIcon = volumeLevel === 0 ? VolumeX : volumeLevel < 0.5 ? Volume1 : Volume2;
  const menuSide = narrow ? "top" : "left";
  const progressPercent = duration > 0 ? (current / duration) * 100 : 0;
  const bufferedPercent = duration > 0 ? Math.min(100, (buffered / duration) * 100) : 0;

  return (
    <div className="bf-transport">
      <div aria-hidden="true" className="bf-transport-scrim" />
      {gestureFeedback && (
        <div
          className={`bf-gesture-feedback is-${gestureFeedback.kind} ${
            gestureFeedback.kind === "seek" ? `is-${gestureFeedback.side}` : ""
          }`}
          role="status"
          aria-live="polite"
        >
          {gestureFeedback.kind === "seek"
            ? t(gestureFeedback.side === "left" ? "gestureSeekBack" : "gestureSeekForward", {
                seconds: gestureFeedback.amount,
              })
            : t("gestureVolume", { volume: Math.round(gestureFeedback.value * 100) })}
        </div>
      )}

      {markerModel.skip && (
        <button
          type="button"
          onClick={() => {
            const video = videoRef.current;
            if (video && markerModel.skip) {
              video.currentTime = markerModel.skip.endSeconds;
            }
          }}
          className="bf-control is-skip-segment"
        >
          {t("skipSegmentPrefix")}
          {markerModel.skip.title || t("segment")}
          <ChevronRight aria-hidden="true" />
        </button>
      )}
      <div className="bf-transport-bottom">
        <div className="bf-player-meta">
          <h2>{mediaTitle || t("playbackDetails")}</h2>
          {state.stream && (
            <div className={"bf-route-summary " + routeStateClass(state.decision)}>
              {routeSummary(t, state, manualRenditionID)}
            </div>
          )}
        </div>

        <div className="bf-scrub-row">
          <span className="bf-time is-start">{formatClockTime(current)}</span>
          <div className="bf-scrub-wrap">
            <Slider.Root
              min={0}
              max={Math.max(duration, 0.1)}
              step={0.1}
              value={current}
              onValueChange={setPlaybackPosition}
              className="bf-slider-root bf-scrubber"
            >
              <input
                aria-hidden="true"
                tabIndex={-1}
                type="range"
                min={0}
                max={duration || 0}
                step={0.1}
                value={current}
                onChange={() => undefined}
                className="bf-scrubber-visual"
              />
              <Slider.Control
                className="bf-slider-control"
                onPointerMove={onScrubberPointerMove}
                onPointerLeave={() => {
                  setTrickplay(null);
                  setHover(null);
                }}
              >
                <Slider.Track className="bf-slider-track">
                  <span
                    aria-hidden="true"
                    className="bf-slider-buffered"
                    style={{ width: `${bufferedPercent}%` }}
                  />
                  {hover && (
                    <span
                      aria-hidden="true"
                      className="bf-slider-hover"
                      style={{
                        left: `${Math.min(progressPercent, hover.left)}%`,
                        width: `${Math.abs(hover.left - progressPercent)}%`,
                      }}
                    />
                  )}
                  <Slider.Indicator className="bf-slider-indicator" />
                  {/* Marker strip overlaid on the scrubber track (aria-hidden). */}
                  <span aria-hidden="true" className="bf-marker-strip">
                    {markerModel.ranges.map((range, i) => (
                      <span
                        key={`range-${i}`}
                        title={range.title}
                        className="bf-marker-range"
                        style={{
                          left: `${range.leftPercent}%`,
                          width: `${range.widthPercent}%`,
                        }}
                      />
                    ))}
                    {markerModel.ticks.map((tick, i) => (
                      <span
                        key={`tick-${i}`}
                        title={tick.title}
                        className="bf-marker-tick"
                        style={{ left: `${tick.leftPercent}%` }}
                      />
                    ))}
                  </span>
                </Slider.Track>
                <Slider.Thumb
                  className="bf-slider-thumb"
                  onKeyDown={onScrubberKeyDown}
                  getAriaLabel={() => t("playbackPosition")}
                  aria-valuetext={formatAriaTime(current, duration)}
                />
              </Slider.Control>
            </Slider.Root>
            {trickplay ? (
              <div className="bf-trickplay" style={{ left: `${trickplay.left}%` }}>
                <div
                  className="bf-trickplay-frame"
                  style={{
                    backgroundImage: `url("${trickplay.sheetURL}")`,
                    backgroundSize: `${trickplay.spriteW}px ${trickplay.spriteH}px`,
                    backgroundPosition: `-${trickplay.x}px -${trickplay.y}px`,
                  }}
                />
                <span className="bf-trickplay-time">{formatClockTime(trickplay.time)}</span>
              </div>
            ) : (
              hover && (
                <span
                  aria-hidden="true"
                  className="bf-scrub-tooltip"
                  style={{ left: `${hover.left}%` }}
                >
                  {formatClockTime(hover.time)}
                </span>
              )
            )}
          </div>
          <span className="bf-time is-end">{formatRemainingTime(current, duration)}</span>
        </div>

        <div className="bf-controls bf-transport-row">
          <div className="bf-primary-controls">
            {onPreviousInQueue && (
              <button
                type="button"
                onClick={onPreviousInQueue}
                aria-label={t("previousInQueue")}
                className="bf-control is-icon"
              >
                <SkipBack aria-hidden="true" />
                <span className="sr-only">{t("previousInQueue")}</span>
              </button>
            )}
            <button
              type="button"
              onClick={() => seekBy(-skipInterval)}
              aria-label={skipBackLabel}
              className="bf-control is-icon is-seek"
            >
              <RotateCcw aria-hidden="true" />
              <span aria-hidden="true" className="bf-seek-amount">
                {skipInterval}
              </span>
              <span className="sr-only">{skipBackLabel}</span>
            </button>
            <button
              type="button"
              onClick={togglePlay}
              aria-label={paused ? t("play") : t("pause")}
              className="bf-control is-icon is-primary"
            >
              {paused ? <Play aria-hidden="true" /> : <Pause aria-hidden="true" />}
              <span className="sr-only">{paused ? t("play") : t("pause")}</span>
            </button>
            <button
              type="button"
              onClick={() => seekBy(skipInterval)}
              aria-label={skipForwardLabel}
              className="bf-control is-icon is-seek"
            >
              <RotateCw aria-hidden="true" />
              <span aria-hidden="true" className="bf-seek-amount">
                {skipInterval}
              </span>
              <span className="sr-only">{skipForwardLabel}</span>
            </button>
            {onNextInQueue && (
              <button
                type="button"
                onClick={onNextInQueue}
                aria-label={t("nextInQueue")}
                className="bf-control is-icon"
              >
                <SkipForward aria-hidden="true" />
                <span className="sr-only">{t("nextInQueue")}</span>
              </button>
            )}
          </div>

          <button
            type="button"
            onClick={() => seekTo(previousChapter)}
            disabled={previousChapter === null}
            aria-label={t("previousChapter")}
            className="bf-control is-icon is-chapter"
          >
            <ChevronsLeft aria-hidden="true" />
            <span className="sr-only">{t("previousChapter")}</span>
          </button>
          <button
            type="button"
            onClick={() => seekTo(nextChapter)}
            disabled={nextChapter === null}
            aria-label={t("nextChapter")}
            className="bf-control is-icon is-chapter"
          >
            <ChevronsRight aria-hidden="true" />
            <span className="sr-only">{t("nextChapter")}</span>
          </button>

          <div className="bf-volume">
            <button
              type="button"
              onClick={toggleMute}
              aria-label={muted ? t("unmute") : t("mute")}
              className="bf-control is-icon"
            >
              <VolumeIcon aria-hidden="true" />
              <span className="sr-only">{muted ? t("unmute") : t("mute")}</span>
            </button>
            <label className="bf-volume-control">
              <span className="sr-only">{t("volume")}</span>
              <Slider.Root
                min={0}
                max={1}
                step={0.05}
                value={volumeLevel}
                onValueChange={setVolume}
                className="bf-slider-root"
              >
                <input
                  aria-hidden="true"
                  tabIndex={-1}
                  type="range"
                  min={0}
                  max={1}
                  step={0.05}
                  value={volumeLevel}
                  onChange={() => undefined}
                  className="bf-volume-slider"
                />
                <Slider.Control className="bf-slider-control">
                  <Slider.Track className="bf-slider-track">
                    <Slider.Indicator className="bf-slider-indicator" />
                  </Slider.Track>
                  <Slider.Thumb
                    className="bf-slider-thumb"
                    getAriaLabel={() => t("volume")}
                    aria-valuetext={t("volumePercent", {
                      volume: Math.round(volumeLevel * 100),
                    })}
                  />
                </Slider.Control>
              </Slider.Root>
            </label>
          </div>

          <span aria-hidden="true" className="bf-transport-spacer" />

          {onShare && (
            <button
              type="button"
              onClick={() => onShare(Math.floor(current))}
              aria-label={t("shareCurrentTime")}
              className="bf-control is-icon"
            >
              <Share2 aria-hidden="true" />
              <span className="sr-only">{t("share")}</span>
            </button>
          )}

          {/* One settings sheet holds the secondary controls: speed, tracks,
              subtitle styling, the per-show skip interval and details. */}
          <div ref={settingsAnchorRef} className="bf-settings-anchor">
            <button
              ref={settingsButtonRef}
              type="button"
              onClick={() => (settingsOpen ? closeSettings(false) : setSettingsOpen(true))}
              aria-label={t("playbackSettings")}
              aria-haspopup="dialog"
              aria-expanded={settingsOpen}
              aria-controls={settingsOpen ? settingsId : undefined}
              className={`bf-control is-icon bf-settings-button${settingsOpen ? " is-open" : ""}`}
            >
              <Settings aria-hidden="true" />
              <span className="sr-only">{t("playbackSettings")}</span>
            </button>
            {settingsOpen && (
              <div
                ref={settingsPanelRef}
                id={settingsId}
                role="dialog"
                aria-label={t("playbackSettings")}
                className="bf-settings"
                onKeyDown={(event) => {
                  if (event.key !== "Escape" || openKey !== null) return;
                  event.preventDefault();
                  event.stopPropagation();
                  closeSettings(true);
                }}
              >
                <p aria-hidden="true" className="bf-settings-title">
                  {t("playbackSettings")}
                </p>
                <label className="bf-settings-row">
                  <span aria-hidden="true" className="bf-settings-tile">
                    <Gauge />
                  </span>
                  <span className="bf-settings-label">{t("playbackSpeed")}</span>
                  <NativeSelect
                    aria-label={t("playbackSpeed")}
                    className="bf-settings-select"
                    value={String(playbackRate)}
                    onValueChange={(value) => {
                      const next = Number(value);
                      if (videoRef.current) videoRef.current.playbackRate = next;
                      setPlaybackRate(next);
                    }}
                  >
                    {SPEEDS.map((speed) => (
                      <option key={speed} value={speed}>
                        {speed}×
                      </option>
                    ))}
                  </NativeSelect>
                </label>
                <TransportMenu
                  label={t("audio")}
                  ariaLabel={t("audio")}
                  icon={<AudioLines />}
                  container={layerRef}
                  side={menuSide}
                  options={tracks.audioOptions}
                  open={openKey === "audio"}
                  onOpenChange={(open) => setOpen("audio", open)}
                  onSelect={tracks.selectAudio}
                />
                <TransportMenu
                  label={t("subtitles")}
                  ariaLabel={t("subtitles")}
                  icon={<Captions />}
                  container={layerRef}
                  side={menuSide}
                  options={tracks.subtitleOptions}
                  open={openKey === "subtitle"}
                  onOpenChange={(open) => setOpen("subtitle", open)}
                  onSelect={tracks.selectSubtitle}
                />
                {subtitle.stylingAvailable && (
                  <SubtitleStylePanel
                    open={openKey === "subtitleStyle"}
                    onOpenChange={(open) => setOpen("subtitleStyle", open)}
                    sizeScale={subtitle.sizeScale}
                    setSizeScale={subtitle.setSizeScale}
                    background={subtitle.background}
                    setBackground={subtitle.setBackground}
                    syncOffset={subtitle.syncOffset}
                    nudgeSync={subtitle.nudgeSync}
                    reset={subtitle.reset}
                    syncSupported={subtitle.syncSupported}
                  />
                )}
                <TransportMenu
                  label={t("quality")}
                  ariaLabel={t("quality")}
                  icon={<MonitorPlay />}
                  container={layerRef}
                  side={menuSide}
                  options={tracks.qualityOptions}
                  open={openKey === "quality"}
                  onOpenChange={(open) => setOpen("quality", open)}
                  onSelect={tracks.selectQuality}
                />
                {onShowSkipIntervalChange && showSkipIntervalLabel && (
                  <label className="bf-settings-row">
                    <span aria-hidden="true" className="bf-settings-tile">
                      <Timer />
                    </span>
                    <span className="bf-settings-label is-stacked">
                      <span>{t("settingsSkipIntervalLabel")}</span>
                      {showSkipIntervalCollection && (
                        <span className="bf-settings-sublabel">{showSkipIntervalCollection}</span>
                      )}
                    </span>
                    <NativeSelect
                      aria-label={showSkipIntervalLabel}
                      className="bf-settings-select"
                      value={showSkipInterval || "__profile_default__"}
                      onValueChange={(value) =>
                        onShowSkipIntervalChange(
                          value === "__profile_default__" ? "" : (value as SkipInterval),
                        )
                      }
                    >
                      <option value="__profile_default__">{t("settingsSkipIntervalLabel")}</option>
                      {SKIP_INTERVALS.map((seconds) => (
                        <option key={seconds} value={seconds}>
                          {t("secondsShort", { seconds })}
                        </option>
                      ))}
                    </NativeSelect>
                  </label>
                )}
                <InfoOverlay
                  mediaTitle={mediaTitle}
                  info={state.info}
                  stream={state.stream}
                  manualRenditionID={manualRenditionID}
                  hlsActive={state.hlsActive}
                  open={openKey === "info"}
                  onOpenChange={(open) => setOpen("info", open)}
                />
              </div>
            )}
          </div>

          <button
            type="button"
            onClick={() => void togglePictureInPicture()}
            disabled={!pipSupported}
            aria-label={pipActive ? t("exitPictureInPicture") : t("pictureInPicture")}
            aria-describedby={pipStatusId}
            className="bf-control is-icon"
            title={!pipSupported ? t("pictureInPictureUnavailable") : undefined}
          >
            <PictureInPicture2 aria-hidden="true" />
            <span className="sr-only">
              {pipActive ? t("exitPictureInPicture") : t("pictureInPicture")}
            </span>
          </button>
          <span
            id={pipStatusId}
            className="bf-player-control-status"
            role={pipFailure ? "alert" : "status"}
          >
            {!pipSupported
              ? t("pictureInPictureUnavailable")
              : pipFailure
                ? t(pipFailure === "exit" ? "pictureInPictureExitFailed" : "pictureInPictureFailed")
                : ""}
          </span>
          <button
            type="button"
            onClick={toggleFullscreen}
            aria-label={fullscreen ? t("exitFullscreen") : t("enterFullscreen")}
            className="bf-control is-icon"
          >
            {fullscreen ? <Minimize aria-hidden="true" /> : <Maximize aria-hidden="true" />}
            <span className="sr-only">
              {fullscreen ? t("exitFullscreen") : t("enterFullscreen")}
            </span>
          </button>
        </div>
      </div>
      {/* Listboxes portal here: inside the stage (so stage fullscreen shows
          them) but outside the glass sheet (a backdrop-filter ancestor would
          break their fixed positioning). */}
      <div ref={layerRef} className="bf-transport-layer" />
    </div>
  );
}

// The furthest buffered point at or after the playhead, for the scrubber.
function bufferedEnd(video: HTMLVideoElement): number {
  const ranges = video.buffered;
  if (!ranges) return 0;
  for (let i = 0; i < ranges.length; i += 1) {
    if (ranges.start(i) <= video.currentTime + 0.5 && ranges.end(i) >= video.currentTime) {
      return ranges.end(i);
    }
  }
  return 0;
}

const NARROW_QUERY = "(max-width: 640px)";

// Phone width turns the settings sheet into a bottom sheet with listboxes above
// their rows rather than beside them.
function useNarrowViewport(): boolean {
  const [narrow, setNarrow] = useState(
    () => typeof window !== "undefined" && window.matchMedia?.(NARROW_QUERY).matches === true,
  );
  useEffect(() => {
    const query = window.matchMedia?.(NARROW_QUERY);
    if (!query) return;
    const sync = () => setNarrow(query.matches);
    sync();
    query.addEventListener?.("change", sync);
    return () => query.removeEventListener?.("change", sync);
  }, []);
  return narrow;
}

// The route summary borrows the player's honest state coloring: green for
// direct-play, cyan for remote/relay (design-preview .dark-badge.direct). The
// transcode/direct-stream states stay neutral — the label carries the meaning.
function routeStateClass(decision: string): string {
  if (decision === "direct-play" || decision === "direct") return "is-direct";
  if (decision === "remote" || decision === "relay") return "is-remote";
  return "";
}

function routeLabel(t: ReturnType<typeof useT>, decision: string): string {
  if (decision === "transcode") return t("serverTranscode");
  if (decision === "direct-stream") return t("directStream");
  return t("directPlay");
}

// The route summary line, ported from updateRouteSummary (app.js): the route
// label, the active quality (Auto / a pinned rendition / Direct play), and a
// "manual pin active" note when a rendition is pinned.
function routeSummary(
  t: ReturnType<typeof useT>,
  state: PlayerState,
  manualRenditionID: string,
): string {
  const model = buildRenditionMenuModel({
    stream: state.stream,
    manualRenditionID,
    labels: { auto: t("auto"), directPlay: t("directPlay") },
  });
  const selected = model.options.find((option) => option.selected) || model.options[0];
  const route = routeLabel(t, state.decision);
  const parts = [route];
  if (selected && selected.label !== route) {
    parts.push(selected.label);
  }
  if (model.manualPinActive) {
    parts.push(t("manualPinActive"));
  }
  return parts.join(" · ");
}
