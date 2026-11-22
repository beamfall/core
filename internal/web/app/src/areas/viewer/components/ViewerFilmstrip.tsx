import { useEffect, useRef, useState } from "react";
import { useT } from "@/lib/i18n";
import { artworkBucketURL } from "../lib/artwork";
import { fetchViewerThumb, isClipClass, type ViewerThumb } from "../lib/imageViewer";
import { formatDuration } from "../lib/trackList";
import type { PlayQueueItem } from "../lib/watchQueue";
import { PosterGlyph } from "./PosterGlyph";

// The image viewer's filmstrip (CORE-2978, photos design §3.1 W3): thumbnails
// of the items around the current one. Each thumbnail's artwork and class come
// from a small media(id) read, fetched once per item while the strip is
// mounted; the learned class goes back to the viewer so an item that turns out
// not to be viewable drops out of stepping. The strip scrolls the current item
// into view without animation (effects come only from Oscillux).

export function ViewerFilmstrip({
  items,
  currentId,
  onSelect,
  onClass,
}: {
  items: readonly PlayQueueItem[];
  currentId: string;
  onSelect: (item: PlayQueueItem) => void;
  /** Reports an item's class once its thumbnail read returns. */
  onClass: (id: string, mediaClass: string) => void;
}) {
  const t = useT();
  const [thumbs, setThumbs] = useState<Record<string, ViewerThumb | null>>({});
  const [failed, setFailed] = useState<ReadonlySet<string>>(() => new Set());
  const requested = useRef(new Set<string>());
  const controller = useRef<AbortController | null>(null);
  const onClassRef = useRef(onClass);
  onClassRef.current = onClass;
  const currentRef = useRef<HTMLButtonElement | null>(null);

  useEffect(() => {
    const active = new AbortController();
    controller.current = active;
    return () => {
      active.abort();
      // Aborted reads are asked again if the strip mounts again (Strict Mode).
      requested.current.clear();
    };
  }, []);

  useEffect(() => {
    const signal = controller.current?.signal;
    if (!signal || signal.aborted) return;
    for (const item of items) {
      if (requested.current.has(item.id)) continue;
      requested.current.add(item.id);
      fetchViewerThumb(item.id, signal).then(
        (thumb) => {
          if (signal.aborted) return;
          setThumbs((current) => ({ ...current, [item.id]: thumb }));
          if (thumb?.mediaClass) onClassRef.current(item.id, thumb.mediaClass);
        },
        () => {
          if (!signal.aborted) setThumbs((current) => ({ ...current, [item.id]: null }));
        },
      );
    }
  }, [items]);

  // Follow the current item: a jump or a step scrolls its thumbnail into view.
  useEffect(() => {
    currentRef.current?.scrollIntoView?.({ block: "nearest", inline: "center" });
  }, [currentId]);

  return (
    <nav
      className="bf-image-viewer-filmstrip"
      aria-label={t("imageViewerFilmstrip")}
      onWheel={(event) => event.stopPropagation()}
      onDoubleClick={(event) => event.stopPropagation()}
    >
      <ul>
        {items.map((item, i) => {
          const thumb = thumbs[item.id];
          const mediaClass = thumb?.mediaClass ?? item.mediaClass;
          const clip = isClipClass(mediaClass);
          const current = item.id === currentId;
          const artwork = thumb?.artwork && !failed.has(thumb.artwork) ? thumb.artwork : "";
          return (
            <li key={item.id}>
              <button
                type="button"
                ref={current ? currentRef : undefined}
                className="bf-image-viewer-thumb"
                aria-current={current ? "true" : undefined}
                aria-label={item.title || t("imageViewerItem", { position: String(i + 1) })}
                onClick={() => {
                  if (!current) onSelect(item);
                }}
              >
                {artwork ? (
                  <img
                    src={artworkBucketURL(artwork, "w320")}
                    alt=""
                    loading="lazy"
                    decoding="async"
                    draggable={false}
                    onError={() => setFailed((set) => new Set(set).add(artwork))}
                  />
                ) : (
                  <PosterGlyph mediaClass={mediaClass ?? "image"} clip={clip} />
                )}
                {clip && thumb?.durationSeconds ? (
                  <span className="bf-image-viewer-thumb-duration" aria-hidden="true">
                    {formatDuration(thumb.durationSeconds)}
                  </span>
                ) : null}
              </button>
            </li>
          );
        })}
      </ul>
    </nav>
  );
}
