import { Check, Play } from "lucide-react";
import type { PointerEvent } from "react";
import { useCallback, useEffect, useRef, useState } from "react";
import { Link, matchPath, useLocation, useNavigate } from "react-router";
import { useCardImageSizes } from "@/components/chrome/posterSize";
import type { SourceItem } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { useViewerIdentity } from "@/lib/session";
import { itemTargetFor } from "../actions";
import { activateLink } from "../lib/activateLink";
import { artworkBucketURL, artworkSlotURL, artworkSrcSet } from "../lib/artwork";
import { artFrameClass } from "../lib/cardArt";
import { cardAccessibleName, cardRating, cardState } from "../lib/cardState";
import { cardHeading, cardSubline, showsAudioEntityDetails } from "../lib/cardTitles";
import { entityLinkState } from "../lib/entityPreview";
import type { LibraryViewMode } from "../lib/libraryBrowse";
import { mediaPath } from "../lib/mediaAddress";
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

// One catalog card, ported from the vanilla renderItem (app.js). Generic by
// invariant #1: the wire entity is Media and `kind` is a plugin display
// discriminant — a "media" item plays, anything else opens entity detail. The
// poster uses a 2:3 or square frame (cardArt.ts) until its artwork loads.
// Home cards then fit the artwork's natural ratio. A tinted glyph fallback
// shows when artwork is absent or fails to load. Titles and
// summaries render as React text only (invariant #5 / ADR-0012 §5).
//
// The card link is the only control that carries the item's name: its
// accessible name is the title plus the acting Profile's state, and the same
// state is drawn on the artwork (progress bar, finished mark, remaining count).
// It is an anchor with an href (CORE-2511), so a modifier or middle click opens
// the page in a new tab. Play and More are sibling controls outside the link.
export function MediaItemCard({
  item,
  posInSet,
  setSize,
  focusAnchor,
  queue,
  playOnActivate,
  showPlayButton = false,
  showYear = false,
  display = "poster",
  nameContainer = true,
  intrinsicArtwork = false,
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
  /** Child Media have a Play button alongside the detail link. */
  showPlayButton?: boolean;
  /** The title repeats in this list, so the card shows its year (CORE-2320). */
  showYear?: boolean;
  /**
   * How the Library draws its items (CORE-2386): a poster, a landscape
   * thumbnail (the thumb slot, else the poster), a compact list row with
   * the item's ordinal and runtime, or a dense table row without artwork that
   * also states the year (CORE-2443).
   */
  display?: LibraryViewMode;
  /**
   * Head the card with the container holding the item (CORE-2524); false
   * inside a container's own page, where it goes without saying.
   */
  nameContainer?: boolean;
  intrinsicArtwork?: boolean;
}) {
  const navigate = useNavigate();
  const cardImageSizes = useCardImageSizes();
  const identity = useViewerIdentity();
  const t = useT();
  const displayLocale = useDisplayLocale();
  const location = useLocation();
  const [failedPoster, setFailedPoster] = useState("");
  const [previewClip, setPreviewClip] = useState("");
  const isMedia = item.kind === "media";
  const hasPlayButton = showPlayButton && isMedia && !item.locked;
  const state = cardState(item, t);
  const rating = isMedia ? cardRating(item, displayLocale, t) : undefined;
  const isTable = display === "table";
  const { heading, lead } = cardHeading(item, nameContainer, displayLocale);
  const subline = cardSubline(item, showYear || isTable, displayLocale, lead);
  const featureAudioDetails = showsAudioEntityDetails(item);
  const isRow = display === "list" || isTable;
  const wide = display === "thumbnail";
  // The page the card's own activation opens (and the More menu's copied link).
  const path = `/${isMedia ? (playOnActivate ? "watch" : "media") : "entity"}/${encodeURIComponent(item.id)}`;
  const primaryLabel = isMedia
    ? t(state.progressPercent === undefined ? "play" : "cardResume")
    : t("cardOpen");
  const poster = wide
    ? artworkSlotURL(item, "thumb") || artworkSlotURL(item, "poster")
    : artworkSlotURL(item, "poster") ||
      artworkSlotURL(item, "thumb") ||
      (intrinsicArtwork ? artworkSlotURL(item, "backdrop") || artworkSlotURL(item, "banner") : "");
  const frameClass = wide ? "bf-poster bf-poster-wide" : artFrameClass(item);
  const showArtwork = poster && failedPoster !== poster;
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

  // Carry the title forward so the player's info overlay can name the Media
  // (the playback-info wire does not report it).
  function playState(): PlayerNavState {
    return { title: item.title, ...queueState() };
  }

  function play() {
    navigate(mediaPath("watch", item.id), { state: playState() });
  }

  // The navigation state the card's link carries to the page it opens.
  function linkState(): unknown {
    if (isMedia && playOnActivate) return playState();
    if (isMedia) {
      const link = mediaLinkState(item, identity, focusAnchor?.section);
      return link ? { ...link, ...queueState() } : undefined;
    }
    return entityLinkState(item, identity);
  }

  function open() {
    navigate(path, { state: linkState() });
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

  function handlePointerEnter(event: PointerEvent<HTMLAnchorElement>) {
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

  function canStartPreview(event?: PointerEvent<HTMLAnchorElement>) {
    if (!isMedia) {
      return false;
    }
    if (event?.pointerType && event.pointerType !== "mouse") {
      return false;
    }
    return mediaQueryMatches(POINTER_PREVIEW_QUERY) && !mediaQueryMatches(REDUCED_MOTION_QUERY);
  }

  // Search results lack lock information. Their queue actions are tracked in CORE-2867.
  const queueUnlocked = !matchPath("/search", location.pathname) && !item.locked;
  const menu = !(showPlayButton && isMedia && item.locked) && (
    <MediaItemCardMenu
      // The actions target this item, not the container its card is headed by.
      title={item.title}
      primaryLabel={primaryLabel}
      isMedia={isMedia}
      isAudio={item.mediaClass === "audio"}
      queueUnlocked={queueUnlocked}
      containerID={item.container?.id}
      href={new URL(`${import.meta.env.BASE_URL}${path.slice(1)}`, window.location.origin).href}
      target={itemTargetFor(item)}
      onPrimary={isMedia ? play : open}
      onDetails={
        isMedia
          ? () =>
              navigate(mediaPath("media", item.id), {
                state: {
                  ...mediaLinkState(item, identity, focusAnchor?.section),
                  ...queueState(),
                },
              })
          : undefined
      }
    />
  );

  return (
    <li
      className={`bf-card-shell list-none${hasPlayButton ? " bf-card-shell-with-play" : ""}`}
      aria-posinset={posInSet}
      aria-setsize={setSize}
    >
      <Link
        to={path}
        state={showPlayButton ? undefined : linkState()}
        onClick={showPlayButton ? (event) => activateLink(event, open) : undefined}
        aria-label={cardAccessibleName(
          {
            ...item,
            title: cardLabelTitle(
              item,
              heading,
              showYear && !featureAudioDetails,
              featureAudioDetails ? subline : lead,
            ),
          },
          state,
          rating,
        )}
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
        {!isTable && (
          <div
            className={
              intrinsicArtwork && showArtwork ? `${frameClass} bf-poster-intrinsic` : frameClass
            }
          >
            {showArtwork ? (
              <img
                key={poster}
                alt=""
                loading="lazy"
                decoding="async"
                src={artworkBucketURL(poster, "w320")}
                srcSet={artworkSrcSet(poster, CARD_BUCKETS)}
                sizes={cardImageSizes}
                onError={() => setFailedPoster(poster)}
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
        )}
        <div className="bf-card-copy">
          <span className="bf-card-title">{heading}</span>
          {subline && <span className="bf-card-sub">{subline}</span>}
          {!isRow && isMedia && (item.durationSeconds ?? 0) > 0 && (
            <span aria-hidden="true" className="bf-card-sub bf-card-runtime">
              {formatDuration(item.durationSeconds ?? 0)}
            </span>
          )}
          {rating && (
            <span aria-hidden="true" className="bf-card-rating">
              {rating.text && <span className="bf-card-cert">{rating.text}</span>}
              {rating.score && <span>{`\u2605 ${rating.score}`}</span>}
            </span>
          )}
        </div>
        {isRow && isMedia && item.durationSeconds ? (
          <span aria-hidden="true" className="bf-card-runtime">
            {formatDuration(item.durationSeconds)}
          </span>
        ) : null}
      </Link>
      {hasPlayButton ? (
        <div className="bf-child-actions">
          <button
            type="button"
            className="bf-row-play"
            aria-label={t("mediaDetailPlayLabel", { title: item.title })}
            onClick={play}
            onPointerEnter={preloadPlayer}
            onFocus={preloadPlayer}
          >
            <Play aria-hidden />
          </button>
          {menu}
        </div>
      ) : (
        menu
      )}
    </li>
  );
}

// The title a card's accessible name starts with: its heading, with the year
// when the title repeats in the list (CORE-2320), then the line that leads under
// it, so siblings that share a heading are told apart by ear as by eye
// (CORE-1226, CORE-2524).
function cardLabelTitle(
  item: SourceItem,
  heading: string,
  showYear: boolean,
  lead: string,
): string {
  const title = showYear && item.year ? `${heading} (${item.year})` : heading;
  return lead ? `${title}, ${lead}` : title;
}

// The state drawn on the artwork. Decorative for assistive technology: the
// card link's accessible name already says the same thing in words.
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
