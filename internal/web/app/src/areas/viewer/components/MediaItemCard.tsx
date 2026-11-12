import { Check, Play } from "lucide-react";
import type { PointerEvent } from "react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router";
import type { SourceItem } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { useViewerIdentity } from "@/lib/session";
import { itemTargetFor } from "../actions";
import { artworkBucketURL, artworkSlotURL } from "../lib/artwork";
import { cardAccessibleName, cardState } from "../lib/cardState";
import { entityLinkState } from "../lib/entityPreview";
import { fetchPreviewInfo } from "../lib/playback";
import { type RenderFocusAnchor, rememberRenderFocus } from "../lib/renderFocus";
import { type PlayerNavState, type PlayQueueItem, queueWindow } from "../lib/watchQueue";
import { preloadPlayer } from "../player/preload";
import { MediaItemCardMenu } from "./MediaItemCardMenu";

export const PREVIEW_DWELL_MS = 400;
const POINTER_PREVIEW_QUERY = "(hover: hover) and (pointer: fine)";
const REDUCED_MOTION_QUERY = "(prefers-reduced-motion: reduce)";

// One catalog card, ported from the vanilla renderItem (app.js). Generic by
// invariant #1: the wire entity is Media and `kind` is a plugin display
// discriminant — a "media" item plays, anything else opens entity detail. The
// poster sits in a fixed 2:3 frame whose accent-tinted fallback gradient shows
// when artwork is absent or fails to load, so layout never shifts. Titles and
// summaries render as React text only (invariant #5 / ADR-0012 §5).
//
// The card button is the only control that carries the item's name: its
// accessible name is the title plus the acting Profile's state, and the same
// state is drawn on the artwork (progress bar, finished mark, remaining count).
// The More menu is a sibling control, never nested inside the button.
export function MediaItemCard({
  item,
  posInSet,
  setSize,
  focusAnchor,
  queue,
}: {
  item: SourceItem;
  /** 1-based aria-posinset for grid a11y; omit outside a grid. */
  posInSet?: number;
  setSize?: number;
  /** Stable renderer identity used for initial focus and Reveal/relock restore. */
  focusAnchor?: RenderFocusAnchor;
  /** The playable siblings this card is shown with, handed to the player as its queue. */
  queue?: PlayQueueItem[];
}) {
  const navigate = useNavigate();
  const identity = useViewerIdentity();
  const t = useT();
  const location = useLocation();
  const [imageFailed, setImageFailed] = useState(false);
  const [previewClip, setPreviewClip] = useState("");
  const isMedia = item.kind === "media";
  const state = cardState(item, t);
  const path = `/${isMedia ? "watch" : "entity"}/${encodeURIComponent(item.id)}`;
  const primaryLabel = isMedia
    ? t(state.progressPercent === undefined ? "play" : "cardResume")
    : t("cardOpen");
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
      // (the playback-info wire does not report it), plus the siblings as the
      // player's queue (previous/next and the pre-minted next stream). A
      // one-item queue still goes, so it ends an earlier Play all queue.
      const state: PlayerNavState = { title: item.title };
      if (identity && queue && queue.length > 0) {
        state.queue = queueWindow(queue, item.id);
        state.queueIdentity = identity;
      }
      navigate(path, { state });
    } else {
      navigate(path, { state: entityLinkState(item, identity) });
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
    if (isMedia) preloadPlayer();
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
    if (isMedia) preloadPlayer();
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
    <li className="bf-card-shell list-none" aria-posinset={posInSet} aria-setsize={setSize}>
      <button
        type="button"
        aria-label={cardAccessibleName(item, state)}
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
          <CardStateOverlay
            finished={state.finished}
            remainingCount={state.remainingCount}
            progressPercent={state.progressPercent}
          />
          {isMedia && (
            <span aria-hidden="true" className="bf-card-play">
              <Play size={16} fill="currentColor" />
              {primaryLabel}
            </span>
          )}
        </div>
        <div className="bf-card-copy">
          <span className="bf-card-title">{item.title}</span>
          {item.summary && <span className="bf-card-sub">{item.summary}</span>}
        </div>
      </button>
      <MediaItemCardMenu
        title={item.title}
        primaryLabel={primaryLabel}
        isMedia={isMedia}
        href={new URL(`${import.meta.env.BASE_URL}${path.slice(1)}`, window.location.origin).href}
        target={itemTargetFor(item)}
        onPrimary={activate}
      />
    </li>
  );
}

// The state drawn on the artwork. Decorative for assistive technology: the
// card button's accessible name already says the same thing in words.
function CardStateOverlay({
  finished,
  remainingCount,
  progressPercent,
}: {
  finished: boolean;
  remainingCount?: number;
  progressPercent?: number;
}) {
  return (
    <>
      {finished && (
        <span aria-hidden="true" className="bf-card-badge bf-card-badge-finished">
          <Check size={13} strokeWidth={3} />
        </span>
      )}
      {remainingCount !== undefined && (
        <span aria-hidden="true" className="bf-card-badge">
          {remainingCount}
        </span>
      )}
      {progressPercent !== undefined && (
        <span aria-hidden="true" className="bf-card-progress">
          <span style={{ width: `${progressPercent}%` }} />
        </span>
      )}
    </>
  );
}

function mediaQueryMatches(query: string): boolean {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") {
    return false;
  }
  return window.matchMedia(query).matches;
}
