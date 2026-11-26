import { X } from "lucide-react";
import { useEffect, useRef } from "react";
import { useT } from "@/lib/i18n";
import { type ChapterEntry, formatClockTime } from "../lib/transport";

// The audio-stage chapters drawer: follows the right-edge drawer pattern of
// UpNextDrawer so standalone chaptered audio (such as audiobooks) surfaces its
// chapters with start times, durations, active highlight and click-to-seek.
export function ChaptersDrawer({
  chapters,
  onSeek,
  onClose,
}: {
  chapters: readonly ChapterEntry[];
  onSeek: (seconds: number) => void;
  onClose: () => void;
}) {
  const t = useT();
  const closeRef = useRef<HTMLButtonElement>(null);
  const currentRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    closeRef.current?.focus({ preventScroll: true });
    currentRef.current?.scrollIntoView?.({ block: "nearest" });
  }, []);

  return (
    <aside
      className="bf-queue-drawer bf-chapters-drawer"
      aria-label={t("chapters")}
      onKeyDown={(event) => {
        if (event.key === "Escape") {
          event.stopPropagation();
          onClose();
        }
      }}
    >
      <header className="bf-queue-drawer-head">
        <h2 className="bf-queue-drawer-heading">{t("chapters")}</h2>
        <button
          ref={closeRef}
          type="button"
          className="bf-control is-icon"
          aria-label={t("dismiss")}
          onClick={onClose}
        >
          <X aria-hidden="true" />
        </button>
      </header>
      <ol className="bf-queue-drawer-list">
        {chapters.map((chapter, index) => {
          const title = chapter.title || t("chapterNumber", { number: index + 1 });
          const start = formatClockTime(chapter.startSeconds);
          const duration =
            chapter.durationSeconds !== null &&
            chapter.durationSeconds !== undefined &&
            chapter.durationSeconds > 0
              ? formatClockTime(chapter.durationSeconds)
              : "";
          return (
            <li key={chapter.startSeconds}>
              <button
                ref={chapter.current ? currentRef : undefined}
                type="button"
                className="bf-queue-drawer-row bf-chapters-drawer-row"
                aria-current={chapter.current ? "true" : undefined}
                data-current={chapter.current ? "" : undefined}
                onClick={() => onSeek(chapter.startSeconds)}
              >
                <span className="bf-chapters-drawer-text">
                  <span className="bf-chapters-drawer-title" title={title}>
                    {title}
                  </span>
                  <span className="bf-chapters-drawer-meta">
                    <span>{start}</span>
                    {duration && (
                      <>
                        <span aria-hidden="true">·</span>
                        <span>{duration}</span>
                      </>
                    )}
                  </span>
                </span>
              </button>
            </li>
          );
        })}
      </ol>
    </aside>
  );
}
