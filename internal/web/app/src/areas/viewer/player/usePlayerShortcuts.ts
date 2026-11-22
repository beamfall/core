import { useEffect, useRef } from "react";
import type { PlaybackMarker } from "@/lib/graphql/types";
import { chapterSeekTarget } from "../lib/transport";
import {
  type FrameClock,
  frameStepTarget,
  JUMP_SECONDS,
  nextTrackId,
  type ResolvedShortcut,
  resolvePlayerShortcut,
  stepSpeed,
  subtitleToggleId,
} from "./playerShortcuts";
import type { MenuOption } from "./useLiveTracks";

// The player's window keydown listener (CORE-1682). Keys resolve through the
// shared shortcut table; input aimed at a control, field, menu or dialog stays
// native. The latest handlers ride a ref so the listener subscribes once.
export interface PlayerShortcutHandlers {
  videoRef: React.RefObject<HTMLVideoElement | null>;
  skipInterval: number;
  speeds: readonly number[];
  /** Where a speed step goes when the player keeps a persisted speed (CORE-2960). */
  setSpeed?: (speed: number) => void;
  markers: PlaybackMarker[];
  togglePlay: () => void;
  toggleFullscreen: () => void;
  toggleMute: () => void;
  seekBy: (seconds: number) => void;
  setVolume: (volume: number) => void;
  subtitleOptions: MenuOption[];
  audioOptions: MenuOption[];
  selectSubtitle: (id: string) => void;
  selectAudio: (id: string) => void;
  onPreviousInQueue?: () => void;
  onNextInQueue?: () => void;
  /** The audio visualizer's previous and next visual (CORE-2814); unset where none draws. */
  onPreviousVisual?: () => void;
  onNextVisual?: () => void;
  onHelp: () => void;
}

const INTERACTIVE =
  'button, a, input, select, textarea, menu, dialog, [role="menu"], [role="listbox"], [role="dialog"], [contenteditable]:not([contenteditable="false"])';

function interactive(target: EventTarget | null) {
  const element = target instanceof Element ? target : document.activeElement;
  if (!(element instanceof HTMLElement)) return false;
  if (element.isContentEditable) return true;
  return Boolean(element.closest(INTERACTIVE));
}

export function usePlayerShortcuts(handlers: PlayerShortcutHandlers) {
  const handlersRef = useRef(handlers);
  handlersRef.current = handlers;
  // The subtitle track C brings back; follows whatever track was last shown.
  const lastSubtitleRef = useRef<string | null>(null);
  const shown = handlers.subtitleOptions.find((option) => option.selected && option.id !== "off");
  if (shown) lastSubtitleRef.current = shown.id;
  const frameClock = useFrameClock(handlers.videoRef);

  useEffect(() => {
    const keydown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || interactive(event.target)) return;
      const shortcut = resolvePlayerShortcut(event);
      if (!shortcut) return;
      event.preventDefault();
      apply(shortcut, handlersRef.current, lastSubtitleRef, frameClock.current);
    };
    window.addEventListener("keydown", keydown);
    return () => window.removeEventListener("keydown", keydown);
  }, [frameClock]);
}

// Frame timing from the frames the browser presents (requestVideoFrameCallback):
// the media-time step between consecutive frames while playing steadily at 1x,
// the smallest seen since the source last changed, and the timestamp of the
// frame on screen, cleared by a seek until the seek presents its frame.
// Browsers without frame callbacks fall back to 1/30 s.
function useFrameClock(videoRef: React.RefObject<HTMLVideoElement | null>) {
  const clock = useRef<FrameClock>({ frame: null, mediaTime: null });
  useEffect(() => {
    const video = videoRef.current;
    if (!video || typeof video.requestVideoFrameCallback !== "function") return;
    let last: VideoFrameCallbackMetadata | null = null;
    let handle = 0;
    const reset = () => {
      clock.current = { frame: null, mediaTime: null };
      last = null;
    };
    const seeking = () => {
      clock.current.mediaTime = null;
    };
    const onFrame: VideoFrameRequestCallback = (_now, meta) => {
      const steady = !video.paused && !video.seeking && video.playbackRate === 1;
      if (steady && last && meta.presentedFrames === last.presentedFrames + 1) {
        const step = meta.mediaTime - last.mediaTime;
        if (step > 1 / 240 && step <= 1 && step < (clock.current.frame ?? Infinity)) {
          clock.current.frame = step;
        }
      }
      last = meta;
      clock.current.mediaTime = meta.mediaTime;
      handle = video.requestVideoFrameCallback(onFrame);
    };
    handle = video.requestVideoFrameCallback(onFrame);
    video.addEventListener("emptied", reset);
    video.addEventListener("seeking", seeking);
    return () => {
      video.cancelVideoFrameCallback(handle);
      video.removeEventListener("emptied", reset);
      video.removeEventListener("seeking", seeking);
    };
  }, [videoRef]);
  return clock;
}

function apply(
  shortcut: ResolvedShortcut,
  h: PlayerShortcutHandlers,
  lastSubtitleRef: React.RefObject<string | null>,
  frameClock: FrameClock,
) {
  const video = h.videoRef.current;
  const seekTo = (seconds: number | null) => {
    if (!video || seconds === null || !Number.isFinite(seconds)) return;
    const duration = Number.isFinite(video.duration) ? video.duration : Infinity;
    video.currentTime = Math.max(0, Math.min(duration, seconds));
  };
  switch (shortcut.action) {
    case "togglePlay":
      return h.togglePlay();
    case "jumpBack":
      return h.seekBy(-JUMP_SECONDS);
    case "jumpForward":
      return h.seekBy(JUMP_SECONDS);
    case "skipBack":
      return h.seekBy(-h.skipInterval);
    case "skipForward":
      return h.seekBy(h.skipInterval);
    case "volumeUp":
      return h.setVolume((video?.volume ?? 0) + 0.05);
    case "volumeDown":
      return h.setVolume((video?.volume ?? 0) - 0.05);
    case "toggleMute":
      return h.toggleMute();
    case "toggleFullscreen":
      return h.toggleFullscreen();
    case "frameBack":
    case "frameForward":
      if (video?.paused) {
        // A seek still in flight has not shown its frame yet; step from its target.
        const clock = video.seeking ? { ...frameClock, mediaTime: null } : frameClock;
        seekTo(frameStepTarget(clock, video.currentTime, shortcut.action === "frameBack" ? -1 : 1));
      }
      return;
    case "slower":
    case "faster": {
      if (!video) return;
      const next = stepSpeed(h.speeds, video.playbackRate, shortcut.action === "faster" ? 1 : -1);
      if (next === null) return;
      if (h.setSpeed) h.setSpeed(next);
      else video.playbackRate = next;
      return;
    }
    case "toggleSubtitles": {
      const id = subtitleToggleId(h.subtitleOptions, lastSubtitleRef.current);
      if (id !== null) h.selectSubtitle(id);
      return;
    }
    case "nextSubtitle": {
      const id = nextTrackId(h.subtitleOptions);
      if (id !== null) h.selectSubtitle(id);
      return;
    }
    case "nextAudio": {
      const id = nextTrackId(h.audioOptions);
      if (id !== null) h.selectAudio(id);
      return;
    }
    case "previousChapter":
    case "nextChapter":
      return seekTo(
        chapterSeekTarget(
          h.markers,
          video?.currentTime ?? 0,
          shortcut.action === "nextChapter" ? 1 : -1,
        ),
      );
    case "previousItem":
      return h.onPreviousInQueue?.();
    case "nextItem":
      return h.onNextInQueue?.();
    case "previousVisual":
      return h.onPreviousVisual?.();
    case "nextVisual":
      return h.onNextVisual?.();
    case "seekPercent":
      if (video && Number.isFinite(video.duration) && video.duration > 0) {
        seekTo((video.duration * (shortcut.percent ?? 0)) / 100);
      }
      return;
    case "help":
      return h.onHelp();
  }
}
