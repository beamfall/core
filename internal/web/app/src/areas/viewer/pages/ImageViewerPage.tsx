import {
  ChevronLeft,
  ChevronRight,
  Heart,
  ImageIcon,
  Info,
  LoaderCircle,
  Pause,
  Play,
  RotateCw,
  Trash2,
  X,
  ZoomIn,
  ZoomOut,
} from "lucide-react";
import { type PointerEvent, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router";
import { MAIN_CONTENT_ID } from "@/components/chrome/SkipLink";
import { ApiError, resetIdentityEpoch } from "@/lib/api";
import { displayString } from "@/lib/display";
import { useDocumentTitle } from "@/lib/documentTitle";
import { restoreMedia } from "@/lib/graphql";
import type { EntityFieldInfo, GenericMediaInfo } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { useSession, useViewerIdentity } from "@/lib/session";
import { NotAvailable } from "../components/NotAvailable";
import { ViewerFilmstrip } from "../components/ViewerFilmstrip";
import { artworkBucketURL } from "../lib/artwork";
import {
  clampZoom,
  fetchDirectStream,
  filmstripWindow,
  isClipClass,
  isImageClass,
  nextZoom,
  setMediaFavorite,
  trashMedia,
  trashStepTarget,
  viewerNeighbours,
} from "../lib/imageViewer";
import { mediaPath } from "../lib/mediaAddress";
import { fetchMedia, fetchMediaDetailFields } from "../lib/mediaDetail";
import { endPlayback, isRevealTerminalStatus } from "../lib/playback";
import { useCanonicalMediaAddress, useMediaAddress } from "../lib/useCanonicalMediaAddress";
import "./image-viewer.css";
import { fieldDisplayValue, viewerFacingFields } from "../lib/viewerFields";
import {
  type PlayerNavState,
  type PlayQueueItem,
  readPlayerNavState,
  withQueueClasses,
} from "../lib/watchQueue";

// The web image viewer (/view/:address, CORE-0542, photos design §3.1 W3).
// It shows the poster derivative at once and fetches the original through the
// image playback posture (a direct stream) on the first zoom; the original
// replaces the derivative only once it has decoded, so a RAW or HEIC original
// the browser cannot draw leaves the derivative up with a notice. Prev and
// next, swipes and the filmstrip step through the images and camera-roll clips
// of the queue the opening card carried (CORE-2978); a clip plays muted and
// inline from its direct stream (CORE-2977). On a manager device, Delete moves
// the item to the server trash and offers Undo. The chrome (bar, step buttons,
// filmstrip) hides when the pointer goes idle; show and hide are plain CSS,
// with no transition (effects come only from Oscillux).

type Loaded = { id: string; media: GenericMediaInfo | null };
type Original = { id: string; url: string; state: "loading" | "ready" | "failed" };
type Toast = { kind: "trashed" | "trashFailed" | "restoreFailed"; id: string; title: string };

const SWIPE_PX = 48;
const PAN_PX = 64;
const CHROME_IDLE_MS = 3000;
const TOAST_MS = 8000;

export function ImageViewerPage() {
  const t = useT();
  const locale = useDisplayLocale();
  const navigate = useNavigate();
  const location = useLocation();
  const identity = useViewerIdentity();
  const { revealed, isManager } = useSession();
  const { id: mediaId } = useMediaAddress();
  const navState = useMemo(
    () => readPlayerNavState(location.state, identity),
    [location.state, identity],
  );
  const [loaded, setLoaded] = useState<Loaded | null>(null);
  const [failed, setFailed] = useState(false);
  const [favorite, setFavorite] = useState<{ id: string; value: boolean } | null>(null);
  // Items whose favourite change is in flight: a second toggle waits for the first.
  const [favoritePending, setFavoritePending] = useState<ReadonlySet<string>>(() => new Set());
  const favoriteInFlight = useRef(new Set<string>());
  const [original, setOriginal] = useState<Original | null>(null);
  const [scale, setScale] = useState(1);
  const [offset, setOffset] = useState({ x: 0, y: 0 });
  const [rotation, setRotation] = useState(0);
  const [infoOpen, setInfoOpen] = useState(false);
  const [fields, setFields] = useState<{ id: string; list: EntityFieldInfo[] } | null>(null);
  const drag = useRef<{ x: number; y: number; ox: number; oy: number; moved: boolean } | null>(
    null,
  );
  // The direction of the last step, so a neighbour that turns out not to be an
  // image is stepped over instead of opened in the player.
  const stepping = useRef<"previous" | "next" | null>(null);
  const [terminal, setTerminal] = useState(false);
  // Classes the filmstrip learned for queue items that did not carry one.
  const [learned, setLearned] = useState<Record<string, string>>({});
  // Items trashed during this visit, left out of stepping until restored.
  const [removed, setRemoved] = useState<ReadonlySet<string>>(() => new Set());
  const [toast, setToast] = useState<Toast | null>(null);
  const trashing = useRef(false);
  const [clipPlaying, setClipPlaying] = useState(false);
  const video = useRef<HTMLVideoElement | null>(null);
  const [chromeIdle, setChromeIdle] = useState(false);
  const [overChrome, setOverChrome] = useState(false);
  const idleTimer = useRef<number | undefined>(undefined);
  const currentId = useRef(mediaId);
  currentId.current = mediaId;

  const media = loaded && loaded.id === mediaId ? loaded.media : null;
  const resolved = !!loaded && loaded.id === mediaId;

  useEffect(() => {
    if (!mediaId) return;
    const controller = new AbortController();
    setFailed(false);
    fetchMedia(mediaId, controller.signal).then(
      (info) => {
        if (!controller.signal.aborted) setLoaded({ id: mediaId, media: info });
      },
      () => {
        if (!controller.signal.aborted) setFailed(true);
      },
    );
    return () => controller.abort();
  }, [mediaId]);

  // A new item opens fitted, upright and on its derivative.
  useEffect(() => {
    if (!mediaId) return;
    setScale(1);
    setOffset({ x: 0, y: 0 });
    setRotation(0);
    setClipPlaying(false);
  }, [mediaId]);

  const queue = useMemo(
    () => (navState.queue ? withQueueClasses(navState.queue, learned) : undefined),
    [navState.queue, learned],
  );
  const { items, index, previous, next, position, total } = viewerNeighbours(
    queue,
    mediaId ?? "",
    removed,
  );
  // A clip is viewable here only as part of the viewer's queue; a video opened
  // at /view on its own belongs to the player.
  const clip = !!media && isClipClass(media.mediaClass) && index >= 0;
  const viewable = !!media && (isImageClass(media.mediaClass) || clip);

  useDocumentTitle(revealed ? null : media?.title || "", t("brand"));
  useCanonicalMediaAddress("view", media, viewable);

  // An item the server classes as something else plays where it belongs.
  const wrongClass = !!media && !viewable;
  const stateFor = useCallback(
    (title: string): PlayerNavState => {
      const state: PlayerNavState = { title };
      if (navState.queue && navState.queueIdentity) {
        state.queue = navState.queue;
        state.queueIdentity = navState.queueIdentity;
      }
      return state;
    },
    [navState.queue, navState.queueIdentity],
  );
  const step = useCallback(
    (target: PlayQueueItem | undefined, direction: "previous" | "next") => {
      if (!target) return;
      stepping.current = direction;
      navigate(mediaPath("view", target.id), { replace: true, state: stateFor(target.title) });
    },
    [navigate, stateFor],
  );
  const jump = useCallback(
    (target: PlayQueueItem) => {
      const from = items.findIndex((item) => item.id === mediaId);
      const to = items.findIndex((item) => item.id === target.id);
      step(target, to < from ? "previous" : "next");
    },
    [items, mediaId, step],
  );
  const learnClass = useCallback((id: string, mediaClass: string) => {
    setLearned((current) => (current[id] ? current : { ...current, [id]: mediaClass }));
  }, []);
  useEffect(() => {
    if (media && !wrongClass) stepping.current = null;
  }, [media, wrongClass]);
  useEffect(() => {
    if (!wrongClass || !mediaId) return;
    const direction = stepping.current;
    if (direction) {
      // Stepped onto an unclassified clip: keep going, or turn back at the end.
      const onward = direction === "next" ? next : previous;
      const back = direction === "next" ? previous : next;
      if (onward) step(onward, direction);
      else step(back, direction === "next" ? "previous" : "next");
      return;
    }
    navigate(mediaPath("watch", mediaId), { replace: true, state: location.state });
  }, [wrongClass, mediaId, navigate, location.state, next, previous, step]);

  // The original's stream is a playback session on the server: close it when
  // the viewer leaves this item.
  const originalID = original?.id;
  useEffect(() => {
    if (!originalID) return;
    return () => endPlayback(originalID);
  }, [originalID]);

  const ensureOriginal = useCallback(() => {
    if (!mediaId || !media || (original && original.id === mediaId)) return;
    const id = mediaId;
    setOriginal({ id, url: "", state: "loading" });
    fetchDirectStream(id, media.mediaClass || "image").then(
      (url) =>
        setOriginal((current) =>
          current?.id === id ? { id, url, state: url ? "loading" : "failed" } : current,
        ),
      (err) => {
        if (err instanceof ApiError && isRevealTerminalStatus(err.status)) {
          // The server ended the revealed identity: drop the image, reset once.
          setTerminal(true);
          resetIdentityEpoch();
        }
        setOriginal((current) => (current?.id === id ? { id, url: "", state: "failed" } : current));
      },
    );
  }, [media, mediaId, original]);

  // A clip loads its stream on arrival and starts muted.
  useEffect(() => {
    if (clip) ensureOriginal();
  }, [clip, ensureOriginal]);

  const toggleClip = useCallback(() => {
    const element = video.current;
    if (!element) return;
    if (element.paused) element.play()?.catch(() => setClipPlaying(false));
    else element.pause();
  }, []);

  const zoomTo = useCallback(
    (next: number) => {
      if (clip) return;
      const clamped = clampZoom(next);
      setScale(clamped);
      if (clamped === 1) setOffset({ x: 0, y: 0 });
      else ensureOriginal();
    },
    [clip, ensureOriginal],
  );

  const close = useCallback(() => {
    // React Router numbers its history entries; idx 0 is where the app was
    // loaded (a replaced shared link included), so there is nothing to go back to.
    const idx = (window.history.state as { idx?: number } | null)?.idx ?? 0;
    if (idx > 0) navigate(-1);
    else navigate("/", { replace: true });
  }, [navigate]);

  const favorited = favorite && favorite.id === mediaId ? favorite.value : !!media?.favorited;
  const toggleFavorite = useCallback(() => {
    if (!mediaId || !media || favoriteInFlight.current.has(mediaId)) return;
    const id = mediaId;
    const wanted = !favorited;
    favoriteInFlight.current.add(id);
    setFavoritePending(new Set(favoriteInFlight.current));
    setFavorite({ id, value: wanted });
    setMediaFavorite(id, wanted)
      .then(
        (stored) =>
          setFavorite((current) => (current?.id === id ? { id, value: stored } : current)),
        () => setFavorite((current) => (current?.id === id ? { id, value: !wanted } : current)),
      )
      .finally(() => {
        favoriteInFlight.current.delete(id);
        setFavoritePending(new Set(favoriteInFlight.current));
      });
  }, [favorited, media, mediaId]);

  // Trash (manager role, as the trashMedia mutation is): move the item to the
  // server trash, step on, and offer Undo.
  const canTrash = isManager;
  const trash = useCallback(() => {
    if (!canTrash || !mediaId || !media || trashing.current) return;
    const id = mediaId;
    const title = media.title || "";
    const after = trashStepTarget(items, index);
    trashing.current = true;
    trashMedia(id)
      .then(
        () => {
          setRemoved((current) => new Set(current).add(id));
          setToast({ kind: "trashed", id, title });
          if (currentId.current !== id) return;
          if (after) step(after.target, after.direction);
          else close();
        },
        () => setToast({ kind: "trashFailed", id, title }),
      )
      .finally(() => {
        trashing.current = false;
      });
  }, [canTrash, close, index, items, media, mediaId, step]);

  const undoTrash = useCallback(
    (entry: Toast) => {
      setToast(null);
      restoreMedia(entry.id).then(
        () => {
          setRemoved((current) => {
            const left = new Set(current);
            left.delete(entry.id);
            return left;
          });
          stepping.current = null;
          navigate(mediaPath("view", entry.id), { replace: true, state: stateFor(entry.title) });
        },
        () => setToast({ ...entry, kind: "restoreFailed" }),
      );
    },
    [navigate, stateFor],
  );

  useEffect(() => {
    if (!toast) return;
    const timer = window.setTimeout(() => setToast(null), TOAST_MS);
    return () => window.clearTimeout(timer);
  }, [toast]);

  // The chrome hides after the pointer has been idle, and any pointer move or
  // key brings it back.
  const wakeChrome = useCallback(() => {
    setChromeIdle(false);
    window.clearTimeout(idleTimer.current);
    idleTimer.current = window.setTimeout(() => setChromeIdle(true), CHROME_IDLE_MS);
  }, []);
  useEffect(() => {
    wakeChrome();
    return () => window.clearTimeout(idleTimer.current);
  }, [wakeChrome]);
  const chromeHidden = chromeIdle && !overChrome && !infoOpen && !toast;

  useEffect(() => {
    if (!infoOpen || !mediaId || fields?.id === mediaId) return;
    const controller = new AbortController();
    fetchMediaDetailFields(mediaId, controller.signal).then(
      (list) => {
        if (!controller.signal.aborted) setFields({ id: mediaId, list });
      },
      () => {
        if (!controller.signal.aborted) setFields({ id: mediaId, list: [] });
      },
    );
    return () => controller.abort();
  }, [infoOpen, mediaId, fields?.id]);

  useEffect(() => {
    function onKey(event: KeyboardEvent) {
      wakeChrome();
      if (event.defaultPrevented || event.metaKey || event.ctrlKey || event.altKey) return;
      const target = event.target as HTMLElement | null;
      if (target?.closest("input, textarea, select, [contenteditable='true']")) return;
      switch (event.key) {
        case " ":
          // Space on a focused control presses it; elsewhere it plays a clip.
          if (!clip || target?.closest("button, a")) return;
          toggleClip();
          break;
        case "Delete":
        case "Backspace":
          if (!canTrash) return;
          trash();
          break;
        case "ArrowLeft":
        case "ArrowRight":
        case "ArrowUp":
        case "ArrowDown": {
          const horizontal = event.key === "ArrowLeft" || event.key === "ArrowRight";
          if (scale > 1) {
            // Zoomed in, the arrows pan the image instead of stepping.
            const sign = event.key === "ArrowLeft" || event.key === "ArrowUp" ? 1 : -1;
            setOffset((o) =>
              horizontal ? { x: o.x + sign * PAN_PX, y: o.y } : { x: o.x, y: o.y + sign * PAN_PX },
            );
          } else if (event.key === "ArrowLeft") step(previous, "previous");
          else if (event.key === "ArrowRight") step(next, "next");
          else return;
          break;
        }
        case "z":
        case "Z":
        case "+":
        case "=":
          zoomTo(nextZoom(scale));
          break;
        case "0":
          zoomTo(1);
          setRotation(0);
          break;
        case "r":
        case "R":
          setRotation((value) => (value + 90) % 360);
          break;
        case "f":
        case "F":
          toggleFavorite();
          break;
        case "i":
        case "I":
          setInfoOpen((open) => !open);
          break;
        case "Escape":
          if (infoOpen) setInfoOpen(false);
          else close();
          break;
        default:
          return;
      }
      event.preventDefault();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [
    canTrash,
    clip,
    close,
    infoOpen,
    next,
    previous,
    scale,
    step,
    toggleClip,
    toggleFavorite,
    trash,
    wakeChrome,
    zoomTo,
  ]);

  function onPointerDown(event: PointerEvent<HTMLDivElement>) {
    // A press on a stage control stays a click on that control.
    if ((event.target as HTMLElement | null)?.closest("button")) return;
    drag.current = { x: event.clientX, y: event.clientY, ox: offset.x, oy: offset.y, moved: false };
    event.currentTarget.setPointerCapture?.(event.pointerId);
  }
  function onPointerMove(event: PointerEvent<HTMLDivElement>) {
    const start = drag.current;
    if (!start) return;
    const dx = event.clientX - start.x;
    const dy = event.clientY - start.y;
    if (Math.abs(dx) > 4 || Math.abs(dy) > 4) start.moved = true;
    if (scale > 1) setOffset({ x: start.ox + dx, y: start.oy + dy });
  }
  function onPointerUp(event: PointerEvent<HTMLDivElement>) {
    const start = drag.current;
    drag.current = null;
    if (!start || scale > 1) return;
    const dx = event.clientX - start.x;
    if (Math.abs(dx) >= SWIPE_PX && Math.abs(dx) > Math.abs(event.clientY - start.y)) {
      if (dx < 0) step(next, "next");
      else step(previous, "previous");
    }
  }

  if (mediaId === "" || terminal || (resolved && !media)) {
    return (
      <main
        id={MAIN_CONTENT_ID}
        className="bf-image-viewer flex min-h-screen items-center justify-center"
      >
        <NotAvailable />
      </main>
    );
  }

  const slots = media?.artworkSlots ?? {};
  const poster = slots.poster || slots.thumb || "";
  const derivative = poster ? artworkBucketURL(poster, "w1280") : "";
  const originalHere = original && original.id === mediaId ? original : null;
  const showOriginal = originalHere?.state === "ready";
  const title = media?.title || "";
  const shownFields =
    fields && fields.id === mediaId
      ? viewerFacingFields(fields.list, { title: media?.title, year: media?.year })
      : null;
  const strip = filmstripWindow(items, index);
  const clipFailed = clip && originalHere?.state === "failed";

  return (
    <main
      id={MAIN_CONTENT_ID}
      className="bf-image-viewer"
      aria-label={t("imageViewerRegion")}
      data-zoomed={scale > 1 ? "true" : undefined}
      data-chrome={chromeHidden ? "hidden" : undefined}
      onPointerMove={wakeChrome}
    >
      <header
        className="bf-image-viewer-bar"
        onPointerEnter={() => setOverChrome(true)}
        onPointerLeave={() => setOverChrome(false)}
      >
        <button
          type="button"
          className="bf-image-viewer-button"
          onClick={close}
          aria-label={t("imageViewerClose")}
        >
          <X aria-hidden />
        </button>
        <div className="bf-image-viewer-title">
          <h1>{title}</h1>
          {total > 1 && (
            <p>{t("imageViewerPosition", { position: String(position), total: String(total) })}</p>
          )}
        </div>
        {clip ? (
          <button
            type="button"
            className="bf-image-viewer-button"
            onClick={toggleClip}
            disabled={originalHere?.state !== "ready"}
            aria-label={t(clipPlaying ? "imageViewerPause" : "imageViewerPlay")}
          >
            {clipPlaying ? <Pause aria-hidden /> : <Play aria-hidden />}
          </button>
        ) : (
          <>
            <button
              type="button"
              className="bf-image-viewer-button"
              onClick={() => zoomTo(scale > 1 ? 1 : nextZoom(scale))}
              aria-label={t(scale > 1 ? "imageViewerZoomReset" : "imageViewerZoomIn")}
            >
              {scale > 1 ? <ZoomOut aria-hidden /> : <ZoomIn aria-hidden />}
            </button>
            <button
              type="button"
              className="bf-image-viewer-button"
              onClick={() => setRotation((value) => (value + 90) % 360)}
              aria-label={t("imageViewerRotate")}
            >
              <RotateCw aria-hidden />
            </button>
          </>
        )}
        {canTrash && (
          <button
            type="button"
            className="bf-image-viewer-button"
            onClick={trash}
            disabled={!media}
            aria-label={t("imageViewerTrash")}
          >
            <Trash2 aria-hidden />
          </button>
        )}
        <button
          type="button"
          className="bf-image-viewer-button"
          onClick={toggleFavorite}
          disabled={!media || favoritePending.has(mediaId ?? "")}
          aria-pressed={favorited}
          aria-label={t(favorited ? "imageViewerUnfavorite" : "imageViewerFavorite")}
        >
          <Heart aria-hidden fill={favorited ? "currentColor" : "none"} />
        </button>
        <button
          type="button"
          className="bf-image-viewer-button"
          onClick={() => setInfoOpen((open) => !open)}
          aria-pressed={infoOpen}
          aria-label={t("imageViewerInfo")}
        >
          <Info aria-hidden />
        </button>
      </header>

      <div className="bf-image-viewer-body">
        <div
          className="bf-image-viewer-stage"
          data-testid="image-viewer-stage"
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onPointerUp={onPointerUp}
          onPointerCancel={() => {
            drag.current = null;
          }}
          onDoubleClick={() => zoomTo(scale > 1 ? 1 : 2)}
          onWheel={(event) => {
            if (event.deltaY === 0) return;
            zoomTo(scale * (event.deltaY < 0 ? 1.25 : 0.8));
          }}
        >
          <div
            className="bf-image-viewer-frame"
            style={{
              transform: `translate(${offset.x}px, ${offset.y}px) scale(${scale}) rotate(${rotation}deg)`,
            }}
          >
            {derivative && !showOriginal && (
              <img key={derivative} src={derivative} alt={title} draggable={false} />
            )}
            {clip && originalHere?.url && originalHere.state !== "failed" && (
              <video
                key={originalHere.url}
                ref={video}
                src={originalHere.url}
                aria-label={title}
                hidden={!showOriginal}
                muted
                playsInline
                autoPlay
                data-original="true"
                onPlay={() => setClipPlaying(true)}
                onPause={() => setClipPlaying(false)}
                onEnded={() => setClipPlaying(false)}
                onLoadedData={() =>
                  setOriginal((current) =>
                    current?.url === originalHere.url ? { ...current, state: "ready" } : current,
                  )
                }
                onError={() =>
                  setOriginal((current) =>
                    current?.url === originalHere.url ? { ...current, state: "failed" } : current,
                  )
                }
              />
            )}
            {!clip && originalHere?.url && originalHere.state !== "failed" && (
              <img
                key={originalHere.url}
                src={originalHere.url}
                alt={showOriginal ? title : ""}
                hidden={!showOriginal}
                draggable={false}
                data-original="true"
                onLoad={() =>
                  setOriginal((current) =>
                    current?.url === originalHere.url ? { ...current, state: "ready" } : current,
                  )
                }
                onError={() =>
                  setOriginal((current) =>
                    current?.url === originalHere.url ? { ...current, state: "failed" } : current,
                  )
                }
              />
            )}
            {media && !derivative && !showOriginal && (
              <ImageIcon
                aria-hidden
                data-glyph="image"
                strokeWidth={1.25}
                className="bf-image-viewer-glyph"
              />
            )}
          </div>
          {(!resolved ||
            ((scale > 1 || clip) && (!originalHere || originalHere.state === "loading"))) &&
            !failed && (
              <div className="bf-image-viewer-status" role="status">
                <LoaderCircle aria-hidden className="size-6 animate-spin" />
                <span className="sr-only">{t("imageViewerLoading")}</span>
              </div>
            )}
          {clipFailed ? (
            <div className="bf-image-viewer-notice" role="status">
              <p>{t("imageViewerClipUnavailable")}</p>
              <button
                type="button"
                className="bf-image-viewer-text-button"
                onClick={() =>
                  navigate(mediaPath("watch", mediaId ?? ""), { state: location.state })
                }
              >
                {t("imageViewerOpenInPlayer")}
              </button>
            </div>
          ) : (
            (failed || originalHere?.state === "failed") && (
              <p className="bf-image-viewer-notice" role="status">
                {t("imageViewerOriginalUnavailable")}
              </p>
            )
          )}
          {previous && (
            <button
              type="button"
              className="bf-image-viewer-button bf-image-viewer-step"
              data-side="previous"
              onClick={() => step(previous, "previous")}
              aria-label={t("imageViewerPrevious")}
            >
              <ChevronLeft aria-hidden />
            </button>
          )}
          {next && (
            <button
              type="button"
              className="bf-image-viewer-button bf-image-viewer-step"
              data-side="next"
              onClick={() => step(next, "next")}
              aria-label={t("imageViewerNext")}
            >
              <ChevronRight aria-hidden />
            </button>
          )}
          {strip.length > 1 && (
            <div
              className="bf-image-viewer-filmstrip-dock"
              onPointerEnter={() => setOverChrome(true)}
              onPointerLeave={() => setOverChrome(false)}
              onPointerDown={(event) => event.stopPropagation()}
            >
              <ViewerFilmstrip
                items={strip}
                currentId={mediaId ?? ""}
                onSelect={jump}
                onClass={learnClass}
              />
            </div>
          )}
        </div>

        {infoOpen && (
          <aside className="bf-image-viewer-info" aria-label={t("imageViewerInfo")}>
            <h2>{title}</h2>
            <dl>
              {media?.year ? (
                <div className="contents">
                  <dt>{t("itemMetadataYear")}</dt>
                  <dd>{media.year}</dd>
                </div>
              ) : null}
              {shownFields?.map((field, i) => (
                <div key={`${field.name}-${i}`} className="contents">
                  <dt>{displayString(field.label, locale)}</dt>
                  <dd>{fieldDisplayValue(field, locale)}</dd>
                </div>
              ))}
            </dl>
            {media?.summary && <p>{media.summary}</p>}
          </aside>
        )}
      </div>

      {toast && (
        <div className="bf-image-viewer-toast" role="status">
          <p>
            {t(
              toast.kind === "trashed"
                ? "imageViewerTrashed"
                : toast.kind === "trashFailed"
                  ? "imageViewerTrashFailed"
                  : "imageViewerRestoreFailed",
            )}
          </p>
          {toast.kind === "trashed" && (
            <button
              type="button"
              className="bf-image-viewer-text-button"
              onClick={() => undoTrash(toast)}
            >
              {t("imageViewerUndo")}
            </button>
          )}
          <button
            type="button"
            className="bf-image-viewer-button"
            onClick={() => setToast(null)}
            aria-label={t("imageViewerDismiss")}
          >
            <X aria-hidden />
          </button>
        </div>
      )}
    </main>
  );
}
