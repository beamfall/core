import { Check, Play } from "lucide-react";
import type { PointerEvent } from "react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router";
import { displayString } from "@/lib/display";
import type { SourceItem } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { useViewerIdentity } from "@/lib/session";
import { itemTargetFor } from "../actions";
import { artworkBucketURL, artworkSlotURL, artworkSrcSet } from "../lib/artwork";
import { artFrameClass } from "../lib/cardArt";
import { cardAccessibleName, cardState } from "../lib/cardState";
import { cardSubline } from "../lib/cardTitles";
import { entityLinkState } from "../lib/entityPreview";
import type { LibraryViewMode } from "../lib/libraryBrowse";
import { mediaLinkState } from "../lib/mediaDetail";
import { fetchPreviewInfo } from "../lib/playback";
import { type RenderFocusAnchor, rememberRenderFocus } from "../lib/renderFocus";
import { formatDuration } from "../lib/trackList";
import { type PlayerNavState, type PlayQueueItem, queueWindow } from "../lib/watchQueue";
import { preloadPlayer } from "../player/preload";
import { MediaItemCardMenu } from "./MediaItemCardMenu";
import { PosterGlyph } from "./PosterGlyph";

export const PREVIEW_DWELL_MS = 400;
const POINTER_PREVIEW_QUERY = "(hover: hover) and (pointer: fine)";
const REDUCED_MOTION_QUERY = "(prefers-reduced-motion: reduce)";
// Shelf cards are 150px and grid cells (minmax(150px, 1fr)) reach ~300px, so a
// 300px slot picks w320 at 1x and w640 on 2x screens instead of upscaling w320.
const CARD_BUCKETS = ["w320", "w640"] as const;
const CARD_SIZES = "300px";

// One catalog card, ported from the vanilla renderItem (app.js). Generic by
// invariant #1: the wire entity is Media and `kind` is a plugin display
// discriminant — a "media" item plays, anything else opens entity detail. The
// poster sits in a fixed frame (2:3, or square for audio: cardArt.ts) whose tinted
// fallback (with a muted glyph) shows when artwork is absent or fails to load, so
// layout never shifts. Titles and
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
  playOnActivate,
  showYear = false,
  display = "poster",
}: {
  item: SourceItem;
  /** 1-based aria-posinset for grid a11y; omit outside a grid. */
  posInSet?: number;
  setSize?: number;
  /** Stable renderer identity used for initial focus and Reveal/relock restore. */
  focusAnchor?: RenderFocusAnchor;
  /** The playable siblings this card is shown with, handed to the player as its queue. */
  queue?: PlayQueueItem[];
  /** A list row plays at once; a poster opens the Media detail page (CORE-1144). */
  playOnActivate?: boolean;
  /** The title repeats in this list, so the card shows its year (CORE-2320). */
  showYear?: boolean;
  /**
   * How the Library draws its items (CORE-2386): a poster, a landscape
   * thumbnail (the thumb slot, else the poster), or a compact list row with
   * the item's ordinal and runtime.
   */
  display?: LibraryViewMode;
}) {
  const navigate = useNavigate();
  const identity = useViewerIdentity();
  const t = useT();
  const displayLocale = useDisplayLocale();
  const location = useLocation();
  const [imageFailed, setImageFailed] = useState(false);
  const [previewClip, setPreviewClip] = useState("");
  const isMedia = item.kind === "media";
  const state = cardState(item, t);
  const subline = cardSubline(item, showYear, displayLocale);
  const subtitle = displayString(item.subtitle, displayLocale);
  const isRow = display === "list";
  const wide = display === "thumbnail";
  // The page the card's own activation opens (and the More menu's copied link).
  const path = `/${isMedia ? (playOnActivate ? "watch" : "media") : "entity"}/${encodeURIComponent(item.id)}`;
  const primaryLabel = isMedia
    ? t(state.progressPercent === undefined ? "play" : "cardResume")
    : t("cardOpen");
  const poster = wide
    ? artworkSlotURL(item, "thumb") || artworkSlotURL(item, "poster")
    : artworkSlotURL(item, "poster") || artworkSlotURL(item, "thumb");
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

  // The siblings as the player's queue (previous/next and the pre-minted next
  // stream), bound to the viewer identity. A one-item queue still goes, so it
  // ends an earlier Play all queue. The detail page forwards it to its Play.
  function queueState(): Pick<PlayerNavState, "queue" | "queueIdentity"> {
    if (!identity || !queue || queue.length === 0) return {};
    return { queue: queueWindow(queue, item.id), queueIdentity: identity };
  }

  function play() {
    // Carry the title forward so the player's info overlay can name the Media
    // (the playback-info wire does not report it).
    const state: PlayerNavState = { title: item.title, ...queueState() };
    navigate(`/watch/${encodeURIComponent(item.id)}`, { state });
  }

  function activate() {
    if (isMedia && playOnActivate) {
      play();
    } else if (isMedia) {
      const link = mediaLinkState(item, identity, focusAnchor?.section);
      navigate(path, { state: link ? { ...link, ...queueState() } : undefined });
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
        aria-label={cardAccessibleName(
          { ...item, title: cardLabelTitle(item, showYear, subtitle) },
          state,
        )}
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
        {isRow && item.index !== undefined && item.index > 0 && (
          <span aria-hidden="true" className="bf-card-ordinal">
            {item.index}
          </span>
        )}
        <div className={wide ? "bf-poster bf-poster-wide" : artFrameClass(item)}>
          {poster && !imageFailed ? (
            <img
              alt=""
              loading="lazy"
              decoding="async"
              src={artworkBucketURL(poster, "w320")}
              srcSet={artworkSrcSet(poster, CARD_BUCKETS)}
              sizes={CARD_SIZES}
              onError={() => setImageFailed(true)}
            />
          ) : (
            <PosterGlyph mediaClass={item.mediaClass} />
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
            remainingLabel={state.label}
            progressPercent={state.progressPercent}
          />
          {/* The pill names what activating the card does, so it shows only when that plays. */}
          {isMedia && playOnActivate && (
            <span aria-hidden="true" className="bf-card-play">
              <Play size={16} fill="currentColor" />
              {primaryLabel}
            </span>
          )}
        </div>
        <div className="bf-card-copy">
          <span className="bf-card-title">{item.title}</span>
          {subline && <span className="bf-card-sub">{subline}</span>}
        </div>
        {isRow && isMedia && item.durationSeconds ? (
          <span aria-hidden="true" className="bf-card-runtime">
            {formatDuration(item.durationSeconds)}
          </span>
        ) : null}
      </button>
      <MediaItemCardMenu
        title={item.title}
        primaryLabel={primaryLabel}
        isMedia={isMedia}
        isAudio={item.mediaClass === "audio"}
        href={new URL(`${import.meta.env.BASE_URL}${path.slice(1)}`, window.location.origin).href}
        target={itemTargetFor(item)}
        onPrimary={isMedia ? play : activate}
      />
    </li>
  );
}

// The title a card's accessible name starts with: the year when the title
// repeats in the list (CORE-2320), then the plugin's subtitle, so siblings that
// share a title are told apart by ear as by eye (CORE-1226).
function cardLabelTitle(item: SourceItem, showYear: boolean, subtitle: string): string {
  const title = showYear && item.year ? `${item.title} (${item.year})` : item.title;
  return subtitle ? `${title}, ${subtitle}` : title;
}

// The state drawn on the artwork. Decorative for assistive technology: the
// card button's accessible name already says the same thing in words.
function CardStateOverlay({
  finished,
  remainingCount,
  remainingLabel,
  progressPercent,
}: {
  finished: boolean;
  remainingCount?: number;
  /** The count in words ("3 unplayed"): a bare number says nothing (CORE-2322). */
  remainingLabel?: string;
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
          {remainingLabel || remainingCount}
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
