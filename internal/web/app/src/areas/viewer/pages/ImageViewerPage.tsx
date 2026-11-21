import {
  ChevronLeft,
  ChevronRight,
  Heart,
  ImageIcon,
  Info,
  LoaderCircle,
  RotateCw,
  X,
  ZoomIn,
  ZoomOut,
} from "lucide-react";
import { type PointerEvent, useCallback, useEffect, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router";
import { MAIN_CONTENT_ID } from "@/components/chrome/SkipLink";
import { displayString } from "@/lib/display";
import { useDocumentTitle } from "@/lib/documentTitle";
import type { EntityFieldInfo, GenericMediaInfo } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { useSession, useViewerIdentity } from "@/lib/session";
import { NotAvailable } from "../components/NotAvailable";
import { artworkBucketURL } from "../lib/artwork";
import {
  clampZoom,
  fetchImageOriginal,
  imageNeighbours,
  isImageClass,
  nextZoom,
  setMediaFavorite,
} from "../lib/imageViewer";
import { mediaPath } from "../lib/mediaAddress";
import { fetchMedia, fetchMediaDetailFields } from "../lib/mediaDetail";
import { endPlayback } from "../lib/playback";
import { useCanonicalMediaAddress, useMediaAddress } from "../lib/useCanonicalMediaAddress";
import "./image-viewer.css";
import { fieldDisplayValue, viewerFacingFields } from "../lib/viewerFields";
import { type PlayerNavState, type PlayQueueItem, readPlayerNavState } from "../lib/watchQueue";

// The web image viewer (/view/:address, CORE-0542, photos design §3.1 W3).
// It shows the poster derivative at once and fetches the original through the
// image playback posture (a direct stream) on the first zoom; the original
// replaces the derivative only once it has decoded, so a RAW or HEIC original
// the browser cannot draw leaves the derivative up with a notice. Prev and
// next step through the image items of the queue the opening card carried.

type Loaded = { id: string; media: GenericMediaInfo | null };
type Original = { id: string; url: string; state: "loading" | "ready" | "failed" };

const SWIPE_PX = 48;

export function ImageViewerPage() {
  const t = useT();
  const locale = useDisplayLocale();
  const navigate = useNavigate();
  const location = useLocation();
  const identity = useViewerIdentity();
  const { revealed } = useSession();
  const { id: mediaId } = useMediaAddress();
  const navState = readPlayerNavState(location.state, identity);
  const [loaded, setLoaded] = useState<Loaded | null>(null);
  const [failed, setFailed] = useState(false);
  const [favorite, setFavorite] = useState<{ id: string; value: boolean } | null>(null);
  const [original, setOriginal] = useState<Original | null>(null);
  const [scale, setScale] = useState(1);
  const [offset, setOffset] = useState({ x: 0, y: 0 });
  const [rotation, setRotation] = useState(0);
  const [infoOpen, setInfoOpen] = useState(false);
  const [fields, setFields] = useState<{ id: string; list: EntityFieldInfo[] } | null>(null);
  const drag = useRef<{ x: number; y: number; ox: number; oy: number; moved: boolean } | null>(
    null,
  );

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
  }, [mediaId]);

  useDocumentTitle(revealed ? null : media?.title || navState.title || "", t("brand"));
  useCanonicalMediaAddress("view", media, isImageClass(media?.mediaClass));

  // An item the server classes as something else plays where it belongs.
  const wrongClass = !!media && !isImageClass(media.mediaClass);
  useEffect(() => {
    if (wrongClass && mediaId) {
      navigate(mediaPath("watch", mediaId), { replace: true, state: location.state });
    }
  }, [wrongClass, mediaId, navigate, location.state]);

  // The original's stream is a playback session on the server: close it when
  // the viewer leaves this item.
  const originalID = original?.id;
  useEffect(() => {
    if (!originalID) return;
    return () => endPlayback(originalID);
  }, [originalID]);

  const ensureOriginal = useCallback(() => {
    if (!mediaId || (original && original.id === mediaId)) return;
    const id = mediaId;
    setOriginal({ id, url: "", state: "loading" });
    fetchImageOriginal(id).then(
      (url) =>
        setOriginal((current) =>
          current?.id === id ? { id, url, state: url ? "loading" : "failed" } : current,
        ),
      () =>
        setOriginal((current) => (current?.id === id ? { id, url: "", state: "failed" } : current)),
    );
  }, [mediaId, original]);

  const zoomTo = useCallback(
    (next: number) => {
      const clamped = clampZoom(next);
      setScale(clamped);
      if (clamped === 1) setOffset({ x: 0, y: 0 });
      else ensureOriginal();
    },
    [ensureOriginal],
  );

  const { previous, next, position, total } = imageNeighbours(navState.queue, mediaId ?? "");
  const step = useCallback(
    (target: PlayQueueItem | undefined) => {
      if (!target) return;
      const state: PlayerNavState = { title: target.title };
      if (navState.queue && navState.queueIdentity) {
        state.queue = navState.queue;
        state.queueIdentity = navState.queueIdentity;
      }
      navigate(mediaPath("view", target.id), { replace: true, state });
    },
    [navigate, navState.queue, navState.queueIdentity],
  );

  const close = useCallback(() => {
    // "default" is the entry the app was loaded on: nothing in-app to return to.
    if (location.key !== "default") navigate(-1);
    else navigate("/");
  }, [location.key, navigate]);

  const favorited = favorite && favorite.id === mediaId ? favorite.value : !!media?.favorited;
  const toggleFavorite = useCallback(() => {
    if (!mediaId || !media) return;
    const id = mediaId;
    const wanted = !favorited;
    setFavorite({ id, value: wanted });
    setMediaFavorite(id, wanted).then(
      (stored) => setFavorite((current) => (current?.id === id ? { id, value: stored } : current)),
      () => setFavorite((current) => (current?.id === id ? { id, value: !wanted } : current)),
    );
  }, [favorited, media, mediaId]);

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
      if (event.defaultPrevented || event.metaKey || event.ctrlKey || event.altKey) return;
      const target = event.target as HTMLElement | null;
      if (target?.closest("input, textarea, select, [contenteditable='true']")) return;
      switch (event.key) {
        case "ArrowLeft":
          step(previous);
          break;
        case "ArrowRight":
          step(next);
          break;
        case "z":
        case "Z":
        case "+":
        case "=":
          zoomTo(nextZoom(scale));
          break;
        case "0":
          zoomTo(1);
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
  }, [close, infoOpen, next, previous, scale, step, toggleFavorite, zoomTo]);

  function onPointerDown(event: PointerEvent<HTMLDivElement>) {
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
      step(dx < 0 ? next : previous);
    }
  }

  if (mediaId === "" || (resolved && !media)) {
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
  const title = media?.title || navState.title || "";
  const shownFields =
    fields && fields.id === mediaId
      ? viewerFacingFields(fields.list, { title: media?.title, year: media?.year })
      : null;

  return (
    <main
      id={MAIN_CONTENT_ID}
      className="bf-image-viewer"
      aria-label={t("imageViewerRegion")}
      data-zoomed={scale > 1 ? "true" : undefined}
    >
      <header className="bf-image-viewer-bar">
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
        <button
          type="button"
          className="bf-image-viewer-button"
          onClick={toggleFavorite}
          disabled={!media}
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
            {originalHere?.url && originalHere.state !== "failed" && (
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
          {(!resolved || (scale > 1 && originalHere?.state === "loading")) && !failed && (
            <div className="bf-image-viewer-status" role="status">
              <LoaderCircle aria-hidden className="size-6 animate-spin" />
              <span className="sr-only">{t("imageViewerLoading")}</span>
            </div>
          )}
          {(failed || originalHere?.state === "failed") && (
            <p className="bf-image-viewer-notice" role="status">
              {t("imageViewerOriginalUnavailable")}
            </p>
          )}
          {previous && (
            <button
              type="button"
              className="bf-image-viewer-button bf-image-viewer-step"
              data-side="previous"
              onClick={() => step(previous)}
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
              onClick={() => step(next)}
              aria-label={t("imageViewerNext")}
            >
              <ChevronRight aria-hidden />
            </button>
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
    </main>
  );
}
