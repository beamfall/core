import { type RefObject, useCallback, useEffect, useRef, useState } from "react";
import {
  CAPTION_SHOWN_EVENT,
  CAPTION_TRACK_ATTRIBUTE,
  captionElement,
  reloadCaptionElement,
} from "./streamTracks";

/**
 * How long a picked subtitle may take to arrive before the viewer is told it
 * failed. The server converts a muxed stream on first request and waits up to
 * 20 s before answering "still extracting", so this outlasts that wait; a hung
 * request never ends on its own.
 */
export const CAPTION_LOAD_TIMEOUT_MS = 30_000;
/** How many times a track the server is still extracting is fetched again. */
export const CAPTION_MAX_RETRIES = 6;
/** The wait when the server's 503 names no usable Retry-After. */
const DEFAULT_RETRY_AFTER_SECONDS = 5;
const MAX_RETRY_AFTER_SECONDS = 30;

// HTMLTrackElement.readyState values (the constants are absent in some DOMs).
const TRACK_LOADED = 2;
const TRACK_ERROR = 3;

/**
 * "preparing" means the server answered that it is still extracting the track
 * and the player will fetch it again: the viewer sees "Loading subtitles…",
 * not an error.
 */
export type CaptionLoadStatus = "idle" | "loading" | "preparing" | "loaded" | "failed";

export interface CaptionLoad {
  status: CaptionLoadStatus;
  /** Fetch the failed track again. */
  retry: () => void;
}

/**
 * Asks the server why a track failed: the seconds to wait when it is still
 * extracting it (HTTP 503 with Retry-After), or null for any other failure.
 */
export type CaptionProbe = (url: string) => Promise<number | null>;

export interface CaptionLoadOptions {
  timeoutMS?: number;
  maxRetries?: number;
  probe?: CaptionProbe;
}

/**
 * probeCaptionSource reads the failed track's response, which a <track>
 * element hides: the server answers 503 with Retry-After while a subtitle is
 * still being extracted (plaintext and encrypted tracks alike).
 */
export async function probeCaptionSource(url: string): Promise<number | null> {
  try {
    const response = await fetch(url, { credentials: "same-origin", cache: "no-store" });
    // Ready by the time the probe asked (the extraction finished while the
    // <track> was waiting): reload at once rather than report a failure.
    if (response.ok) return 0;
    // Only a 503 carrying Retry-After means "still extracting"; a bare 503 is
    // the server's hard "subtitle unavailable", and retrying it would rerun a
    // full-file extraction each time.
    const raw = response.status === 503 ? response.headers.get("Retry-After") : null;
    if (raw === null) return null;
    const header = Number(raw);
    const seconds = Number.isFinite(header) && header > 0 ? header : DEFAULT_RETRY_AFTER_SECONDS;
    return Math.min(MAX_RETRY_AFTER_SECONDS, Math.max(1, seconds));
  } catch {
    return null;
  }
}

/**
 * useCaptionLoad watches the subtitle track the viewer picked and reports
 * when it fails to load or never arrives (CORE-2315): a <track> that errors or
 * hangs otherwise just shows nothing, and the viewer cannot tell a broken
 * subtitle from a quiet scene. A track the server is still extracting is
 * fetched again after its Retry-After, a capped number of times, before it
 * counts as failed.
 */
export function useCaptionLoad(
  videoRef: RefObject<HTMLVideoElement | null>,
  options: CaptionLoadOptions = {},
): CaptionLoad {
  const {
    timeoutMS = CAPTION_LOAD_TIMEOUT_MS,
    maxRetries = CAPTION_MAX_RETRIES,
    probe = probeCaptionSource,
  } = options;
  const [status, setStatus] = useState<CaptionLoadStatus>("idle");
  const shownRef = useRef<string | null>(null);
  // Set while the player itself reloads a track the server is preparing, so
  // the shown event that reload fires keeps the retry count and the status.
  const autoRetryRef = useRef(false);
  const attemptsRef = useRef(0);

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;
    // Bumped on every pick, so a probe answering for an earlier pick is dropped.
    let generation = 0;
    const clearTimers = () => {
      if (timer !== undefined) clearTimeout(timer);
      if (retryTimer !== undefined) clearTimeout(retryTimer);
      timer = undefined;
      retryTimer = undefined;
    };
    const settle = (next: CaptionLoadStatus) => {
      clearTimers();
      setStatus(next);
    };
    const onShown = (event: Event) => {
      const id = (event as CustomEvent<string | null>).detail ?? null;
      const automatic = autoRetryRef.current && id === shownRef.current;
      autoRetryRef.current = false;
      generation++;
      shownRef.current = id;
      if (!automatic) attemptsRef.current = 0;
      const element = id === null ? null : captionElement(video, id);
      // Burned-in and menu-only tracks have no element to watch.
      if (id === null || !element) return settle("idle");
      if (element.readyState === TRACK_LOADED) return settle("loaded");
      if (element.readyState === TRACK_ERROR) return void onFailed(id, element);
      settle(automatic ? "preparing" : "loading");
      timer = setTimeout(() => {
        if (shownRef.current === id) settle("failed");
      }, timeoutMS);
    };
    const onFailed = (id: string, element: HTMLTrackElement) => {
      clearTimers();
      const pick = generation;
      const url = element.src || element.getAttribute("src") || "";
      if (!url || attemptsRef.current >= maxRetries) return settle("failed");
      void probe(url).then((retryAfter) => {
        if (pick !== generation || shownRef.current !== id) return;
        if (retryAfter === null) return settle("failed");
        attemptsRef.current++;
        settle("preparing");
        retryTimer = setTimeout(() => {
          if (pick !== generation || shownRef.current !== id) return;
          autoRetryRef.current = true;
          reloadCaptionElement(video, id);
        }, retryAfter * 1000);
      });
    };
    const shownTrack = (event: Event): HTMLTrackElement | null => {
      const target = event.target;
      if (!(target instanceof HTMLTrackElement)) return null;
      const id = target.getAttribute(CAPTION_TRACK_ATTRIBUTE);
      return id !== null && id === shownRef.current ? target : null;
    };
    // <track> load and error events do not bubble, so they are caught on the
    // way down instead.
    const onLoad = (event: Event) => {
      if (shownTrack(event)) settle("loaded");
    };
    const onError = (event: Event) => {
      const element = shownTrack(event);
      const id = shownRef.current;
      if (element && id !== null) onFailed(id, element);
    };
    video.addEventListener(CAPTION_SHOWN_EVENT, onShown);
    video.addEventListener("load", onLoad, true);
    video.addEventListener("error", onError, true);
    return () => {
      generation++;
      clearTimers();
      video.removeEventListener(CAPTION_SHOWN_EVENT, onShown);
      video.removeEventListener("load", onLoad, true);
      video.removeEventListener("error", onError, true);
    };
  }, [videoRef, timeoutMS, maxRetries, probe]);

  const retry = useCallback(() => {
    const video = videoRef.current;
    const id = shownRef.current;
    if (video && id !== null) reloadCaptionElement(video, id);
  }, [videoRef]);

  return { status, retry };
}
