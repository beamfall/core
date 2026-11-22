import { useEffect, useState } from "react";
import { Link } from "react-router";
import { fetchPlaybackMarkers } from "@/lib/graphql";
import type { PlaybackMarker } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { mediaPath } from "../lib/mediaAddress";
import { formatDuration } from "../lib/trackList";
import { buildChapterList } from "../lib/transport";

const HEADING_ID = "bf-media-chapters";

// An audio Media's chapters on its detail page (CORE-3014): each opens the
// player at its start, and the one holding the saved position is marked
// current. Nothing renders until the markers arrive, or when there are none;
// a failed read leaves the page without the list.
export function MediaChapters({
  mediaID,
  positionSeconds,
}: {
  mediaID: string;
  positionSeconds?: number;
}) {
  const t = useT();
  const [markers, setMarkers] = useState<{ mediaID: string; markers: PlaybackMarker[] } | null>(
    null,
  );

  useEffect(() => {
    let alive = true;
    fetchPlaybackMarkers(mediaID)
      .then((read) => {
        if (alive) setMarkers({ mediaID, markers: read });
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, [mediaID]);

  const chapters =
    markers?.mediaID === mediaID
      ? buildChapterList(markers.markers, { currentTimeSeconds: positionSeconds ?? -1 })
      : [];
  if (chapters.length < 2) return null;
  return (
    <section className="bf-section flex flex-col gap-3" aria-labelledby={HEADING_ID}>
      <h2 id={HEADING_ID} className="bf-section-title">
        <span>{t("chapters")}</span>
      </h2>
      <ol className="bf-detail-chapters">
        {chapters.map((chapter, index) => (
          <li key={chapter.startSeconds} aria-current={chapter.current ? "true" : undefined}>
            <Link to={`${mediaPath("watch", mediaID)}?t=${Math.floor(chapter.startSeconds)}`}>
              <span className="bf-detail-chapter-title">
                {chapter.title || t("chapterNumber", { number: index + 1 })}
              </span>
              <span className="bf-detail-chapter-start">
                {formatDuration(chapter.startSeconds) || "0:00"}
              </span>
            </Link>
          </li>
        ))}
      </ol>
    </section>
  );
}
