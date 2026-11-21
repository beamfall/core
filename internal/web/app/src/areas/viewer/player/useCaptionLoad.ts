import { useCallback, useEffect, useRef, useState } from "react";
import {
  CAPTION_SHOWN_EVENT,
  CAPTION_TRACK_ATTRIBUTE,
  captionElement,
  reloadCaptionElement,
  STYLED_CAPTION_SOURCE_ATTRIBUTE,
  shownCaptionElement,
  startNativeCaption,
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
  video: HTMLVideoElement | null,
  options: CaptionLoadOptions = {},
): CaptionLoad {
  const {
    timeoutMS = CAPTION_LOAD_TIMEOUT_MS,
    maxRetries = CAPTION_MAX_RETRIES,
    probe = probeCaptionSource,
  } = options;
  const [status, setStatus] = useState<CaptionLoadStatus>("idle");
  const shownRef = useRef<string | null>(null);
  useEffect(() => {
    if (!video) {
      shownRef.current = null;
      setStatus("idle");
      return;
    }
    let timer: ReturnType<typeof setTimeout> | undefined;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;
    // An automatic reload keeps the retry count for this video and pick.
    let autoRetry = false;
    let attempts = 0;
    // Bumped on every pick, so a probe answering for an earlier pick is dropped.
    let generation = 0;
    let styled: { element: HTMLTrackElement; controller: AbortController } | undefined;
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
    const watchSelection = (id: string | null) => {
      const element = id === null ? null : captionElement(video, id);
      if (styled && styled.element === element) return;
      styled?.controller.abort();
      styled = undefined;
      const automatic = autoRetry && id === shownRef.current;
      autoRetry = false;
      generation++;
      shownRef.current = id;
      if (!automatic) attempts = 0;
      // Burned-in and menu-only tracks have no element to watch.
      if (id === null || !element) return settle("idle");
      const watchNative = () => {
        if (element.readyState === TRACK_LOADED) return settle("loaded");
        if (element.readyState === TRACK_ERROR) return void onFailed(id, element);
        settle(automatic ? "preparing" : "loading");
        timer = setTimeout(() => {
          if (shownRef.current === id) settle("failed");
        }, timeoutMS);
      };
      const source = element.getAttribute(STYLED_CAPTION_SOURCE_ATTRIBUTE);
      if (
        !source ||
        element.src ||
        typeof WebAssembly === "undefined" ||
        typeof Worker === "undefined" ||
        typeof HTMLCanvasElement.prototype.transferControlToOffscreen !== "function"
      ) {
        startNativeCaption(element);
        return watchNative();
      }
      const controller = new AbortController();
      styled = { element, controller };
      const fallback = () => {
        if (controller.signal.aborted) return;
        controller.abort();
        styled = undefined;
        startNativeCaption(element);
        watchNative();
      };
      settle("loading");
      timer = setTimeout(fallback, timeoutMS);
      void import("./styledCaptionRenderer")
        .then(({ startStyledCaption }) => {
          if (controller.signal.aborted) return;
          return startStyledCaption(video, source, controller.signal, fallback);
        })
        .then(() => {
          if (!controller.signal.aborted) settle("loaded");
        })
        .catch(fallback);
    };
    const onFailed = (id: string, element: HTMLTrackElement) => {
      clearTimers();
      const pick = generation;
      const url = element.src || element.getAttribute("src") || "";
      if (!url || attempts >= maxRetries) return settle("failed");
      void probe(url).then((retryAfter) => {
        if (pick !== generation || shownRef.current !== id) return;
        if (retryAfter === null) return settle("failed");
        attempts++;
        settle("preparing");
        retryTimer = setTimeout(() => {
          if (pick !== generation || shownRef.current !== id) return;
          autoRetry = true;
          reloadCaptionElement(video, id);
        }, retryAfter * 1000);
      });
    };
    const shownTrack = (event: Event): HTMLTrackElement | null => {
      const target = event.target;
      if (!(target instanceof HTMLTrackElement) || !target.src) return null;
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
    const onShown = (event: Event) => watchSelection((event as CustomEvent<string | null>).detail);
    video.addEventListener(CAPTION_SHOWN_EVENT, onShown);
    video.addEventListener("load", onLoad, true);
    video.addEventListener("error", onError, true);
    watchSelection(shownCaptionElement(video));
    return () => {
      generation++;
      styled?.controller.abort();
      clearTimers();
      video.removeEventListener(CAPTION_SHOWN_EVENT, onShown);
      video.removeEventListener("load", onLoad, true);
      video.removeEventListener("error", onError, true);
    };
  }, [video, timeoutMS, maxRetries, probe]);

  const retry = useCallback(() => {
    const id = shownRef.current;
    if (video && id !== null) reloadCaptionElement(video, id);
  }, [video]);

  return { status, retry };
}
