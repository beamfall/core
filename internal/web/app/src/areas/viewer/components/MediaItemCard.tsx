import type { PointerEvent } from "react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router";
import type { SourceItem } from "@/lib/graphql/types";
import { artworkBucketURL, artworkSlotURL } from "../lib/artwork";
import { fetchPreviewInfo } from "../lib/playback";
import { type RenderFocusAnchor, rememberRenderFocus } from "../lib/renderFocus";

export const PREVIEW_DWELL_MS = 400;
const POINTER_PREVIEW_QUERY = "(hover: hover) and (pointer: fine)";
const REDUCED_MOTION_QUERY = "(prefers-reduced-motion: reduce)";

// One catalog card, ported from the vanilla renderItem (app.js). Generic by
// invariant #1: the wire entity is Media and `kind` is a plugin display
// discriminant — a "media" item plays, anything else opens entity detail. The
// poster sits in a fixed 2:3 frame whose accent-tinted fallback gradient shows
// when artwork is absent or fails to load, so layout never shifts. Titles and
// summaries render as React text only (invariant #5 / ADR-0012 §5).
export function MediaItemCard({
  item,
  posInSet,
  setSize,
  focusAnchor,
}: {
  item: SourceItem;
  /** 1-based aria-posinset for grid a11y; omit outside a grid. */
  posInSet?: number;
  setSize?: number;
  /** Stable renderer identity used for initial focus and Reveal/relock restore. */
  focusAnchor?: RenderFocusAnchor;
}) {
  const navigate = useNavigate();
  const location = useLocation();
  const [imageFailed, setImageFailed] = useState(false);
  const [previewClip, setPreviewClip] = useState("");
  const isMedia = item.kind === "media";
  const poster = artworkSlotURL(item, "poster") || artworkSlotURL(item, "thumb");
  const previewTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const previewVideoRef = useRef<HTMLVideoElement | null>(null);
  const previewRequestRef = useRef(0);

  const clearPreviewTimer = useCallback(() => {
    if (previewTimerRef.current !== null) {
      clearTimeout(previewTimerRef.current);
      previewTimerRef.current = null;
    }
  }, []);

  const stopPreview = useCallback(() => {
    clearPreviewTimer();
    previewRequestRef.current += 1;
    setPreviewClip("");
  }, [clearPreviewTimer]);

  useEffect(() => {
    return () => {
      stopPreview();
    };
  }, [location.key, stopPreview]);

  useEffect(() => {
    function stopOnVisibilityLoss() {
      if (document.visibilityState === "hidden") {
        stopPreview();
      }
    }

    window.addEventListener("blur", stopPreview);
    document.addEventListener("visibilitychange", stopOnVisibilityLoss);
    return () => {
      window.removeEventListener("blur", stopPreview);
      document.removeEventListener("visibilitychange", stopOnVisibilityLoss);
    };
  }, [stopPreview]);

  useEffect(() => {
    if (!previewClip) {
      return;
    }
    const video = previewVideoRef.current;
    if (!video) {
      return;
    }
    video.currentTime = 0;
    const playPromise = video.play();
    if (playPromise && typeof playPromise.catch === "function") {
      playPromise.catch(() => setPreviewClip(""));
    }
    return () => {
      video.pause();
      video.removeAttribute("src");
      video.load();
    };
  }, [previewClip]);

  function activate() {
    if (isMedia) {
      // Carry the title forward so the player's info overlay can name the Media
      // (the playback-info wire does not report it).
      navigate(`/watch/${encodeURIComponent(item.id)}`, {
        state: { title: item.title },
      });
    } else {
      navigate(`/entity/${encodeURIComponent(item.id)}`);
    }
  }

  function beginPreviewDwell() {
    const requestVersion = previewRequestRef.current + 1;
    previewRequestRef.current = requestVersion;
    previewTimerRef.current = setTimeout(() => {
      previewTimerRef.current = null;
      void fetchPreviewInfo(item.id)
        .then((info) => {
          if (previewRequestRef.current === requestVersion) {
            setPreviewClip(info.assets?.previewClip ?? "");
          }
        })
        .catch(() => {
          if (previewRequestRef.current === requestVersion) {
            setPreviewClip("");
          }
        });
    }, PREVIEW_DWELL_MS);
  }

  function handlePointerEnter(event: PointerEvent<HTMLButtonElement>) {
    clearPreviewTimer();
    if (previewClip || !canStartPreview(event)) {
      return;
    }
    beginPreviewDwell();
  }

  // Keyboard-focus dwell mirrors pointer hover (ASSET-1-CLIENT-DESIGN mockup,
  // Web Viewer row: "Trigger: hover or keyboard focus after 400 ms") so
  // keyboard-only navigation gets the same preview affordance as a mouse.
  function handleFocus() {
    if (focusAnchor) {
      rememberRenderFocus(focusAnchor);
    }
    clearPreviewTimer();
    if (previewClip || !canStartPreview()) {
      return;
    }
    beginPreviewDwell();
  }

  function canStartPreview(event?: PointerEvent<HTMLButtonElement>) {
    if (!isMedia) {
      return false;
    }
    if (event?.pointerType && event.pointerType !== "mouse") {
      return false;
    }
    return mediaQueryMatches(POINTER_PREVIEW_QUERY) && !mediaQueryMatches(REDUCED_MOTION_QUERY);
  }

  return (
    <li className="list-none" aria-posinset={posInSet} aria-setsize={setSize}>
      <button
        type="button"
        onClick={activate}
        onBlur={stopPreview}
        onPointerCancel={stopPreview}
        onPointerEnter={handlePointerEnter}
        onPointerLeave={stopPreview}
        onFocus={handleFocus}
        data-focus-item={focusAnchor ? "" : undefined}
        data-focus-navigation={focusAnchor?.navigation}
        data-focus-section={focusAnchor?.section}
        data-focus-id={focusAnchor?.item}
        className="bf-card"
      >
        <div className="bf-poster">
          {poster && !imageFailed && (
            <img
              alt=""
              loading="lazy"
              src={artworkBucketURL(poster, "w320")}
              onError={() => setImageFailed(true)}
            />
          )}
          {previewClip && (
            <video
              ref={previewVideoRef}
              aria-hidden="true"
              className="bf-preview-clip"
              loop
              muted
              playsInline
              preload="metadata"
              src={previewClip}
            />
          )}
        </div>
        <div className="bf-card-copy">
          <span className="bf-card-title">{item.title}</span>
          {item.summary && <span className="bf-card-sub">{item.summary}</span>}
        </div>
      </button>
    </li>
  );
}

function mediaQueryMatches(query: string): boolean {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") {
    return false;
  }
  return window.matchMedia(query).matches;
}
