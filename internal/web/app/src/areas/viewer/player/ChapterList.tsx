import { ListVideo } from "lucide-react";
import { type RefObject, useState } from "react";
import { Menu } from "@/components/ui/astryx-primitives";
import { useT } from "@/lib/i18n";
import {
  type ChapterEntry,
  formatClockTime,
  resolveTrickplaySheetURL,
  resolveTrickplayTile,
  type TrickplayManifest,
} from "../lib/transport";

const THUMB_WIDTH = 96;

// The transport's chapter list (CORE-1683): a menu of the item's chapters that
// seeks on select. Each row shows the server's chapter thumbnail, falling back
// to the trickplay tile at the chapter start, else no image. The menu portals
// into the stage layer so it shows in stage fullscreen.
export function ChapterList({
  chapters,
  trickplay,
  trickplayBaseURL,
  container,
  onSeek,
  onOpenChange,
}: {
  chapters: ChapterEntry[];
  trickplay: TrickplayManifest | null;
  trickplayBaseURL: string;
  container?: RefObject<HTMLElement | null>;
  onSeek: (seconds: number) => void;
  onOpenChange?: (open: boolean) => void;
}) {
  const t = useT();
  if (chapters.length === 0) return null;
  const showThumbs = chapters.some((chapter) => chapter.thumbnailURL) || trickplay !== null;

  return (
    <Menu.Root onOpenChange={(open) => onOpenChange?.(open)}>
      <Menu.Trigger aria-label={t("chapters")} className="bf-control is-icon is-chapter-list">
        <ListVideo aria-hidden="true" />
        <span className="sr-only">{t("chapters")}</span>
      </Menu.Trigger>
      <Menu.Portal container={container}>
        <Menu.Positioner side="top" align="start" sideOffset={10} className="bf-menu-positioner">
          <Menu.Popup className="bf-menu bf-chapter-menu" aria-label={t("chapters")}>
            {chapters.map((chapter, index) => {
              const title = chapter.title || t("chapterNumber", { number: index + 1 });
              return (
                <Menu.Item
                  key={chapter.startSeconds}
                  className="bf-chapter-row"
                  aria-current={chapter.current ? "true" : undefined}
                  data-current={chapter.current ? "" : undefined}
                  onClick={() => onSeek(chapter.startSeconds)}
                >
                  {showThumbs && (
                    <ChapterThumb
                      chapter={chapter}
                      trickplay={trickplay}
                      trickplayBaseURL={trickplayBaseURL}
                    />
                  )}
                  <span className="bf-chapter-text">
                    <span className="bf-chapter-title">{title}</span>
                    <span className="bf-chapter-time">{formatClockTime(chapter.startSeconds)}</span>
                  </span>
                </Menu.Item>
              );
            })}
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}

function ChapterThumb({
  chapter,
  trickplay,
  trickplayBaseURL,
}: {
  chapter: ChapterEntry;
  trickplay: TrickplayManifest | null;
  trickplayBaseURL: string;
}) {
  // A marker thumbnail that is not generated yet (or whose token expired)
  // 404s; that falls through to the trickplay tile without an error.
  const [failed, setFailed] = useState(false);
  if (chapter.thumbnailURL && !failed) {
    return (
      <img
        className="bf-chapter-thumb"
        src={chapter.thumbnailURL}
        alt=""
        loading="lazy"
        decoding="async"
        onError={() => setFailed(true)}
      />
    );
  }
  const tile = trickplay ? resolveTrickplayTile(trickplay, chapter.startSeconds) : null;
  if (!tile) return <span aria-hidden="true" className="bf-chapter-thumb is-empty" />;
  const sheetURL = resolveTrickplaySheetURL(tile.sheet, trickplayBaseURL);
  const scale = THUMB_WIDTH / Math.max(tile.width, 1);
  const columns = trickplay?.columns || 10;
  const rows = trickplay?.rows || 10;
  return (
    <span
      aria-hidden="true"
      className="bf-chapter-thumb is-trickplay"
      style={{
        backgroundImage: `url("${sheetURL}")`,
        backgroundSize: `${tile.width * columns * scale}px ${tile.height * rows * scale}px`,
        backgroundPosition: `${-tile.x * scale}px ${-tile.y * scale}px`,
      }}
    />
  );
}
