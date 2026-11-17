import { type RefObject, useCallback, useEffect, useRef, useState } from "react";
import {
  CAPTION_SHOWN_EVENT,
  CAPTION_TRACK_ATTRIBUTE,
  captionElement,
  reloadCaptionElement,
} from "./streamTracks";

/**
 * How long a picked subtitle may take to arrive before the viewer is told it
 * failed. The server converts a muxed stream on first request, which reads the
 * file, so this is generous; a hung request never ends on its own.
 */
export const CAPTION_LOAD_TIMEOUT_MS = 20_000;

// HTMLTrackElement.readyState values (the constants are absent in some DOMs).
const TRACK_LOADED = 2;
const TRACK_ERROR = 3;

export type CaptionLoadStatus = "idle" | "loading" | "loaded" | "failed";

export interface CaptionLoad {
  status: CaptionLoadStatus;
  /** Fetch the failed track again. */
  retry: () => void;
}

/**
 * useCaptionLoad watches the subtitle track the viewer picked and reports
 * when it fails to load or never arrives (CORE-2315): a <track> that errors or
 * hangs otherwise just shows nothing, and the viewer cannot tell a broken
 * subtitle from a quiet scene.
 */
export function useCaptionLoad(
  videoRef: RefObject<HTMLVideoElement | null>,
  timeoutMS = CAPTION_LOAD_TIMEOUT_MS,
): CaptionLoad {
  const [state, setState] = useState<{ id: string | null; status: CaptionLoadStatus }>({
    id: null,
    status: "idle",
  });
  const shownRef = useRef<string | null>(null);

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const settle = (id: string | null, status: CaptionLoadStatus) => {
      if (timer !== undefined) clearTimeout(timer);
      timer = undefined;
      setState({ id, status });
    };
    const onShown = (event: Event) => {
      const id = (event as CustomEvent<string | null>).detail ?? null;
      shownRef.current = id;
      const element = id === null ? null : captionElement(video, id);
      // Burned-in and menu-only tracks have no element to watch.
      if (id === null || !element) return settle(id, "idle");
      if (element.readyState === TRACK_LOADED) return settle(id, "loaded");
      if (element.readyState === TRACK_ERROR) return settle(id, "failed");
      settle(id, "loading");
      timer = setTimeout(() => {
        if (shownRef.current === id) settle(id, "failed");
      }, timeoutMS);
    };
    const shownTrackEvent = (event: Event): string | null => {
      const target = event.target;
      if (!(target instanceof HTMLTrackElement)) return null;
      const id = target.getAttribute(CAPTION_TRACK_ATTRIBUTE);
      return id !== null && id === shownRef.current ? id : null;
    };
    // <track> load and error events do not bubble, so they are caught on the
    // way down instead.
    const onLoad = (event: Event) => {
      const id = shownTrackEvent(event);
      if (id !== null) settle(id, "loaded");
    };
    const onError = (event: Event) => {
      const id = shownTrackEvent(event);
      if (id !== null) settle(id, "failed");
    };
    video.addEventListener(CAPTION_SHOWN_EVENT, onShown);
    video.addEventListener("load", onLoad, true);
    video.addEventListener("error", onError, true);
    return () => {
      if (timer !== undefined) clearTimeout(timer);
      video.removeEventListener(CAPTION_SHOWN_EVENT, onShown);
      video.removeEventListener("load", onLoad, true);
      video.removeEventListener("error", onError, true);
    };
  }, [videoRef, timeoutMS]);

  const retry = useCallback(() => {
    const video = videoRef.current;
    const id = shownRef.current;
    if (video && id !== null) reloadCaptionElement(video, id);
  }, [videoRef]);

  return { status: state.status, retry };
}
