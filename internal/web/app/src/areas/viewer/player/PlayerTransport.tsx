import type Hls from "hls.js";
import {
  AudioLines,
  Captions,
  ChevronRight,
  ChevronsLeft,
  ChevronsRight,
  Gauge,
  Layers,
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
import type { MetadataAttribution } from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { songSecondsPerStreamSecond } from "../lib/playback";
import { SKIP_INTERVALS, type SkipInterval } from "../lib/preferences";
import {
  SUBTITLE_APPEARANCE_DEFAULTS,
  type SubtitleAppearance,
  type SubtitleAppearanceField,
} from "../lib/subtitleAppearance";
import {
  buildChapterList,
  buildMarkerStripModel,
  chapterSeekTarget,
  formatAriaTime,
  formatClockTime,
  formatRemainingTime,
  resolveTrickplaySheetURL,
  resolveTrickplayTile,
} from "../lib/transport";
import { versionLabel } from "../lib/versionChoice";
import { useRemotePlayerTarget } from "../remote/remoteControl";
import { AudioSyncPanel } from "./AudioSyncPanel";
import { ChapterList } from "./ChapterList";
import { CastControl } from "./cast/CastControl";
import { CAST_RECEIVER_PLAYBACK_READY } from "./cast/castMedia";
import { InfoOverlay } from "./InfoOverlay";
import { listenerMuted, setListenerMuted } from "./listenerMute";
import { ShortcutHelpOverlay } from "./ShortcutHelpOverlay";
import { SubtitleStylePanel } from "./SubtitleStylePanel";
import { type MenuRow, TransportMenu } from "./TransportMenu";
import { useAudioDelay } from "./useAudioDelay";
import { useLiveTracks } from "./useLiveTracks";
import type { PlayerState, SwitchResult } from "./usePlayer";
import { usePlayerShortcuts } from "./usePlayerShortcuts";
import { useSubtitleAppearance } from "./useSubtitleAppearance";
import { useSubtitleStyling } from "./useSubtitleStyling";
import { markViewerIntent } from "./viewerIntent";

// The player's control overlay (CORE-1141): a gradient scrim with the title,
// route summary, slim scrubber (buffered range, marker strip, hover tooltip or
// trickplay) and one transport row. Speed, audio, subtitles, subtitle styling,
// quality, skip interval and playback details live in one settings sheet
// behind the gear; its listboxes portal into the transport layer so they stay
// inside the stage in fullscreen. Exactly one menu/overlay is open at a time
// (openKey), and the open state is reported up so the player stage can
// suppress idle auto-hide while settings or a menu is up. Every control is
// keyboard-operable and labelled (the a11y gate).
type OpenKey =
  | "audio"
  | "subtitle"
  | "quality"
  | "version"
  | "info"
  | "subtitleStyle"
  | "audioSync"
  | "shortcuts"
  | null;
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
  mediaID = "",
  profileID = "",
  metadataAttribution,
  stageRef,
  skipInterval = 10,
  showSkipInterval = "",
  showSkipIntervalLabel = "",
  showSkipIntervalCollection = "",
  onShowSkipIntervalChange,
  subtitleAppearance = SUBTITLE_APPEARANCE_DEFAULTS,
  onSubtitleAppearanceChange,
  onSubtitleAppearanceReset,
  onShare,
  onPreviousInQueue,
  onNextInQueue,
  onStop,
  onRemoteStop,
  onRemoteRestart,
  switchToHls,
  onSelectVersion,
  onRenegotiate,
  onMenuOpenChange,
}: {
  videoRef: React.RefObject<HTMLVideoElement | null>;
  hlsRef: React.RefObject<Hls | null>;
  state: PlayerState;
  /** The current Media's title (from nav state) — names the info overlay. */
  mediaTitle?: string;
  /** The current Media's id, for the info overlay's item actions. */
  mediaID?: string;
  /** The active Profile; subtitle offset and audio delay persist per Profile and Media. */
  profileID?: string;
  /** The current Media's plugin-declared metadata credit, for the info overlay. */
  metadataAttribution?: MetadataAttribution;
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
  /** The Profile's subtitle appearance (subtitle.* client prefs). */
  subtitleAppearance?: SubtitleAppearance;
  /** Stores one appearance field for the Profile. */
  onSubtitleAppearanceChange?: (field: SubtitleAppearanceField, value: string) => void;
  /** Clears every appearance field for the Profile. */
  onSubtitleAppearanceReset?: () => void;
  onShare?: (seconds: number) => void;
  /** Play the queue's previous item; the control is hidden when absent. */
  onPreviousInQueue?: () => void;
  /** Play the queue's next item; the control is hidden when absent. */
  onNextInQueue?: () => void;
  /** Leaves the player when another device sends stop; without it stop only pauses. */
  onStop?: () => void;
  /** Stop requested by another device; falls back to onStop. */
  onRemoteStop?: () => void;
  /** Another device's play-media for this Media: set up afresh at that position. */
  onRemoteRestart?: (atSeconds?: number) => void;
  /** Switches direct play to its HLS rendition stream at the current position. */
  switchToHls?: (renditionKey?: string) => SwitchResult;
  /** Plays another Version of the Media; the Version row is hidden when absent. */
  onSelectVersion?: (versionID: string) => void;
  /** Re-requests playback info at the current position, keeping the play state
   *  it is told; a quality pick then becomes this browser's remembered ceiling
   *  (CORE-1971). */
  onRenegotiate?: (options: { paused: boolean }) => void;
  /** Reports whether any menu/overlay is open (drives auto-hide suppression). */
  onMenuOpenChange?: (open: boolean) => void;
}) {
  const t = useT();
  // The transport's times are song seconds; an adjusted stream runs at song
  // time divided by the applied tempo (CORE-1659).
  const songScale = songSecondsPerStreamSecond(state.info);
  const [current, setCurrent] = useState(0);
  // The position a pointer drag on the scrubber previews; null when not dragging.
  const [scrubValue, setScrubValue] = useState<number | null>(null);
  const pendingScrubRef = useRef<number | null>(null);
  const commitScrubRef = useRef<() => void>(() => undefined);
  useEffect(() => {
    const commit = () => commitScrubRef.current();
    // Only touchcancel reverts: Chrome may fire pointercancel mid-drag while
    // the slider's touch events carry on to a normal touchend.
    const cancel = () => {
      pendingScrubRef.current = null;
      setScrubValue(null);
    };
    document.addEventListener("pointerup", commit);
    document.addEventListener("touchend", commit);
    document.addEventListener("touchcancel", cancel);
    return () => {
      document.removeEventListener("pointerup", commit);
      document.removeEventListener("touchend", commit);
      document.removeEventListener("touchcancel", cancel);
    };
  }, []);
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
  const [chaptersOpen, setChaptersOpen] = useState(false);
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
    onMenuOpenChange?.(settingsOpen || chaptersOpen || openKey !== null);
  }, [openKey, settingsOpen, chaptersOpen, onMenuOpenChange]);

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
    videoRef,
    hlsRef,
    tracksVersion: state.tracksVersion,
    profileID,
    mediaID,
  });
  useSubtitleAppearance({
    stageRef,
    videoRef,
    tracksVersion: state.tracksVersion,
    appearance: subtitleAppearance,
  });
  const resetSubtitles = useCallback(() => {
    onSubtitleAppearanceReset?.();
    subtitle.reset();
  }, [onSubtitleAppearanceReset, subtitle.reset]);

  const audioSync = useAudioDelay({
    videoRef,
    profileID,
    mediaID,
    stream: state.stream,
  });
  // Audio-only items have nothing to line the sound up with.
  const audioSyncAvailable = state.info?.mediaClass !== "audio";

  const renegotiate = useCallback(
    () => onRenegotiate?.({ paused: videoRef.current?.paused === true }),
    [onRenegotiate, videoRef],
  );
  const tracks = useLiveTracks({
    videoRef,
    hlsRef,
    stream: state.stream,
    streams: state.streams,
    info: state.info,
    trackControl: state.trackControl,
    switchToHls,
    manualRenditionID,
    setManualRenditionID,
    onSubtitleChanged: subtitle.applySync,
    renegotiate: onRenegotiate ? renegotiate : undefined,
  });

  // Commands from the Profile's other devices drive this player (CORE-1678).
  useRemotePlayerTarget({
    video: () => videoRef.current,
    mediaID,
    selectAudio: tracks.selectAudio,
    selectSubtitle: tracks.selectSubtitle,
    next: onNextInQueue,
    previous: onPreviousInQueue,
    stop: onRemoteStop ?? onStop,
    restart: onRemoteRestart,
  });
  // The Media's Versions (CORE-1676), listed by playbackInfo only when there
  // are two or more; the default Version takes the client's own name.
  const versionOptions: MenuRow[] = (state.info?.versions ?? []).map((version) => ({
    id: version.id,
    label: versionLabel(version, t("versionDefault")),
    selected: version.selected,
    disabled: false,
    reason: "",
  }));

  // Mirror the live <video> time into React state so the chrome re-renders.
  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    const sync = () => {
      setCurrent(Number.isFinite(video.currentTime) ? video.currentTime * songScale : 0);
      setDuration(Number.isFinite(video.duration) ? video.duration * songScale : 0);
      setPaused(video.paused);
      setVolumeState(video.volume);
      setMuted(listenerMuted(video));
      setPlaybackRate(video.playbackRate);
      setBuffered(bufferedEnd(video) * songScale);
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
  }, [videoRef, songScale]);

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
      video.currentTime = Math.max(0, Math.min(dur, video.currentTime + seconds / songScale));
    },
    [videoRef, songScale],
  );

  const setVolume = useCallback(
    (next: number) => {
      const video = videoRef.current;
      if (!video) return;
      const clamped = Math.max(0, Math.min(1, next));
      video.volume = clamped;
      if (clamped > 0) setListenerMuted(video, false);
      setVolumeState(clamped);
      setMuted(listenerMuted(video));
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
    // Only touchcancel reverts: Chrome may fire pointercancel mid-drag while
    // the slider's touch events carry on to a normal touchend.
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
    setListenerMuted(video, !listenerMuted(video));
    setMuted(listenerMuted(video));
  }, [videoRef]);

  usePlayerShortcuts({
    videoRef,
    skipInterval,
    speeds: SPEEDS,
    markers: state.markers,
    togglePlay,
    toggleFullscreen,
    toggleMute,
    seekBy,
    setVolume,
    subtitleOptions: tracks.subtitleOptions,
    audioOptions: tracks.audioOptions,
    selectSubtitle: tracks.selectSubtitle,
    selectAudio: tracks.selectAudio,
    onPreviousInQueue,
    onNextInQueue,
    onHelp: () => {
      setSettingsOpen(false);
      setOpenKey("shortcuts");
    },
  });

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
      // A scrub can commit long after the press that began it, so the seek
      // itself marks the viewer's intent for a Watch Party.
      markViewerIntent();
      video.currentTime = value / songScale;
      setCurrent(value);
    }
  }

  function onScrubberKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
    e.preventDefault();
    const amount = e.shiftKey ? 30 : 5;
    seekBy(e.key === "ArrowLeft" ? -amount : amount);
  }

  // A drag previews its position (thumb, time and trickplay) and seeks once
  // on release (CORE-1360); keyboard steps seek straight away. The release is
  // read from the document as well as from onValueCommitted: on a touch tap
  // the slider sees touchstart and pointerdown at the same value and skips its
  // own commit, which would leave the tap previewed but never seeked.
  function onScrubberValueChange(value: number, details: Slider.Root.ChangeEventDetails) {
    if (details.reason === "keyboard") {
      setPlaybackPosition(value);
      return;
    }
    pendingScrubRef.current = value;
    setScrubValue(value);
    if (duration > 0) previewAt(value / duration);
  }

  function commitScrub() {
    const value = pendingScrubRef.current;
    if (value === null) return;
    pendingScrubRef.current = null;
    setScrubValue(null);
    setPlaybackPosition(value);
  }
  commitScrubRef.current = commitScrub;

  // Trickplay preview on scrubber hover, ported from updateTrickplayPreview.
  function onScrubberPointerMove(e: ReactPointerEvent<HTMLElement>) {
    const rect = e.currentTarget.getBoundingClientRect();
    previewAt(Math.max(0, Math.min(1, (e.clientX - rect.left) / Math.max(rect.width, 1))));
  }

  function previewAt(ratio: number) {
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
    const sheetURL = resolveTrickplaySheetURL(tile.sheet, state.trickplayBaseURL);
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

  const position = scrubValue ?? current;
  const markerModel = buildMarkerStripModel(state.markers, {
    durationSeconds: duration,
    currentTimeSeconds: current,
  });
  const previousChapter = chapterSeekTarget(state.markers, current, -1);
  const nextChapter = chapterSeekTarget(state.markers, current, 1);
  const chapters = buildChapterList(state.markers, {
    thumbnailBase: state.info?.assets?.markerThumbnailBase,
    currentTimeSeconds: current,
  });
  const skipBackLabel = t("seekBackSeconds", { seconds: skipInterval });
  const skipForwardLabel = t("seekForwardSeconds", { seconds: skipInterval });

  function seekTo(seconds: number | null) {
    const video = videoRef.current;
    if (!video || seconds === null) return;
    video.currentTime = Math.max(0, Math.min(duration || Infinity, seconds)) / songScale;
  }

  const volumeLevel = muted ? 0 : volume;
  const VolumeIcon = volumeLevel === 0 ? VolumeX : volumeLevel < 0.5 ? Volume1 : Volume2;
  const menuSide = narrow ? "top" : "left";
  const progressPercent = duration > 0 ? (position / duration) * 100 : 0;
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
              video.currentTime = markerModel.skip.endSeconds / songScale;
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
        </div>

        <div className="bf-scrub-row">
          <span className="bf-time is-start">{formatClockTime(position)}</span>
          <div className="bf-scrub-wrap">
            <Slider.Root
              min={0}
              max={Math.max(duration, 0.1)}
              step={0.1}
              value={position}
              onValueChange={onScrubberValueChange}
              onValueCommitted={commitScrub}
              className="bf-slider-root bf-scrubber"
            >
              <input
                aria-hidden="true"
                tabIndex={-1}
                type="range"
                min={0}
                max={duration || 0}
                step={0.1}
                value={position}
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
                  aria-valuetext={formatAriaTime(position, duration, t)}
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
          <span className="bf-time is-end">{formatRemainingTime(position, duration)}</span>
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
          <ChapterList
            chapters={chapters}
            trickplay={state.trickplay}
            trickplayBaseURL={state.trickplayBaseURL}
            container={layerRef}
            onSeek={seekTo}
            onOpenChange={setChaptersOpen}
          />

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
                    appearance={subtitleAppearance}
                    onAppearanceChange={onSubtitleAppearanceChange}
                    syncOffset={subtitle.syncOffset}
                    nudgeSync={subtitle.nudgeSync}
                    resetSync={subtitle.resetSync}
                    reset={resetSubtitles}
                    syncSupported={subtitle.syncSupported}
                  />
                )}
                {audioSyncAvailable && (
                  <AudioSyncPanel
                    open={openKey === "audioSync"}
                    onOpenChange={(open) => setOpen("audioSync", open)}
                    audioDelay={audioSync.audioDelay}
                    nudgeAudioDelay={audioSync.nudgeAudioDelay}
                    resetAudioDelay={audioSync.resetAudioDelay}
                    supported={audioSync.audioDelaySupported}
                  />
                )}
                {onSelectVersion && versionOptions.length > 1 && (
                  <TransportMenu
                    label={t("version")}
                    ariaLabel={t("version")}
                    icon={<Layers />}
                    container={layerRef}
                    side={menuSide}
                    options={versionOptions}
                    open={openKey === "version"}
                    onOpenChange={(open) => setOpen("version", open)}
                    onSelect={(id) => {
                      closeSettings(true);
                      onSelectVersion(id);
                    }}
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
                  mediaID={mediaID}
                  metadataAttribution={metadataAttribution}
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

          {CAST_RECEIVER_PLAYBACK_READY && mediaID && state.info?.mediaClass === "video" && (
            <CastControl videoRef={videoRef} mediaID={mediaID} mediaTitle={mediaTitle} />
          )}
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
      <ShortcutHelpOverlay
        open={openKey === "shortcuts"}
        onOpenChange={(open) => setOpen("shortcuts", open)}
        container={layerRef}
        skipInterval={skipInterval}
      />
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
