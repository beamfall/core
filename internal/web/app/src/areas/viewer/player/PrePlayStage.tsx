import Hls from "hls.js";
import { useEffect, useRef, useState } from "react";
import type { PrePlayItem } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { fetchPrePlayStream } from "../lib/prePlay";

// The pre-play stage (CORE-1701): plays the server's pre-play queue (the
// Owner's intro clips, then any items an extension suggested) before the
// title starts, cinema-mode style. Skip moves to the next item, Skip all
// starts the title. Pre-play items report no progress, and an item that
// cannot play is passed over rather than blocking the title.
export function PrePlayStage({ items, onDone }: { items: PrePlayItem[]; onDone: () => void }) {
  const t = useT();
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const [index, setIndex] = useState(0);
  const item = items[index];

  useEffect(() => {
    if (!item) onDone();
  }, [item, onDone]);

  useEffect(() => {
    const video = videoRef.current;
    if (!item || !video) return;
    let cancelled = false;
    let hls: Hls | null = null;
    const advance = () => {
      if (!cancelled) setIndex((current) => current + 1);
    };
    fetchPrePlayStream(item.mediaID)
      .then((stream) => {
        if (cancelled) return;
        if (!stream?.url) {
          advance();
          return;
        }
        const nativeHLS = video.canPlayType("application/vnd.apple.mpegurl");
        const isApple = navigator.vendor === "Apple Computer, Inc.";
        if (stream.kind === "hls" && Hls.isSupported() && !(nativeHLS && isApple)) {
          hls = new Hls();
          hls.on(Hls.Events.ERROR, (_event, data) => {
            if (data.fatal) advance();
          });
          hls.loadSource(stream.url);
          hls.attachMedia(video);
        } else if (stream.kind === "hls" && !nativeHLS) {
          advance();
          return;
        } else {
          video.src = stream.url;
        }
        void video.play()?.catch(() => {});
      })
      .catch(advance);
    return () => {
      cancelled = true;
      hls?.destroy();
      video.removeAttribute("src");
      video.load?.();
    };
  }, [item]);

  if (!item) return null;
  return (
    <section className="bf-pre-play relative flex flex-1 flex-col" aria-label={t("prePlayRegion")}>
      <video
        ref={videoRef}
        className="h-full w-full flex-1 bg-black"
        playsInline
        autoPlay
        onEnded={() => setIndex((current) => current + 1)}
        onError={() => setIndex((current) => current + 1)}
      />
      <div className="absolute inset-x-0 bottom-0 flex flex-wrap items-center gap-3 bg-black/60 px-4 py-3 text-white">
        <span className="min-w-0 flex-1 break-words">
          {item.label ? <span className="mr-2 text-white/70">{item.label}</span> : null}
          {item.title}
        </span>
        <span className="text-sm text-white/70" aria-live="polite">
          {t("prePlayProgress", { index: index + 1, count: items.length })}
        </span>
        <button
          type="button"
          className="bf-control"
          onClick={() => setIndex((current) => current + 1)}
        >
          {t("prePlaySkip")}
        </button>
        <button type="button" className="bf-control is-primary" onClick={onDone}>
          {t("prePlaySkipAll")}
        </button>
      </div>
    </section>
  );
}
