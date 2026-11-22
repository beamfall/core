import { type RefObject, useEffect, useRef, useState } from "react";
import { useT } from "@/lib/i18n";
import { fetchMediaLyrics, type MediaLyrics } from "../lib/mediaDetail";
import { usePrefersReducedMotion } from "./trackChangeMotion";

/** After the listener scrolls the lyrics, the view stops following playback for this long. */
const MANUAL_SCROLL_HOLD_MS = 3000;

type Loaded = { mediaID: string; lyrics: MediaLyrics | null };

/**
 * The lyrics beside the audio stage's artwork (CORE-1638). Synced lyrics brighten the current
 * line, dim the rest and keep it centred; a click on a line plays from there. Plain lyrics show
 * as text. A track without lyrics, or a failed read, shows a quiet "No lyrics".
 */
export function LyricsPanel({
  mediaID,
  videoRef,
  songScale = 1,
}: {
  mediaID: string;
  videoRef?: RefObject<HTMLVideoElement | null>;
  /** Song seconds per stream second under an applied tempo. */
  songScale?: number;
}) {
  const t = useT();
  const [loaded, setLoaded] = useState<Loaded | null>(null);
  useEffect(() => {
    if (!mediaID) return;
    const controller = new AbortController();
    fetchMediaLyrics(mediaID, controller.signal).then(
      (lyrics) => setLoaded({ mediaID, lyrics }),
      () => {
        if (!controller.signal.aborted) setLoaded({ mediaID, lyrics: null });
      },
    );
    return () => controller.abort();
  }, [mediaID]);

  const lyrics = loaded?.mediaID === mediaID ? loaded.lyrics : undefined;
  const synced = !!lyrics?.synced;
  const lines = lyrics?.lines ?? [];
  const active = useActiveLine(synced ? lines : null, videoRef, songScale);
  const listRef = useFollowActiveLine(active);

  if (lyrics === undefined) return <div className="bf-lyrics" aria-busy="true" />;
  if (!lines.some((line) => line.text.trim())) {
    return (
      <div className="bf-lyrics is-empty">
        <p className="bf-lyrics-none">{t("lyricsNone")}</p>
      </div>
    );
  }

  return (
    <section aria-label={t("lyrics")} className="bf-lyrics">
      <div ref={listRef} className={`bf-lyrics-scroll${synced ? " is-synced" : ""}`}>
        {lines.map((line, index) => {
          const key = `${index}:${line.startMs ?? ""}`;
          if (!synced) {
            return (
              <p key={key} className="bf-lyrics-line">
                {line.text || " "}
              </p>
            );
          }
          const state = index === active ? "current" : index < active ? "past" : "upcoming";
          return (
            <button
              key={key}
              type="button"
              data-state={state}
              aria-current={index === active || undefined}
              className="bf-lyrics-line"
              onClick={() => {
                const video = videoRef?.current;
                if (video && line.startMs != null)
                  video.currentTime = line.startMs / 1000 / songScale;
              }}
            >
              {line.text || " "}
            </button>
          );
        })}
      </div>
    </section>
  );
}

/**
 * The index of the last synced line that has started, or -1 before the first. It follows the
 * playing element on every frame while it plays, and on seeks while paused.
 */
function useActiveLine(
  lines: MediaLyrics["lines"] | null,
  videoRef: RefObject<HTMLVideoElement | null> | undefined,
  songScale: number,
): number {
  const [active, setActive] = useState(-1);
  useEffect(() => {
    const video = videoRef?.current;
    if (!lines || !video) {
      setActive(-1);
      return;
    }
    let frame = 0;
    const update = () => setActive(lineAt(lines, video.currentTime * songScale * 1000));
    const tick = () => {
      update();
      frame = video.paused ? 0 : requestAnimationFrame(tick);
    };
    const start = () => {
      if (!frame) frame = requestAnimationFrame(tick);
    };
    update();
    if (!video.paused) start();
    video.addEventListener("play", start);
    video.addEventListener("seeked", update);
    video.addEventListener("timeupdate", update);
    return () => {
      cancelAnimationFrame(frame);
      video.removeEventListener("play", start);
      video.removeEventListener("seeked", update);
      video.removeEventListener("timeupdate", update);
    };
  }, [lines, videoRef, songScale]);
  return active;
}

/** Binary search for the last line starting at or before ms. */
export function lineAt(lines: MediaLyrics["lines"], ms: number): number {
  let low = 0;
  let high = lines.length - 1;
  let found = -1;
  while (low <= high) {
    const mid = (low + high) >> 1;
    if ((lines[mid]?.startMs ?? 0) <= ms) {
      found = mid;
      low = mid + 1;
    } else {
      high = mid - 1;
    }
  }
  return found;
}

/** Keeps the current line centred unless the listener scrolled the lyrics a moment ago. */
function useFollowActiveLine(active: number) {
  const listRef = useRef<HTMLDivElement | null>(null);
  const manualUntil = useRef(0);
  const reduced = usePrefersReducedMotion();
  useEffect(() => {
    const list = listRef.current;
    if (!list) return;
    const hold = () => {
      manualUntil.current = Date.now() + MANUAL_SCROLL_HOLD_MS;
    };
    list.addEventListener("wheel", hold, { passive: true });
    list.addEventListener("touchmove", hold, { passive: true });
    return () => {
      list.removeEventListener("wheel", hold);
      list.removeEventListener("touchmove", hold);
    };
  });
  useEffect(() => {
    const list = listRef.current;
    const line = list?.children[active] as HTMLElement | undefined;
    if (!list || !line || Date.now() < manualUntil.current) return;
    const top = line.offsetTop - list.clientHeight / 2 + line.offsetHeight / 2;
    list.scrollTo?.({ top: Math.max(0, top), behavior: reduced ? "auto" : "smooth" });
  }, [active, reduced]);
  return listRef;
}
